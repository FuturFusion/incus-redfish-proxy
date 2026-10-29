package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/internal/util/testing/boom"
)

func TestResetTracker_handle(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	restartedAt := startedAt.Add(time.Hour)

	event := func(action string, source string, timestamp time.Time) incusapi.Event {
		return lifecycleEvent(t, action, source, timestamp)
	}

	var seen []string

	tracker := &resetTracker{instanceName: "test-instance"}
	tracker.onPowerOn(func(ctx context.Context, e powerEvent) error {
		seen = append(seen, fmt.Sprintf("first %t %s", e.Restart, e.At.Format(time.TimeOnly)))
		return nil
	})
	tracker.onPowerOn(func(ctx context.Context, e powerEvent) error {
		seen = append(seen, "second")
		return errors.New("boom")
	})

	tracker.handle(t.Context(), event(incusapi.EventLifecycleInstanceRestarted, "/1.0/instances/other?project=foo", restartedAt))
	tracker.handle(t.Context(), event(incusapi.EventLifecycleInstanceUpdated, "/1.0/instances/test-instance?project=foo", restartedAt))
	tracker.handle(t.Context(), event(incusapi.EventLifecycleInstanceStopped, "/1.0/instances/test-instance?project=foo", restartedAt))
	require.Zero(t, tracker.last())
	require.Zero(t, tracker.lastPowerOn(), "other instances and other actions are no power on")
	require.Empty(t, seen, "no power on hook runs")

	tracker.handle(t.Context(), event(incusapi.EventLifecycleInstanceStarted, "/1.0/instances/test-instance?project=foo", startedAt))
	require.Zero(t, tracker.last(), "a start is no reset")
	require.Equal(t, startedAt, tracker.lastPowerOn(), "a start is a power on")
	require.Equal(t, []string{"first false 12:00:00", "second"}, seen, "the hooks run in order for a start")

	tracker.handle(t.Context(), event(incusapi.EventLifecycleInstanceRestarted, "/1.0/instances/test-instance?project=foo", restartedAt))
	require.Equal(t, restartedAt, tracker.last())
	require.Equal(t, restartedAt, tracker.lastPowerOn(), "a restart is a power on")
	require.Equal(t, []string{"first false 12:00:00", "second", "first true 13:00:00", "second"}, seen, "a failed hook does not stop the hooks of later events")
}

func lifecycleEvent(t *testing.T, action string, source string, timestamp time.Time) incusapi.Event {
	t.Helper()

	metadata, err := json.Marshal(incusapi.EventLifecycle{Action: action, Source: source})
	require.NoError(t, err, "marshal the event metadata")

	return incusapi.Event{Type: incusapi.EventTypeLifecycle, Timestamp: timestamp, Metadata: metadata}
}

type instanceStateClient struct {
	IncusClient

	state *incusapi.InstanceState
}

func (c instanceStateClient) GetInstanceState(name string) (*incusapi.InstanceState, string, error) {
	return c.state, "", nil
}

func TestRedfishServer_lastResetTime(t *testing.T) {
	startedAt := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	restartedAt := startedAt.Add(time.Hour)

	server := NewRedfishServer("test-instance", instanceStateClient{state: &incusapi.InstanceState{StartedAt: startedAt}})
	instance := &incusapi.Instance{Status: "Running"}

	resetTime, err := server.lastResetTime(instance)
	require.NoError(t, err)
	require.Equal(t, startedAt, *resetTime)

	server.resets.lastRestartAt = restartedAt

	resetTime, err = server.lastResetTime(instance)
	require.NoError(t, err)
	require.Equal(t, restartedAt, *resetTime)
}

