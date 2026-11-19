package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

const redfishConfigPrefix = profile.ReservedConfigPrefix

const biosConfigPrefix = redfishConfigPrefix + "bios."

const biosPendingKey = redfishConfigPrefix + "bios-pending" // Attribute names contain no dash, so the key is apart from the attribute values.

const biosAppliedKey = redfishConfigPrefix + "bios-applied"

const baseRegistry = "Base.1.18."

type biosStore struct {
	bios profile.BIOS
}

func (b biosStore) attributes(instance *incusapi.Instance) BiosV130Attributes {
	attributes := BiosV130Attributes{}

	for configKey, configValue := range instance.Config {
		if strings.HasPrefix(configKey, redfishConfigPrefix) {
			continue
		}

		attributes[fmt.Sprintf("incus.config.%s", configKey)] = configValue
	}

	for deviceName, deviceConfig := range instance.Devices {
		deviceConfigJSON, _ := json.Marshal(deviceConfig)
		attributes[fmt.Sprintf("incus.devices.%s", deviceName)] = string(deviceConfigJSON)
	}

	for _, attribute := range b.bios.Registry.Attributes {
		if !b.requirementMet(instance, attribute) {
			continue
		}

		attributes[attribute.Name] = syntheticValue(instance, attribute)
	}

	return attributes
}

func (b biosStore) requirementMet(instance *incusapi.Instance, attribute profile.Attribute) bool {
	if attribute.Requires == nil {
		return true
	}

	index := slices.IndexFunc(b.bios.Registry.Attributes, func(a profile.Attribute) bool { return a.Name == attribute.Requires.Attribute })
	if index < 0 {
		return false
	}

	return syntheticValue(instance, b.bios.Registry.Attributes[index]) == attribute.Requires.Value
}

func syntheticValue(instance *incusapi.Instance, attribute profile.Attribute) any {
	if attribute.MapsTo == "" {
		value, ok := instance.Config[biosConfigPrefix+attribute.Name]
		if !ok {
			return attribute.DefaultValue()
		}

		return parseAttributeValue(attribute, value)
	}

	value, ok := instanceConfig(instance)[attribute.MapsTo]
	if !ok {
		return attribute.DefaultValue()
	}

	for attributeValue, mappedValue := range attribute.ValueMap {
		if mappedValue == value {
			return parseAttributeValue(attribute, attributeValue)
		}
	}

	return parseAttributeValue(attribute, value)
}

func parseAttributeValue(attribute profile.Attribute, value string) any {
	switch attribute.Type {
	case profile.AttributeTypeInteger:
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			return parsed
		}

	case profile.AttributeTypeBoolean:
		parsed, err := strconv.ParseBool(value)
		if err == nil {
			return parsed
		}
	}

	return value
}

