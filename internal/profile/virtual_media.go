package profile

import (
	"fmt"
	"regexp"
)

// MediaInsert is how virtual media is inserted.
type MediaInsert string

const (
	// MediaInsertStandard offers the #VirtualMedia.InsertMedia action.
	MediaInsertStandard MediaInsert = "standard"
	// MediaInsertOEM offers an OEM action instead of the standard one.
	MediaInsertOEM MediaInsert = "oem"
	// MediaInsertPatch offers no insert action, only a PATCH of the resource.
	MediaInsertPatch MediaInsert = "patch"
)

// InsertResponse is the answer to an insert action.
type InsertResponse string

const (
	// InsertResponseNoContent inserts before it answers 204.
	InsertResponseNoContent InsertResponse = "204"
	// InsertResponseTask answers 202 with a task monitor and inserts in the background.
	InsertResponseTask InsertResponse = "202-task"
)

// oemNamePattern keeps the OEM names usable in an action name and a URI.
var oemNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// IsOEMName reports whether the name is usable as virtual_media.oem_vendor or virtual_media.oem_action.
func IsOEMName(name string) bool {
	return oemNamePattern.MatchString(name)
}

// VirtualMedia describes how virtual media is inserted.
type VirtualMedia struct {
	Insert MediaInsert `yaml:"insert"`

	// OEMVendor is the key below Actions.Oem. It must not name a real vendor.
	OEMVendor string `yaml:"oem_vendor"`
	OEMAction string `yaml:"oem_action"`

	// InsertResponse applies to the insert actions. A PATCH always answers 204.
	InsertResponse InsertResponse `yaml:"insert_response"`

	// BootOverrideOnce offers BootSourceOverrideEnabled Once. Without, only Continuous is offered, as before profiles existed.
	BootOverrideOnce bool `yaml:"boot_override_once"`
}

func (v VirtualMedia) validate() error {
	switch v.Insert {
	case MediaInsertStandard, MediaInsertPatch:

	case MediaInsertOEM:
		if !oemNamePattern.MatchString(v.OEMVendor) {
			return fmt.Errorf("virtual_media.oem_vendor %q must start with a letter and contain only letters and digits", v.OEMVendor)
		}

		if !oemNamePattern.MatchString(v.OEMAction) {
			return fmt.Errorf("virtual_media.oem_action %q must start with a letter and contain only letters and digits", v.OEMAction)
		}

	default:
		return fmt.Errorf("unknown virtual_media.insert %q", v.Insert)
	}

	switch v.InsertResponse {
	case InsertResponseNoContent, InsertResponseTask:

	default:
		return fmt.Errorf("unknown virtual_media.insert_response %q", v.InsertResponse)
	}

	return nil
}
