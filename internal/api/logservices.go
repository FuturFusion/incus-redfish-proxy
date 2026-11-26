package api

import (
	"fmt"
	"net/http"
	"time"
)

// logService is one log of the system. The handlers add the OData fields.
type logService interface {
	resource() LogServiceV190LogService

	// entries returns the entries, newest first if they have an order in time.
	// path is the path of the log service. It is only used to build links.
	entries(path string) ([]LogEntryV1220LogEntry, error)

	// entry returns the entry with the id. It returns false if the entry does not exist.
	entry(path string, id string) (LogEntryV1220LogEntry, bool, error)
}

func findLogEntry(entries []LogEntryV1220LogEntry, id string) (LogEntryV1220LogEntry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}

	return LogEntryV1220LogEntry{}, false
}

func (s redfishServer) logServicesPath() string {
	return fmt.Sprintf("/redfish/v1/Systems/%s/LogServices", s.instanceName)
}

func (s redfishServer) logServices() []logService {
	return []logService{s.eventLog}
}

func (s redfishServer) lookupLogService(w http.ResponseWriter, computerSystemID string, logServiceID string) (logService, string, bool) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return nil, "", false
	}

	for _, service := range s.logServices() {
		if service.resource().ID == logServiceID {
			return service, s.logServicesPath() + "/" + logServiceID, true
		}
	}

	responseErr(w, http.StatusNotFound)
	return nil, "", false
}

func addLogEntryOdata(entry *LogEntryV1220LogEntry, path string) {
	entry.OdataID = ref(path + "/Entries/" + entry.ID)
	entry.OdataType = ref("#LogEntry.v1_22_0.LogEntry")
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDLogServices(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	members := []OdataV4IdRef{}
	for _, service := range s.logServices() {
		members = append(members, OdataV4IdRef{OdataID: ref(s.logServicesPath() + "/" + service.resource().ID)})
	}

	response(w, LogServiceCollectionLogServiceCollection{
		OdataID:           ref(s.logServicesPath()),
		OdataType:         ref("#LogServiceCollection.LogServiceCollection"),
		Members:           &members,
		MembersOdataCount: ref(OdataV4Count(len(members))),
		Name:              "Log Service Collection",
	})
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDLogServicesLogServiceID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string) {
	service, path, ok := s.lookupLogService(w, computerSystemID, logServiceID)
	if !ok {
		return
	}

	resource := service.resource()
	resource.OdataID = ref(path)
	resource.OdataType = ref("#LogService.v1_9_0.LogService")
	resource.DateTime = ref(s.now().UTC().Truncate(time.Second))
	resource.DateTimeLocalOffset = ref("+00:00")
	resource.Entries = &OdataV4IdRef{OdataID: ref(path + "/Entries")}
	resource.Persistency = ref(false)

	if resource.ServiceEnabled == nil {
		state := ResourceStatus_State{}
		_ = state.FromResourceState(ResourceStateEnabled)

		health := ResourceStatus_Health{}
		_ = health.FromResourceHealth(OK)

		resource.ServiceEnabled = ref(true)
		resource.Status = &ResourceStatus{State: &state, Health: &health}
	}

	response(w, resource)
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemIDLogServicesLogServiceID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string) {
	responseReadOnly(w)
}

func (s redfishServer) PutRedfishV1SystemsComputerSystemIDLogServicesLogServiceID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string) {
	responseReadOnly(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDLogServicesLogServiceIDEntries(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string) {
	service, path, ok := s.lookupLogService(w, computerSystemID, logServiceID)
	if !ok {
		return
	}

	entries, err := service.entries(path)
	if err != nil {
		respondLogError(w, err)
		return
	}

	for i := range entries {
		addLogEntryOdata(&entries[i], path)
	}

	// Inline the members. A client then needs a single request for the whole log.
	response(w, LogEntryCollectionLogEntryCollection{
		OdataID:           ref(path + "/Entries"),
		OdataType:         ref("#LogEntryCollection.LogEntryCollection"),
		Members:           &entries,
		MembersOdataCount: ref(OdataV4Count(len(entries))),
		Name:              service.resource().Name + " Entries",
	})
}

func (s redfishServer) PostRedfishV1SystemsComputerSystemIDLogServicesLogServiceIDEntries(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string) {
	responseReadOnly(w)
}

func (s redfishServer) DeleteRedfishV1SystemsComputerSystemIDLogServicesLogServiceIDEntriesLogEntryID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string, logEntryID string) {
	responseReadOnly(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDLogServicesLogServiceIDEntriesLogEntryID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string, logEntryID string) {
	service, path, ok := s.lookupLogService(w, computerSystemID, logServiceID)
	if !ok {
		return
	}

	entry, found, err := service.entry(path, logEntryID)
	if err != nil {
		respondLogError(w, err)
		return
	}

	if !found {
		responseErr(w, http.StatusNotFound)
		return
	}

	addLogEntryOdata(&entry, path)
	response(w, entry)
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemIDLogServicesLogServiceIDEntriesLogEntryID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string, logEntryID string) {
	responseReadOnly(w)
}

func (s redfishServer) PutRedfishV1SystemsComputerSystemIDLogServicesLogServiceIDEntriesLogEntryID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string, logEntryID string) {
	responseReadOnly(w)
}

// responseReadOnly answers a write to a resource that only supports GET.
// Redfish requires 405 and an Allow header in this case.
func responseReadOnly(w http.ResponseWriter) {
	w.Header().Set("Allow", http.MethodGet)
	responseErr(w, http.StatusMethodNotAllowed)
}
