package profile

import (
	"fmt"
	"regexp"
)

// messageIDPattern matches a Redfish message ID like Base.1.18.PropertyUnknown.
var messageIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*\.[0-9]+\.[0-9]+\.[A-Za-z][A-Za-z0-9]*$`)

// ErrorShape is the HTTP status and the Redfish message ID a profile answers a refused request with.
type ErrorShape struct {
	Status    int    `yaml:"status"`
	MessageID string `yaml:"message_id"`
}

// Validate checks the status is an error status and the message ID is well formed.
func (e ErrorShape) Validate() error {
	if e.Status < 400 || e.Status > 599 {
		return fmt.Errorf("status %d is not an HTTP error status", e.Status)
	}

	if !messageIDPattern.MatchString(e.MessageID) {
		return fmt.Errorf("message ID %q is not of the form <Registry>.<major>.<minor>.<Id>", e.MessageID)
	}

	return nil
}
