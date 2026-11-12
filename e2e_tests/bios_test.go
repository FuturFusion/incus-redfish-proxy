//go:build e2e

package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	biosPendingKey = "user.redfish.bios-pending"
	biosAppliedKey = "user.redfish.bios-applied"
	biosModeKey    = "user.redfish.bios.Mode"
)

func (p *proxy) biosPath() string {
	return p.systemPath() + "/Bios"
}

// biosAttributes returns the attributes of the Bios resource or of its settings object.
func (p *proxy) biosAttributes(t *testing.T, path string) map[string]any {
	t.Helper()

	resp := p.do(t, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, resp.status, "GET %s: %s", path, resp.body)

	var bios struct {
		Attributes map[string]any `json:"Attributes"`
	}

	resp.decode(t, &bios)

	return bios.Attributes
}

// stage writes the attributes into the settings object, to be applied on the next reset.
func (p *proxy) stage(t *testing.T, attributes map[string]any) response {
	t.Helper()

	return p.do(t, http.MethodPatch, p.biosPath()+"/Settings", map[string]any{
		"Attributes":                 attributes,
		"@Redfish.SettingsApplyTime": map[string]string{"ApplyTime": "OnReset"},
	})
}

// pendingAttributes decodes the attributes that the proxy keeps staged in the Incus config.
func pendingAttributes(t *testing.T, vm string) map[string]any {
	t.Helper()

	value, ok := instanceConfig(t, vm)[biosPendingKey]
	if !ok {
		return nil
	}

	var staged struct {
		Attributes map[string]any `json:"attributes"`
	}

	require.NoError(t, json.Unmarshal([]byte(value), &staged), "decode %s", value)

	return staged.Attributes
}

func TestBIOS_Generic(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "generic")

	// Run test: an Incus attribute of a stopped VM is written at once.
	resp := p.do(t, http.MethodPatch, p.biosPath(), map[string]any{"Attributes": map[string]any{"incus.config.limits.memory": "768MiB"}})
	require.Equal(t, http.StatusAccepted, resp.status, "PATCH of a stopped VM: %s", resp.body)

	running := p.waitTask(t, resp.header.Get("Location"))

	// Assert
	require.Empty(t, running, "the task of an immediate write has ended already")
	require.Equal(t, "768MiB", instanceConfig(t, vm)["limits.memory"], "the attribute is the Incus config")
	require.Equal(t, "768MiB", p.biosAttributes(t, p.biosPath())["incus.config.limits.memory"], "Bios shows the Incus config")

	// Run test: the state of the proxy is neither visible nor writable as an attribute.
	resp = p.do(t, http.MethodPatch, p.biosPath(), map[string]any{"Attributes": map[string]any{"incus.config.user.redfish.bios-pending": "{}"}})

	// Assert
	require.Equal(t, http.StatusBadRequest, resp.status, "the proxy state is reserved: %s", resp.body)

	for name := range p.biosAttributes(t, p.biosPath()) {
		require.False(t, strings.HasPrefix(name, "incus.config.user.redfish."), "the proxy state %s is hidden", name)
	}

	// Run test: a running VM refuses Incus attributes.
	require.Equal(t, http.StatusNoContent, p.reset(t, "On").status, "power on")

	resp = p.do(t, http.MethodPatch, p.biosPath(), map[string]any{"Attributes": map[string]any{"incus.config.limits.memory": "1GiB"}})

	// Assert
	require.Equal(t, http.StatusPreconditionFailed, resp.status, "PATCH of a running VM: %s", resp.body)
	require.Equal(t, "768MiB", instanceConfig(t, vm)["limits.memory"], "the refused write changes nothing")
}

