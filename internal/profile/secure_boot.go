package profile

import (
	"errors"
	"fmt"
	"regexp"
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
)

// CertificateIDStyle is how the proxy names the entries of the secure boot databases.
type CertificateIDStyle string

const (
	// CertificateIDStyleHash identifies an entry by the hash of its content.
	CertificateIDStyleHash CertificateIDStyle = "hash"
	// CertificateIDStyleSequential numbers the entries from 1, the numbers shift when a client deletes an entry.
	CertificateIDStyleSequential CertificateIDStyle = "sequential"
)

// certificateIDPrefixPattern keeps the prefix usable in a URI.
var certificateIDPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9_-]*$`)

// SecureBoot describes the secure boot resource and its key databases.
type SecureBoot struct {
	// DatabaseWrite allows adding and deleting certificates and signatures over Redfish.
	DatabaseWrite      bool       `yaml:"database_write"`
	DatabaseWriteError ErrorShape `yaml:"database_write_error"`

	ResetKeys ResetKeys `yaml:"reset_keys"`

	CertificateIDStyle CertificateIDStyle `yaml:"certificate_id_style"`

	// CertificateIDPrefix precedes the number of a sequential ID, "Certificate" gives Certificate1.
	CertificateIDPrefix string `yaml:"certificate_id_prefix"`

	// EnableRequiresPowerOff refuses to change SecureBootEnable on a running instance. Without, the next reset applies it.
	EnableRequiresPowerOff bool `yaml:"enable_requires_power_off"`
}

// ResetKeys describes the #SecureBoot.ResetKeys action.
type ResetKeys struct {
	Supported bool              `yaml:"supported"`
	Types     []ResetKeysType   `yaml:"types"`
	Response  ResetKeysResponse `yaml:"response"`
}

func (s SecureBoot) validate() error {
	err := s.DatabaseWriteError.Validate()
	if err != nil {
		return fmt.Errorf("secure_boot.database_write_error: %w", err)
	}

	err = s.ResetKeys.validate()
	if err != nil {
		return fmt.Errorf("secure_boot.reset_keys: %w", err)
	}

	switch s.CertificateIDStyle {
	case CertificateIDStyleHash:
		if s.CertificateIDPrefix != "" {
			return errors.New("secure_boot.certificate_id_prefix requires secure_boot.certificate_id_style sequential")
		}

	case CertificateIDStyleSequential:
		if !certificateIDPrefixPattern.MatchString(s.CertificateIDPrefix) {
			return fmt.Errorf("secure_boot.certificate_id_prefix %q must contain only letters, digits, dashes and underscores", s.CertificateIDPrefix)
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
	case ResetKeysResponseNoContent, ResetKeysResponseTask:

	default:
		return fmt.Errorf("unknown response %q", r.Response)
	}

	return nil
}
