package api

import (
	"net/http"
)

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDLogServices(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	responseErr(w, http.StatusNotFound)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDLogServicesLogServiceID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	responseErr(w, http.StatusNotFound)
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemIDLogServicesLogServiceID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string) {
	responseReadOnly(w)
}

func (s redfishServer) PutRedfishV1SystemsComputerSystemIDLogServicesLogServiceID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string) {
	responseReadOnly(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDLogServicesLogServiceIDEntries(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	responseErr(w, http.StatusNotFound)
}

func (s redfishServer) PostRedfishV1SystemsComputerSystemIDLogServicesLogServiceIDEntries(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string) {
	responseReadOnly(w)
}

func (s redfishServer) DeleteRedfishV1SystemsComputerSystemIDLogServicesLogServiceIDEntriesLogEntryID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string, logEntryID string) {
	responseReadOnly(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDLogServicesLogServiceIDEntriesLogEntryID(w http.ResponseWriter, r *http.Request, computerSystemID string, logServiceID string, logEntryID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	responseErr(w, http.StatusNotFound)
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
