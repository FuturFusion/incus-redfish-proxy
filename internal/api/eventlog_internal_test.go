package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
	"github.com/stretchr/testify/require"
)

func TestSystemEventLog_record(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 26, 12, 0, 0, 0, time.FixedZone("CET", 3600))
	alice := &incusapi.EventLifecycleRequestor{Username: "alice", Protocol: "tls", Address: "10.0.0.1"}

	tests := []struct {
		name      string
		lifecycle incusapi.EventLifecycle

		wantEntries []LogEntryV1220LogEntry
	}{
		{
			name:      "start with requestor",
			lifecycle: incusapi.EventLifecycle{Action: incusapi.EventLifecycleInstanceStarted, Source: "/1.0/instances/test-instance?project=foo", Requestor: alice},

			wantEntries: []LogEntryV1220LogEntry{eventEntry(t, at, "The instance was started by alice over tls.", LogEntryV1220EventSeverityOK)},
		},
		{
			name:      "shutdown without requestor",
			lifecycle: incusapi.EventLifecycle{Action: incusapi.EventLifecycleInstanceShutdown, Source: "/1.0/instances/test-instance"},

			wantEntries: []LogEntryV1220LogEntry{eventEntry(t, at, "The instance was shut down by an unknown requestor.", LogEntryV1220EventSeverityWarning)},
		},
		{
			name:      "requestor without protocol and address",
			lifecycle: incusapi.EventLifecycle{Action: incusapi.EventLifecycleInstanceStopped, Source: "/1.0/instances/test-instance", Requestor: &incusapi.EventLifecycleRequestor{Username: "bob"}},

			wantEntries: []LogEntryV1220LogEntry{eventEntry(t, at, "The instance was stopped by bob.", LogEntryV1220EventSeverityOK)},
		},
		{
			name:      "snapshot of the instance",
			lifecycle: incusapi.EventLifecycle{Action: incusapi.EventLifecycleInstanceSnapshotCreated, Source: "/1.0/instances/test-instance/snapshots/snap0", Requestor: alice},

			wantEntries: []LogEntryV1220LogEntry{eventEntry(t, at, "The snapshot snap0 was created by alice over tls.", LogEntryV1220EventSeverityOK)},
		},
		{
			name:      "action without requestor argument",
			lifecycle: incusapi.EventLifecycle{Action: incusapi.EventLifecycleInstanceAgentStarted, Source: "/1.0/instances/test-instance"},

			wantEntries: []LogEntryV1220LogEntry{eventEntry(t, at, "The guest agent started.", LogEntryV1220EventSeverityOK)},
		},
		{
			name:      "other instance",
			lifecycle: incusapi.EventLifecycle{Action: incusapi.EventLifecycleInstanceStarted, Source: "/1.0/instances/test-instance-2"},

			wantEntries: []LogEntryV1220LogEntry{},
		},
		{
			name:      "action caused by reading the logs",
			lifecycle: incusapi.EventLifecycle{Action: incusapi.EventLifecycleInstanceLogRetrieved, Source: "/1.0/instances/test-instance/logs/qemu.qmp.log"},

			wantEntries: []LogEntryV1220LogEntry{},
		},
		{
			name:      "invalid source",
			lifecycle: incusapi.EventLifecycle{Action: incusapi.EventLifecycleInstanceStarted, Source: "%zz"},

			wantEntries: []LogEntryV1220LogEntry{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := &systemEventLog{instanceName: "test-instance"}

			log.record(at, tc.lifecycle)
			entries, err := log.entries("")

			require.NoError(t, err, "the event log has no failure")
			require.Equal(t, tc.wantEntries, entries, "the event log records the allowed events of the instance")
		})
	}
}

func TestSystemEventLog_wrapsWhenFull(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	log := &systemEventLog{instanceName: "test-instance"}

	for i := range eventLogCapacity + 2 {
		log.record(start.Add(time.Duration(i)*time.Second), incusapi.EventLifecycle{Action: incusapi.EventLifecycleInstanceStarted, Source: "/1.0/instances/test-instance"})
	}

	entries, err := log.entries("")

	require.NoError(t, err, "the event log has no failure")
	require.Len(t, entries, eventLogCapacity, "the event log keeps a fixed number of events")
	require.Equal(t, strconv.Itoa(eventLogCapacity+2), entries[0].ID, "the newest event comes first")
	require.Equal(t, "3", entries[eventLogCapacity-1].ID, "the oldest events are dropped")
}

