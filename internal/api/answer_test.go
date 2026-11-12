package api_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stmcginnis/gofish/schemas"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/api/mock"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
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

// doneAnswer is what a test checks of the answer to a request that is done.
type doneAnswer struct {
	status    int
	messageID string

	// inError tells that the messages are inside an error object.
	inError bool

	// odataID is the @odata.id of a resource in the body.
	odataID string
}

// doneAnswerOf reads the status and the body of an answer.
func doneAnswerOf(t *testing.T, resp *http.Response, err error) doneAnswer {
	t.Helper()

	require.NoError(t, err, "the request succeeds")

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read the body")

	type messages struct {
		ExtendedInfo []struct {
			MessageID string `json:"MessageId"`
		} `json:"@Message.ExtendedInfo"`
	}

	var decoded struct {
		messages

		Error   *messages `json:"error"`
		OdataID string    `json:"@odata.id"`
	}

	if len(body) > 0 {
		err = json.Unmarshal(body, &decoded)
		require.NoError(t, err, "decode the body %s", body)
	}

	answer := doneAnswer{status: resp.StatusCode, odataID: decoded.OdataID}

	info := decoded.ExtendedInfo
	if decoded.Error != nil {
		answer.inError = true
		info = decoded.Error.ExtendedInfo
	}

	if len(info) > 0 {
		answer.messageID = info[0].MessageID
	}

	return answer
}

// wantDoneAnswer returns the answer that a profile answer like "204", "200" or "200-error" asks for.
func wantDoneAnswer[T ~string](answer T, messageID string) doneAnswer {
	switch string(answer) {
	case profile.AnswerOK:
		return doneAnswer{status: http.StatusOK, messageID: messageID}

	case profile.AnswerOKError:
		return doneAnswer{status: http.StatusOK, messageID: messageID, inError: true}

	default:
		return doneAnswer{status: http.StatusNoContent}
	}
}
