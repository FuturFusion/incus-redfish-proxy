package api_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stmcginnis/gofish/schemas"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/api/mock"
)

func TestRedfishServer_LogServicesReadOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		url    string
	}{
		{method: http.MethodPatch, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog"},
		{method: http.MethodPut, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog"},
		{method: http.MethodPost, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries"},
		{method: http.MethodDelete, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries/1"},
		{method: http.MethodPatch, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries/1"},
		{method: http.MethodPut, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries/1"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.url), func(t *testing.T) {
			t.Parallel()

			status, header := rawRequest(t, &mock.IncusClientMock{}, tc.method, tc.url)

			require.Equal(t, http.StatusMethodNotAllowed, status, "the log services are read only")
			require.Equal(t, http.MethodGet, header.Get("Allow"), "the answer names the allowed method")
		})
	}
}

func TestRedfishServer_LogServicesNotFound(t *testing.T) {
	t.Parallel()

	emptyClient := func() *mock.IncusClientMock { return &mock.IncusClientMock{} }

	tests := []struct {
		url    string
		client func() *mock.IncusClientMock
	}{
		{url: "/redfish/v1/Systems/invalid/LogServices", client: emptyClient},
		{url: "/redfish/v1/Systems/invalid/LogServices/EventLog", client: emptyClient},
		{url: "/redfish/v1/Systems/test-instance/LogServices/Unknown", client: emptyClient},
		{url: "/redfish/v1/Systems/test-instance/LogServices/Unknown/Entries", client: emptyClient},
		{url: "/redfish/v1/Systems/test-instance/LogServices/Unknown/Entries/1", client: emptyClient},
		{url: "/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries/999", client: emptyClient},
	}

	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			t.Parallel()

			status, _ := rawRequest(t, tc.client(), http.MethodGet, tc.url)

			require.Equal(t, http.StatusNotFound, status, "the log service or the entry does not exist")
		})
	}
}

func TestRedfishServer_SystemLogServices(t *testing.T) {
	t.Parallel()

	incusClient := &mock.IncusClientMock{
		GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
			return &incusapi.Instance{Status: "Stopped"}, "", nil
		},
	}

	client := setup(t, incusClient)

	systems, err := client.Service.Systems()
	require.NoError(t, err, "list the systems")

	services, err := systems[0].LogServices()
	require.NoError(t, err, "list the log services of the system")

	ids := make([]string, 0, len(services))
	for _, service := range services {
		ids = append(ids, service.ID)
	}

	require.Equal(t, []string{"EventLog", "HypervisorLog"}, ids, "the system links its log services")

	resp, err := client.Get("/redfish/v1/Systems/test-instance/LogServices")
	require.NoError(t, err, "get the log service collection")

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read the log service collection")
	require.JSONEq(t, `{
		"@odata.id": "/redfish/v1/Systems/test-instance/LogServices",
		"@odata.type": "#LogServiceCollection.LogServiceCollection",
		"Members": [
			{"@odata.id": "/redfish/v1/Systems/test-instance/LogServices/EventLog"},
			{"@odata.id": "/redfish/v1/Systems/test-instance/LogServices/HypervisorLog"}
		],
		"Members@odata.count": 2,
		"Name": "Log Service Collection"
	}`, string(body), "the collection lists every log service")
}

// rawRequest sends a request without gofish, which hides the response of a failed request.
func rawRequest(t *testing.T, client api.IncusClient, method string, url string) (int, http.Header) {
	t.Helper()

	server := httptest.NewServer(api.NewHandler("test-instance", client))
	t.Cleanup(server.Close)

	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+url, nil)
	require.NoError(t, err, "build the request")

	resp, err := server.Client().Do(req)
	require.NoError(t, err, "send the request")

	defer resp.Body.Close()

	return resp.StatusCode, resp.Header
}

func TestRedfishServer_HypervisorLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		logfile func() (io.ReadCloser, error)

		wantMessages []string
	}{
		{
			name: "events of the current boot",
			logfile: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(`[2026-11-26T10:00:00+01:00] QUERY: {"execute":"system_reset"}
[2026-11-26T10:00:00+01:00] REPLY: {"return":{}}

[2026-11-26T10:00:00+01:00] Event: {"event":"RESET","data":{"guest":false}}
[2026-11-26T10:00:05+01:00] Event: {"event":"SHUTDOWN","data":{"guest":true}}
`)), nil
			},

			wantMessages: []string{`SHUTDOWN {"guest":true}`, `RESET {"guest":false}`},
		},
		{
			name: "instance never started",
			logfile: func() (io.ReadCloser, error) {
				return nil, incusapi.StatusErrorf(http.StatusNotFound, "not found")
			},

			wantMessages: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			incusClient := &mock.IncusClientMock{
				GetInstanceLogfileFunc: func(name string, filename string) (io.ReadCloser, error) {
					return tc.logfile()
				},
			}

			client := setup(t, incusClient)

			logService, err := schemas.GetObject[schemas.LogService](client, "/redfish/v1/Systems/test-instance/LogServices/HypervisorLog")
			require.NoError(t, err, "get the hypervisor log")

			entries, err := logService.Entries()
			require.NoError(t, err, "get the entries of the hypervisor log")

			require.Equal(t, schemas.OEMLogEntryTypes, logService.LogEntryType, "QMP events are not Redfish events")
			require.Equal(t, tc.wantMessages, entryMessages(entries), "the QMP events are the entries, newest first")
			require.Equal(t, []string{"qemu.qmp.log"}, logfileNames(incusClient), "the log of the QMP events is read")
		})
	}
}