func TestRedfishServer_EventLogService(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 26, 12, 0, 0, 500, time.FixedZone("CET", 3600))

	tests := []struct {
		name      string
		configure func(log *systemEventLog)

		wantState string
	}{
		{
			name:      "without event source",
			configure: func(log *systemEventLog) {},

			wantState: `"ServiceEnabled": false, "Status": {"State": "Disabled"}`,
		},
		{
			name:      "with event source",
			configure: func(log *systemEventLog) { log.watched = true },

			wantState: `"ServiceEnabled": true, "Status": {"State": "Enabled", "Health": "OK"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			service := NewHandler("test-instance", instanceStateClient{state: &incusapi.InstanceState{}}, WithClock(func() time.Time { return now }))
			tc.configure(service.server.eventLog)

			httpserver := httptest.NewServer(service)
			t.Cleanup(httpserver.Close)

			resp, err := httpserver.Client().Get(httpserver.URL + "/redfish/v1/Systems/test-instance/LogServices/EventLog")
			require.NoError(t, err, "get the event log")

			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err, "read the event log")

			require.JSONEq(t, `{
				"@odata.id": "/redfish/v1/Systems/test-instance/LogServices/EventLog",
				"@odata.type": "#LogService.v1_9_0.LogService",
				"DateTime": "2026-11-26T11:00:00Z",
				"DateTimeLocalOffset": "+00:00",
				"Entries": {"@odata.id": "/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries"},
				"Id": "EventLog",
				"LogEntryType": "Event",
				"LogPurposes": ["Operations"],
				"MaxNumberOfRecords": 1000,
				"Name": "Event Log",
				"OverWritePolicy": "WrapsWhenFull",
				"Persistency": false,
				`+tc.wantState+`
			}`, string(body), "the event log has the fields of a Redfish log service")
		})
	}
}

func TestLifecycleMessages_placeholders(t *testing.T) {
	t.Parallel()

	placeholder := regexp.MustCompile(`%([0-9]+)`)

	for action, message := range lifecycleMessages {
		t.Run(action, func(t *testing.T) {
			t.Parallel()

			args := message.args("s", "r")

			indices := []int{}
			for _, match := range placeholder.FindAllStringSubmatch(message.message, -1) {
				index, err := strconv.Atoi(match[1])
				require.NoError(t, err, "the placeholder has a number")

				indices = append(indices, index)
			}

			wantIndices := []int{}
			for i := range args {
				wantIndices = append(wantIndices, i+1)
			}

			require.ElementsMatch(t, wantIndices, indices, "the message has one placeholder per argument")
			require.NotRegexp(t, placeholder, message.format(args), "the format replaces every placeholder")
		})
	}
}

func TestLifecycleMessage_format(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message string
		args    []string

		want string
	}{
		{
			name:    "argument with a placeholder",
			message: "The snapshot %1 was created by %2.",
			args:    []string{"x%2", "alice"},

			want: "The snapshot x%2 was created by alice.",
		},
		{
			name:    "ten arguments",
			message: "%1 %10",
			args:    []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},

			want: "a j",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := lifecycleMessage{message: tc.message}.format(tc.args)

			require.Equal(t, tc.want, got, "the format replaces each placeholder once")
		})
	}
}

func TestRedfishServer_EventLog(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	stoppedAt := startedAt.Add(time.Hour)

	service := NewHandler("test-instance", instanceStateClient{state: &incusapi.InstanceState{}})
	service.server.resets.handle(t.Context(), lifecycleEvent(t, incusapi.EventLifecycleInstanceStarted, "/1.0/instances/test-instance", startedAt))
	service.server.resets.handle(t.Context(), lifecycleEvent(t, incusapi.EventLifecycleInstanceStopped, "/1.0/instances/test-instance", stoppedAt))

	httpserver := httptest.NewServer(service)
	t.Cleanup(httpserver.Close)

	client, err := gofish.Connect(gofish.ClientConfig{Endpoint: httpserver.URL, HTTPClient: httpserver.Client()})
	require.NoError(t, err, "connect to the proxy")

	logService, err := schemas.GetObject[schemas.LogService](client, "/redfish/v1/Systems/test-instance/LogServices/EventLog")
	require.NoError(t, err, "get the event log")

	entries, err := logService.Entries()
	require.NoError(t, err, "get the entries of the event log")

	resp, err := client.Get("/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries/1")
	require.NoError(t, err, "get a single entry")

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read the entry")

	require.Equal(t, schemas.EventLogEntryTypes, logService.LogEntryType, "the event log holds Redfish events")
	require.Equal(t, uint(eventLogCapacity), logService.MaxNumberOfRecords, "the event log has a fixed size")
	require.Equal(t, schemas.WrapsWhenFullLogServiceOverWritePolicy, logService.OverWritePolicy, "the oldest events go first")
	require.Len(t, entries, 2, "the event log holds both events")
	require.Equal(t, "The instance was stopped by an unknown requestor.", entries[0].Message, "the newest event comes first")
	require.Equal(t, stoppedAt.Format(time.RFC3339), entries[0].Created, "the time of the event is the creation time of the entry")
	require.Equal(t, schemas.EventLogEntryType, entries[0].EntryType, "the entry is a Redfish event")
	require.Equal(t, schemas.OKEventSeverity, entries[0].Severity, "a stop is no warning")
	require.Equal(t, http.StatusOK, resp.StatusCode, "a single entry is served")
	require.JSONEq(t, `{
		"@odata.id": "/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries/1",
		"@odata.type": "#LogEntry.v1_22_0.LogEntry",
		"Created": "2026-11-26T12:00:00Z",
		"EntryType": "Event",
		"Id": "1",
		"Message": "The instance was started by an unknown requestor.",
		"Name": "Event Log Entry 1",
		"Severity": "OK"
	}`, string(body), "the entry has the fields of a Redfish event")
}

// eventEntry builds the first entry of the event log.
func eventEntry(t *testing.T, at time.Time, message string, severity LogEntryV1220EventSeverity) LogEntryV1220LogEntry {
	t.Helper()

	entrySeverity := LogEntryV1220LogEntry_Severity{}
	err := entrySeverity.FromLogEntryV1220EventSeverity(severity)
	require.NoError(t, err, "set the severity")

	return LogEntryV1220LogEntry{
		ID:        "1",
		Name:      "Event Log Entry 1",
		EntryType: LogEntryV1220LogEntryTypeEvent,
		Created:   ref(at.UTC()),
		Message:   ref(message),
		Severity:  &entrySeverity,
	}
}
