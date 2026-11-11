package api_test

import (
	"errors"
	"net/http"
	"testing"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stmcginnis/gofish/schemas"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/api/mock"
)

// closeBody closes the body of a response. gofish returns no response for some errors.
func closeBody(resp *http.Response) {
	if resp != nil {
		resp.Body.Close()
	}
}

// redfishAnswer is what a test checks of an answer. gofish returns an error answer as an error, not as a response.
type redfishAnswer struct {
	status            int
	location          string
	messageID         string
	relatedProperties []string
}

func answerOf(t *testing.T, resp *http.Response, err error) redfishAnswer {
	t.Helper()

	if resp != nil {
		resp.Body.Close()
	}

	redfishErr := &schemas.Error{}
	if errors.As(err, &redfishErr) {
		answer := redfishAnswer{status: redfishErr.HTTPReturnedStatusCode}
		if len(redfishErr.ExtendedInfos) > 0 {
			answer.messageID = redfishErr.ExtendedInfos[0].MessageID
			answer.relatedProperties = redfishErr.ExtendedInfos[0].RelatedProperties
		}

		return answer
	}

	require.NoError(t, err, "the request reaches the proxy")

	return redfishAnswer{status: resp.StatusCode, location: resp.Header.Get("Location")}
}

// nvramNames returns the names of the given UEFI variables, which the instance has.
func nvramNames(incusClient api.IncusClient, variables []struct{ guid, name string }) []string {
	names := []string{}
	for _, variable := range variables {
		_, _, err := incusClient.GetInstanceNVRAMGUIDVar("test-instance", variable.guid, variable.name)
		if err == nil {
			names = append(names, variable.name)
		}
	}

	return names
}

// updatedConfigs returns the instance configs the proxy wrote, in order.
func updatedConfigs(incusClient *mock.IncusClientMock) []incusapi.ConfigMap {
	calls := incusClient.UpdateInstanceCalls()

	configs := make([]incusapi.ConfigMap, 0, len(calls))
	for _, call := range calls {
		configs = append(configs, call.Instance.Config)
	}

	return configs
}