func entryMessages(entries []*schemas.LogEntry) []string {
	messages := make([]string, 0, len(entries))
	for _, entry := range entries {
		messages = append(messages, entry.Message)
	}

	return messages
}

func TestRedfishServer_HypervisorLogEntry(t *testing.T) {
	t.Parallel()

	incusClient := &mock.IncusClientMock{
		GetInstanceLogfileFunc: func(name string, filename string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(`[2026-11-26T10:00:00+01:00] Event: {"timestamp":{"seconds":1795683601,"microseconds":250000},"event":"GUEST_PANICKED","data":{"action":"pause"}}` + "\n")), nil
		},
	}

	client := setup(t, incusClient)

	resp, err := client.Get("/redfish/v1/Systems/test-instance/LogServices/HypervisorLog/Entries/1")
	require.NoError(t, err, "get the entry")

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read the entry")
	require.JSONEq(t, `{
		"@odata.id": "/redfish/v1/Systems/test-instance/LogServices/HypervisorLog/Entries/1",
		"@odata.type": "#LogEntry.v1_22_0.LogEntry",
		"Created": "2026-11-26T09:00:01.25Z",
		"EntryType": "Oem",
		"Id": "1",
		"Message": "GUEST_PANICKED {\"action\":\"pause\"}",
		"Name": "Hypervisor Log Entry 1",
		"OemRecordFormat": "QEMU QMP",
		"Severity": "Critical"
	}`, string(body), "the entry is a QMP event")
}

func TestRedfishServer_HypervisorLogFailure(t *testing.T) {
	t.Parallel()

	instanceExists := func(name string) (*incusapi.InstanceState, string, error) {
		return &incusapi.InstanceState{}, "", nil
	}

	tests := []struct {
		name          string
		url           string
		logfile       func() (io.ReadCloser, error)
		instanceState func(name string) (*incusapi.InstanceState, string, error)

		wantStatus int
	}{
		{
			name:          "open failure",
			url:           "/redfish/v1/Systems/test-instance/LogServices/HypervisorLog/Entries",
			logfile:       func() (io.ReadCloser, error) { return nil, errors.New("boom") },
			instanceState: instanceExists,

			wantStatus: http.StatusInternalServerError,
		},
		{
			name:          "open failure of a single entry",
			url:           "/redfish/v1/Systems/test-instance/LogServices/HypervisorLog/Entries/1",
			logfile:       func() (io.ReadCloser, error) { return nil, errors.New("boom") },
			instanceState: instanceExists,

			wantStatus: http.StatusInternalServerError,
		},
		{
			name:          "missing permission",
			url:           "/redfish/v1/Systems/test-instance/LogServices/HypervisorLog/Entries",
			logfile:       func() (io.ReadCloser, error) { return nil, incusapi.StatusErrorf(http.StatusForbidden, "forbidden") },
			instanceState: instanceExists,

			wantStatus: http.StatusForbidden,
		},
		{
			name:    "missing instance",
			url:     "/redfish/v1/Systems/test-instance/LogServices/HypervisorLog/Entries",
			logfile: func() (io.ReadCloser, error) { return nil, incusapi.StatusErrorf(http.StatusNotFound, "not found") },
			instanceState: func(name string) (*incusapi.InstanceState, string, error) {
				return nil, "", incusapi.StatusErrorf(http.StatusNotFound, "instance not found")
			},

			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			incusClient := &mock.IncusClientMock{
				GetInstanceLogfileFunc: func(name string, filename string) (io.ReadCloser, error) {
					return tc.logfile()
				},
				GetInstanceStateFunc: tc.instanceState,
			}

			status, _ := rawRequest(t, incusClient, http.MethodGet, tc.url)

			require.Equal(t, tc.wantStatus, status, "a failure of Incus is an error")
		})
	}
}

func logfileNames(client *mock.IncusClientMock) []string {
	calls := client.GetInstanceLogfileCalls()

	names := make([]string, 0, len(calls))
	for _, call := range calls {
		names = append(names, call.Filename)
	}

	return names
}
