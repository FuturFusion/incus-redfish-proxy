package profile

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"
)

// ResetKeysType is a reset of the secure boot key databases, as named by the Redfish SecureBoot schema.
type ResetKeysType string

const (
	ResetKeysTypeDeleteAllKeys ResetKeysType = "DeleteAllKeys"
	ResetKeysTypeDeletePK      ResetKeysType = "DeletePK"
)

// ResetKeysResponse is the answer to a reset of the secure boot keys.
type ResetKeysResponse string

const (
	// ResetKeysResponseNoContent answers 204.
	ResetKeysResponseNoContent ResetKeysResponse = "204"
	// ResetKeysResponseTask answers 202 with the task monitor of a completed task.
	ResetKeysResponseTask ResetKeysResponse = "202-task"
	// ResetKeysResponseOK answers 200 with a top level @Message.ExtendedInfo.
	ResetKeysResponseOK ResetKeysResponse = AnswerOK
	// ResetKeysResponseOKError answers 200 with the messages inside an error object.
	ResetKeysResponseOKError ResetKeysResponse = AnswerOKError
)

// ResetKeysApplies is when a reset of the secure boot keys takes effect.
type ResetKeysApplies string

const (
	// ResetKeysAppliesImmediately deletes the keys before the action answers. The instance must be stopped.
	ResetKeysAppliesImmediately ResetKeysApplies = "immediately"
	// ResetKeysAppliesOnReset stores the request. The next reset that powers the instance on deletes the keys.
	ResetKeysAppliesOnReset ResetKeysApplies = "on_reset"
)

// DatabaseAddResponse is the answer to a new entry of a secure boot database.
type DatabaseAddResponse string

const (
	// DatabaseAddResponseCreated answers 201 with the Location and the new entry.
	DatabaseAddResponseCreated DatabaseAddResponse = "201"
	// DatabaseAddResponseNoContent answers 204 without Location. A client reads the collection to learn the ID.
	DatabaseAddResponseNoContent DatabaseAddResponse = "204"
)

// DatabaseDeleteResponse is the answer to the deletion of an entry of a secure boot database.
type DatabaseDeleteResponse string

const (
	// DatabaseDeleteResponseNoContent answers 204.
	DatabaseDeleteResponseNoContent DatabaseDeleteResponse = "204"
	// DatabaseDeleteResponseOK answers 200 with a top level @Message.ExtendedInfo.
	DatabaseDeleteResponseOK DatabaseDeleteResponse = AnswerOK
	// DatabaseDeleteResponseOKError answers 200 with the messages inside an error object.
	DatabaseDeleteResponseOKError DatabaseDeleteResponse = AnswerOKError
)

// CertificateIDStyle is how the proxy names the entries of the secure boot databases.
type CertificateIDStyle string

const (
	// CertificateIDStyleHash identifies an entry by the hash of its content.
	CertificateIDStyleHash CertificateIDStyle = "hash"
	// CertificateIDStyleSequential numbers the entries of all databases with one counter.
	// A deleted entry leaves a gap, because a number is used only once. A reset of the keys restarts the count.
	CertificateIDStyleSequential CertificateIDStyle = "sequential"
)

