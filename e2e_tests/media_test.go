//go:build e2e

package e2e_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	virtualMediaPath = "/redfish/v1/Managers/bmc1/VirtualMedia/CD"
	bootOnceKey      = "user.redfish.boot.once"
)

func bootMediaDevice(t *testing.T, vm string) map[string]string {
	t.Helper()

	instance, _, err := env.client.GetInstance(vm)
	require.NoError(t, err, "read the devices of the VM %s", vm)

	return instance.Devices["boot-media"]
}

// bootMediaVolume reports whether the full fetch volume of the VM exists. Volumes live in the default project.
func bootMediaVolume(t *testing.T, vm string) bool {
	t.Helper()

	_, _, err := env.client.GetStoragePoolVolume("default", "custom", vm+"-boot-media.iso")

	return err == nil
}

func (p *proxy) insert(t *testing.T, image string) response {
	t.Helper()

	return p.do(t, http.MethodPost, virtualMediaPath+"/Actions/VirtualMedia.InsertMedia", map[string]any{"Image": image})
}

func (p *proxy) eject(t *testing.T) response {
	t.Helper()

	return p.do(t, http.MethodPost, virtualMediaPath+"/Actions/VirtualMedia.EjectMedia", map[string]any{})
}

func (p *proxy) inserted(t *testing.T) bool {
	t.Helper()

	resp := p.do(t, http.MethodGet, virtualMediaPath, nil)
	require.Equal(t, http.StatusOK, resp.status, "GET the virtual media: %s", resp.body)

	var media struct {
		Inserted bool `json:"Inserted"`
	}

	resp.decode(t, &media)

	return media.Inserted
}

// bootOverride returns the boot override of the system and the values it offers.
func (p *proxy) bootOverride(t *testing.T) (string, []string) {
	t.Helper()

	resp := p.do(t, http.MethodGet, p.systemPath(), nil)
	require.Equal(t, http.StatusOK, resp.status, "GET the system: %s", resp.body)

	var system struct {
		Boot struct {
			Enabled         string   `json:"BootSourceOverrideEnabled"`
			AllowableValues []string `json:"BootSourceOverrideEnabled@Redfish.AllowableValues"`
		} `json:"Boot"`
	}

	resp.decode(t, &system)

	return system.Boot.Enabled, system.Boot.AllowableValues
}

func (p *proxy) setBootOverride(t *testing.T, enabled string) response {
	t.Helper()

	return p.do(t, http.MethodPatch, p.systemPath(), map[string]any{
		"Boot": map[string]string{"BootSourceOverrideEnabled": enabled, "BootSourceOverrideTarget": "Cd"},
	})
}

func TestMedia_FullFetch(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "generic")
	images := newImageServer(t, "install.iso", randomImage(t, 4<<20))

	// Run test
	resp := p.insert(t, images.url)

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "insert: %s", resp.body)
	require.Equal(t, map[string]string{
		"type":               "disk",
		"pool":               "default",
		"source":             vm + "-boot-media.iso",
		"user.redfish.image": images.url,
	}, bootMediaDevice(t, vm), "the device attaches the imported volume")
	require.True(t, bootMediaVolume(t, vm), "the image is imported as a volume")
	require.Equal(t, []imageRequest{{Method: http.MethodGet}}, images.recorded(), "a full fetch downloads the image once, without ranges")
	require.True(t, p.inserted(t), "the virtual media shows the image")

	// Run test: a second insert is refused.
	resp = p.insert(t, images.url)

	// Assert
	require.Equal(t, http.StatusConflict, resp.status, "second insert: %s", resp.body)

	// Run test
	resp = p.eject(t)

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "eject: %s", resp.body)
	require.Nil(t, bootMediaDevice(t, vm), "eject removes the device")
	require.False(t, bootMediaVolume(t, vm), "eject removes the volume")
	require.False(t, p.inserted(t), "the virtual media is empty")
}

