package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

func response(w http.ResponseWriter, resp any) {
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
}

func responseCreated(w http.ResponseWriter, location string, resp any) {
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusCreated)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
}

func responseAccepted(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusAccepted)
}

func responseNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

func responseNotImplemented(w http.ResponseWriter) {
	responseErr(w, http.StatusNotImplemented)
}

func responseErr(w http.ResponseWriter, statusCode int) {
	responseErrWithMessage(w, statusCode, http.StatusText(statusCode))
}

func responseErrWithMessage(w http.ResponseWriter, statusCode int, message string) {
	e := RedfishError{
		Error: struct {
			MessageExtendedInfo *[]MessageV130Message `json:"@Message.ExtendedInfo,omitempty"`
			Code                *string               `json:"code,omitempty"`
			Message             *string               `json:"message,omitempty"`
		}{
			Code:    ref("error"),
			Message: ref(message),
		},
	}

	w.WriteHeader(statusCode)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(e)
}

// redfishMessage is one entry of @Message.ExtendedInfo.
type redfishMessage struct {
	MessageID         string
	Message           string
	MessageArgs       []string
	RelatedProperties []string

	// Severity is left out of the answer, if empty.
	Severity ResourceHealth
}

// extendedInfo converts the messages into @Message.ExtendedInfo.
func extendedInfo(messages []redfishMessage) []MessageV130Message {
	info := make([]MessageV130Message, 0, len(messages))
	for _, m := range messages {
		info = append(info, MessageV130Message{
			MessageID:         ref(m.MessageID),
			Message:           refNonZero(m.Message),
			MessageArgs:       refNonEmpty(m.MessageArgs),
			RelatedProperties: refNonEmpty(m.RelatedProperties),
			MessageSeverity:   refNonZero(m.Severity),
			Severity:          refNonZero(string(m.Severity)),
		})
	}

	return info
}

// successMessage tells that the request changed the state at once.
func successMessage() redfishMessage {
	return redfishMessage{MessageID: baseRegistry + "Success", Message: "The request completed successfully.", Severity: OK}
}

// resetRequiredMessage tells that the next power on applies the change.
func (s redfishServer) resetRequiredMessage() redfishMessage {
	resetAction := fmt.Sprintf("/redfish/v1/Systems/%s/Actions/ComputerSystem.Reset", s.instanceName)
	resetType := string(ResourceResetTypeOn)

	return redfishMessage{
		MessageID:   baseRegistry + "ResetRequired",
		Message:     fmt.Sprintf("In order to complete the operation, a component reset is required with the Reset action URI '%s' and ResetType '%s'.", resetAction, resetType),
		MessageArgs: []string{resetAction, resetType},
		Severity:    Warning,
	}
}

// responseOK answers 200 with the messages, at the top level or inside an error object.
// Some BMCs wrap success messages in an error object. A client must accept this.
func responseOK(w http.ResponseWriter, inError bool, messages ...redfishMessage) {
	if inError {
		responseRedfishError(w, http.StatusOK, "See @Message.ExtendedInfo for more information.", messages...)
		return
	}

	response(w, struct {
		MessageExtendedInfo []MessageV130Message `json:"@Message.ExtendedInfo"`
	}{
		MessageExtendedInfo: extendedInfo(messages),
	})
}

// responseDone answers a request that is done: 200 with the messages, if the profile answer asks for it, 204 otherwise.
func responseDone[T ~string](w http.ResponseWriter, answer T, messages ...redfishMessage) {
	switch string(answer) {
	case profile.AnswerOK:
		responseOK(w, false, messages...)

	case profile.AnswerOKError:
		responseOK(w, true, messages...)

	default:
		responseNoContent(w)
	}
}

// respondPatch answers a PATCH of Systems, SecureBoot or VirtualMedia the way the profile asks for. get serves the patched resource.
func (s redfishServer) respondPatch(w http.ResponseWriter, r *http.Request, get http.HandlerFunc, messages ...redfishMessage) {
	if s.profile.Responses.Patch == profile.ResourcePatchResponseResource {
		get(w, r)
		return
	}

	if len(messages) == 0 {
		messages = []redfishMessage{successMessage()}
	}

	responseDone(w, s.profile.Responses.Patch, messages...)
}

// responseRedfishError writes a Redfish error with extended info, its code is the message ID of the first message.
func responseRedfishError(w http.ResponseWriter, statusCode int, message string, messages ...redfishMessage) {
	code := "error"
	if len(messages) > 0 {
		code = messages[0].MessageID
	}

	e := RedfishError{}
	e.Error.Code = ref(code)
	e.Error.Message = ref(message)
	e.Error.MessageExtendedInfo = refNonEmpty(extendedInfo(messages))

	w.WriteHeader(statusCode)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(e)
}

// retryAfterSeconds formats a duration for the Retry-After header, rounded up to whole seconds.
func retryAfterSeconds(d time.Duration) string {
	return strconv.Itoa(int(math.Ceil(d.Seconds())))
}
