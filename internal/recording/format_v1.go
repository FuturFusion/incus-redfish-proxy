package recording

import (
	"encoding/json"
	"time"
)

// The types in this file copy the BMC recording format of Operations Center,
// shared/api/provisioning_server_bmc_recording.go, format_version 1. They hold
// only the fields the draft needs. The proxy must not import Operations Center,
// because the end to end tests of Operations Center import the proxy.

// FormatVersion is the only recording format version the reader accepts.
const FormatVersion = 1

// bodyEncodingBase64 marks a body that is base64 encoded because it is not valid text.
const bodyEncodingBase64 = "base64"

// Exchange is one HTTP exchange between Operations Center and a BMC.
type Exchange struct {
	Sequence        int64     `json:"sequence"`
	Timestamp       time.Time `json:"timestamp"`
	DeploymentState string    `json:"deployment_state"`

	// Attempt numbers the transport attempts of one request from 0. Values above 0 are retries.
	Attempt int `json:"attempt"`

	Method string `json:"method"`
	URL    string `json:"url"`

	RequestHeaders      map[string][]string `json:"request_headers"`
	RequestBody         string              `json:"request_body"`
	RequestBodyEncoding string              `json:"request_body_encoding,omitempty"`
	RequestTruncated    bool                `json:"request_truncated,omitempty"`

	Status               int                 `json:"status"`
	ResponseHeaders      map[string][]string `json:"response_headers"`
	ResponseBody         string              `json:"response_body"`
	ResponseBodyEncoding string              `json:"response_body_encoding,omitempty"`
	ResponseTruncated    bool                `json:"response_truncated,omitempty"`

	TransportError string `json:"transport_error,omitempty"`
}

// Manifest describes one recording.
type Manifest struct {
	FormatVersion           int               `json:"format_version"`
	RecordingID             string            `json:"recording_id"`
	OperationsCenterVersion string            `json:"operations_center_version"`
	StartedAt               time.Time         `json:"started_at"`
	FinishedAt              time.Time         `json:"finished_at,omitzero"`
	FinalState              string            `json:"final_state,omitempty"`
	Request                 DeploymentRequest `json:"request"`
	BMCData                 BMCData           `json:"bmc_data"`
	History                 json.RawMessage   `json:"history,omitempty"`
}

// DeploymentRequest holds the parts of the deployment request that tell why a draft lacks evidence.
type DeploymentRequest struct {
	SkipSecureBootCertificates bool `json:"skip_secure_boot_certificates"`
	SecureBootEnrollmentMedia  bool `json:"secure_boot_enrollment_media"`
}

// BMCData holds the parts of the BMC data that describe the firmware and not the identity of the server.
type BMCData struct {
	BMCFirmwareVersion string `json:"bmc_firmware_version"`
	ServerModel        string `json:"server_model"`
	ServerBIOSVersion  string `json:"server_bios_version"`
}