func TestMedia_OEM(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "oem-media")
	images := newImageServer(t, "install.iso", randomImage(t, 4<<20))

	resp := p.do(t, http.MethodGet, virtualMediaPath, nil)
	require.Equal(t, http.StatusOK, resp.status, "GET the virtual media: %s", resp.body)

	var media struct {
		Actions struct {
			Insert *struct{} `json:"#VirtualMedia.InsertMedia"`
			OEM    map[string]map[string]struct {
				Target string `json:"target"`
			} `json:"Oem"`
		} `json:"Actions"`
	}

	resp.decode(t, &media)
	require.Nil(t, media.Actions.Insert, "the standard insert action is not offered")

	target := media.Actions.OEM["Incus"]["#IncusVirtualMedia.InsertVirtualMedia"].Target
	require.NotEmpty(t, target, "the OEM insert action is offered")

	// Run test
	resp = p.do(t, http.MethodPost, target, map[string]any{"Image": images.url})
	require.Equal(t, http.StatusAccepted, resp.status, "OEM insert: %s", resp.body)

	p.waitTask(t, resp.header.Get("Location"))

	// Assert
	require.Equal(t, vm+"-boot-media.iso", bootMediaDevice(t, vm)["source"], "the task ends after the device is attached")
	require.True(t, bootMediaVolume(t, vm), "the image is imported as a volume")
	require.True(t, p.inserted(t), "the virtual media shows the image")

	// Run test
	resp = p.eject(t)

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "eject: %s", resp.body)
	require.False(t, bootMediaVolume(t, vm), "eject removes the volume")
}

func TestMedia_BootOverrideOnce(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "testdata/profiles/e2e-once.yaml")
	images := newImageServer(t, "install.iso", randomImage(t, 4<<20))
	require.Equal(t, http.StatusNoContent, p.insert(t, images.url).status, "insert")

	_, allowed := p.bootOverride(t)
	require.Contains(t, allowed, "Once", "the profile offers Once")

	consumed := func() bool {
		_, once := instanceConfig(t, vm)[bootOnceKey]

		return !once && bootMediaDevice(t, vm)["boot.priority"] == ""
	}

	// Run test: Once on a stopped VM.
	resp := p.setBootOverride(t, "Once")

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "PATCH Once: %s", resp.body)
	require.Equal(t, "10", bootMediaDevice(t, vm)["boot.priority"], "the media boots first")
	require.Contains(t, instanceConfig(t, vm), bootOnceKey, "the override is marked as one time")

	enabled, _ := p.bootOverride(t)
	require.Equal(t, "Once", enabled, "the system shows Once")

	// Run test: a power on through the proxy drops it.
	require.Equal(t, http.StatusNoContent, p.reset(t, "On").status, "power on")

	// Assert
	eventually(t, time.Minute, consumed, "the power on drops the one time override")

	enabled, _ = p.bootOverride(t)
	require.Equal(t, "Disabled", enabled, "the system shows no override")
	require.Equal(t, "Running", instanceStatus(t, vm), "the VM runs")

	// Run test: a start outside of the proxy drops it as well.
	require.Equal(t, http.StatusNoContent, p.reset(t, "ForceOff").status, "power off")
	require.Equal(t, http.StatusNoContent, p.setBootOverride(t, "Once").status, "PATCH Once")
	changeState(t, vm, "start")

	// Assert
	eventually(t, time.Minute, consumed, "the start event drops the one time override")

	// Run test: Continuous survives a reset.
	require.Equal(t, http.StatusNoContent, p.reset(t, "ForceOff").status, "power off")
	require.Equal(t, http.StatusNoContent, p.setBootOverride(t, "Continuous").status, "PATCH Continuous")
	require.Equal(t, http.StatusNoContent, p.reset(t, "On").status, "power on")
	require.Equal(t, http.StatusNoContent, p.reset(t, "ForceRestart").status, "restart")

	// Assert
	enabled, _ = p.bootOverride(t)
	require.Equal(t, "Continuous", enabled, "Continuous stays")
	require.Equal(t, "10", bootMediaDevice(t, vm)["boot.priority"], "the media still boots first")
}
