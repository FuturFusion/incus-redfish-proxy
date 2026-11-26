package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
)

const (
	hypervisorLogID = "HypervisorLog"

	// hypervisorLogCapacity is the number of QMP events the hypervisor log shows. Older events are left out.
	hypervisorLogCapacity = 1000

	qmpLogFile = "qemu.qmp.log"

	qmpMaxLineSize = 64 * 1024
)

var qmpEventMarker = []byte("] Event: ")

var qmpEventSeverities = map[string]LogEntryV1220EventSeverity{
	"BLOCK_IO_ERROR": LogEntryV1220EventSeverityCritical,
	"GUEST_PANICKED": LogEntryV1220EventSeverityCritical,
	"WATCHDOG":       LogEntryV1220EventSeverityWarning,
}

// hypervisorLog shows the QMP (QEMU Machine Protocol) events that Incus logs to qemu.qmp.log.
// Guest resets keep QEMU running, so these events can span multiple guest boots.
type hypervisorLog struct {
	instanceName string
	client       IncusClient
}

type qmpEvent struct {
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data"`
	Timestamp *struct {
		Seconds      int64 `json:"seconds"`
		Microseconds int64 `json:"microseconds"`
	} `json:"timestamp"`
}

func (l hypervisorLog) resource() LogServiceV190LogService {
	entryType := LogServiceV190LogService_LogEntryType{}
	_ = entryType.FromLogServiceV190LogEntryTypes(LogServiceV190LogEntryTypesOEM)

	purpose := LogServiceV190LogService_LogPurposes_Item{}
	_ = purpose.FromLogServiceV190LogPurpose(LogServiceV190LogPurposeDiagnostic)

	return LogServiceV190LogService{
		ID:                 hypervisorLogID,
		Name:               "Hypervisor Log",
		LogEntryType:       &entryType,
		LogPurposes:        &[]LogServiceV190LogService_LogPurposes_Item{purpose},
		MaxNumberOfRecords: ref(int64(hypervisorLogCapacity)),
		OverWritePolicy:    ref(LogServiceV190OverWritePolicyWrapsWhenFull),
	}
}

func (l hypervisorLog) entry(path string, id string) (LogEntryV1220LogEntry, bool, error) {
	entries, err := l.entries(path)
	if err != nil {
		return LogEntryV1220LogEntry{}, false, err
	}

	entry, ok := findLogEntry(entries, id)

	return entry, ok, nil
}

func (l hypervisorLog) entries(_ string) ([]LogEntryV1220LogEntry, error) {
	content, err := l.client.GetInstanceLogfile(l.instanceName, qmpLogFile)
	if incusapi.StatusErrorCheck(err, http.StatusNotFound) {
		// Incus also answers 404 for a missing instance.
		_, _, err = l.client.GetInstanceState(l.instanceName)
		if err != nil {
			return nil, fmt.Errorf("get the state of instance %q: %w", l.instanceName, err)
		}

		// The instance did not start yet.
		return []LogEntryV1220LogEntry{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("open the %s of instance %q: %w", qmpLogFile, l.instanceName, err)
	}

	defer content.Close()

	entries, err := parseQMPLog(content)
	if err != nil {
		return nil, fmt.Errorf("read the %s of instance %q: %w", qmpLogFile, l.instanceName, err)
	}

	return entries, nil
}

// parseQMPLog returns the newest events of the log, newest first. The Id of an entry is its line number.
func parseQMPLog(content io.Reader) ([]LogEntryV1220LogEntry, error) {
	reader := bufio.NewReaderSize(content, qmpMaxLineSize)

	// newest keeps only the latest events to bound memory use. The log of a long running instance can be large.
	newest := make([]LogEntryV1220LogEntry, 0, hypervisorLogCapacity)
	count := 0

	for lineNumber := 1; ; lineNumber++ {
		line, err := readQMPLine(reader)
		entry, ok := parseQMPEvent(lineNumber, line)
		if ok {
			if len(newest) < hypervisorLogCapacity {
				newest = append(newest, entry)
			} else {
				newest[count%hypervisorLogCapacity] = entry
			}

			count++
		}

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, err
		}
	}

	entries := make([]LogEntryV1220LogEntry, 0, len(newest))
	for i := range newest {
		entries = append(entries, newest[(count-1-i)%len(newest)])
	}

	return entries, nil
}

// readQMPLine returns the next line without the newline. A line longer than the buffer of the reader is skipped
// and returned empty. Skip oversized lines to bound memory use. Query replies can exceed this limit.
func readQMPLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if !errors.Is(err, bufio.ErrBufferFull) {
		return bytes.TrimSuffix(line, []byte("\n")), err
	}

	for errors.Is(err, bufio.ErrBufferFull) {
		_, err = reader.ReadSlice('\n')
	}

	return nil, err
}

// parseQMPEvent parses a line like "[2026-11-26T10:00:00+01:00] Event: {...}". Queries and replies are not events.
func parseQMPEvent(lineNumber int, line []byte) (LogEntryV1220LogEntry, bool) {
	if !bytes.HasPrefix(line, []byte("[")) {
		return LogEntryV1220LogEntry{}, false
	}

	loggedAt, raw, ok := bytes.Cut(line[1:], qmpEventMarker)
	if !ok {
		return LogEntryV1220LogEntry{}, false
	}

	event := qmpEvent{}

	err := json.Unmarshal(raw, &event)
	if err != nil || event.Event == "" {
		return LogEntryV1220LogEntry{}, false
	}

	message := event.Event
	if len(event.Data) > 0 && !bytes.Equal(event.Data, []byte("null")) {
		message += " " + string(event.Data)
	}

	severity := LogEntryV1220LogEntry_Severity{}
	_ = severity.FromLogEntryV1220EventSeverity(qmpEventSeverity(event.Event))

	id := strconv.Itoa(lineNumber)

	return LogEntryV1220LogEntry{
		ID:              id,
		Name:            "Hypervisor Log Entry " + id,
		EntryType:       LogEntryV1220LogEntryTypeOem,
		OemRecordFormat: ref("QEMU QMP"),
		Created:         qmpEventTime(event, string(loggedAt)),
		Message:         ref(message),
		Severity:        &severity,
	}, true
}

func qmpEventSeverity(event string) LogEntryV1220EventSeverity {
	severity, ok := qmpEventSeverities[event]
	if !ok {
		return LogEntryV1220EventSeverityOK
	}

	return severity
}

// qmpEventTime prefers the time of QEMU, which has microseconds. Incus only logs seconds.
func qmpEventTime(event qmpEvent, loggedAt string) *time.Time {
	if event.Timestamp != nil && event.Timestamp.Seconds > 0 {
		return ref(time.Unix(event.Timestamp.Seconds, event.Timestamp.Microseconds*int64(time.Microsecond)).UTC())
	}

	at, err := time.Parse(time.RFC3339, loggedAt)
	if err != nil {
		return nil
	}

	return ref(at.UTC())
}
