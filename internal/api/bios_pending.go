package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// stageBIOS merges the attributes into the staged ones and returns the ID of their task.
func (s redfishServer) stageBIOS(attributes map[string]any, applyTime profile.ApplyTime) (string, error) {
	var created string
	var taskID string

	err := s.updateInstance(func(instance *incusapi.Instance, put *incusapi.InstancePut) (bool, error) {
		if s.profile.BIOS.PatchRequiresPowerOff && s.bios.requiresStop(attributes) && instance.Status != "Stopped" {
			return false, statusErrorf(http.StatusPreconditionFailed, "instance %q is not stopped", s.instanceName)
		}

		staged, err := s.bios.pending(instance)
		if err != nil {
			return false, err
		}

		if staged == nil {
			staged = &pendingBIOS{StagedAt: time.Now().UTC()}
		}

		if staged.Attributes == nil {
			staged.Attributes = map[string]any{}
		}

		maps.Copy(staged.Attributes, attributes)
		staged.ApplyTime = applyTime

		// Check the values now, so a refused value fails the PATCH and not the reset.
		scratch := clonePut(*put)

		err = s.bios.apply(instance, &scratch, staged.Attributes)
		if err != nil {
			return false, err
		}

		t, ok := s.tasks.get(staged.TaskID)
		if !ok || taskEnded(t.state) {
			if created == "" {
				created = s.tasks.create(s.biosPath(), TaskV174TaskStateRunning)
			}

			staged.TaskID = created
		}

		taskID = staged.TaskID

		return true, s.bios.setPending(put, staged)
	})
	if err != nil {
		if created != "" {
			s.tasks.fail(created, errorMessages(err)...)
		}

		return "", err
	}

	// A retry after a conflict may reuse the task of a concurrent PATCH.
	if created != "" && created != taskID {
		s.tasks.remove(created)
	}

	return taskID, nil
}

// applyPendingBIOS applies the staged attributes and ends their task once none are left.
// stopped tells whether the proxy may write attributes that need a stopped instance.
func (s redfishServer) applyPendingBIOS(stopped bool) error {
	var taskID string
	var applyErr error
	var left []string

	err := s.updateInstance(func(instance *incusapi.Instance, put *incusapi.InstancePut) (bool, error) {
		staged, err := s.bios.pending(instance)
		if err != nil || staged == nil {
			return false, err
		}

		ready := map[string]any{}
		for name, value := range staged.Attributes {
			if stopped || !s.bios.attributeRequiresStop(name) {
				ready[name] = value
			}
		}

		if len(ready) == 0 && len(staged.Attributes) > 0 {
			return false, nil
		}

		taskID = staged.TaskID

		applied := clonePut(*put)

		applyErr = s.bios.apply(instance, &applied, ready)
		if applyErr != nil {
			// Firmware drops the whole job on a refused value and boots anyway.
			staged = nil
		} else {
			*put = applied
			put.Config[biosAppliedKey] = time.Now().UTC().Format(time.RFC3339Nano)

			for name := range ready {
				delete(staged.Attributes, name)
			}

			left = slices.Sorted(maps.Keys(staged.Attributes))
		}

		return true, s.bios.setPending(put, staged)
	})
	if err != nil {
		return err
	}

	switch {
	case taskID == "":

	case applyErr != nil:
		log.Printf("staged bios attributes of instance %q failed to apply: %v", s.instanceName, applyErr)
		s.tasks.fail(taskID, errorMessages(applyErr)...)

	case len(left) > 0:
		log.Printf("bios attributes %s of instance %q stay pending, they need a stopped instance", strings.Join(left, ", "), s.instanceName)

	default:
		s.tasks.complete(taskID)
	}

	return nil
}

// applyPendingOnReset applies the staged attributes before a reset powers the instance on and returns the instance state action left to do.
// A restart becomes a stop and a start, if staged attributes need a stopped instance.
func (s redfishServer) applyPendingOnReset(instance *incusapi.Instance, action string, force bool) (string, error) {
	stopped := instance.Status == "Stopped"

	if action == "restart" && !stopped {
		staged, err := s.bios.pending(instance)
		if err != nil {
			return "", err
		}

		if staged != nil && s.bios.requiresStop(staged.Attributes) {
			err = s.changeInstanceState("stop", force)
			if err != nil {
				return "", err
			}

			stopped = true
			action = "start"
		}
	}

	return action, s.applyPendingBIOS(stopped)
}

// applyPendingOnPowerOn applies the staged attributes after a power on, which the proxy did not issue.
// Attributes needing a stopped instance stay pending, as the instance already runs.
func (s redfishServer) applyPendingOnPowerOn(_ context.Context, _ powerEvent) error {
	s.powerMu.Lock()
	defer s.powerMu.Unlock()

	return s.applyPendingBIOS(false)
}

// updateInstance changes the instance guarded by its ETag, so a concurrent change fails instead of getting lost.
// It retries once on such a conflict. change returns false to leave the instance as it is.
func (s redfishServer) updateInstance(change func(instance *incusapi.Instance, put *incusapi.InstancePut) (bool, error)) error {
	err := s.updateInstanceOnce(change)
	if incusapi.StatusErrorCheck(err, http.StatusPreconditionFailed) {
		err = s.updateInstanceOnce(change)
	}

	return err
}

func (s redfishServer) updateInstanceOnce(change func(instance *incusapi.Instance, put *incusapi.InstancePut) (bool, error)) error {
	instance, etag, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		return err
	}

	put := instance.Writable()

	changed, err := change(instance, &put)
	if err != nil || !changed {
		return err
	}

	op, err := s.client.UpdateInstance(s.instanceName, put, etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

func (s redfishServer) changeInstanceState(action string, force bool) error {
	op, err := s.client.UpdateInstanceState(s.instanceName, incusapi.InstanceStatePut{
		Action: action,
		Force:  force,
	}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

// clonePut copies the maps of the writable instance, because apply changes them in place.
func clonePut(put incusapi.InstancePut) incusapi.InstancePut {
	put.Config = maps.Clone(put.Config)
	put.Devices = maps.Clone(put.Devices)

	return put
}

// errorMessages returns the Redfish messages of an error for a task.
func errorMessages(err error) []redfishMessage {
	var statusErr *statusError
	if errors.As(err, &statusErr) && len(statusErr.messages) > 0 {
		return statusErr.messages
	}

	return []redfishMessage{{MessageID: baseRegistry + "GeneralError", Message: fmt.Sprint(err)}}
}
