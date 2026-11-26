package api

import (
	"fmt"
	"log"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
)

const (
	eventLogID = "EventLog"

	// Drop the oldest event when the log reaches this limit.
	eventLogCapacity = 1000
)

type lifecycleArgs func(subject string, requestor string) []string

func noArgs(string, string) []string {
	return nil
}

func requestorArg(_ string, requestor string) []string {
	return []string{requestor}
}

func subjectAndRequestorArgs(subject string, requestor string) []string {
	return []string{subject, requestor}
}

type lifecycleMessage struct {
	key      string
	message  string
	severity LogEntryV1220EventSeverity
	args     lifecycleArgs
}

func (m lifecycleMessage) eventSeverity() LogEntryV1220EventSeverity {
	if m.severity == "" {
		return LogEntryV1220EventSeverityOK
	}

	return m.severity
}

// format replaces the placeholders %1, %2, and so on with the arguments, like a Redfish client does with a message registry.
// A single pass keeps placeholders in the arguments. The highest index goes first, so %1 does not match inside %10.
func (m lifecycleMessage) format(args []string) string {
	pairs := make([]string, 0, 2*len(args))
	for i := len(args) - 1; i >= 0; i-- {
		pairs = append(pairs, "%"+strconv.Itoa(i+1), args[i])
	}

	return strings.NewReplacer(pairs...).Replace(m.message)
}

// lifecycleMessages lists the lifecycle actions the event log records.
// The other actions are left out. The proxy causes some of them itself, for example by reading the logs.
var lifecycleMessages = map[string]lifecycleMessage{
	incusapi.EventLifecycleInstanceCreated:         {key: "InstanceCreated", message: "The instance was created by %1.", args: requestorArg},
	incusapi.EventLifecycleInstanceDeleted:         {key: "InstanceDeleted", message: "The instance was deleted by %1.", args: requestorArg},
	incusapi.EventLifecycleInstanceStarted:         {key: "InstanceStarted", message: "The instance was started by %1.", args: requestorArg},
	incusapi.EventLifecycleInstanceStopped:         {key: "InstanceStopped", message: "The instance was stopped by %1.", args: requestorArg},
	incusapi.EventLifecycleInstanceShutdown:        {key: "InstanceShutdown", message: "The instance was shut down by %1.", severity: LogEntryV1220EventSeverityWarning, args: requestorArg},
	incusapi.EventLifecycleInstanceRestarted:       {key: "InstanceRestarted", message: "The instance was restarted by %1.", args: requestorArg},
	incusapi.EventLifecycleInstancePaused:          {key: "InstancePaused", message: "The instance was paused by %1.", args: requestorArg},
	incusapi.EventLifecycleInstanceResumed:         {key: "InstanceResumed", message: "The instance was resumed by %1.", args: requestorArg},
	incusapi.EventLifecycleInstanceReady:           {key: "InstanceReady", message: "The instance is ready.", args: noArgs},
	incusapi.EventLifecycleInstanceUpdated:         {key: "InstanceUpdated", message: "The configuration of the instance was changed by %1.", args: requestorArg},
	incusapi.EventLifecycleInstanceAgentStarted:    {key: "AgentStarted", message: "The guest agent started.", args: noArgs},
	incusapi.EventLifecycleInstanceAgentStopped:    {key: "AgentStopped", message: "The guest agent stopped.", args: noArgs},
	incusapi.EventLifecycleInstanceRenamed:         {key: "InstanceRenamed", message: "The instance was renamed by %1.", args: requestorArg},
	incusapi.EventLifecycleInstanceMigrated:        {key: "InstanceMigrated", message: "The instance was migrated by %1.", args: requestorArg},
	incusapi.EventLifecycleInstanceRestored:        {key: "InstanceRestored", message: "The instance was restored from a snapshot by %1.", args: requestorArg},
	incusapi.EventLifecycleInstanceSnapshotCreated: {key: "SnapshotCreated", message: "The snapshot %1 was created by %2.", args: subjectAndRequestorArgs},
	incusapi.EventLifecycleInstanceSnapshotDeleted: {key: "SnapshotDeleted", message: "The snapshot %1 was deleted by %2.", args: subjectAndRequestorArgs},
	incusapi.EventLifecycleInstanceSnapshotRenamed: {key: "SnapshotRenamed", message: "The snapshot %1 was renamed by %2.", args: subjectAndRequestorArgs},
	incusapi.EventLifecycleInstanceSnapshotUpdated: {key: "SnapshotUpdated", message: "The snapshot %1 was changed by %2.", args: subjectAndRequestorArgs},
	incusapi.EventLifecycleInstanceBackupCreated:   {key: "BackupCreated", message: "The backup %1 was created by %2.", args: subjectAndRequestorArgs},
	incusapi.EventLifecycleInstanceBackupDeleted:   {key: "BackupDeleted", message: "The backup %1 was deleted by %2.", args: subjectAndRequestorArgs},
	incusapi.EventLifecycleInstanceBackupRenamed:   {key: "BackupRenamed", message: "The backup %1 was renamed by %2.", args: subjectAndRequestorArgs},
}