func (b biosStore) apply(instance *incusapi.Instance, put *incusapi.InstancePut, attributes map[string]any) error {
	// Check requirements against the current instance, never against attributes written along with them.
	if put.Config == nil {
		put.Config = map[string]string{}
	}

	if put.Devices == nil {
		put.Devices = incusapi.DevicesMap{}
	}

	for name, value := range attributes {
		var err error

		switch {
		case strings.HasPrefix(name, "incus.config."):
			err = applyConfigAttribute(put, name, value)

		case strings.HasPrefix(name, "incus.devices."):
			err = applyDeviceAttribute(put, name, value)

		default:
			err = b.applySyntheticAttribute(instance, put, name, value)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func applyConfigAttribute(put *incusapi.InstancePut, name string, value any) error {
	valueStr, ok := value.(string)
	if !ok {
		return statusErrorf(http.StatusBadRequest, "bios attribute %q has invalid type %T, string expected", name, value)
	}

	configKey := strings.TrimPrefix(name, "incus.config.")
	if configKey == "" {
		return statusErrorf(http.StatusBadRequest, "bios attribute %q has an empty config key", name)
	}

	if strings.HasPrefix(configKey, redfishConfigPrefix) {
		return statusErrorf(http.StatusBadRequest, "bios attribute %q is reserved", name)
	}

	if valueStr == "" {
		delete(put.Config, configKey)
		return nil
	}

	put.Config[configKey] = valueStr

	return nil
}

func applyDeviceAttribute(put *incusapi.InstancePut, name string, value any) error {
	valueStr, ok := value.(string)
	if !ok {
		return statusErrorf(http.StatusBadRequest, "bios attribute %q has invalid type %T, string expected", name, value)
	}

	deviceName := strings.TrimPrefix(name, "incus.devices.")
	if deviceName == "" {
		return statusErrorf(http.StatusBadRequest, "bios attribute %q has an empty device name", name)
	}

	if valueStr == "" {
		delete(put.Devices, deviceName)
		return nil
	}

	var deviceConfig map[string]string

	err := json.Unmarshal([]byte(valueStr), &deviceConfig)
	if err != nil {
		return statusErrorf(http.StatusBadRequest, "invalid value for device %q, JSON string expected: %v", deviceName, err)
	}

	put.Devices[deviceName] = deviceConfig

	return nil
}

func (b biosStore) applySyntheticAttribute(instance *incusapi.Instance, put *incusapi.InstancePut, name string, value any) error {
	index := slices.IndexFunc(b.bios.Registry.Attributes, func(a profile.Attribute) bool { return a.Name == name })
	if index < 0 {
		shape := b.bios.Errors.UnknownAttribute
		return attributeError(shape.Status, shape.MessageID, name, []string{name}, "bios attribute %q is not supported", name)
	}

	attribute := b.bios.Registry.Attributes[index]

	if !b.requirementMet(instance, attribute) {
		shape := b.bios.Errors.DeferredRejected
		return attributeError(shape.Status, shape.MessageID, name, []string{name}, "bios attribute %q requires %s to be %v", name, attribute.Requires.Attribute, attribute.Requires.Value)
	}

	if attribute.ReadOnly {
		return attributeError(http.StatusBadRequest, baseRegistry+"PropertyNotWritable", name, []string{name}, "bios attribute %q is read only", name)
	}

	valueStr, err := formatAttributeValue(attribute, value)
	if err != nil {
		return err
	}

	if attribute.MapsTo == "" {
		put.Config[biosConfigPrefix+name] = valueStr
		return nil
	}

	mappedValue, ok := attribute.ValueMap[valueStr]
	if !ok {
		mappedValue = valueStr
	}

	put.Config[attribute.MapsTo] = mappedValue

	return nil
}

func formatAttributeValue(attribute profile.Attribute, value any) (string, error) {
	args := []string{fmt.Sprint(value), attribute.Name}

	switch attribute.Type {
	case profile.AttributeTypeEnumeration:
		valueStr, ok := value.(string)
		if !ok {
			return "", attributeError(http.StatusBadRequest, baseRegistry+"PropertyValueTypeError", attribute.Name, args, "bios attribute %q has invalid type %T, string expected", attribute.Name, value)
		}

		if !slices.Contains(attribute.Values, valueStr) {
			return "", attributeError(http.StatusBadRequest, baseRegistry+"PropertyValueNotInList", attribute.Name, args, "bios attribute %q value %q is not one of %s", attribute.Name, valueStr, strings.Join(attribute.Values, ", "))
		}

		return valueStr, nil

	case profile.AttributeTypeString:
		valueStr, ok := value.(string)
		if !ok {
			return "", attributeError(http.StatusBadRequest, baseRegistry+"PropertyValueTypeError", attribute.Name, args, "bios attribute %q has invalid type %T, string expected", attribute.Name, value)
		}

		return valueStr, nil

	case profile.AttributeTypeInteger:
		// JSON numbers decode as float64. As a float64, math.MaxInt64 rounds up to 2^63, which int64 cannot hold.
		number, ok := value.(float64)
		if !ok || number != math.Trunc(number) || number < math.MinInt64 || number >= math.MaxInt64 {
			return "", attributeError(http.StatusBadRequest, baseRegistry+"PropertyValueTypeError", attribute.Name, args, "bios attribute %q has invalid value %v, integer expected", attribute.Name, value)
		}

		integer := int64(number)
		if (attribute.Min != nil && integer < *attribute.Min) || (attribute.Max != nil && integer > *attribute.Max) {
			return "", attributeError(http.StatusBadRequest, baseRegistry+"PropertyValueOutOfRange", attribute.Name, args, "bios attribute %q value %d is out of range", attribute.Name, integer)
		}

		return strconv.FormatInt(integer, 10), nil

	case profile.AttributeTypeBoolean:
		boolean, ok := value.(bool)
		if !ok {
			return "", attributeError(http.StatusBadRequest, baseRegistry+"PropertyValueTypeError", attribute.Name, args, "bios attribute %q has invalid type %T, boolean expected", attribute.Name, value)
		}

		return strconv.FormatBool(boolean), nil
	}

	return "", statusErrorf(http.StatusInternalServerError, "bios attribute %q has unknown type %q", attribute.Name, attribute.Type)
}

func (b biosStore) requiresStop(attributes map[string]any) bool {
	for name := range attributes {
		if b.attributeRequiresStop(name) {
			return true
		}
	}

	return false
}

func (b biosStore) attributeRequiresStop(name string) bool {
	index := slices.IndexFunc(b.bios.Registry.Attributes, func(a profile.Attribute) bool { return a.Name == name })

	return index < 0 || b.bios.Registry.Attributes[index].MapsTo != ""
}

type settingsApplyTime struct {
	ApplyTime profile.ApplyTime `json:"ApplyTime"`
}

func (b biosStore) applyTime(requested *settingsApplyTime) (profile.ApplyTime, error) {
	// Without published apply times, clients assume any is supported, so the request is not refused.
	if requested == nil || len(b.bios.ApplyTime.Supported) == 0 {
		return b.bios.ApplyTime.Default, nil
	}

	if !slices.Contains(b.bios.ApplyTime.Supported, requested.ApplyTime) {
		return "", propertyError(http.StatusBadRequest, baseRegistry+"PropertyValueNotInList", "#/@Redfish.SettingsApplyTime/ApplyTime", []string{string(requested.ApplyTime), "ApplyTime"}, "apply time %q is not supported", requested.ApplyTime)
	}

	return requested.ApplyTime, nil
}

type pendingBIOS struct {
	TaskID     string            `json:"task_id"`
	ApplyTime  profile.ApplyTime `json:"apply_time"`
	Attributes map[string]any    `json:"attributes"`
	StagedAt   time.Time         `json:"staged_at"`
}

func (b biosStore) pending(instance *incusapi.Instance) (*pendingBIOS, error) {
	value, ok := instance.Config[biosPendingKey]
	if !ok {
		return nil, nil
	}

	staged := &pendingBIOS{}

	err := json.Unmarshal([]byte(value), staged)
	if err != nil {
		return nil, fmt.Errorf("decode pending bios attributes: %w", err)
	}

	return staged, nil
}

func (b biosStore) setPending(put *incusapi.InstancePut, staged *pendingBIOS) error {
	if staged == nil || len(staged.Attributes) == 0 {
		delete(put.Config, biosPendingKey)
		return nil
	}

	data, err := json.Marshal(staged)
	if err != nil {
		return fmt.Errorf("encode pending bios attributes: %w", err)
	}

	if put.Config == nil {
		put.Config = map[string]string{}
	}

	put.Config[biosPendingKey] = string(data)

	return nil
}

func attributeError(status int, messageID string, name string, messageArgs []string, format string, args ...any) error {
	return propertyError(status, messageID, "#/Attributes/"+name, messageArgs, format, args...)
}

func propertyError(status int, messageID string, property string, messageArgs []string, format string, args ...any) error {
	err := fmt.Errorf(format, args...)

	return &statusError{
		status: status,
		err:    err,
		messages: []redfishMessage{{
			MessageID:         messageID,
			Message:           err.Error(),
			MessageArgs:       messageArgs,
			RelatedProperties: []string{property},
		}},
	}
}
