package profile

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// AttributeType is the type of a synthetic BIOS attribute, as named by the Redfish attribute registry.
type AttributeType string

const (
	AttributeTypeEnumeration AttributeType = "Enumeration"
	AttributeTypeString      AttributeType = "String"
	AttributeTypeInteger     AttributeType = "Integer"
	AttributeTypeBoolean     AttributeType = "Boolean"
)

// PatchResponse is the answer to a BIOS settings PATCH.
type PatchResponse string

const (
	// PatchResponseTaskMonitor answers 202 with a task monitor in the location.
	PatchResponseTaskMonitor PatchResponse = "202-taskmonitor"
	// PatchResponseTask answers 202 with a task resource in the location and the body.
	PatchResponseTask PatchResponse = "202-task"
	// PatchResponseOK answers 200 with the Bios resource.
	PatchResponseOK PatchResponse = "200"
	// PatchResponseNoContent answers 204.
	PatchResponseNoContent PatchResponse = "204"
)

// ApplyTime is when BIOS attributes written by a PATCH take effect, as named by the Redfish settings schema.
type ApplyTime string

const (
	ApplyTimeImmediate ApplyTime = "Immediate"
	ApplyTimeOnReset   ApplyTime = "OnReset"
)

// ReservedConfigPrefix marks the instance config and device keys the proxy keeps its own state in.
const ReservedConfigPrefix = "user.redfish."

