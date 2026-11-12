package profile

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"
)

// ResetType is a reset of the system, as named by the Redfish Resource schema.
type ResetType string

const (
	ResetTypeOn               ResetType = "On"
	ResetTypeForceOn          ResetType = "ForceOn"
	ResetTypeGracefulShutdown ResetType = "GracefulShutdown"
	ResetTypeForceOff         ResetType = "ForceOff"
	ResetTypeGracefulRestart  ResetType = "GracefulRestart"
	ResetTypeForceRestart     ResetType = "ForceRestart"
)

// PowerResponse is the answer to a reset.
type PowerResponse string

const (
	// PowerResponseNoContent changes the power state before it answers 204.
	PowerResponseNoContent PowerResponse = "204"
	// PowerResponseTask answers 202 with a task monitor and changes the power state in the background.
	PowerResponseTask PowerResponse = "202-task"
	// PowerResponseOK changes the power state before it answers 200 with a top level @Message.ExtendedInfo.
	PowerResponseOK PowerResponse = AnswerOK
	// PowerResponseOKError changes the power state before it answers 200 with the messages inside an error object.
	PowerResponseOKError PowerResponse = AnswerOKError
)

// transientStatuses are the statuses Operations Center asks again for.
var transientStatuses = []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}

// maxRetries is the number of times Operations Center asks again.
const maxRetries = 3

// notSettledMessageIDs are the message IDs Operations Center treats as "the baseboard management controller (BMC) is not ready yet".
var notSettledMessageIDs = []string{"UnableToModifyDuringSystemPOST", "ResourceInStandby"}

// Power describes the reset action and how the BMC behaves after a power on.
type Power struct {
	// ResetTypes become ResetType@Redfish.AllowableValues. Empty publishes nothing and accepts every type.
	ResetTypes []ResetType   `yaml:"reset_types"`
	Response   PowerResponse `yaml:"response"`

	// NotSettledWindow is how long the proxy refuses writes after a power on.
	NotSettledWindow time.Duration `yaml:"not_settled_window"`
	NotSettledError  ErrorShape    `yaml:"not_settled_error"`

	// Delay is how long a reset waits before it changes the power state.
	Delay PowerDelay `yaml:"delay"`

	// RedundantResetErrors refuse a reset to the power state the system has already.
	RedundantResetErrors RedundantResetErrors `yaml:"redundant_reset_errors"`

	// BusyReads refuse the first reads of some resources after every power on.
	BusyReads BusyReads `yaml:"busy_reads"`
}

// BusyResource is a resource below the system, whose reads a busy BMC refuses.
type BusyResource string

// The resources a busy BMC refuses to read.
const (
	BusyResourceSystem     BusyResource = "system"
	BusyResourceBios       BusyResource = "bios"
	BusyResourceSecureBoot BusyResource = "secure_boot"
)

// BusyReads model a BMC that can not read the data of the host for a while after a power on, like iDRAC with 503 and SYS518.
// After every power on, the first Failures GET requests of each resource answer Error.
type BusyReads struct {
	Resources []BusyResource `yaml:"resources"`
	Failures  int            `yaml:"failures"`
	Error     ErrorShape     `yaml:"error"`
}

// Enabled reports whether the BMC refuses any read.
func (b BusyReads) Enabled() bool {
	return b.Failures > 0 && len(b.Resources) > 0
}

// PowerDelay is how long a reset waits before it changes the power state, per target power state.
// YAML accepts one duration for both, or a mapping with on and off.
type PowerDelay struct {
	// On applies to On and ForceOn.
	On time.Duration `yaml:"on"`

	// Off applies to ForceOff, GracefulShutdown and the restarts, which first turn the system off.
	Off time.Duration `yaml:"off"`
}

// UnmarshalYAML accepts a duration like 10s, or a mapping like {on: 20s, off: 5s}.
func (d *PowerDelay) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var both time.Duration

		err := node.Decode(&both)
		if err != nil {
			return err
		}

		*d = PowerDelay{On: both, Off: both}

		return nil
	}

	// node.Decode does not inherit KnownFields of the profile decoder.
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if key != "on" && key != "off" {
			return fmt.Errorf("unknown power.delay key %q, only on and off are known", key)
		}
	}

	// The alias drops the method, so Decode does not call it again.
	type plain PowerDelay

	return node.Decode((*plain)(d))
}

