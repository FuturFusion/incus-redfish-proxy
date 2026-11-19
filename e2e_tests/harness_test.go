//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/redfishproxy"
)

const pollInterval = time.Second

func newVM(t *testing.T) string {
	// Without an operating system, the VM only runs the firmware. This is enough to observe power states.
	t.Helper()

	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	name := "vm-" + hex.EncodeToString(suffix)

	instance := api.InstancesPost{
		Name:   name,
		Type:   api.InstanceTypeVM,
		Source: api.InstanceSource{Type: "none"},
		InstancePut: api.InstancePut{
			Profiles: []string{},
			Config: map[string]string{
				"limits.cpu":          "1",
				"limits.memory":       "512MiB",
				"security.secureboot": "false",
			},
			Devices: map[string]map[string]string{
				"root": {"type": "disk", "path": "/", "pool": env.pool, "size": "1GiB"},
			},
		},
	}

	op, err := env.client.CreateInstance(instance)
	require.NoError(t, err, "create the VM %s", name)
	require.NoError(t, op.Wait(), "create the VM %s", name)

	t.Cleanup(func() {
		if t.Failed() {
			dumpInstance(t, name)
		}

		if !env.keep {
			deleteInstance(name)
		}
	})

	return name
}

func deleteInstance(name string) {
	op, err := env.client.UpdateInstanceState(name, api.InstanceStatePut{Action: "stop", Force: true}, "")
	if err == nil {
		_ = op.Wait()
	}

	op, err = env.client.DeleteInstance(name)
	if err == nil {
		_ = op.Wait()
	}
}

func dumpInstance(t *testing.T, name string) {
	t.Helper()

	var b strings.Builder

	instance, _, err := env.client.GetInstance(name)
	if err == nil {
		data, _ := json.MarshalIndent(instance, "", "  ")
		fmt.Fprintf(&b, "instance:\n%s\n", data)
	}

	console, err := env.client.GetInstanceConsoleLog(name, nil)
	if err == nil {
		data, _ := io.ReadAll(console)
		_ = console.Close()
		fmt.Fprintf(&b, "console log:\n%s\n", data)
	}

	logfile, err := env.client.GetInstanceLogfile(name, "qemu.log")
	if err == nil {
		data, _ := io.ReadAll(logfile)
		_ = logfile.Close()
		fmt.Fprintf(&b, "qemu.log:\n%s\n", data)
	}

	file := filepath.Join(env.logDir, strings.ReplaceAll(t.Name(), "/", "_")+".log")
	_ = os.WriteFile(file, []byte(b.String()), 0o644)
	t.Logf("instance %s dumped to %s", name, file)
}

func eventually(t *testing.T, d time.Duration, condition func() bool, msgAndArgs ...any) {
	// Poll in the test goroutine, so the condition may use require.
	t.Helper()

	deadline := time.Now().Add(timeout(d))

	for !condition() {
		if time.Now().After(deadline) {
			require.Fail(t, "the condition is not met in time", msgAndArgs...)
		}

		time.Sleep(pollInterval)
	}
}

func changeState(t *testing.T, name string, action string) {
	t.Helper()

	op, err := env.client.UpdateInstanceState(name, api.InstanceStatePut{Action: action, Force: true}, "")
	require.NoError(t, err, "%s the VM %s", action, name)
	require.NoError(t, op.Wait(), "%s the VM %s", action, name)
}

func instanceConfig(t *testing.T, name string) map[string]string {
	t.Helper()

	instance, _, err := env.client.GetInstance(name)
	require.NoError(t, err, "read the config of the VM %s", name)

	return instance.Config
}

func instanceStatus(t *testing.T, name string) string {
	t.Helper()

	state, _, err := env.client.GetInstanceState(name)
	require.NoError(t, err, "read the state of the VM %s", name)

	return state.Status
}

type proxy struct {
	vm      string
	profile string
	url     string
	client  *http.Client
	stop    func()
}

func startProxy(t *testing.T, vm string, profileName string) *proxy {
	t.Helper()

	p := &proxy{vm: vm, profile: profileName}
	p.start(t)

	t.Cleanup(func() { p.stop() })

	return p
}

func (p *proxy) start(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	handler, err := redfishproxy.NewHandler(redfishproxy.Config{
		InstanceName: p.vm,
		Remote:       env.remote,
		Project:      env.project,
		Profile:      p.profile,
		MediaDir:     env.mediaDir,
		Context:      ctx,
	})
	require.NoError(t, err, "start the proxy with the profile %s", p.profile)

	server := httptest.NewServer(handler)

	p.url, p.client = server.URL, server.Client()
	p.stop = func() {
		server.Close()
		cancel()
	}
}

func (p *proxy) restart(t *testing.T) {
	// Wait for the old mount to go. Its unmount goes by path and would detach the new mount.
	t.Helper()

	p.stop()

	eventually(t, 30*time.Second, func() bool { return mounts(t, mountPoint(p.vm)) == 0 }, "the old proxy unmounts the media")

	p.start(t)
}

func mountPoint(vm string) string {
	return filepath.Join(env.mediaDir, env.project+"_"+vm, "mnt")
}

