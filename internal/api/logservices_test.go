package api_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/api/mock"
)

func TestRedfishServer_LogServicesReadOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		url    string
	}{
		{method: http.MethodPatch, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog"},
		{method: http.MethodPut, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog"},
		{method: http.MethodPost, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries"},
		{method: http.MethodDelete, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries/1"},
		{method: http.MethodPatch, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries/1"},
		{method: http.MethodPut, url: "/redfish/v1/Systems/test-instance/LogServices/EventLog/Entries/1"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.url), func(t *testing.T) {
			t.Parallel()

			status, header := rawRequest(t, &mock.IncusClientMock{}, tc.method, tc.url)

			require.Equal(t, http.StatusMethodNotAllowed, status, "the log services are read only")
			require.Equal(t, http.MethodGet, header.Get("Allow"), "the answer names the allowed method")
		})
	}
}

func TestRedfishServer_LogServicesNotFound(t *testing.T) {
	t.Parallel()

	emptyClient := func() *mock.IncusClientMock { return &mock.IncusClientMock{} }

	tests := []struct {
		url    string
		client func() *mock.IncusClientMock
	}{
		{url: "/redfish/v1/Systems/invalid/LogServices", client: emptyClient},
		{url: "/redfish/v1/Systems/invalid/LogServices/EventLog", client: emptyClient},
		{url: "/redfish/v1/Systems/test-instance/LogServices/Unknown", client: emptyClient},
		{url: "/redfish/v1/Systems/test-instance/LogServices/Unknown/Entries", client: emptyClient},
		{url: "/redfish/v1/Systems/test-instance/LogServices/Unknown/Entries/1", client: emptyClient},
	}

	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			t.Parallel()

			status, _ := rawRequest(t, tc.client(), http.MethodGet, tc.url)

			require.Equal(t, http.StatusNotFound, status, "the log service or the entry does not exist")
		})
	}
}

func TestRedfishServer_SystemLogServices(t *testing.T) {
	t.Parallel()

	incusClient := &mock.IncusClientMock{
		GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
			return &incusapi.Instance{Status: "Stopped"}, "", nil
		},
	}

	client := setup(t, incusClient)

	systems, err := client.Service.Systems()
	require.NoError(t, err, "list the systems")

	services, err := systems[0].LogServices()
	require.NoError(t, err, "list the log services of the system")

	ids := make([]string, 0, len(services))
	for _, service := range services {
		ids = append(ids, service.ID)
	}

	require.Equal(t, []string{}, ids, "the system links its log services")

	resp, err := client.Get("/redfish/v1/Systems/test-instance/LogServices")
	require.NoError(t, err, "get the log service collection")

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read the log service collection")
	require.JSONEq(t, `{
		"@odata.id": "/redfish/v1/Systems/test-instance/LogServices",
		"@odata.type": "#LogServiceCollection.LogServiceCollection",
		"Members": [],
		"Members@odata.count": 0,
		"Name": "Log Service Collection"
	}`, string(body), "the collection lists every log service")
}

// rawRequest sends a request without gofish, which hides the response of a failed request.
func rawRequest(t *testing.T, client api.IncusClient, method string, url string) (int, http.Header) {
	t.Helper()

	server := httptest.NewServer(api.NewHandler("test-instance", client))
	t.Cleanup(server.Close)

	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+url, nil)
	require.NoError(t, err, "build the request")

	resp, err := server.Client().Do(req)
	require.NoError(t, err, "send the request")

	defer resp.Body.Close()

	return resp.StatusCode, resp.Header
}
