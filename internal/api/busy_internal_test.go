package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

func TestRedfishServer_busy(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

	type step struct {
		method string
		path   string

		// powerOn is a power on, that the reset tracker records before the request.
		powerOn time.Time

		wantStatus int
	}

	tests := []struct {
		name   string
		status string
		steps  []step
	}{
		{
			name:   "first reads of each resource after every power on",
			status: "Running",
			steps: []step{
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance/Bios", wantStatus: http.StatusServiceUnavailable},
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance/Bios/", wantStatus: http.StatusServiceUnavailable},
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance", wantStatus: http.StatusServiceUnavailable},
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance/Bios", wantStatus: http.StatusOK},
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance", wantStatus: http.StatusServiceUnavailable},
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance", wantStatus: http.StatusOK},
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance/Bios", powerOn: startedAt.Add(time.Hour), wantStatus: http.StatusServiceUnavailable},
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance/Bios", wantStatus: http.StatusServiceUnavailable},
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance/Bios", wantStatus: http.StatusOK},
			},
		},
		{
			name:   "other requests pass",
			status: "Running",
			steps: []step{
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance/SecureBoot", wantStatus: http.StatusOK},
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance/Processors", wantStatus: http.StatusOK},
				{method: http.MethodGet, path: "/redfish/v1/Managers/bmc1", wantStatus: http.StatusOK},
				{method: http.MethodPatch, path: "/redfish/v1/Systems/test-instance/Bios", wantStatus: http.StatusOK},
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance/Bios", wantStatus: http.StatusServiceUnavailable},
			},
		},
		{
			name:   "an instance that does not run",
			status: "Stopped",
			steps: []step{
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance/Bios", wantStatus: http.StatusOK},
				{method: http.MethodGet, path: "/redfish/v1/Systems/test-instance", wantStatus: http.StatusOK},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			p := profile.Default()
			p.Power.BusyReads = profile.BusyReads{
				Resources: []profile.BusyResource{profile.BusyResourceSystem, profile.BusyResourceBios},
				Failures:  2,
				Error:     profile.ErrorShape{Status: http.StatusServiceUnavailable, MessageID: "IDRAC.2.8.SYS518"},
			}

			state := incusapi.InstanceState{Status: tc.status, StartedAt: startedAt}
			server := NewRedfishServer("test-instance", instanceStateClient{state: &state}, WithProfile(p))
			handler := server.busy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			statuses := make([]int, 0, len(tc.steps))
			wantStatuses := make([]int, 0, len(tc.steps))

			// Run test
			for _, step := range tc.steps {
				server.resets.poweredOn(step.powerOn)

				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(step.method, step.path, nil))

				statuses = append(statuses, rec.Code)
				wantStatuses = append(wantStatuses, step.wantStatus)
			}

			// Assert
			require.Equal(t, wantStatuses, statuses, "the first 2 reads of each listed resource fail after every power on")
		})
	}
}

func TestRedfishServer_busyAnswer(t *testing.T) {
	t.Parallel()

	// Setup
	p := profile.Default()
	p.Power.BusyReads = profile.BusyReads{
		Resources: []profile.BusyResource{profile.BusyResourceSecureBoot},
		Failures:  1,
		Error:     profile.ErrorShape{Status: http.StatusServiceUnavailable, MessageID: "IDRAC.2.8.SYS518"},
	}

	state := incusapi.InstanceState{Status: "Running"}
	server := NewRedfishServer("test-instance", instanceStateClient{state: &state}, WithProfile(p))
	handler := server.busy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()

	// Run test
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/redfish/v1/Systems/test-instance/SecureBoot", nil))

	// Assert
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, "status of the profile")
	require.Contains(t, rec.Body.String(), `"MessageId": "IDRAC.2.8.SYS518"`, "message ID of the profile")
	require.Empty(t, rec.Header().Get("Retry-After"), "iDRAC sends no Retry-After")
}
