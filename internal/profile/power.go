package profile

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
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
)

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
	Delay time.Duration `yaml:"delay"`
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
	case PowerResponseNoContent, PowerResponseTask:

	default:
		return fmt.Errorf("unknown power.response %q", p.Response)
	}

	if p.NotSettledWindow < 0 {
		return errors.New("power.not_settled_window must not be negative")
	}

	if p.Delay < 0 {
		return errors.New("power.delay must not be negative")
	}

	err := p.NotSettledError.Validate()
	if err != nil {
		return fmt.Errorf("power.not_settled_error: %w", err)
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
