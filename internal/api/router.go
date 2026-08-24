package api

import "net/http"

// NewHandler builds the HTTP handler for the Redfish API: the routes
// generated from the DMTF Redfish OpenAPI schema, plus routes for resources
// the schema does not enumerate because their URI is service-defined rather
// than fixed.
func NewHandler(instanceName string, client IncusClient) http.Handler {
	server := NewRedfishServer(instanceName, client)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /redfish/v1/Managers/{ManagerId}/VirtualMedia/{VirtualMediaId}/InsertMediaActionInfo", func(w http.ResponseWriter, r *http.Request) {
		server.GetRedfishV1ManagersManagerIDVirtualMediaVirtualMediaIDInsertMediaActionInfo(w, r, r.PathValue("ManagerId"), r.PathValue("VirtualMediaId"))
	})

	return HandlerFromMux(server, mux)
}
