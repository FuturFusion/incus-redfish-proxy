package api

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/media"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/internal/util/testing/boom"
)

type deviceClient struct {
	IncusClient

	mu      sync.Mutex
	devices map[string]map[string]string
	updates int
	log     *eventLog

	getErr    error
	updateErr error
	waitErr   error
}

func (c *deviceClient) GetInstance(name string) (*incusapi.Instance, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.log.add("get instance")

	if c.getErr != nil {
		return nil, "", c.getErr
	}

	return &incusapi.Instance{Status: "Running", InstancePut: incusapi.InstancePut{Devices: maps.Clone(c.devices)}}, "", nil
}

func (c *deviceClient) UpdateInstance(name string, put incusapi.InstancePut, ETag string) (incusclient.Operation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.updates++
	if c.updateErr != nil {
		return nil, c.updateErr
	}

	c.devices = put.Devices

	return waitOperation{err: c.waitErr}, nil
}

type waitOperation struct {
	incusclient.Operation

	err error
}

func (o waitOperation) Wait() error {
	return o.err
}

func (c *deviceClient) state() (map[string]map[string]string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return maps.Clone(c.devices), c.updates
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.events = append(l.events, event)
}

func (l *eventLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string(nil), l.events...)
}

type closeMounter struct {
	log *eventLog
	err error
}

func (m closeMounter) Mount(context.Context, string, string, media.Options) (MountedMedia, error) {
	return closeMedia{log: m.log}, nil
}

func (m closeMounter) Cleanup() error {
	m.log.add("cleanup")
	return m.err
}

type closeMedia struct {
	log *eventLog
	err error
}

func (m closeMedia) Path() string {
	return "/media/boot-media.iso"
}

func (m closeMedia) Close() error {
	m.log.add("unmount")
	return m.err
}

func newCloseServer(client *deviceClient, log *eventLog, opts ...Option) *redfishServer {
	opts = append([]Option{WithMediaMounter(closeMounter{log: log})}, opts...)
	server := NewRedfishServer("test-instance", client, opts...)
	server.mounted.set(closeMedia{log: log})

	return server
}

func streamProfile() profile.Profile {
	p := profile.Default()
	p.VirtualMedia.Delivery = profile.DeliveryStream

	return p
}

func TestRedfishServer_closeEject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		devices map[string]map[string]string

		wantDevices map[string]map[string]string
		wantUpdates int
	}{
		{
			name:        "stream",
			devices:     map[string]map[string]string{bootMediaDeviceName: {bootMediaDeliveryKey: "stream"}, "root": {}},
			wantDevices: map[string]map[string]string{"root": {}},
			wantUpdates: 1,
		},
		{
			name:        "prefetch",
			devices:     map[string]map[string]string{bootMediaDeviceName: {bootMediaDeliveryKey: "prefetch"}},
			wantDevices: map[string]map[string]string{},
			wantUpdates: 1,
		},
		{
			name:        "full fetch",
			devices:     map[string]map[string]string{bootMediaDeviceName: {bootMediaDeliveryKey: "full-fetch"}},
			wantDevices: map[string]map[string]string{bootMediaDeviceName: {bootMediaDeliveryKey: "full-fetch"}},
			wantUpdates: 0,
		},
		{
			name:        "volume",
			devices:     map[string]map[string]string{bootMediaDeviceName: {"pool": "default"}},
			wantDevices: map[string]map[string]string{bootMediaDeviceName: {"pool": "default"}},
			wantUpdates: 0,
		},
		{
			name:        "no media",
			devices:     map[string]map[string]string{},
			wantDevices: map[string]map[string]string{},
			wantUpdates: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			log := &eventLog{}
			client := &deviceClient{devices: tc.devices, log: log}
			server := newCloseServer(client, log)

			// Run test
			err := server.close()

			// Assert
			require.NoError(t, err, "close the server")

			devices, updates := client.state()
			require.Equal(t, tc.wantDevices, devices, "close removes only a streamed device")
			require.Equal(t, tc.wantUpdates, updates, "close updates the instance only to remove a streamed device")
			require.Equal(t, []string{"get instance", "unmount", "cleanup"}, log.all(), "close always unmounts and cleans up the media directory")
		})
	}
}

