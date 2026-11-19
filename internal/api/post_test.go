package api_test

import (
	"net/http"
	"testing"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/internal/util/testing/boom"
)

func TestRedfishServer_POSTDuration(t *testing.T) {
	t.Parallel()

	const section = "[boot-opts]\nmenu = \"on\"\nsplash-time = \"60000\""

	tests := []struct {
		name         string
		postDuration time.Duration
		scale        float64
		instance     incusapi.Instance
		resetType    string

		wantConf    string
		wantOwnConf bool
		wantStatus  string
	}{
		{
			name:         "power on writes the POST duration",
			postDuration: time.Minute,
			scale:        1,
			instance:     incusapi.Instance{Status: "Stopped"},
			resetType:    "On",

			wantConf:    section,
			wantOwnConf: true,
			wantStatus:  "Running",
		},
		{
			name:         "the POST duration is scaled",
			postDuration: time.Minute,
			scale:        0.5,
			instance:     incusapi.Instance{Status: "Stopped"},
			resetType:    "ForceOn",

			wantConf:    "[boot-opts]\nmenu = \"on\"\nsplash-time = \"30000\"",
			wantOwnConf: true,
			wantStatus:  "Running",
		},
		{
			name:         "power on keeps the own config of the instance",
			postDuration: time.Minute,
			scale:        1,
			instance:     incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Config: map[string]string{"raw.qemu.conf": "[machine]\ngraphics = \"off\""}}},
			resetType:    "On",

			wantConf:    "[machine]\ngraphics = \"off\"\n\n" + section,
			wantOwnConf: true,
			wantStatus:  "Running",
		},
		{
			name:         "power on keeps the config of the profiles of the instance",
			postDuration: time.Minute,
			scale:        1,
			instance:     incusapi.Instance{Status: "Stopped", ExpandedConfig: map[string]string{"raw.qemu.conf": "[machine]\ngraphics = \"off\""}},
			resetType:    "On",

			wantConf:    "[machine]\ngraphics = \"off\"\n\n" + section,
			wantOwnConf: true,
			wantStatus:  "Running",
		},
		{
			name:         "power on without POST duration removes it",
			postDuration: 0,
			scale:        1,
			instance:     incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Config: map[string]string{"raw.qemu.conf": section}}},
			resetType:    "On",

			wantConf:    "",
			wantOwnConf: false,
			wantStatus:  "Running",
		},
		{
			name:         "power on without POST duration keeps other sections",
			postDuration: 0,
			scale:        1,
			instance:     incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Config: map[string]string{"raw.qemu.conf": "[machine]\ngraphics = \"off\"\n\n" + section}}},
			resetType:    "On",

			wantConf:    "[machine]\ngraphics = \"off\"",
			wantOwnConf: true,
			wantStatus:  "Running",
		},
		{
			name:         "power on without POST duration changes nothing",
			postDuration: 0,
			scale:        1,
			instance:     incusapi.Instance{Status: "Stopped"},
			resetType:    "On",

			wantConf:    "",
			wantOwnConf: false,
			wantStatus:  "Running",
		},
		{
			name:         "restart of a running instance applies the POST duration later",
			postDuration: time.Minute,
			scale:        1,
			instance:     incusapi.Instance{Status: "Running"},
			resetType:    "ForceRestart",

			wantConf:    "",
			wantOwnConf: false,
			wantStatus:  "Running",
		},
		{
			name:         "power off leaves the config",
			postDuration: time.Minute,
			scale:        1,
			instance:     incusapi.Instance{Status: "Running"},
			resetType:    "ForceOff",

			wantConf:    "",
			wantOwnConf: false,
			wantStatus:  "Stopped",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			p := profile.Default()
			p.Power.PostDuration = tc.postDuration
			p.Timing.Scale = tc.scale

			incusClient := fakeIncus(tc.instance)
			client := setup(t, incusClient, api.WithProfile(p))

			// Run test
			resp, err := client.Post("/redfish/v1/Systems/test-instance/Actions/ComputerSystem.Reset", map[string]any{"ResetType": tc.resetType})
			require.NoError(t, err, "reset the system")
			resp.Body.Close()

			// Assert
			require.Equal(t, http.StatusNoContent, resp.StatusCode, "the reset succeeds")

			instance, _, err := incusClient.GetInstance("test-instance")
			require.NoError(t, err, "get the instance")

			conf, ok := instance.Config["raw.qemu.conf"]
			require.Equal(t, tc.wantOwnConf, ok, "whether the instance has an own raw.qemu.conf")
			require.Equal(t, tc.wantConf, conf, "the raw.qemu.conf of the instance")
			require.Equal(t, tc.wantStatus, instance.Status, "the power state after the reset")
		})
	}
}

func TestRedfishServer_POSTDurationError(t *testing.T) {
	t.Parallel()

	// Setup
	p := profile.Default()
	p.Power.PostDuration = time.Minute

	incusClient := fakeIncus(incusapi.Instance{Status: "Stopped"})
	incusClient.UpdateInstanceFunc = func(name string, put incusapi.InstancePut, ETag string) (incusclient.Operation, error) {
		return nil, boom.Error
	}

	client := setup(t, incusClient, api.WithProfile(p))

	// Run test
	resp, err := client.Post("/redfish/v1/Systems/test-instance/Actions/ComputerSystem.Reset", map[string]any{"ResetType": "On"})
	closeBody(resp)

	// Assert
	boom.ErrorContains(t, err, "the power on fails, if the POST duration can not be written")
	require.Empty(t, incusClient.UpdateInstanceStateCalls(), "the instance does not start without the POST duration")
}