func mounts(t *testing.T, path string) int {
	// The proxy runs in the test process, so its mounts show in its own mount table.
	t.Helper()

	data, err := os.ReadFile("/proc/self/mountinfo")
	require.NoError(t, err, "read the mount table")

	count := 0

	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) > 4 && fields[4] == path {
			count++
		}
	}

	return count
}

func (p *proxy) systemPath() string {
	return "/redfish/v1/Systems/" + p.vm
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) decode(t *testing.T, v any) {
	t.Helper()

	require.NoError(t, json.Unmarshal(r.body, v), "decode %s", r.body)
}

func (r response) messageID(t *testing.T) string {
	t.Helper()

	var body struct {
		Error struct {
			ExtendedInfo []struct {
				MessageID string `json:"MessageId"`
			} `json:"@Message.ExtendedInfo"`
		} `json:"error"`
	}

	require.NoError(t, json.Unmarshal(r.body, &body), "decode %s", r.body)
	require.NotEmpty(t, body.Error.ExtendedInfo, "extended info in %s", r.body)

	return body.Error.ExtendedInfo[0].MessageID
}

func (p *proxy) do(t *testing.T, method string, path string, body any) response {
	t.Helper()

	var reader io.Reader

	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err, "encode the request body")

		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, p.url+path, reader)
	require.NoError(t, err, "create the request %s %s", method, path)

	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	require.NoError(t, err, "%s %s", method, path)

	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read the answer of %s %s", method, path)

	return response{status: resp.StatusCode, header: resp.Header, body: data}
}

func (p *proxy) reset(t *testing.T, resetType string) response {
	t.Helper()

	return p.do(t, http.MethodPost, p.systemPath()+"/Actions/ComputerSystem.Reset", map[string]string{"ResetType": resetType})
}

func (p *proxy) powerState(t *testing.T) string {
	t.Helper()

	resp := p.do(t, http.MethodGet, p.systemPath(), nil)
	require.Equal(t, http.StatusOK, resp.status, "GET the system: %s", resp.body)

	var system struct {
		PowerState string `json:"PowerState"`
	}

	resp.decode(t, &system)

	return system.PowerState
}

func (p *proxy) waitTask(t *testing.T, location string) []response {
	t.Helper()

	var running []response

	deadline := time.Now().Add(timeout(2 * time.Minute))

	for time.Now().Before(deadline) {
		resp := p.do(t, http.MethodGet, location, nil)
		if resp.status != http.StatusAccepted {
			require.Equal(t, http.StatusOK, resp.status, "the task monitor %s ends with 200: %s", location, resp.body)

			return running
		}

		running = append(running, resp)

		wait := pollInterval

		seconds, err := strconv.Atoi(resp.header.Get("Retry-After"))
		if err == nil {
			wait = min(time.Duration(seconds)*time.Second, wait)
		}

		time.Sleep(wait)
	}

	require.Failf(t, "task monitor did not end", "%s is still running", location)

	return nil
}

type imageServer struct {
	url string

	mu       sync.Mutex
	requests []imageRequest
}

type imageRequest struct {
	Method string
	Range  string
}

func newImageServer(t *testing.T, name string, image []byte) *imageServer {
	t.Helper()

	modified := time.Now()

	return startImageServer(t, name, func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, name, modified, bytes.NewReader(image))
	})
}

func newPlainImageServer(t *testing.T, name string, image []byte) *imageServer {
	// Without range support, the proxy cannot stream the image.
	t.Helper()

	return startImageServer(t, name, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(image)))
		_, _ = w.Write(image)
	})
}

func startImageServer(t *testing.T, name string, serve http.HandlerFunc) *imageServer {
	t.Helper()

	s := &imageServer{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, imageRequest{Method: r.Method, Range: r.Header.Get("Range")})
		s.mu.Unlock()

		serve(w, r)
	}))
	t.Cleanup(server.Close)

	s.url = server.URL + "/" + name

	return s
}

func (s *imageServer) recorded() []imageRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.requests)
}

type byteRange struct {
	start int64
	end   int64
}

func (s *imageServer) ranges(t *testing.T, from int) []byteRange {
	t.Helper()

	var ranges []byteRange

	for _, request := range s.recorded()[from:] {
		if request.Method != http.MethodGet || request.Range == "" {
			continue
		}

		var r byteRange

		_, err := fmt.Sscanf(request.Range, "bytes=%d-%d", &r.start, &r.end)
		require.NoError(t, err, "parse the range %q", request.Range)

		ranges = append(ranges, r)
	}

	return ranges
}

func covers(ranges []byteRange, start int64, end int64) bool {
	for start < end {
		index := slices.IndexFunc(ranges, func(r byteRange) bool { return r.start <= start && start <= r.end })
		if index < 0 {
			return false
		}

		start = ranges[index].end + 1
	}

	return true
}

func overlaps(ranges []byteRange, start int64, end int64) bool {
	return slices.ContainsFunc(ranges, func(r byteRange) bool { return r.start < end && start <= r.end })
}

func randomImage(t *testing.T) []byte {
	t.Helper()

	image := make([]byte, 4<<20)
	_, err := rand.Read(image)
	require.NoError(t, err, "create the random image")

	return image
}