func TestRedfishServer_settle(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		method      string
		path        string
		state       incusapi.InstanceState
		lastPowerOn time.Time
		now         time.Time

		wantStatus     int
		wantRetryAfter string
	}{
		{
			name:   "success - read in the window",
			method: http.MethodGet,
			path:   "/redfish/v1/Systems/test-instance",
			state:  incusapi.InstanceState{Status: "Running", StartedAt: startedAt},
			now:    startedAt.Add(5 * time.Second),

			wantStatus: http.StatusOK,
		},
		{
			name:   "success - write after the window",
			method: http.MethodPatch,
			path:   "/redfish/v1/Systems/test-instance",
			state:  incusapi.InstanceState{Status: "Running", StartedAt: startedAt},
			now:    startedAt.Add(20 * time.Second),

			wantStatus: http.StatusOK,
		},
		{
			name:   "success - write to a stopped instance",
			method: http.MethodPost,
			path:   "/redfish/v1/Systems/test-instance/Actions/ComputerSystem.Reset",
			state:  incusapi.InstanceState{Status: "Stopped", StartedAt: startedAt},
			now:    startedAt.Add(5 * time.Second),

			wantStatus: http.StatusOK,
		},
		{
			name:   "success - write outside of systems and managers",
			method: http.MethodPatch,
			path:   "/redfish/v1/AccountService",
			state:  incusapi.InstanceState{Status: "Running", StartedAt: startedAt},
			now:    startedAt.Add(5 * time.Second),

			wantStatus: http.StatusOK,
		},
		{
			name:   "error - write in the window",
			method: http.MethodPatch,
			path:   "/redfish/v1/Managers/bmc1/VirtualMedia/CD",
			state:  incusapi.InstanceState{Status: "Running", StartedAt: startedAt},
			now:    startedAt.Add(5500 * time.Millisecond),

			wantStatus:     http.StatusBadRequest,
			wantRetryAfter: "15",
		},
		{
			name:        "error - in place restart seen by the tracker",
			method:      http.MethodPost,
			path:        "/redfish/v1/Systems/test-instance/Actions/ComputerSystem.Reset",
			state:       incusapi.InstanceState{Status: "Running", StartedAt: startedAt},
			lastPowerOn: startedAt.Add(time.Hour),
			now:         startedAt.Add(time.Hour + 5*time.Second),

			wantStatus:     http.StatusBadRequest,
			wantRetryAfter: "15",
		},
		{
			name:   "error - clock behind the power on",
			method: http.MethodPost,
			path:   "/redfish/v1/Systems/test-instance/Actions/ComputerSystem.Reset",
			state:  incusapi.InstanceState{Status: "Running", StartedAt: startedAt},
			now:    startedAt.Add(-time.Hour),

			wantStatus:     http.StatusBadRequest,
			wantRetryAfter: "20",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := profile.Default()
			p.Power.NotSettledWindow = 20 * time.Second

			server := NewRedfishServer("test-instance", instanceStateClient{state: &tc.state}, WithProfile(p), WithClock(func() time.Time { return tc.now }))
			server.resets.poweredOn(tc.lastPowerOn)

			handler := server.settle(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))

			require.Equal(t, tc.wantStatus, rec.Code, "only writes to a running instance in the window are refused")
			require.Equal(t, tc.wantRetryAfter, rec.Header().Get("Retry-After"), "Retry-After is the rest of the window, rounded up")
		})
	}
}

func TestRedfishServer_consumeBootOnce(t *testing.T) {
	t.Parallel()

	p := profile.Default()
	p.VirtualMedia.BootOverrideOnce = true

	client := &stateClient{instance: incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{
		Config:  map[string]string{},
		Devices: incusapi.DevicesMap{"boot-media": {"type": "disk", "source": "boot-media"}},
	}}}
	server := NewRedfishServer("test-instance", client, WithProfile(p))

	err := server.setBootOverride(ComputerSystemV1290BootSourceOverrideEnabledOnce)
	require.NoError(t, err, "set the boot override once")
	require.Equal(t, ComputerSystemV1290BootSourceOverrideEnabledOnce, bootOverride(&client.instance), "the override is set once")

	server.resets.handle(t.Context(), lifecycleEvent(t, incusapi.EventLifecycleInstanceStopped, "/1.0/instances/test-instance", time.Now()))
	require.Equal(t, ComputerSystemV1290BootSourceOverrideEnabledOnce, bootOverride(&client.instance), "a power off keeps the override")

	server.resets.handle(t.Context(), lifecycleEvent(t, incusapi.EventLifecycleInstanceStarted, "/1.0/instances/test-instance", time.Now()))
	require.Equal(t, ComputerSystemV1290BootSourceOverrideEnabledDisabled, bootOverride(&client.instance), "the power on drops the override")
	require.NotContains(t, client.instance.Config, bootOnceKey, "the power on clears the boot once marker")
	require.Equal(t, map[string]string{"type": "disk", "source": "boot-media"}, client.instance.Devices["boot-media"], "the media stays inserted")

	err = server.setBootOverride(ComputerSystemV1290BootSourceOverrideEnabledContinuous)
	require.NoError(t, err, "set the continuous boot override")

	server.resets.handle(t.Context(), lifecycleEvent(t, incusapi.EventLifecycleInstanceRestarted, "/1.0/instances/test-instance", time.Now()))
	require.Equal(t, ComputerSystemV1290BootSourceOverrideEnabledContinuous, bootOverride(&client.instance), "a power on keeps a continuous override")
}