// registryIDPattern matches a versioned registry ID like BiosAttributeRegistry.v1_0_0.
var registryIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*\.v[0-9]+_[0-9]+_[0-9]+$`)

// attributeNamePattern keeps synthetic attributes apart from the incus.* attributes.
var attributeNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// BIOS describes the BIOS attributes and how the proxy answers writes to them.
type BIOS struct {
	PatchResponse PatchResponse `yaml:"patch_response"`

	// TaskResource exposes /redfish/v1/TaskService and its collection. Single tasks are always served, as task bodies link to them.
	TaskResource bool `yaml:"task_resource"`

	// SettingsObject makes Bios/Settings a separate resource, which holds the pending attributes.
	SettingsObject bool `yaml:"settings_object"`

	// Pending stages the attributes of a PATCH until the next reset.
	Pending bool `yaml:"pending"`

	ApplyTime ApplyTimes `yaml:"apply_time"`

	// PatchRequiresPowerOff refuses to stage attributes on a running instance, if they need a stopped one.
	PatchRequiresPowerOff bool `yaml:"patch_requires_power_off"`

	Registry Registry   `yaml:"registry"`
	Errors   BIOSErrors `yaml:"errors"`
}

// ApplyTimes are the apply times a PATCH may ask for.
type ApplyTimes struct {
	// Supported appears on the Bios resource. While it is empty, the proxy ignores the apply time of a PATCH.
	Supported []ApplyTime `yaml:"supported"`

	// Default applies to a PATCH without an apply time. Parse turns empty into OnReset with pending and Immediate otherwise.
	Default ApplyTime `yaml:"default"`
}

// Registry is the attribute registry of the synthetic BIOS attributes.
type Registry struct {
	// ID is the attribute registry ID, empty for no registry and no synthetic attributes.
	ID         string      `yaml:"id"`
	Attributes []Attribute `yaml:"attributes"`
}

// Attribute is one synthetic BIOS attribute.
type Attribute struct {
	Name     string        `yaml:"name"`
	Type     AttributeType `yaml:"type"`
	Values   []string      `yaml:"values"`
	Default  any           `yaml:"default"`
	ReadOnly bool          `yaml:"read_only"`
	Min      *int64        `yaml:"min"`
	Max      *int64        `yaml:"max"`

	// MapsTo is the Incus config key that stores the attribute instead of the proxy state.
	MapsTo string `yaml:"maps_to"`

	// ValueMap translates attribute values into the values of the Incus config key.
	ValueMap map[string]string `yaml:"value_map"`
}

// BIOSErrors are the answers to refused BIOS attribute writes.
type BIOSErrors struct {
	UnknownAttribute ErrorShape `yaml:"unknown_attribute"`
}

// DefaultValue returns the value of the attribute before it is written.
func (a Attribute) DefaultValue() any {
	switch a.Type {
	case AttributeTypeEnumeration:
		if a.Default == nil && len(a.Values) > 0 {
			return a.Values[0]
		}

	case AttributeTypeInteger:
		value, ok := integerValue(a.Default)
		if ok {
			return value
		}

		if a.Default != nil {
			return a.Default
		}

		if a.Min != nil {
			return *a.Min
		}

		return int64(0)

	case AttributeTypeBoolean:
		if a.Default == nil {
			return false
		}

	case AttributeTypeString:
		if a.Default == nil {
			return ""
		}
	}

	return a.Default
}

// integerValue converts the integer types the YAML decoder produces.
func integerValue(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true

	case int64:
		return v, true

	case uint64:
		return int64(v), true
	}

	return 0, false
}

func (b BIOS) validate() error {
	switch b.PatchResponse {
	case PatchResponseTaskMonitor, PatchResponseOK, PatchResponseNoContent:

	case PatchResponseTask:
		if !b.TaskResource {
			return fmt.Errorf("bios.patch_response %q requires bios.task_resource", b.PatchResponse)
		}

	default:
		return fmt.Errorf("unknown bios.patch_response %q", b.PatchResponse)
	}

	if b.Pending && !b.SettingsObject {
		return errors.New("bios.pending requires bios.settings_object")
	}

	err := b.ApplyTime.validate(b.Pending)
	if err != nil {
		return fmt.Errorf("bios.apply_time: %w", err)
	}

	err = b.Registry.validate()
	if err != nil {
		return fmt.Errorf("bios.registry: %w", err)
	}

	err = b.Errors.UnknownAttribute.Validate()
	if err != nil {
		return fmt.Errorf("bios.errors.unknown_attribute: %w", err)
	}

	return nil
}

// resolve fills in the values that depend on other settings, so the users of the profile do not repeat the rules.
func (b *BIOS) resolve() {
	if b.ApplyTime.Default == "" {
		b.ApplyTime.Default = ApplyTimeImmediate
		if b.Pending {
			b.ApplyTime.Default = ApplyTimeOnReset
		}
	}
}

func (a ApplyTimes) validate(pending bool) error {
	for _, applyTime := range append(slices.Clone(a.Supported), a.Default) {
		switch applyTime {
		case "", ApplyTimeImmediate:

		case ApplyTimeOnReset:
			if !pending {
				return fmt.Errorf("%q requires bios.pending", applyTime)
			}

		case "AtMaintenanceWindowStart", "InMaintenanceWindowOnReset", "Deferred":
			return fmt.Errorf("%q is not implemented, only %q and %q are", applyTime, ApplyTimeImmediate, ApplyTimeOnReset)

		default:
			return fmt.Errorf("unknown apply time %q", applyTime)
		}
	}

	if slices.Contains(a.Supported, "") {
		return errors.New("supported must not contain an empty apply time")
	}

	if a.Default != "" && len(a.Supported) > 0 && !slices.Contains(a.Supported, a.Default) {
		return fmt.Errorf("default %q is not supported", a.Default)
	}

	return nil
}

func (r Registry) validate() error {
	if r.ID == "" {
		if len(r.Attributes) > 0 {
			return errors.New("attributes require a registry id")
		}

		return nil
	}

	if !registryIDPattern.MatchString(r.ID) {
		return fmt.Errorf("id %q is not of the form <Name>.v<major>_<minor>_<patch>", r.ID)
	}

	names := map[string]bool{}

	for _, attribute := range r.Attributes {
		if names[attribute.Name] {
			return fmt.Errorf("attribute %q is declared twice", attribute.Name)
		}

		names[attribute.Name] = true

		err := attribute.validate()
		if err != nil {
			return fmt.Errorf("attribute %q: %w", attribute.Name, err)
		}
	}

	return nil
}

func (a Attribute) validate() error {
	if !attributeNamePattern.MatchString(a.Name) {
		return errors.New("name must start with a letter and contain only letters, digits and underscores")
	}

	if a.Type != AttributeTypeEnumeration && len(a.Values) > 0 {
		return errors.New("values are only allowed for enumerations")
	}

	if a.Type != AttributeTypeInteger && (a.Min != nil || a.Max != nil) {
		return errors.New("min and max are only allowed for integers")
	}

	err := a.validateDefault()
	if err != nil {
		return err
	}

	if strings.HasPrefix(a.MapsTo, ReservedConfigPrefix) {
		return fmt.Errorf("maps_to %q is reserved for the proxy", a.MapsTo)
	}

	if a.MapsTo == "" && len(a.ValueMap) > 0 {
		return errors.New("value_map requires maps_to")
	}

	if a.Type == AttributeTypeEnumeration && len(a.ValueMap) > 0 && !slices.Equal(slices.Sorted(maps.Keys(a.ValueMap)), slices.Sorted(slices.Values(a.Values))) {
		return errors.New("value_map must map exactly the values")
	}

	// A read translates the Incus value back, which needs a unique attribute value.
	if len(slices.Compact(slices.Sorted(maps.Values(a.ValueMap)))) != len(a.ValueMap) {
		return errors.New("value_map must map every value to a different Incus value")
	}

	return nil
}

func (a Attribute) validateDefault() error {
	switch a.Type {
	case AttributeTypeEnumeration:
		if len(a.Values) == 0 {
			return errors.New("enumeration without values")
		}

		if len(slices.Compact(slices.Sorted(slices.Values(a.Values)))) != len(a.Values) {
			return errors.New("values must be unique")
		}

		value, ok := a.DefaultValue().(string)
		if !ok || !slices.Contains(a.Values, value) {
			return fmt.Errorf("default %v is not one of the values", a.Default)
		}

	case AttributeTypeString:
		_, ok := a.DefaultValue().(string)
		if !ok {
			return fmt.Errorf("default %v is not a string", a.Default)
		}

	case AttributeTypeInteger:
		// The Redfish client library decodes the bounds as unsigned integers.
		if a.Min != nil && *a.Min < 0 {
			return errors.New("min must not be negative")
		}

		if a.Min != nil && a.Max != nil && *a.Min > *a.Max {
			return errors.New("min is greater than max")
		}

		value, ok := a.DefaultValue().(int64)
		if !ok {
			return fmt.Errorf("default %v is not an integer", a.Default)
		}

		if (a.Min != nil && value < *a.Min) || (a.Max != nil && value > *a.Max) {
			return fmt.Errorf("default %d is out of range", value)
		}

	case AttributeTypeBoolean:
		_, ok := a.DefaultValue().(bool)
		if !ok {
			return fmt.Errorf("default %v is not a boolean", a.Default)
		}

	default:
		return fmt.Errorf("unknown type %q", a.Type)
	}

	return nil
}
