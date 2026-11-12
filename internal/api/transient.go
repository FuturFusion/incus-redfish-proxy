package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
)

const (
	// transientAttempts is the first read and its retries.
	transientAttempts   = 3
	transientRetryDelay = 200 * time.Millisecond

	// transientRetryAfter is the Retry-After of the answer, if the retries fail.
	transientRetryAfter = 5 * time.Second
)

// Incus fails some reads for a short time.
var (
	// restartErrors come while the VM process restarts, Incus then has no PID to read the state from.
	restartErrors = []string{"Invalid PID"}

	// nvramErrors come while the firmware writes the NVRAM store, Incus then reads a partly written store.
	nvramErrors = []string{"Unexpected data"}
)

// transientRetryClient retries the Incus reads that fail for a short time.
// If the retries fail, it returns 503, like a busy baseboard management controller (BMC).
type transientRetryClient struct {
	IncusClient

	sleep func(time.Duration)
}

func (c transientRetryClient) GetInstanceState(name string) (*incusapi.InstanceState, string, error) {
	var state *incusapi.InstanceState

	var etag string

	err := c.retry(restartErrors, func() error {
		var err error

		state, etag, err = c.IncusClient.GetInstanceState(name)

		return err
	})

	return state, etag, err
}

func (c transientRetryClient) GetInstanceNVRAMGUID(name string, guid string) (map[string]*incusapi.InstanceNVRAMVariable, error) {
	var vars map[string]*incusapi.InstanceNVRAMVariable

	err := c.retry(nvramErrors, func() error {
		var err error

		vars, err = c.IncusClient.GetInstanceNVRAMGUID(name, guid)

		return err
	})

	return vars, err
}

func (c transientRetryClient) GetInstanceNVRAMGUIDVar(name string, guid string, varName string) (*incusapi.InstanceNVRAMVariable, string, error) {
	var variable *incusapi.InstanceNVRAMVariable

	var etag string

	err := c.retry(nvramErrors, func() error {
		var err error

		variable, etag, err = c.IncusClient.GetInstanceNVRAMGUIDVar(name, guid, varName)

		return err
	})

	return variable, etag, err
}

func (c transientRetryClient) retry(transient []string, read func() error) error {
	var err error

	for attempt := range transientAttempts {
		if attempt > 0 {
			c.sleep(transientRetryDelay)
		}

		err = read()
		if err == nil || !isTransient(err, transient) {
			return err
		}
	}

	message := "the instance is busy, retry later"

	return &statusError{
		status:     http.StatusServiceUnavailable,
		err:        fmt.Errorf("%s: %w", message, err),
		messages:   []redfishMessage{{MessageID: "Base.1.18.ServiceTemporarilyUnavailable", Message: message, MessageArgs: []string{retryAfterSeconds(transientRetryAfter)}}},
		retryAfter: transientRetryAfter,
	}
}

func isTransient(err error, transient []string) bool {
	for _, text := range transient {
		if strings.Contains(err.Error(), text) {
			return true
		}
	}

	return false
}
