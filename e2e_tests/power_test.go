//go:build e2e

package e2e_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPower_Generic(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "generic")

	steps := []struct {
		name   string
		action func(t *testing.T) response

		wantStatus     int
		wantIncus      string
		wantPowerState string
	}{
		{
			name:           "On starts the VM",
			action:         func(t *testing.T) response { t.Helper(); return p.reset(t, "On") },
			wantStatus:     http.StatusNoContent,
			wantIncus:      "Running",
			wantPowerState: "On",
		},
		{
			name:           "On of a running VM changes nothing",
			action:         func(t *testing.T) response { t.Helper(); return p.reset(t, "On") },
			wantStatus:     http.StatusNoContent,
			wantIncus:      "Running",
			wantPowerState: "On",
		},
		{
			name:           "ForceRestart keeps the VM running",
			action:         func(t *testing.T) response { t.Helper(); return p.reset(t, "ForceRestart") },
			wantStatus:     http.StatusNoContent,
			wantIncus:      "Running",
			wantPowerState: "On",
		},
		{
			name:           "ForceOff stops the VM",
			action:         func(t *testing.T) response { t.Helper(); return p.reset(t, "ForceOff") },
			wantStatus:     http.StatusNoContent,
			wantIncus:      "Stopped",
			wantPowerState: "Off",
		},
		{
			name:           "ForceOff of a stopped VM changes nothing",
			action:         func(t *testing.T) response { t.Helper(); return p.reset(t, "ForceOff") },
			wantStatus:     http.StatusNoContent,
			wantIncus:      "Stopped",
			wantPowerState: "Off",
		},
		{
			name: "a start outside of the proxy shows as On",
			action: func(t *testing.T) response {
				t.Helper()
				changeState(t, vm, "start")

				return response{status: http.StatusNoContent}
			},
			wantStatus:     http.StatusNoContent,
			wantIncus:      "Running",
			wantPowerState: "On",
		},
		{
			name:           "ForceOff works after a start outside of the proxy",
			action:         func(t *testing.T) response { t.Helper(); return p.reset(t, "ForceOff") },
			wantStatus:     http.StatusNoContent,
			wantIncus:      "Stopped",
			wantPowerState: "Off",
		},
	}

	resp := p.do(t, http.MethodGet, p.systemPath(), nil)
	require.Equal(t, http.StatusOK, resp.status, "GET the system: %s", resp.body)
	require.NotContains(t, string(resp.body), "ResetType@Redfish.AllowableValues", "generic publishes no reset types")

	for _, step := range steps {
		// Run test
		resp := step.action(t)

		// Assert
		require.Equal(t, step.wantStatus, resp.status, "%s: answer %s", step.name, resp.body)
		require.Equal(t, step.wantIncus, instanceStatus(t, vm), "%s: Incus status", step.name)
		require.Equal(t, step.wantPowerState, p.powerState(t), "%s: PowerState", step.name)
	}
}

func TestPower_Task(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "testdata/profiles/e2e-power-task.yaml")

	resp := p.do(t, http.MethodGet, p.systemPath(), nil)
	require.Equal(t, http.StatusOK, resp.status, "GET the system: %s", resp.body)

	var system struct {
		Actions struct {
			Reset struct {
				AllowableValues []string `json:"ResetType@Redfish.AllowableValues"`
			} `json:"#ComputerSystem.Reset"`
		} `json:"Actions"`
	}

	resp.decode(t, &system)
	require.Equal(t, []string{"On", "ForceOff", "ForceRestart"}, system.Actions.Reset.AllowableValues, "published reset types")

	// Run test: power on with a task.
	resp = p.reset(t, "On")
	require.Equal(t, http.StatusAccepted, resp.status, "On answers with a task: %s", resp.body)

	location := resp.header.Get("Location")
	require.NotEmpty(t, location, "On names the task monitor")

	second := p.reset(t, "On")
	require.Equal(t, http.StatusConflict, second.status, "a second reset during the first is refused: %s", second.body)
	require.Equal(t, "Base.1.18.ResourceInUse", second.messageID(t), "message of the refused second reset")

	running := p.waitTask(t, location)

	// Assert
	require.NotEmpty(t, running, "the monitor answers 202 during the delay of the profile")
	require.NotEmpty(t, running[0].header.Get("Retry-After"), "a running task asks for a poll interval")
	require.Equal(t, "Running", instanceStatus(t, vm), "the task ends after the VM runs")

	// Run test: writes are refused right after the power on.
	resp = p.do(t, http.MethodPatch, p.systemPath(), map[string]any{})

	// Assert
	require.Equal(t, http.StatusBadRequest, resp.status, "a write after the power on is refused: %s", resp.body)
	require.Equal(t, "Base.1.18.ResourceInStandby", resp.messageID(t), "message of the refused write")
	require.NotEmpty(t, resp.header.Get("Retry-After"), "the refusal asks for a retry interval")

	eventually(t, 30*time.Second, func() bool {
		return p.do(t, http.MethodPatch, p.systemPath(), map[string]any{}).status == http.StatusNoContent
	}, "writes are accepted after the settle window")

	// Run test: reset types outside of the profile are refused.
	resp = p.reset(t, "GracefulRestart")

	// Assert
	require.Equal(t, http.StatusBadRequest, resp.status, "GracefulRestart is not published: %s", resp.body)
	require.Equal(t, "Base.1.18.ActionParameterValueNotInList", resp.messageID(t), "message of the refused reset type")

	// Run test: power off with a task.
	resp = p.reset(t, "ForceOff")
	require.Equal(t, http.StatusAccepted, resp.status, "ForceOff answers with a task: %s", resp.body)

	p.waitTask(t, resp.header.Get("Location"))

	// Assert
	require.Equal(t, "Stopped", instanceStatus(t, vm), "the task ends after the VM stopped")
	require.Equal(t, "Off", p.powerState(t), "PowerState after ForceOff")
}
