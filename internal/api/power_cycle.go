package api

import (
	"errors"
	"log"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	incusutil "github.com/lxc/incus/v7/shared/util"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// powerCycleKey marks a secure boot change. The next power on powers the instance off and on again.
const powerCycleKey = redfishConfigPrefix + "secureboot.power_cycle"

// errPowerCycleSkipped ends a power cycle, after a client changed the power state in the meantime.
var errPowerCycleSkipped = errors.New("the power state changed in the meantime")

// markPowerCycle marks the change, if the profile powers the system off and on again after it.
func (s redfishServer) markPowerCycle(put *incusapi.InstancePut, trigger profile.PowerCycleTrigger) {
	if !s.profile.SecureBoot.PowerCycle.Triggers(trigger) {
		return
	}

	if put.Config == nil {
		put.Config = map[string]string{}
	}

	put.Config[powerCycleKey] = string(trigger)
}

// markSecureBootEnable marks a change that switches secure boot on.
// The own config of the instance holds the value the proxy wrote, the expanded config the value of its profiles.
func (s redfishServer) markSecureBootEnable(instance *incusapi.Instance, put *incusapi.InstancePut, enable string) {
	current, ok := put.Config["security.secureboot"]
	if !ok {
		current = instance.ExpandedConfig["security.secureboot"]
	}

	if enable == "true" && !incusutil.IsTrueOrEmpty(current) {
		s.markPowerCycle(put, profile.PowerCycleTriggerEnable)
	}
}

// afterResetKeys changes the instance like the firmware of the profile, once the keys are deleted.
func (s redfishServer) afterResetKeys(put *incusapi.InstancePut) {
	if s.profile.SecureBoot.ResetKeys.DisablesSecureBoot {
		if put.Config == nil {
			put.Config = map[string]string{}
		}

		put.Config["security.secureboot"] = "false"
	}

	s.markPowerCycle(put, profile.PowerCycleTriggerResetKeys)
}

// startPowerCycle removes the mark of a secure boot change and powers the instance off and on again in the background.
func (s redfishServer) startPowerCycle() error {
	marked := false

	err := s.updateInstance(func(_ *incusapi.Instance, put *incusapi.InstancePut) (bool, error) {
		_, marked = put.Config[powerCycleKey]
		if !marked {
			return false, nil
		}

		delete(put.Config, powerCycleKey)

		return true, nil
	})
	if err != nil || !marked {
		return err
	}

	go func() {
		cycle := s.profile.SecureBoot.PowerCycle

		err := s.powerCycleStep(s.profile.Scaled(cycle.OffAfter), "Running", "stop")
		if err == nil {
			err = s.powerCycleStep(s.profile.Scaled(cycle.OffFor), "Stopped", "start")
		}

		if err != nil {
			log.Printf("power cycle of instance %q after a secure boot change ended: %v", s.instanceName, err)
		}
	}()

	return nil
}

// powerCycleStep waits and changes the power state, if the instance still has the status from.
func (s redfishServer) powerCycleStep(d time.Duration, from string, action string) error {
	err := s.wait(s.ctx, d)
	if err != nil {
		return err
	}

	s.powerMu.Lock()
	defer s.powerMu.Unlock()

	instance, _, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		return err
	}

	if instance.Status != from {
		return errPowerCycleSkipped
	}

	err = s.changeInstanceState(action, true)
	if err != nil {
		return err
	}

	if action == "start" {
		s.resets.poweredOn(s.now())
	}

	return nil
}
