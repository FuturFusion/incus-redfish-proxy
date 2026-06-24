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