func TestBIOS_DeferredPending(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "deferred-bios")
	require.Equal(t, http.StatusNoContent, p.reset(t, "On").status, "power on")

	// Run test: the Bios resource itself is read only, the settings object takes the writes.
	resp := p.do(t, http.MethodPatch, p.biosPath(), map[string]any{"Attributes": map[string]any{"Mode": "Performance"}})

	// Assert
	require.Equal(t, http.StatusMethodNotAllowed, resp.status, "PATCH of Bios: %s", resp.body)

	// Run test: stage two attributes on the running VM.
	resp = p.stage(t, map[string]any{"Mode": "Performance", "Threshold": 75})
	require.Equal(t, http.StatusAccepted, resp.status, "PATCH of Bios/Settings: %s", resp.body)

	stagedMonitor := resp.header.Get("Location")

	// Assert
	require.Equal(t, http.StatusAccepted, p.do(t, http.MethodGet, stagedMonitor, nil).status, "the task runs until the next reset")
	require.Equal(t, map[string]any{"Mode": "Performance", "Threshold": float64(75)}, pendingAttributes(t, vm), "Incus holds the staged attributes")
	require.NotContains(t, instanceConfig(t, vm), biosModeKey, "the staged value is not in effect")

	bios := p.biosAttributes(t, p.biosPath())
	require.Equal(t, "Standard", bios["Mode"], "Bios shows the value in effect")
	require.NotContains(t, bios, "ModeOption", "the deferred attribute is hidden while its requirement is not in effect")
	require.Equal(t, "Performance", p.biosAttributes(t, p.biosPath()+"/Settings")["Mode"], "Bios/Settings shows the staged value")

	// Run test: the deferred attribute is refused, the staged requirement does not count.
	resp = p.stage(t, map[string]any{"ModeOption": "Enabled"})

	// Assert
	require.Equal(t, http.StatusBadRequest, resp.status, "the deferred attribute is refused: %s", resp.body)
	require.Equal(t, "Base.1.18.PropertyUnknown", resp.messageID(t), "message of the refused deferred attribute")

	// Run test: a new proxy reads the staged attributes from Incus.
	p.restart(t)

	// Assert
	require.Equal(t, "Performance", p.biosAttributes(t, p.biosPath()+"/Settings")["Mode"], "the staged value survives the proxy restart")
	require.Equal(t, http.StatusNotFound, p.do(t, http.MethodGet, stagedMonitor, nil).status, "tasks live in the memory of the proxy, OC treats 404 as gone")

	// Run test: a reset applies the staged attributes.
	require.Equal(t, http.StatusNoContent, p.reset(t, "ForceRestart").status, "ForceRestart")

	// Assert
	config := instanceConfig(t, vm)
	require.Equal(t, "Performance", config[biosModeKey], "Mode is in effect")
	require.Equal(t, "75", config["user.redfish.bios.Threshold"], "Threshold is in effect")
	require.NotContains(t, config, biosPendingKey, "nothing is staged any more")
	require.Contains(t, config, biosAppliedKey, "the time of the apply is recorded")
	require.Equal(t, "Running", instanceStatus(t, vm), "the VM runs after the reset")

	bios = p.biosAttributes(t, p.biosPath())
	require.Equal(t, "Performance", bios["Mode"], "Bios shows the applied value")
	require.Equal(t, "Disabled", bios["ModeOption"], "the deferred attribute shows once its requirement is in effect")

	// Run test: the deferred pass stages the attribute, the next reset applies it and ends the task.
	resp = p.stage(t, map[string]any{"ModeOption": "Enabled"})
	require.Equal(t, http.StatusAccepted, resp.status, "the deferred attribute is accepted now: %s", resp.body)

	deferredMonitor := resp.header.Get("Location")

	require.Equal(t, http.StatusNoContent, p.reset(t, "ForceRestart").status, "ForceRestart")
	p.waitTask(t, deferredMonitor)

	// Assert
	require.Equal(t, "Enabled", p.biosAttributes(t, p.biosPath())["ModeOption"], "the deferred attribute is in effect")
}

func TestBIOS_GuestRestart(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "deferred-bios")
	require.Equal(t, http.StatusNoContent, p.reset(t, "On").status, "power on")

	resp := p.stage(t, map[string]any{"Mode": "Performance", "incus.config.limits.memory": "768MiB"})
	require.Equal(t, http.StatusAccepted, resp.status, "PATCH of Bios/Settings: %s", resp.body)

	monitor := resp.header.Get("Location")

	// Run test: a restart that the proxy did not issue applies only what needs no stopped VM.
	changeState(t, vm, "restart")

	// Assert
	eventually(t, time.Minute, func() bool {
		return instanceConfig(t, vm)[biosModeKey] == "Performance"
	}, "the restart applies the synthetic attribute")
	require.Equal(t, map[string]any{"incus.config.limits.memory": "768MiB"}, pendingAttributes(t, vm), "the Incus attribute stays staged")
	require.Equal(t, "512MiB", instanceConfig(t, vm)["limits.memory"], "the Incus attribute is not in effect")
	require.Equal(t, http.StatusAccepted, p.do(t, http.MethodGet, monitor, nil).status, "the task runs while attributes are staged")

	// Run test: a reset through the proxy stops the VM, applies the rest and starts it again.
	require.Equal(t, http.StatusNoContent, p.reset(t, "ForceRestart").status, "ForceRestart")
	p.waitTask(t, monitor)

	// Assert
	require.Equal(t, "768MiB", instanceConfig(t, vm)["limits.memory"], "the Incus attribute is in effect")
	require.Nil(t, pendingAttributes(t, vm), "nothing is staged any more")
	require.Equal(t, "Running", instanceStatus(t, vm), "the VM runs after the reset")
}