type eventRecord struct {
	id      uint64
	at      time.Time
	message lifecycleMessage
	args    []string
}

// systemEventLog keeps the latest lifecycle events of the instance in memory. Incus has no history of its events.
type systemEventLog struct {
	instanceName string

	// watched is false without an event source. The log then stays empty.
	watched bool

	mu      sync.Mutex
	records []eventRecord
	lastID  uint64
}

func (l *systemEventLog) record(at time.Time, lifecycle incusapi.EventLifecycle) {
	message, ok := lifecycleMessages[lifecycle.Action]
	if !ok {
		return
	}

	source, err := url.Parse(lifecycle.Source)
	if err != nil {
		log.Printf("event log of instance %q drops lifecycle event %s with source %q: %v", l.instanceName, lifecycle.Action, lifecycle.Source, err)
		return
	}

	// Snapshots and backups have their own path below the path of the instance.
	instancePath := "/1.0/instances/" + l.instanceName
	if source.Path != instancePath && !strings.HasPrefix(source.Path, instancePath+"/") {
		return
	}

	args := message.args(path.Base(source.Path), requestorText(lifecycle.Requestor))

	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.records) == eventLogCapacity {
		l.records = append(l.records[:0], l.records[1:]...)
	}

	l.lastID++
	l.records = append(l.records, eventRecord{id: l.lastID, at: at.UTC(), message: message, args: args})
}

func requestorText(requestor *incusapi.EventLifecycleRequestor) string {
	if requestor == nil || requestor.Username == "" {
		return "an unknown requestor"
	}

	// Leave out the address of the Incus client. A Redfish client does not need it.
	if requestor.Protocol == "" {
		return requestor.Username
	}

	return requestor.Username + " over " + requestor.Protocol
}

func (l *systemEventLog) resource() LogServiceV190LogService {
	entryType := LogServiceV190LogService_LogEntryType{}
	_ = entryType.FromLogServiceV190LogEntryTypes(LogServiceV190LogEntryTypesEvent)

	purpose := LogServiceV190LogService_LogPurposes_Item{}
	_ = purpose.FromLogServiceV190LogPurpose(LogServiceV190LogPurposeOperations)

	resource := LogServiceV190LogService{
		ID:                 eventLogID,
		Name:               "Event Log",
		LogEntryType:       &entryType,
		LogPurposes:        &[]LogServiceV190LogService_LogPurposes_Item{purpose},
		MaxNumberOfRecords: ref(int64(eventLogCapacity)),
		OverWritePolicy:    ref(LogServiceV190OverWritePolicyWrapsWhenFull),
	}

	if !l.watched {
		state := ResourceStatus_State{}
		_ = state.FromResourceState(ResourceStateDisabled)

		resource.ServiceEnabled = ref(false)
		resource.Status = &ResourceStatus{State: &state}
	}

	return resource
}

func (l *systemEventLog) entry(servicePath string, id string) (LogEntryV1220LogEntry, bool, error) {
	entries, err := l.entries(servicePath)
	if err != nil {
		return LogEntryV1220LogEntry{}, false, err
	}

	entry, ok := findLogEntry(entries, id)

	return entry, ok, nil
}

func (l *systemEventLog) entries(_ string) ([]LogEntryV1220LogEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries := make([]LogEntryV1220LogEntry, 0, len(l.records))
	for i := len(l.records) - 1; i >= 0; i-- {
		record := l.records[i]

		severity := LogEntryV1220LogEntry_Severity{}
		_ = severity.FromLogEntryV1220EventSeverity(record.message.eventSeverity())

		entries = append(entries, LogEntryV1220LogEntry{
			ID:        strconv.FormatUint(record.id, 10),
			Name:      fmt.Sprintf("Event Log Entry %d", record.id),
			EntryType: LogEntryV1220LogEntryTypeEvent,
			Created:   ref(record.at),
			Message:   ref(record.message.format(record.args)),
			Severity:  &severity,
		})
	}

	return entries, nil
}