// RedundantResetErrors are the answers to a reset that would not change the power state.
// A nil shape accepts the reset and does nothing, as before profiles existed.
type RedundantResetErrors struct {
	// Off refuses ForceOff and GracefulShutdown of a system that is off.
	Off *ErrorShape `yaml:"off"`

	// On refuses On and ForceOn of a system that is on.
	On *ErrorShape `yaml:"on"`
}

// Timing scales the durations of a profile.
type Timing struct {
	// Scale multiplies every duration, 0.1 makes a 20s window last 2s.
	Scale float64 `yaml:"scale"`
}

// Scaled returns the duration multiplied by the timing scale.
func (p Profile) Scaled(d time.Duration) time.Duration {
	return time.Duration(float64(d) * p.Timing.Scale)
}

func (p Power) validate() error {
	for _, resetType := range p.ResetTypes {
		switch resetType {
		case ResetTypeOn, ResetTypeForceOn, ResetTypeGracefulShutdown, ResetTypeForceOff, ResetTypeGracefulRestart, ResetTypeForceRestart:

		default:
			return fmt.Errorf("unknown power.reset_types entry %q", resetType)
		}
	}

	switch p.Response {
	case PowerResponseNoContent, PowerResponseTask, PowerResponseOK, PowerResponseOKError:

	default:
		return fmt.Errorf("unknown power.response %q", p.Response)
	}

	if p.NotSettledWindow < 0 {
		return errors.New("power.not_settled_window must not be negative")
	}

	if p.Delay.On < 0 || p.Delay.Off < 0 {
		return errors.New("power.delay must not be negative")
	}

	err := p.NotSettledError.Validate()
	if err != nil {
		return fmt.Errorf("power.not_settled_error: %w", err)
	}

	err = p.BusyReads.validate()
	if err != nil {
		return err
	}

	shapes := []struct {
		name  string
		shape *ErrorShape
	}{{"off", p.RedundantResetErrors.Off}, {"on", p.RedundantResetErrors.On}}

	for _, s := range shapes {
		if s.shape == nil {
			continue
		}

		err = s.shape.Validate()
		if err != nil {
			return fmt.Errorf("power.redundant_reset_errors.%s: %w", s.name, err)
		}
	}

	return nil
}

func (b BusyReads) validate() error {
	for _, resource := range b.Resources {
		switch resource {
		case BusyResourceSystem, BusyResourceBios, BusyResourceSecureBoot:

		default:
			return fmt.Errorf("unknown power.busy_reads.resources entry %q", resource)
		}
	}

	if b.Failures < 0 {
		return errors.New("power.busy_reads.failures must not be negative")
	}

	if !b.Enabled() {
		return nil
	}

	err := b.Error.Validate()
	if err != nil {
		return fmt.Errorf("power.busy_reads.error: %w", err)
	}

	return nil
}

func (t Timing) validate() error {
	// The negated check also refuses NaN.
	if !(t.Scale > 0) || math.IsInf(t.Scale, 1) {
		return fmt.Errorf("timing.scale %v must be a finite number greater than 0", t.Scale)
	}

	return nil
}

// Warnings returns the valid settings that Operations Center does not handle as intended.
func (p Profile) Warnings() []string {
	var warnings []string

	if p.Power.NotSettledWindow > 0 && !IsNotSettledMessageID(p.Power.NotSettledError.MessageID) {
		warnings = append(warnings, fmt.Sprintf("power.not_settled_error %q does not end in one of %s, so Operations Center does not wait for the system to settle", p.Power.NotSettledError.MessageID, strings.Join(notSettledMessageIDs, ", ")))
	}

	if p.Power.BusyReads.Enabled() && !slices.Contains(transientStatuses, p.Power.BusyReads.Error.Status) {
		warnings = append(warnings, fmt.Sprintf("power.busy_reads.error status %d is not one of %v, so Operations Center does not ask again", p.Power.BusyReads.Error.Status, transientStatuses))
	}

	if p.Power.BusyReads.Failures > maxRetries {
		warnings = append(warnings, fmt.Sprintf("power.busy_reads.failures %d is above the %d retries of Operations Center, so a read fails", p.Power.BusyReads.Failures, maxRetries))
	}

	return warnings
}

// IsNotSettledMessageID reports whether Operations Center waits for the system to settle after an error with the message ID.
func IsNotSettledMessageID(messageID string) bool {
	for _, id := range notSettledMessageIDs {
		if strings.HasSuffix(messageID, "."+id) {
			return true
		}
	}

	return false
}