func TestRedfishServer_closeErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		client   func(log *eventLog) *deviceClient
		mounter  closeMounter
		mediaErr error

		wantErr string
	}{
		{
			name:    "get instance fails",
			client:  func(log *eventLog) *deviceClient { return &deviceClient{log: log, getErr: boom.Error} },
			wantErr: `remove the streamed media device of instance "test-instance"`,
		},
		{
			name: "update fails",
			client: func(log *eventLog) *deviceClient {
				return &deviceClient{devices: map[string]map[string]string{bootMediaDeviceName: {bootMediaDeliveryKey: "stream"}}, log: log, updateErr: boom.Error}
			},
			wantErr: `remove the streamed media device of instance "test-instance"`,
		},
		{
			name: "wait fails",
			client: func(log *eventLog) *deviceClient {
				return &deviceClient{devices: map[string]map[string]string{bootMediaDeviceName: {bootMediaDeliveryKey: "stream"}}, log: log, waitErr: boom.Error}
			},
			wantErr: `remove the streamed media device of instance "test-instance"`,
		},
		{
			name:     "unmount fails",
			client:   func(log *eventLog) *deviceClient { return &deviceClient{log: log} },
			mediaErr: boom.Error,
			wantErr:  `unmount the media of instance "test-instance"`,
		},
		{
			name:    "cleanup fails",
			client:  func(log *eventLog) *deviceClient { return &deviceClient{log: log} },
			mounter: closeMounter{err: boom.Error},
			wantErr: `clean up the media directory of instance "test-instance"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			log := &eventLog{}
			mounter := tc.mounter
			mounter.log = log
			server := NewRedfishServer("test-instance", tc.client(log), WithMediaMounter(mounter))
			server.mounted.set(closeMedia{log: log, err: tc.mediaErr})

			// Run test
			err := server.close()
			errAgain := server.close()

			// Assert
			require.ErrorIs(t, err, boom.Error, "close returns the error of the failed step")
			require.ErrorContains(t, err, tc.wantErr, "the error names the failed step")
			require.ErrorIs(t, errAgain, boom.Error, "a second close returns the error of the first")
			require.Equal(t, []string{"get instance", "unmount", "cleanup"}, log.all(), "close runs every step once, also after a failed step")
		})
	}
}

func TestRedfishServer_closeKeepsDownloadedFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile profile.Profile
		stop    func(server *redfishServer, cancel context.CancelFunc) error
	}{
		{
			name:    "close, then cancel",
			profile: profile.Default(),
			stop: func(server *redfishServer, cancel context.CancelFunc) error {
				err := server.close()
				cancel()

				return err
			},
		},
		{
			name:    "close, then cancel, stream with full fetch fallback",
			profile: streamProfile(),
			stop: func(server *redfishServer, cancel context.CancelFunc) error {
				err := server.close()
				cancel()

				return err
			},
		},
		{
			name:    "abort, stream with full fetch fallback",
			profile: streamProfile(),
			stop: func(server *redfishServer, cancel context.CancelFunc) error {
				cancel()
				server.shutdown.stopAbort()

				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			log := &eventLog{}
			devices := map[string]map[string]string{bootMediaDeviceName: {bootMediaDeliveryKey: "full-fetch"}}
			client := &deviceClient{devices: devices, log: log}
			ctx, cancel := context.WithCancel(t.Context())
			server := NewRedfishServer("test-instance", client, WithMediaMounter(closeMounter{log: log}), WithProfile(tc.profile), WithContext(ctx))

			file := filepath.Join(t.TempDir(), downloadedMediaName)
			require.NoError(t, os.WriteFile(file, []byte("image"), 0o600), "write the downloaded file")
			server.mounted.set(downloadedMedia{path: file})

			// Run test
			err := tc.stop(server, cancel)

			// Assert
			require.NoError(t, err, "stop the server")
			require.FileExists(t, file, "the downloaded file stays with its device")

			gotDevices, _ := client.state()
			require.Equal(t, devices, gotDevices, "the device of the downloaded file stays")
		})
	}
}

func TestRedfishServer_closeAndAbort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		stop func(server *redfishServer, cancel context.CancelFunc) error

		wantLog []string
	}{
		{
			name: "close, then cancel",
			stop: func(server *redfishServer, cancel context.CancelFunc) error {
				err := server.close()
				cancel()

				return err
			},
			wantLog: []string{"get instance", "unmount", "cleanup"},
		},
		{
			name: "cancel, then close",
			stop: func(server *redfishServer, cancel context.CancelFunc) error {
				cancel()

				return server.close()
			},
			wantLog: []string{"unmount", "get instance", "cleanup"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			log := &eventLog{}
			client := &deviceClient{devices: map[string]map[string]string{bootMediaDeviceName: {bootMediaDeliveryKey: "stream"}}, log: log}
			ctx, cancel := context.WithCancel(t.Context())
			server := NewRedfishServer("test-instance", client, WithMediaMounter(closeMounter{log: log}), WithProfile(streamProfile()), WithContext(ctx))
			restoreLog := len(log.all())

			// Run test
			err := tc.stop(server, cancel)

			// Assert
			require.NoError(t, err, "stop the server")
			require.Equal(t, tc.wantLog, log.all()[restoreLog:], "the media is unmounted once, the close removes the device")

			devices, _ := client.state()
			require.Empty(t, devices, "close removes the streamed media device, also after an abort")
		})
	}
}

func TestRedfishServer_closeWaitsForBackgroundWork(t *testing.T) {
	t.Parallel()

	// Setup
	log := &eventLog{}
	client := &deviceClient{log: log}
	server := newCloseServer(client, log, WithCloseTimeout(time.Minute))
	release := make(chan struct{})

	server.goBackground(func() {
		<-release
		log.add(fmt.Sprintf("work done: %v", server.ctx.Err()))
	})

	// Run test
	closed := make(chan error, 1)

	go func() { closed <- server.close() }()

	// Assert
	require.Never(t, func() bool { return len(closed) > 0 }, 20*time.Millisecond, time.Millisecond, "close waits for the background work")

	close(release)

	require.NoError(t, <-closed, "close the server")
	require.Equal(t, []string{"work done: <nil>", "get instance", "unmount", "cleanup"}, log.all(), "close waits for the background work without a cancel, then ejects")
}

func TestRedfishServer_closeCancelsBackgroundWorkAfterTimeout(t *testing.T) {
	t.Parallel()

	// Setup
	log := &eventLog{}
	client := &deviceClient{log: log}
	server := newCloseServer(client, log, WithCloseTimeout(time.Millisecond))

	server.goBackground(func() {
		<-server.ctx.Done()
		log.add("work canceled")
	})

	// Run test
	err := server.close()

	// Assert
	require.NoError(t, err, "close the server")
	require.Equal(t, []string{"work canceled", "get instance", "unmount", "cleanup"}, log.all(), "close cancels the background work after the timeout, then ejects")
}

func TestRedfishServer_closeStopsWatcher(t *testing.T) {
	t.Parallel()

	// Setup
	log := &eventLog{}
	client := &deviceClient{log: log}
	source := failingEvents{attempts: make(chan struct{}, 1)}
	server := newCloseServer(client, log, WithEventSource(source), WithContext(t.Context()))
	<-source.attempts

	// Run test
	err := server.close()

	// Assert
	require.NoError(t, err, "close the server")
	require.ErrorIs(t, server.ctx.Err(), context.Canceled, "close cancels the server context")
	require.NoError(t, t.Context().Err(), "close does not cancel the outer context")
}

func TestRedfishServer_closeDropsLateWork(t *testing.T) {
	t.Parallel()

	// Setup
	log := &eventLog{}
	client := &deviceClient{log: log}
	server := newCloseServer(client, log)

	// Run test
	err := server.close()
	admitted := server.goBackground(func() { log.add("late work") })

	// Assert
	require.NoError(t, err, "close the server")
	require.False(t, admitted, "close refuses later work")
	require.Equal(t, []string{"get instance", "unmount", "cleanup"}, log.all(), "work after close does not run")
}

func TestService_Close(t *testing.T) {
	t.Parallel()

	// Setup
	log := &eventLog{}
	client := &deviceClient{devices: map[string]map[string]string{bootMediaDeviceName: {bootMediaDeliveryKey: "stream"}}, log: log}
	service := NewHandler("test-instance", client, WithMediaMounter(closeMounter{log: log}))

	// Run test
	err := service.Close()

	// Assert
	require.NoError(t, err, "close the service")

	devices, _ := client.state()
	require.Empty(t, devices, "close of the service ejects the streamed media")
}

func taskStates(server *redfishServer) []TaskV174TaskState {
	tasks := server.tasks.list()

	states := make([]TaskV174TaskState, 0, len(tasks))
	for _, task := range tasks {
		states = append(states, task.state)
	}

	return states
}

func post(service *Service, path string, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	service.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))

	return rec
}

const resetPath = "/redfish/v1/Systems/test-instance/Actions/ComputerSystem.Reset"

func TestService_refusesBackgroundWorkAfterClose(t *testing.T) {
	t.Parallel()

	insertTask := profile.Default()
	insertTask.VirtualMedia.InsertResponse = profile.InsertResponseTask

	resetAnswer := profile.Default()
	resetAnswer.Power.Delay.Off = time.Hour

	resetTask := resetAnswer
	resetTask.Power.Response = profile.PowerResponseTask

	tests := []struct {
		name    string
		profile profile.Profile
		path    string
		body    string
		lock    func(server *redfishServer) *sync.Mutex

		wantTasks []TaskV174TaskState
	}{
		{
			name:      "insert with a task",
			profile:   insertTask,
			path:      "/redfish/v1/Managers/bmc1/VirtualMedia/CD/Actions/VirtualMedia.InsertMedia",
			body:      `{"Image": "http://images.invalid/boot.iso"}`,
			lock:      func(server *redfishServer) *sync.Mutex { return server.mediaMu },
			wantTasks: []TaskV174TaskState{TaskV174TaskStateException},
		},
		{
			name:      "reset with an answer",
			profile:   resetAnswer,
			path:      resetPath,
			body:      `{"ResetType": "ForceOff"}`,
			lock:      func(server *redfishServer) *sync.Mutex { return server.resetMu },
			wantTasks: []TaskV174TaskState{},
		},
		{
			name:      "reset with a task",
			profile:   resetTask,
			path:      resetPath,
			body:      `{"ResetType": "ForceOff"}`,
			lock:      func(server *redfishServer) *sync.Mutex { return server.resetMu },
			wantTasks: []TaskV174TaskState{TaskV174TaskStateException},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			log := &eventLog{}
			service := NewHandler("test-instance", &deviceClient{log: log}, WithMediaMounter(closeMounter{log: log}), WithProfile(tc.profile))
			require.NoError(t, service.Close(), "close the service")

			// Run test
			rec := post(service, tc.path, tc.body)

			// Assert
			require.Equal(t, http.StatusServiceUnavailable, rec.Code, "the request is refused during the shutdown: %s", rec.Body)
			require.True(t, tc.lock(service.server).TryLock(), "the refusal releases the lock")
			require.Equal(t, tc.wantTasks, taskStates(service.server), "the refusal ends the task")
			require.NoError(t, service.Close(), "a second close returns at once")
		})
	}
}

func TestService_closeWithReset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		delay   time.Duration
		timeout time.Duration

		wantActions []string
		wantTasks   []TaskV174TaskState
	}{
		{
			name:        "close waits for the reset",
			delay:       10 * time.Millisecond,
			timeout:     time.Minute,
			wantActions: []string{"stop"},
			wantTasks:   []TaskV174TaskState{TaskV174TaskStateCompleted},
		},
		{
			name:        "close cancels the reset after the timeout",
			delay:       time.Hour,
			timeout:     time.Millisecond,
			wantActions: nil,
			wantTasks:   []TaskV174TaskState{TaskV174TaskStateException},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			p := profile.Default()
			p.Power.Response = profile.PowerResponseTask
			p.Power.Delay.Off = tc.delay

			client := &stateClient{instance: incusapi.Instance{Status: "Running"}}
			service := NewHandler("test-instance", client, WithMediaMounter(closeMounter{log: &eventLog{}}), WithProfile(p), WithCloseTimeout(tc.timeout))

			rec := post(service, resetPath, `{"ResetType": "ForceOff"}`)
			require.Equal(t, http.StatusAccepted, rec.Code, "the reset answers with a task: %s", rec.Body)

			// Run test
			err := service.Close()

			// Assert
			require.NoError(t, err, "close the service")
			require.Equal(t, tc.wantActions, client.actions, "the power state changes only if the reset ends before the timeout")
			require.Equal(t, tc.wantTasks, taskStates(service.server), "state of the reset task")
			require.True(t, service.server.resetMu.TryLock(), "the reset releases its lock")
		})
	}
}

func TestRedfishServer_powerCycleKeepsMarkerInShutdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, server *redfishServer) error

		wantErr error
	}{
		{
			name: "refused after close",
			run: func(t *testing.T, server *redfishServer) error {
				t.Helper()

				require.NoError(t, server.close(), "close the server")

				return server.startPowerCycle()
			},
			wantErr: errShuttingDown,
		},
		{
			name: "canceled before the marker update",
			run: func(t *testing.T, server *redfishServer) error {
				t.Helper()

				server.shutdown.cancel()
				_, err := server.takePowerCycleMarker()

				return err
			},
			wantErr: context.Canceled,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			client := &stateClient{instance: incusapi.Instance{
				Status:      "Running",
				InstancePut: incusapi.InstancePut{Config: map[string]string{powerCycleKey: string(profile.PowerCycleTriggerEnable)}},
			}}
			server := NewRedfishServer("test-instance", client, WithMediaMounter(closeMounter{log: &eventLog{}}))

			// Run test
			err := tc.run(t, server)

			// Assert
			require.ErrorIs(t, err, tc.wantErr, "the power cycle does not start")

			instance, _, err := client.GetInstance("test-instance")
			require.NoError(t, err, "read the instance")
			require.Contains(t, instance.Config, powerCycleKey, "the marker stays for the next power on")
		})
	}
}
