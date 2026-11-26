package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	incusapi "github.com/lxc/incus/v7/shared/api"
)

const (
	diagnosticLogID = "Diagnostic"

	diagnosticMessageSize = 4096
)

type diagnosticFile struct {
	id       string
	name     string
	dataType LogEntryV1220LogDiagnosticDataTypes

	// oemDataType names the content if dataType is OEM.
	oemDataType string

	open func(client IncusClient, instanceName string) (io.ReadCloser, error)
}

var diagnosticFiles = []diagnosticFile{
	{
		id:       "SerialConsole",
		name:     "Serial Console Log",
		dataType: LogEntryV1220LogDiagnosticDataTypesOS,
		open: func(client IncusClient, instanceName string) (io.ReadCloser, error) {
			return client.GetInstanceConsoleLog(instanceName, &incusclient.InstanceConsoleLogArgs{})
		},
	},
	{
		id:          "QEMU",
		name:        "QEMU Log",
		dataType:    LogEntryV1220LogDiagnosticDataTypesOEM,
		oemDataType: "QEMU",
		open:        openLogfile("qemu.log"),
	},
	{
		id:          "QEMUEarly",
		name:        "QEMU Early Log",
		dataType:    LogEntryV1220LogDiagnosticDataTypesOEM,
		oemDataType: "QEMU",
		open:        openLogfile("qemu.early.log"),
	},
}

func openLogfile(filename string) func(client IncusClient, instanceName string) (io.ReadCloser, error) {
	return func(client IncusClient, instanceName string) (io.ReadCloser, error) {
		return client.GetInstanceLogfile(instanceName, filename)
	}
}

func lookupDiagnosticFile(id string) (diagnosticFile, bool) {
	for _, file := range diagnosticFiles {
		if file.id == id {
			return file, true
		}
	}

	return diagnosticFile{}, false
}

// diagnosticLog shows the logs of Incus that have no time per line. Incus creates them again on each start of the instance.
type diagnosticLog struct {
	instanceName string
	client       IncusClient
}

func (l diagnosticLog) resource() LogServiceV190LogService {
	entryType := LogServiceV190LogService_LogEntryType{}
	_ = entryType.FromLogServiceV190LogEntryTypes(LogServiceV190LogEntryTypesOEM)

	purpose := LogServiceV190LogService_LogPurposes_Item{}
	_ = purpose.FromLogServiceV190LogPurpose(LogServiceV190LogPurposeDiagnostic)

	return LogServiceV190LogService{
		ID:                 diagnosticLogID,
		Name:               "Diagnostic Log",
		LogEntryType:       &entryType,
		LogPurposes:        &[]LogServiceV190LogService_LogPurposes_Item{purpose},
		MaxNumberOfRecords: ref(int64(len(diagnosticFiles))),
		OverWritePolicy:    ref(LogServiceV190OverWritePolicyUnknown),
	}
}

// created returns the time when Incus created the logs. This is the start of the instance.
func (l diagnosticLog) created() (*time.Time, error) {
	state, _, err := l.client.GetInstanceState(l.instanceName)
	if err != nil {
		return nil, fmt.Errorf("get the state of instance %q: %w", l.instanceName, err)
	}

	if state.StartedAt.IsZero() {
		return nil, nil
	}

	return ref(state.StartedAt.UTC()), nil
}

func (l diagnosticLog) entry(path string, id string) (LogEntryV1220LogEntry, bool, error) {
	file, ok := lookupDiagnosticFile(id)
	if !ok {
		return LogEntryV1220LogEntry{}, false, nil
	}

	created, err := l.created()
	if err != nil {
		return LogEntryV1220LogEntry{}, false, err
	}

	return l.fileEntry(file, path, created)
}

