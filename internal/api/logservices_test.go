package api_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
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
		{url: "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/Unknown", client: func() *mock.IncusClientMock { return diagnosticClient(time.Time{}) }},
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

	resp, err := client.Get("/redfish/v1/Systems/test-instance/LogServices")
	require.NoError(t, err, "get the log service collection")

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read the log service collection")

	require.Equal(t, []string{"EventLog", "HypervisorLog", "Diagnostic"}, logServiceIDs(services), "the system links its log services")
	require.JSONEq(t, `{
		"@odata.id": "/redfish/v1/Systems/test-instance/LogServices",
		"@odata.type": "#LogServiceCollection.LogServiceCollection",
		"Members": [
			{"@odata.id": "/redfish/v1/Systems/test-instance/LogServices/EventLog"},
			{"@odata.id": "/redfish/v1/Systems/test-instance/LogServices/HypervisorLog"},
			{"@odata.id": "/redfish/v1/Systems/test-instance/LogServices/Diagnostic"}
		],
		"Members@odata.count": 3,
		"Name": "Log Service Collection"
	}`, string(body), "the collection lists every log service")
}

func logServiceIDs(services []*schemas.LogService) []string {
	ids := make([]string, 0, len(services))
	for _, service := range services {
		ids = append(ids, service.ID)
	}

	return ids
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

func entryIDs(entries []*schemas.LogEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}

	return ids
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
			name:          "read failure",
			url:           "/redfish/v1/Systems/test-instance/LogServices/HypervisorLog/Entries",
			logfile:       func() (io.ReadCloser, error) { return io.NopCloser(iotest.ErrReader(errors.New("boom"))), nil },
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

func diagnosticClient(startedAt time.Time) *mock.IncusClientMock {
	return &mock.IncusClientMock{
		GetInstanceStateFunc: func(name string) (*incusapi.InstanceState, string, error) {
			return &incusapi.InstanceState{StartedAt: startedAt}, "", nil
		},
		GetInstanceConsoleLogFunc: func(instanceName string, args *incusclient.InstanceConsoleLogArgs) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("Booting\nlogin: ")), nil
		},
		GetInstanceLogfileFunc: func(name string, filename string) (io.ReadCloser, error) {
			if filename != "qemu.log" {
				return nil, incusapi.StatusErrorf(http.StatusNotFound, "not found")
			}

			return io.NopCloser(strings.NewReader("qemu warning\n")), nil
		},
	}
}

func TestRedfishServer_DiagnosticLog(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	client := setup(t, diagnosticClient(startedAt))

	logService, err := schemas.GetObject[schemas.LogService](client, "/redfish/v1/Systems/test-instance/LogServices/Diagnostic")
	require.NoError(t, err, "get the diagnostic log")

	entries, err := logService.Entries()
	require.NoError(t, err, "get the entries of the diagnostic log")
	require.Len(t, entries, 2, "the diagnostic log has an entry per existing log")

	resp, err := client.Get("/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/SerialConsole")
	require.NoError(t, err, "get the entry of the console log")

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read the entry")

	require.Equal(t, []schemas.LogPurpose{schemas.DiagnosticLogPurpose}, logService.LogPurposes, "the logs help to diagnose a failure")
	require.Equal(t, []string{"SerialConsole", "QEMU"}, entryIDs(entries), "a missing log has no entry")
	require.Equal(t, schemas.OEMLogDiagnosticDataTypes, entries[1].DiagnosticDataType, "the QEMU log is OEM data")
	require.Equal(t, "QEMU", entries[1].OEMDiagnosticDataType, "the QEMU log names its content")
	require.JSONEq(t, `{
		"@odata.id": "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/SerialConsole",
		"@odata.type": "#LogEntry.v1_22_0.LogEntry",
		"AdditionalDataSizeBytes": 15,
		"AdditionalDataURI": "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/SerialConsole/attachment",
		"Created": "2026-11-26T12:00:00Z",
		"DiagnosticDataType": "OS",
		"EntryType": "Oem",
		"Id": "SerialConsole",
		"Message": "Booting\nlogin: ",
		"Name": "Serial Console Log",
		"OemRecordFormat": "Incus",
		"Severity": "OK"
	}`, string(body), "the entry shows the console log")
}

func TestRedfishServer_DiagnosticLogStopped(t *testing.T) {
	t.Parallel()

	client := setup(t, diagnosticClient(time.Time{}))

	logService, err := schemas.GetObject[schemas.LogService](client, "/redfish/v1/Systems/test-instance/LogServices/Diagnostic")
	require.NoError(t, err, "get the diagnostic log")

	entries, err := logService.Entries()
	require.NoError(t, err, "get the entries of the diagnostic log")
	require.Len(t, entries, 2, "the diagnostic log has an entry per existing log")

	require.Empty(t, entries[0].Created, "without a start time, the creation time is unknown")
	require.Equal(t, "Booting\nlogin: ", entries[0].Message, "the logs of the last boot stay")
}