// certificateIDPrefixPattern keeps the prefix usable in a URI.
var certificateIDPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]*$`)

// SecureBoot describes the secure boot resource and its key databases.
type SecureBoot struct {
	// DatabaseWrite allows adding and deleting certificates and signatures over Redfish.
	DatabaseWrite      bool       `yaml:"database_write"`
	DatabaseWriteError ErrorShape `yaml:"database_write_error"`

	// DatabaseAddResponse and DatabaseDeleteResponse only matter with DatabaseWrite.
	DatabaseAddResponse    DatabaseAddResponse    `yaml:"database_add_response"`
	DatabaseDeleteResponse DatabaseDeleteResponse `yaml:"database_delete_response"`

	ResetKeys ResetKeys `yaml:"reset_keys"`

	CertificateIDStyle CertificateIDStyle `yaml:"certificate_id_style"`

	// CertificateIDPrefix precedes the number of a sequential ID, "Certificate" gives Certificate1.
	CertificateIDPrefix string `yaml:"certificate_id_prefix"`

	// EnableRequiresPowerOff refuses to change SecureBootEnable on a running instance. Without, the next reset applies it.
	EnableRequiresPowerOff bool `yaml:"enable_requires_power_off"`

	// PowerCycle powers the system off and on again during the boot that applies a secure boot change.
	PowerCycle PowerCycle `yaml:"power_cycle"`

	// EmptyAfterPowerOn reads the key databases empty for a while after a power on, like iDRAC while it collects the data of the host.
	EmptyAfterPowerOn EmptyAfterPowerOn `yaml:"empty_after_power_on"`
}

// EmptyAfterPowerOn makes the key databases read empty after the first power ons that follow a write to them.
type EmptyAfterPowerOn struct {
	// Times is the number of power ons after a write, after which the key databases read empty.
	Times int `yaml:"times"`

	// For is how long the key databases read empty after such a power on.
	For time.Duration `yaml:"for"`
}

// Enabled reports whether the key databases ever read empty.
func (e EmptyAfterPowerOn) Enabled() bool {
	return e.Times > 0 && e.For > 0
}

// PowerCycleTrigger is a secure boot change after which the system powers itself off and on again.
type PowerCycleTrigger string

const (
	// PowerCycleTriggerResetKeys is a reset of the keys.
	PowerCycleTriggerResetKeys PowerCycleTrigger = "reset_keys"

	// PowerCycleTriggerEnable switches secure boot on.
	PowerCycleTriggerEnable PowerCycleTrigger = "enable"
)

// PowerCycle is a power off and on that the system does by itself, like HPE iLO during the boot that applies a secure boot change.
type PowerCycle struct {
	After []PowerCycleTrigger `yaml:"after"`

	// OffAfter is the time from the power on to the power off.
	OffAfter time.Duration `yaml:"off_after"`

	// OffFor is the time from the power off to the power on.
	OffFor time.Duration `yaml:"off_for"`
}

// Triggers reports whether the change powers the system off and on again.
func (p PowerCycle) Triggers(trigger PowerCycleTrigger) bool {
	return slices.Contains(p.After, trigger)
}

func (p PowerCycle) validate() error {
	for _, trigger := range p.After {
		switch trigger {
		case PowerCycleTriggerResetKeys, PowerCycleTriggerEnable:

		default:
			return fmt.Errorf("unknown secure_boot.power_cycle.after entry %q", trigger)
		}
	}

	if p.OffAfter < 0 || p.OffFor < 0 {
		return errors.New("secure_boot.power_cycle.off_after and off_for must not be negative")
	}

	return nil
}

// ResetKeys describes the #SecureBoot.ResetKeys action.
type ResetKeys struct {
	Supported bool              `yaml:"supported"`
	Types     []ResetKeysType   `yaml:"types"`
	Response  ResetKeysResponse `yaml:"response"`
	Applies   ResetKeysApplies  `yaml:"applies"`

	// DisablesSecureBoot switches SecureBootEnable off, when the keys are deleted, like HPE iLO.
	DisablesSecureBoot bool `yaml:"disables_secure_boot"`

	// Unreachable stops answering for a while after the power on that follows the deletion of the keys, like Lenovo XCC.
	Unreachable Unreachable `yaml:"unreachable"`
}

// Unreachable is a time window, in which the BMC answers no request. A client only sees its own timeout.
type Unreachable struct {
	// After is the time from the power on to the start of the window.
	After time.Duration `yaml:"after"`

	// For is the length of the window. A request that arrives in the window gets no answer, also after the window.
	For time.Duration `yaml:"for"`
}

func (s SecureBoot) validate() error {
	err := s.DatabaseWriteError.Validate()
	if err != nil {
		return fmt.Errorf("secure_boot.database_write_error: %w", err)
	}

	switch s.DatabaseAddResponse {
	case DatabaseAddResponseCreated, DatabaseAddResponseNoContent:

	default:
		return fmt.Errorf("unknown secure_boot.database_add_response %q", s.DatabaseAddResponse)
	}

	switch s.DatabaseDeleteResponse {
	case DatabaseDeleteResponseNoContent, DatabaseDeleteResponseOK, DatabaseDeleteResponseOKError:

	default:
		return fmt.Errorf("unknown secure_boot.database_delete_response %q", s.DatabaseDeleteResponse)
	}

	err = s.ResetKeys.validate()
	if err != nil {
		return fmt.Errorf("secure_boot.reset_keys: %w", err)
	}

	err = s.PowerCycle.validate()
	if err != nil {
		return err
	}

	if s.EmptyAfterPowerOn.Times < 0 || s.EmptyAfterPowerOn.For < 0 {
		return errors.New("secure_boot.empty_after_power_on.times and for must not be negative")
	}

	switch s.CertificateIDStyle {
	case CertificateIDStyleHash:
		if s.CertificateIDPrefix != "" {
			return errors.New("secure_boot.certificate_id_prefix requires secure_boot.certificate_id_style sequential")
		}

	case CertificateIDStyleSequential:
		if !certificateIDPrefixPattern.MatchString(s.CertificateIDPrefix) {
			return fmt.Errorf("secure_boot.certificate_id_prefix %q must contain only letters, digits, dots, dashes and underscores", s.CertificateIDPrefix)
		}

	default:
		return fmt.Errorf("unknown secure_boot.certificate_id_style %q", s.CertificateIDStyle)
	}

	return nil
}

func (r ResetKeys) validate() error {
	if r.Supported && len(r.Types) == 0 {
		return errors.New("types must not be empty")
	}

	for _, resetKeysType := range r.Types {
		switch resetKeysType {
		case ResetKeysTypeDeleteAllKeys, ResetKeysTypeDeletePK:

		case "ResetAllKeysToDefault":
			// Incus has no API to restore the default keys of the firmware.
			return fmt.Errorf("type %q is not implemented", resetKeysType)

		default:
			return fmt.Errorf("unknown type %q", resetKeysType)
		}
	}

	switch r.Response {
	case ResetKeysResponseNoContent, ResetKeysResponseTask, ResetKeysResponseOK, ResetKeysResponseOKError:

	default:
		return fmt.Errorf("unknown response %q", r.Response)
	}

	if r.Unreachable.After < 0 || r.Unreachable.For < 0 {
		return errors.New("unreachable.after and for must not be negative")
	}

	switch r.Applies {
	case ResetKeysAppliesImmediately:

	case ResetKeysAppliesOnReset:
		if !r.Supported {
			return fmt.Errorf("applies %q requires supported", r.Applies)
		}

	default:
		return fmt.Errorf("unknown applies %q", r.Applies)
	}

	return nil
}