func TestRedfishServer_wait(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name      string
		ctx       context.Context
		serverCtx context.Context
		d         time.Duration

		assertErr require.ErrorAssertionFunc
	}{
		{name: "success - no delay", ctx: canceled, serverCtx: canceled, d: 0, assertErr: require.NoError},
		{name: "success - delay elapsed", ctx: context.Background(), serverCtx: context.Background(), d: time.Millisecond, assertErr: require.NoError},
		{name: "error - request gone", ctx: canceled, serverCtx: context.Background(), d: time.Hour, assertErr: require.Error},
		{name: "error - server stopped", ctx: context.Background(), serverCtx: canceled, d: time.Hour, assertErr: require.Error},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := NewRedfishServer("test-instance", nil, WithContext(tc.serverCtx))

			tc.assertErr(t, server.wait(tc.ctx, tc.d), "wait fails if a context ends before the delay")
		})
	}
}

// failingEvents refuses the event stream and reports each attempt.
type failingEvents struct {
	attempts chan struct{}
}

func (e failingEvents) GetEventsByType([]string) (*incusclient.EventListener, error) {
	e.attempts <- struct{}{}
	return nil, boom.Error
}

func TestResetTracker_watch(t *testing.T) {
	t.Parallel()

	// Setup
	ctx, cancel := context.WithCancel(t.Context())
	source := failingEvents{attempts: make(chan struct{}, 1)}
	tracker := &resetTracker{instanceName: "test-instance"}
	done := make(chan struct{}, 1)

	// Run test
	go func() {
		tracker.watch(ctx, source)
		done <- struct{}{}
	}()

	<-source.attempts
	cancel()

	// Assert
	require.Eventually(t, func() bool { return len(done) > 0 }, time.Second, time.Millisecond, "watch ends with the context, also while it waits to reconnect")
}

func TestRedfishServer_resetBootOnceFailure(t *testing.T) {
	t.Parallel()

	// Setup
	p := profile.Default()
	p.VirtualMedia.BootOverrideOnce = true

	client := &stateClient{instance: incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{
		Config:  map[string]string{},
		Devices: incusapi.DevicesMap{"boot-media": {"type": "disk", "source": "boot-media"}},
	}}}
	server := NewRedfishServer("test-instance", client, WithProfile(p))

	err := server.setBootOverride(ComputerSystemV1290BootSourceOverrideEnabledOnce)
	require.NoError(t, err, "set the boot override once")

	client.updateErr = func(*incusapi.Instance) error {
		return boom.Error
	}

	// Run test
	rec := httptest.NewRecorder()
	server.PostRedfishV1SystemsComputerSystemIDActionsComputerSystemReset(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ResetType":"On"}`)), "test-instance")

	// Assert
	require.Equal(t, http.StatusNoContent, rec.Code, "the reset succeeds, as the power state changed")
	require.Equal(t, []string{"start"}, client.actions, "the reset starts the instance")
	require.Equal(t, ComputerSystemV1290BootSourceOverrideEnabledOnce, bootOverride(&client.instance), "the override stays for the power on hook")
}
