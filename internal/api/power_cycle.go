package api

import (
	"errors"
	"fmt"
	"log"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	incusutil "github.com/lxc/incus/v7/shared/util"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

const powerCycleKey = redfishConfigPrefix + "secureboot.power_cycle"

var errPowerCycleSkipped = errors.New("the power state changed in the meantime")

func (s redfishServer) markPowerCycle(put *incusapi.InstancePut, trigger profile.PowerCycleTrigger) {
	if !s.profile.SecureBoot.PowerCycle.Triggers(trigger) {
		return
	}

	if put.Config == nil {
		put.Config = map[string]string{}
	}

	put.Config[powerCycleKey] = string(trigger)
}

func (s redfishServer) markSecureBootEnable(instance *incusapi.Instance, put *incusapi.InstancePut, enable string) {
	// The own config of the instance holds the value the proxy wrote, the expanded config the value of its profiles.
	current, ok := put.Config["security.secureboot"]
	if !ok {
		current = instance.ExpandedConfig["security.secureboot"]
	}

	if enable == "true" && !incusutil.IsTrueOrEmpty(current) {
		s.markPowerCycle(put, profile.PowerCycleTriggerEnable)
	}
}

func (s redfishServer) afterResetKeys(put *incusapi.InstancePut) {
	if s.profile.SecureBoot.ResetKeys.DisablesSecureBoot {
		if put.Config == nil {
			put.Config = map[string]string{}
		}

		put.Config["security.secureboot"] = "false"
	}

	s.markPowerCycle(put, profile.PowerCycleTriggerResetKeys)
}

func (s redfishServer) startPowerCycle() error {
	instance, _, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		return err
	}

	_, marked := instance.Config[powerCycleKey]
	if !marked {
		return nil
	}

	// The marker stays until the admitted work takes it, so a refused power cycle runs after the next power on.
	admitted := s.goBackground(s.powerCycle)
	if !admitted {
		return fmt.Errorf("keep the marker for the next power on: %w", errShuttingDown)
	}

	return nil
}

func (s redfishServer) powerCycle() {
	marked, err := s.takePowerCycleMarker()
	if err != nil {
		log.Printf("power cycle of instance %q after a secure boot change: %v", s.instanceName, err)
		return
	}

	if !marked {
		return
	}

	cycle := s.profile.SecureBoot.PowerCycle

	err = s.powerCycleStep(s.profile.Scaled(cycle.OffAfter), "Running", "stop")
	if err == nil {
		err = s.powerCycleStep(s.profile.Scaled(cycle.OffFor), "Stopped", "start")
	}

	if err != nil {
		log.Printf("power cycle of instance %q after a secure boot change ended: %v", s.instanceName, err)
	}
}

func (s redfishServer) takePowerCycleMarker() (bool, error) {
	s.powerMu.Lock()
	defer s.powerMu.Unlock()

	// A canceled shutdown keeps the marker for the next power on.
	err := s.ctx.Err()
	if err != nil {
		return false, err
	}

	marked := false

	err = s.updateInstance(func(_ *incusapi.Instance, put *incusapi.InstancePut) (bool, error) {
		_, marked = put.Config[powerCycleKey]
		if !marked {
			return false, nil
		}

		delete(put.Config, powerCycleKey)

		return true, nil
	})

	return marked, err
}

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

	if action == "stop" {
		s.keyResetView.show()
	}

	if action == "start" {
		s.resets.poweredOn(s.now())
	}

	return nil
}