func (l diagnosticLog) entries(path string) ([]LogEntryV1220LogEntry, error) {
	created, err := l.created()
	if err != nil {
		return nil, err
	}

	entries := []LogEntryV1220LogEntry{}
	for _, file := range diagnosticFiles {
		entry, ok, err := l.fileEntry(file, path, created)
		if err != nil {
			return nil, err
		}

		if ok {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

func (l diagnosticLog) fileEntry(file diagnosticFile, path string, created *time.Time) (LogEntryV1220LogEntry, bool, error) {
	content, err := file.open(l.client, l.instanceName)
	if incusapi.StatusErrorCheck(err, http.StatusNotFound) {
		return LogEntryV1220LogEntry{}, false, nil
	}

	if err != nil {
		return LogEntryV1220LogEntry{}, false, fmt.Errorf("open the %s of instance %q: %w", file.name, l.instanceName, err)
	}

	defer content.Close()

	attachment := path + "/Entries/" + file.id + "/attachment"

	end, size, err := tail(content, diagnosticMessageSize)
	if err != nil {
		return LogEntryV1220LogEntry{}, false, fmt.Errorf("read the %s of instance %q: %w", file.name, l.instanceName, err)
	}

	dataType := LogEntryV1220LogEntry_DiagnosticDataType{}
	_ = dataType.FromLogEntryV1220LogDiagnosticDataTypes(file.dataType)

	severity := LogEntryV1220LogEntry_Severity{}
	_ = severity.FromLogEntryV1220EventSeverity(LogEntryV1220EventSeverityOK)

	return LogEntryV1220LogEntry{
		ID:                      file.id,
		Name:                    file.name,
		EntryType:               LogEntryV1220LogEntryTypeOem,
		OemRecordFormat:         ref("Incus"),
		Created:                 created,
		Message:                 ref(diagnosticMessage(end, size, attachment)),
		Severity:                &severity,
		DiagnosticDataType:      &dataType,
		OEMDiagnosticDataType:   refNonZero(file.oemDataType),
		AdditionalDataURI:       ref(attachment),
		AdditionalDataSizeBytes: ref(size),
	}, true, nil
}

// tail returns the last n bytes of the content and the size of the content.
func tail(content io.Reader, n int) ([]byte, int64, error) {
	buffer := make([]byte, 32*1024)
	end := make([]byte, 0, 2*n)

	var size int64

	for {
		read, err := content.Read(buffer)
		size += int64(read)

		end = append(end, buffer[:read]...)
		if len(end) > n {
			end = append(end[:0], end[len(end)-n:]...)
		}

		if errors.Is(err, io.EOF) {
			return end, size, nil
		}

		if err != nil {
			return nil, 0, err
		}
	}
}

// diagnosticMessage cuts the end of a log at the start of a line. A note points to the full log if the start is missing.
func diagnosticMessage(end []byte, size int64, attachment string) string {
	if size <= int64(len(end)) {
		return strings.ToValidUTF8(string(end), "")
	}

	_, rest, ok := bytes.Cut(end, []byte("\n"))
	if ok {
		end = rest
	}

	return "[truncated, full log: " + attachment + "]\n" + strings.ToValidUTF8(string(end), "")
}

func (s redfishServer) registerDiagnosticLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /redfish/v1/Systems/{ComputerSystemId}/LogServices/"+diagnosticLogID+"/Entries/{LogEntryId}/attachment", func(w http.ResponseWriter, r *http.Request) {
		s.getDiagnosticAttachment(w, r.Method, r.PathValue("ComputerSystemId"), r.PathValue("LogEntryId"))
	})
}

func (s redfishServer) getDiagnosticAttachment(w http.ResponseWriter, method string, computerSystemID string, logEntryID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	file, ok := lookupDiagnosticFile(logEntryID)
	if !ok {
		responseErr(w, http.StatusNotFound)
		return
	}

	content, err := file.open(s.client, s.instanceName)
	if incusapi.StatusErrorCheck(err, http.StatusNotFound) {
		responseErr(w, http.StatusNotFound)
		return
	}

	if err != nil {
		respondLogError(w, fmt.Errorf("open the %s of instance %q: %w", file.name, s.instanceName, err))
		return
	}

	defer content.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	// A HEAD request has no body. Do not read the log from Incus.
	if method == http.MethodHead {
		return
	}

	_, err = io.Copy(w, content)
	if err != nil {
		log.Printf("send the %s of instance %q: %v", file.name, s.instanceName, err)
	}
}
