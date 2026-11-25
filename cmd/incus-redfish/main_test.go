package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	incusclient "github.com/lxc/incus/v7/client"
	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/api/mock"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string

		assertErr    require.ErrorAssertionFunc
		assertStdout func(t *testing.T, got string)
		assertStderr func(t *testing.T, got string)
	}{
		{
			name:         "help",
			args:         []string{"--help"},
			assertErr:    require.NoError,
			assertStdout: contains("Usage: incus-redfish [flags] <command> [arguments]"),
			assertStderr: empty,
		},
		{
			name:         "help shorthand",
			args:         []string{"-h"},
			assertErr:    require.NoError,
			assertStdout: contains("Usage: incus-redfish [flags] <command> [arguments]"),
			assertStderr: empty,
		},
		{
			name: "missing command",
			args: []string{},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "missing argument: command", a...)
			},
			assertStdout: empty,
			assertStderr: contains("Usage: incus-redfish"),
		},
		{
			name: "unknown command",
			args: []string{"unknown"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, `unknown command "unknown"`, a...)
			},
			assertStdout: empty,
			assertStderr: contains("Usage: incus-redfish"),
		},
		{
			name: "unknown flag",
			args: []string{"--unknown"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "unknown flag", a...)
			},
			assertStdout: empty,
			assertStderr: contains("Usage: incus-redfish"),
		},
		{
			name: "insert virtual media without file",
			args: []string{"insert-virtual-media"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "missing argument: file to insert as boot media", a...)
			},
			assertStdout: empty,
			assertStderr: contains("Usage: incus-redfish"),
		},
		{
			name: "set secure boot without enabled state",
			args: []string{"set-secureboot"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "missing argument: enabled state", a...)
			},
			assertStdout: empty,
			assertStderr: contains("Usage: incus-redfish"),
		},
		{
			name: "get secure boot certificate without name",
			args: []string{"get-secureboot-certificate"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "missing argument: certificate name", a...)
			},
			assertStdout: empty,
			assertStderr: contains("Usage: incus-redfish"),
		},
		{
			name: "add secure boot certificate without file",
			args: []string{"add-secureboot-certificate"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "missing argument: certificate file", a...)
			},
			assertStdout: empty,
			assertStderr: contains("Usage: incus-redfish"),
		},
		{
			name: "delete secure boot certificate without name",
			args: []string{"delete-secureboot-certificate"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "missing argument: certificate name", a...)
			},
			assertStdout: empty,
			assertStderr: contains("Usage: incus-redfish"),
		},
		{
			name: "unreachable endpoint",
			args: []string{"--endpoint", "http://127.0.0.1:1", "info"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, `connect to "http://127.0.0.1:1"`, a...)
			},
			assertStdout: empty,
			assertStderr: empty,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			var stdout, stderr strings.Builder

			// Run test
			err := run(tc.args, &stdout, &stderr)

			// Assert
			tc.assertErr(t, err, "the error of run")
			tc.assertStdout(t, stdout.String())
			tc.assertStderr(t, stderr.String())
		})
	}
}

func TestRunCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		getInstance func(name string) (*incusapi.Instance, string, error)

		assertErr    require.ErrorAssertionFunc
		assertStdout func(t *testing.T, got string)
		wantStates   []incusapi.InstanceStatePut
	}{
		{
			name:         "info",
			args:         []string{"info"},
			getInstance:  instance("Running"),
			assertErr:    require.NoError,
			assertStdout: contains("id: vm\n"),
		},
		{
			name:         "debug output",
			args:         []string{"--debug", "info"},
			getInstance:  instance("Running"),
			assertErr:    require.NoError,
			assertStdout: contains("GET /redfish/v1/ HTTP/1.1"),
		},
		{
			name:         "start",
			args:         []string{"start"},
			getInstance:  instance("Stopped"),
			assertErr:    require.NoError,
			assertStdout: empty,
			wantStates:   []incusapi.InstanceStatePut{{Action: "start", Force: true}},
		},
		{
			name:         "stop",
			args:         []string{"stop"},
			getInstance:  instance("Running"),
			assertErr:    require.NoError,
			assertStdout: empty,
			wantStates:   []incusapi.InstanceStatePut{{Action: "stop", Force: true}},
		},
		{
			name:         "restart",
			args:         []string{"restart"},
			getInstance:  instance("Running"),
			assertErr:    require.NoError,
			assertStdout: empty,
			wantStates:   []incusapi.InstanceStatePut{{Action: "restart", Force: true}},
		},
		{
			name:         "get bios settings",
			args:         []string{"get-bios-settings"},
			getInstance:  instance("Running"),
			assertErr:    require.NoError,
			assertStdout: contains("incus.config.limits.cpu: \"2\""),
		},
		{
			name: "incus failure",
			args: []string{"info"},
			getInstance: func(string) (*incusapi.Instance, string, error) {
				return nil, "", errors.New("boom")
			},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "get systems", a...)
			},
			assertStdout: empty,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			incus := &mock.IncusClientMock{
				GetInstanceFunc: tc.getInstance,
				GetInstanceStateFunc: func(string) (*incusapi.InstanceState, string, error) {
					return &incusapi.InstanceState{}, "", nil
				},
				GetServerFunc: func() (*incusapi.Server, string, error) {
					return &incusapi.Server{}, "", nil
				},
				GetServerResourcesFunc: func() (*incusapi.Resources, error) {
					return &incusapi.Resources{}, nil
				},
				UpdateInstanceStateFunc: func(string, incusapi.InstanceStatePut, string) (incusclient.Operation, error) {
					return &mock.IncusOperationMock{WaitFunc: func() error { return nil }}, nil
				},
			}

			server := httptest.NewServer(api.NewHandler("vm", incus))
			t.Cleanup(server.Close)

			var stdout, stderr strings.Builder

			// Run test
			err := run(append([]string{"--endpoint", server.URL}, tc.args...), &stdout, &stderr)

			// Assert
			tc.assertErr(t, err, "the error of run")
			tc.assertStdout(t, stdout.String())

			var states []incusapi.InstanceStatePut
			for _, call := range incus.UpdateInstanceStateCalls() {
				states = append(states, call.State)
			}

			require.Equal(t, tc.wantStates, states, "changes of the power state")
		})
	}
}

func TestFileServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		stop func(t *testing.T, fs *fileServer) error
	}{
		{
			name: "serve requests until the context is canceled",
			stop: func(t *testing.T, fs *fileServer) error {
				t.Helper()

				ctx, cancel := context.WithCancel(t.Context())
				result := make(chan error, 1)

				go func() {
					result <- fs.serveUntil(ctx)
				}()

				// gofish reads http.DefaultTransport without a lock, so do not use it in parallel tests.
				client := &http.Client{Transport: &http.Transport{}}

				require.Empty(t, request(t, client, http.MethodHead, fs.address, ""), "body of the HEAD request")
				require.Equal(t, "con", request(t, client, http.MethodGet, fs.address, "bytes=0-2"), "first range")
				require.Equal(t, "tent", request(t, client, http.MethodGet, fs.address, "bytes=3-6"), "second range")

				cancel()

				return <-result
			},
		},
		{
			name: "close without request",
			stop: func(_ *testing.T, fs *fileServer) error {
				return fs.close()
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			filename := filepath.Join(t.TempDir(), "media.iso")
			require.NoError(t, os.WriteFile(filename, []byte("content"), 0o644), "write the file")

			var errorLog strings.Builder

			fs, err := serveFile(filename, &errorLog)
			require.NoError(t, err, "start the file server")

			// Run test
			err = tc.stop(t, fs)

			// Assert
			require.NoError(t, err, "the error of stopping the file server")
			require.Empty(t, errorLog.String(), "error log of the file server")
		})
	}
}

func instance(status string) func(name string) (*incusapi.Instance, string, error) {
	return func(name string) (*incusapi.Instance, string, error) {
		return &incusapi.Instance{
			Name:        name,
			Status:      status,
			InstancePut: incusapi.InstancePut{Config: map[string]string{"limits.cpu": "2"}},
		}, "", nil
	}
}

func request(t *testing.T, client *http.Client, method string, url string, byteRange string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	require.NoError(t, err, "create the request")

	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}

	resp, err := client.Do(req)
	require.NoError(t, err, "send the request")

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read the body")

	return string(body)
}

func empty(t *testing.T, got string) {
	t.Helper()

	require.Empty(t, got, "output stream")
}

func contains(want string) func(t *testing.T, got string) {
	return func(t *testing.T, got string) {
		t.Helper()

		require.Contains(t, got, want, "output stream")
	}
}
