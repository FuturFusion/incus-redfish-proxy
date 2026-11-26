package api

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseQMPLog(t *testing.T) {
	t.Parallel()

	loggedAt := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	qemuAt := time.Date(2026, 11, 26, 9, 0, 1, 250000000, time.UTC)

	tests := []struct {
		name    string
		content io.Reader

		assertErr   require.ErrorAssertionFunc
		wantEntries []LogEntryV1220LogEntry
	}{
		{
			name: "events between queries and replies",
			content: strings.NewReader(`[2026-11-26T10:00:00+01:00] QUERY: {"execute":"query-status"}
[2026-11-26T10:00:00+01:00] REPLY: {"return":{"status":"running"}}

[2026-11-26T10:00:00+01:00] Event: {"timestamp":{"seconds":1795683601,"microseconds":250000},"event":"SHUTDOWN","data":{"guest":true,"reason":"guest-shutdown"}}
[2026-11-26T10:00:00+01:00] Event: {"event":"GUEST_PANICKED","data":{"action":"pause"}}
`),

			assertErr: require.NoError,
			wantEntries: []LogEntryV1220LogEntry{
				hypervisorEntry(t, "5", &loggedAt, `GUEST_PANICKED {"action":"pause"}`, LogEntryV1220EventSeverityCritical),
				hypervisorEntry(t, "4", &qemuAt, `SHUTDOWN {"guest":true,"reason":"guest-shutdown"}`, LogEntryV1220EventSeverityOK),
			},
		},
		{
			name:    "event without data and last line without newline",
			content: strings.NewReader(`[2026-11-26T10:00:00+01:00] Event: {"event":"WATCHDOG","data":null}`),

			assertErr:   require.NoError,
			wantEntries: []LogEntryV1220LogEntry{hypervisorEntry(t, "1", &loggedAt, "WATCHDOG", LogEntryV1220EventSeverityWarning)},
		},
		{
			name:    "event without valid time",
			content: strings.NewReader(`[yesterday] Event: {"event":"RESET"}`),

			assertErr:   require.NoError,
			wantEntries: []LogEntryV1220LogEntry{hypervisorEntry(t, "1", nil, "RESET", LogEntryV1220EventSeverityOK)},
		},
		{
			name: "lines that are no events",
			content: strings.NewReader(`garbage
[2026-11-26T10:00:00+01:00] Event: not json
[2026-11-26T10:00:00+01:00] Event: {"data":{}}
`),

			assertErr:   require.NoError,
			wantEntries: []LogEntryV1220LogEntry{},
		},
		{
			name:    "line longer than the limit",
			content: strings.NewReader(`[2026-11-26T10:00:00+01:00] REPLY: ` + strings.Repeat("x", qmpMaxLineSize) + "\n" + `[2026-11-26T10:00:00+01:00] Event: {"event":"RESET"}` + "\n"),

			assertErr:   require.NoError,
			wantEntries: []LogEntryV1220LogEntry{hypervisorEntry(t, "2", &loggedAt, "RESET", LogEntryV1220EventSeverityOK)},
		},
		{
			name:    "read failure",
			content: iotest.ErrReader(errors.New("boom")),

			assertErr:   require.Error,
			wantEntries: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entries, err := parseQMPLog(tc.content)

			tc.assertErr(t, err, "only a failed read is an error")
			require.Equal(t, tc.wantEntries, entries, "the events become entries, newest first")
		})
	}
}

func TestParseQMPLog_capacity(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("[2026-11-26T10:00:00+01:00] Event: {\"event\":\"RESET\"}\n", hypervisorLogCapacity+2)

	entries, err := parseQMPLog(strings.NewReader(content))

	require.NoError(t, err, "the log is valid")
	require.Len(t, entries, hypervisorLogCapacity, "only the newest events are shown")
	require.Equal(t, "1002", entries[0].ID, "the newest event comes first")
	require.Equal(t, "3", entries[hypervisorLogCapacity-1].ID, "the oldest events are left out")
}

func hypervisorEntry(t *testing.T, id string, created *time.Time, message string, severity LogEntryV1220EventSeverity) LogEntryV1220LogEntry {
	t.Helper()

	entrySeverity := LogEntryV1220LogEntry_Severity{}
	err := entrySeverity.FromLogEntryV1220EventSeverity(severity)
	require.NoError(t, err, "set the severity")

	return LogEntryV1220LogEntry{
		ID:              id,
		Name:            "Hypervisor Log Entry " + id,
		EntryType:       LogEntryV1220LogEntryTypeOem,
		OemRecordFormat: ref("QEMU QMP"),
		Created:         created,
		Message:         ref(message),
		Severity:        &entrySeverity,
	}
}
