package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	incusapi "github.com/lxc/incus/v7/shared/api"
)

// redfishConfigPrefix marks the instance config keys the proxy keeps its own state in.
const redfishConfigPrefix = "user.redfish."

// biosStore maps BIOS attributes to the configuration of the instance.
type biosStore struct{}

// attributes returns the current BIOS attributes of the instance.
func (biosStore) attributes(instance *incusapi.Instance) BiosV130Attributes {
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

	return attributes
}

// apply writes the attributes into the writable instance, it does not talk to Incus.
func (biosStore) apply(put *incusapi.InstancePut, attributes map[string]any) error {
	if put.Config == nil {
		put.Config = map[string]string{}
	}

	if put.Devices == nil {
		put.Devices = incusapi.DevicesMap{}
	}

	for name, value := range attributes {
		valueStr, ok := value.(string)
		if !ok {
			return statusErrorf(http.StatusBadRequest, "bios attribute %q has invalid type %T, string expected", name, value)
		}

		switch {
		case strings.HasPrefix(name, "incus.config."):
			configKey := strings.TrimPrefix(name, "incus.config.")
			if configKey == "" {
				return statusErrorf(http.StatusBadRequest, "bios attribute %q has an empty config key", name)
			}

			if strings.HasPrefix(configKey, redfishConfigPrefix) {
				return statusErrorf(http.StatusBadRequest, "bios attribute %q is reserved", name)
			}

			if valueStr == "" {
				delete(put.Config, configKey)
				continue
			}

			put.Config[configKey] = valueStr

		case strings.HasPrefix(name, "incus.devices."):
			deviceName := strings.TrimPrefix(name, "incus.devices.")
			if deviceName == "" {
				return statusErrorf(http.StatusBadRequest, "bios attribute %q has an empty device name", name)
			}

			if valueStr == "" {
				delete(put.Devices, deviceName)
				continue
			}

			var deviceConfig map[string]string

			err := json.Unmarshal([]byte(valueStr), &deviceConfig)
			if err != nil {
				return statusErrorf(http.StatusBadRequest, "invalid value for device %q, JSON string expected: %v", deviceName, err)
			}

			put.Devices[deviceName] = deviceConfig

		default:
			return statusErrorf(http.StatusBadRequest, "bios attribute %q is not supported", name)
		}
	}

	return nil
}
