package api

import (
	"encoding/json"
	"net/http"
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
}

// responseRedfishError writes a Redfish error with extended info, its code is the message ID of the first message.
func responseRedfishError(w http.ResponseWriter, statusCode int, message string, messages ...redfishMessage) {
	extendedInfo := make([]MessageV130Message, 0, len(messages))
	for _, m := range messages {
		extendedInfo = append(extendedInfo, MessageV130Message{
			MessageID:         ref(m.MessageID),
			Message:           refNonZero(m.Message),
			MessageArgs:       refNonEmpty(m.MessageArgs),
			RelatedProperties: refNonEmpty(m.RelatedProperties),
		})
	}

	code := "error"
	if len(messages) > 0 {
		code = messages[0].MessageID
	}

	e := RedfishError{}
	e.Error.Code = ref(code)
	e.Error.Message = ref(message)
	e.Error.MessageExtendedInfo = refNonEmpty(extendedInfo)

	w.WriteHeader(statusCode)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(e)
}
