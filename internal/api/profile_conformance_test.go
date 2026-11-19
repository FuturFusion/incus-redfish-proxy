package api_test

import (
	"testing"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/api/mock"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// conformanceScenario checks one behavior against the switches of a profile.
type conformanceScenario struct {
	name string

	// applies reports whether the scenario is meaningful for the profile.
	applies func(p profile.Profile) bool

	run func(t *testing.T, p profile.Profile)
}

// conformanceScenarios derive their expectations from the profile, never from a literal per profile.
var conformanceScenarios = []conformanceScenario{
	{name: "identity", applies: always, run: conformanceIdentity},
}

func always(profile.Profile) bool {
	return true
}

func TestProfileConformance(t *testing.T) {
	for _, name := range profile.List() {
		p, err := profile.Load(name)
		require.NoError(t, err)

		t.Run(name, func(t *testing.T) {
			for _, scenario := range conformanceScenarios {
				if !scenario.applies(p) {
					continue
				}

				t.Run(scenario.name, func(t *testing.T) {
					scenario.run(t, p)
				})
			}
		})
	}
}

func conformanceIdentity(t *testing.T, p profile.Profile) {
	t.Helper()

	incusClient := &mock.IncusClientMock{
		GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
			return &incusapi.Instance{Status: "Stopped"}, "", nil
		},
	}

	client := setup(t, incusClient, api.WithProfile(p))

	systems, err := client.Service.Systems()
	require.NoError(t, err)
	require.Len(t, systems, 1)

	require.Equal(t, "linuxcontainers.org", systems[0].Manufacturer)
	require.Equal(t, "Incus ("+p.Name+")", systems[0].Model)
}
