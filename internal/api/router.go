package api

import "net/http"

// NewHandler builds the HTTP handler for the Redfish API: the routes
// generated from the DMTF Redfish OpenAPI schema, plus routes for resources
// the schema does not enumerate because their URI is service-defined rather
// than fixed. The caller must call Service.Close after the HTTP server
// stopped serving requests.
func NewHandler(instanceName string, client IncusClient, opts ...Option) *Service {
	server := NewRedfishServer(instanceName, client, opts...)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /redfish/v1/Managers/{ManagerId}/VirtualMedia/{VirtualMediaId}/InsertMediaActionInfo", func(w http.ResponseWriter, r *http.Request) {
		server.GetRedfishV1ManagersManagerIDVirtualMediaVirtualMediaIDInsertMediaActionInfo(w, r, r.PathValue("ManagerId"), r.PathValue("VirtualMediaId"))
	})

	mux.HandleFunc("GET /redfish/v1/TaskMonitors/{TaskId}", func(w http.ResponseWriter, r *http.Request) {
		server.GetRedfishV1TaskMonitorsTaskID(w, r, r.PathValue("TaskId"))
	})

	server.registerRegistryRoutes(mux)
	server.registerTaskRoutes(mux)
	server.registerSecureBootRoutes(mux)
	server.registerVirtualMediaRoutes(mux)

	return &Service{
		Handler: jsonContentType(server.unreachable(server.settle(server.busy(server.overlay(HandlerFromMux(server, mux)))))),
		server:  server,
	}
}

func jsonContentType(next http.Handler) http.Handler {
	// Like a real BMC. Without it, Go guesses text/plain.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		next.ServeHTTP(w, r)
	})
}