func TestRedfishServer_DiagnosticLogFailure(t *testing.T) {
	t.Parallel()

	stateFailure := func() *mock.IncusClientMock {
		client := diagnosticClient(time.Time{})
		client.GetInstanceStateFunc = func(name string) (*incusapi.InstanceState, string, error) {
			return nil, "", errors.New("boom")
		}

		return client
	}

	openFailure := func() *mock.IncusClientMock {
		client := diagnosticClient(time.Time{})
		client.GetInstanceConsoleLogFunc = func(instanceName string, args *incusclient.InstanceConsoleLogArgs) (io.ReadCloser, error) {
			return nil, errors.New("boom")
		}

		return client
	}

	readFailure := func() *mock.IncusClientMock {
		client := diagnosticClient(time.Time{})
		client.GetInstanceConsoleLogFunc = func(instanceName string, args *incusclient.InstanceConsoleLogArgs) (io.ReadCloser, error) {
			return io.NopCloser(io.MultiReader(strings.NewReader("partial log"), iotest.ErrReader(errors.New("boom")))), nil
		}

		return client
	}

	forbidden := func() *mock.IncusClientMock {
		client := diagnosticClient(time.Time{})
		client.GetInstanceConsoleLogFunc = func(instanceName string, args *incusclient.InstanceConsoleLogArgs) (io.ReadCloser, error) {
			return nil, incusapi.StatusErrorf(http.StatusForbidden, "forbidden")
		}

		return client
	}

	tests := []struct {
		name   string
		url    string
		client func() *mock.IncusClientMock

		wantStatus int
	}{
		{
			name:   "state failure",
			url:    "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries",
			client: stateFailure,

			wantStatus: http.StatusInternalServerError,
		},
		{
			name:   "state failure of a single entry",
			url:    "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/SerialConsole",
			client: stateFailure,

			wantStatus: http.StatusInternalServerError,
		},
		{
			name:   "open failure",
			url:    "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries",
			client: openFailure,

			wantStatus: http.StatusInternalServerError,
		},
		{
			name:   "read failure",
			url:    "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/SerialConsole",
			client: readFailure,

			wantStatus: http.StatusInternalServerError,
		},
		{
			name:   "attachment open failure",
			url:    "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/SerialConsole/attachment",
			client: openFailure,

			wantStatus: http.StatusInternalServerError,
		},
		{
			name:   "missing permission",
			url:    "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/SerialConsole",
			client: forbidden,

			wantStatus: http.StatusForbidden,
		},
		{
			name:   "missing permission for the attachment",
			url:    "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/SerialConsole/attachment",
			client: forbidden,

			wantStatus: http.StatusForbidden,
		},
		{
			name:   "failure of another log",
			url:    "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/QEMU",
			client: openFailure,

			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, _ := rawRequest(t, tc.client(), http.MethodGet, tc.url)

			require.Equal(t, tc.wantStatus, status, "only a failure of the requested log is an error")
		})
	}
}

func TestRedfishServer_DiagnosticAttachmentHead(t *testing.T) {
	t.Parallel()

	incusClient := diagnosticClient(time.Time{})
	incusClient.GetInstanceConsoleLogFunc = func(instanceName string, args *incusclient.InstanceConsoleLogArgs) (io.ReadCloser, error) {
		return io.NopCloser(iotest.ErrReader(errors.New("the log must not be read"))), nil
	}

	status, header := rawRequest(t, incusClient, http.MethodHead, "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/SerialConsole/attachment")

	require.Equal(t, http.StatusOK, status, "a HEAD request of an existing log succeeds")
	require.Equal(t, "text/plain; charset=utf-8", header.Get("Content-Type"), "the attachment is plain text")
}

func TestRedfishServer_DiagnosticAttachment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url string

		wantStatus      int
		wantContentType string
		assertBody      func(t *testing.T, body string)
	}{
		{
			url: "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/SerialConsole/attachment",

			wantStatus:      http.StatusOK,
			wantContentType: "text/plain; charset=utf-8",
			assertBody:      bodyEqual("Booting\nlogin: "),
		},
		{
			url: "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/QEMU/attachment",

			wantStatus:      http.StatusOK,
			wantContentType: "text/plain; charset=utf-8",
			assertBody:      bodyEqual("qemu warning\n"),
		},
		{
			url: "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/QEMUEarly/attachment",

			wantStatus:      http.StatusNotFound,
			wantContentType: "application/json; charset=utf-8",
			assertBody:      bodyJSONEq(`{"error":{"code":"error","message":"Not Found"}}`),
		},
		{
			url: "/redfish/v1/Systems/test-instance/LogServices/Diagnostic/Entries/Unknown/attachment",

			wantStatus:      http.StatusNotFound,
			wantContentType: "application/json; charset=utf-8",
			assertBody:      bodyJSONEq(`{"error":{"code":"error","message":"Not Found"}}`),
		},
		{
			url: "/redfish/v1/Systems/invalid/LogServices/Diagnostic/Entries/QEMU/attachment",

			wantStatus:      http.StatusNotFound,
			wantContentType: "application/json; charset=utf-8",
			assertBody:      bodyJSONEq(`{"error":{"code":"error","message":"Not Found"}}`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(api.NewHandler("test-instance", diagnosticClient(time.Time{})))
			t.Cleanup(server.Close)

			resp, err := server.Client().Get(server.URL + tc.url)
			require.NoError(t, err, "get the attachment")

			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err, "read the attachment")

			require.Equal(t, tc.wantStatus, resp.StatusCode, "only an existing log has an attachment")
			require.Equal(t, tc.wantContentType, resp.Header.Get("Content-Type"), "the attachment is plain text")
			tc.assertBody(t, string(body))
		})
	}
}

func bodyEqual(want string) func(t *testing.T, body string) {
	return func(t *testing.T, body string) {
		t.Helper()

		require.Equal(t, want, body, "the attachment is the full log")
	}
}

func bodyJSONEq(want string) func(t *testing.T, body string) {
	return func(t *testing.T, body string) {
		t.Helper()

		require.JSONEq(t, want, body, "the answer is a Redfish error")
	}
}
