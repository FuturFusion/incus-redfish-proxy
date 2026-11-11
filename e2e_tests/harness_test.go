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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/redfishproxy"
)

// pollInterval bounds the wait between two polls, a task monitor may ask for longer.
const pollInterval = time.Second

// vmOption changes the instance before it is created.
type vmOption func(*api.InstancesPost)

// newVM creates an empty, stopped VM. Without an operating system it only runs the firmware, which is enough to observe power states.
func newVM(t *testing.T, opts ...vmOption) string {
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

	for _, opt := range opts {
		opt(&instance)
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

// dumpInstance writes the config, the state and the console log of a VM for the CI artifact.
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

	file := filepath.Join(env.logDir, strings.ReplaceAll(t.Name(), "/", "_")+".log")
	_ = os.WriteFile(file, []byte(b.String()), 0o644)
	t.Logf("instance %s dumped to %s", name, file)
}

// startVM starts the VM outside of the proxy.
func startVM(t *testing.T, name string) {
	t.Helper()

	op, err := env.client.UpdateInstanceState(name, api.InstanceStatePut{Action: "start"}, "")
	require.NoError(t, err, "start the VM %s", name)
	require.NoError(t, op.Wait(), "start the VM %s", name)
}

func instanceStatus(t *testing.T, name string) string {
	t.Helper()

	state, _, err := env.client.GetInstanceState(name)
	require.NoError(t, err, "read the state of the VM %s", name)

	return state.Status
}

// proxy is an in process proxy for one VM.
type proxy struct {
	vm     string
	url    string
	client *http.Client
}

// startProxy serves the Redfish API of the VM with the profile, an embedded name or a file path.
func startProxy(t *testing.T, vm string, profileName string) *proxy {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	handler, err := redfishproxy.NewHandler(redfishproxy.Config{
		InstanceName: vm,
		Remote:       env.remote,
		Project:      env.project,
		Profile:      profileName,
		MediaDir:     env.mediaDir,
		Context:      ctx,
	})
	require.NoError(t, err, "start the proxy with the profile %s", profileName)

	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		cancel()
	})

	return &proxy{vm: vm, url: server.URL, client: server.Client()}
}

func (p *proxy) systemPath() string {
	return "/redfish/v1/Systems/" + p.vm
}

// response is a Redfish answer.
type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) decode(t *testing.T, v any) {
	t.Helper()

	require.NoError(t, json.Unmarshal(r.body, v), "decode %s", r.body)
}

// messageID returns the first message ID of a Redfish error.
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

// waitTask polls the task monitor until it ends. It returns the answers seen while the task was running.
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
