package api_test

import (
	"fmt"
	"maps"
	"path/filepath"
	"sync"
	"testing"

	incusclient "github.com/lxc/incus/v7/client"
	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
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
	{name: "bios-registry", applies: hasRegistry, run: conformanceBIOSRegistry},
	{name: "bios-unknown-attribute", applies: always, run: conformanceBIOSUnknownAttribute},
}

func always(profile.Profile) bool {
	return true
}

func hasRegistry(p profile.Profile) bool {
	return p.BIOS.Registry.ID != ""
}

// conformanceProfiles returns the embedded profiles and the test profiles.
func conformanceProfiles(t *testing.T) []profile.Profile {
	t.Helper()

	files, err := filepath.Glob(filepath.Join("testdata", "profiles", "*.yaml"))
	require.NoError(t, err)

	names := append(profile.List(), files...)

	profiles := make([]profile.Profile, 0, len(names))
	for _, name := range names {
		p, err := profile.Load(name)
		require.NoError(t, err)

		profiles = append(profiles, p)
	}

	return profiles
}

func TestProfileConformance(t *testing.T) {
	for _, p := range conformanceProfiles(t) {
		t.Run(p.Name, func(t *testing.T) {
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

// fakeIncus returns an Incus client keeping the instance, so writes can be read back.
func fakeIncus(instance incusapi.Instance) *mock.IncusClientMock {
	var mu sync.Mutex

	clone := func(put incusapi.InstancePut) incusapi.InstancePut {
		put.Config = maps.Clone(put.Config)
		put.Devices = maps.Clone(put.Devices)

		return put
	}

	return &mock.IncusClientMock{
		GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
			mu.Lock()
			defer mu.Unlock()

			current := instance
			current.InstancePut = clone(instance.InstancePut)

			return &current, "", nil
		},
		UpdateInstanceFunc: func(name string, put incusapi.InstancePut, ETag string) (incusclient.Operation, error) {
			mu.Lock()
			defer mu.Unlock()

			instance.InstancePut = clone(put)

			return &mock.IncusOperationMock{WaitFunc: func() error { return nil }}, nil
		},
	}
}

func conformanceIdentity(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	systems, err := client.Service.Systems()
	require.NoError(t, err)
	require.Len(t, systems, 1)

	require.Equal(t, "linuxcontainers.org", systems[0].Manufacturer)
	require.Equal(t, "Incus ("+p.Name+")", systems[0].Model)
}

func conformanceBIOSRegistry(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	bios := getBIOS(t, client)
	require.Equal(t, p.BIOS.Registry.ID, bios.AttributeRegistry)

	// Discover the registry the way Operations Center does.
	files, err := client.Service.Registries()
	require.NoError(t, err)

	var registry *schemas.AttributeRegistry

	for _, file := range files {
		if file.Registry != bios.AttributeRegistry || len(file.Location) == 0 {
			continue
		}

		registry, err = schemas.GetAttributeRegistry(client, file.Location[0].URI)
		require.NoError(t, err)
	}

	require.NotNil(t, registry)

	published := map[string]bool{}
	for _, attribute := range registry.RegistryEntries.Attributes {
		published[attribute.AttributeName] = true
	}

	for _, attribute := range p.BIOS.Registry.Attributes {
		require.True(t, published[attribute.Name], attribute.Name)
		require.Equal(t, fmt.Sprint(attribute.DefaultValue()), fmt.Sprint(bios.Attributes[attribute.Name]), attribute.Name)
	}

	for _, attribute := range p.BIOS.Registry.Attributes {
		if attribute.ReadOnly {
			continue
		}

		value := otherValue(attribute)

		resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": map[string]any{attribute.Name: value}})
		require.NoError(t, err, attribute.Name)
		resp.Body.Close()

		require.Equal(t, fmt.Sprint(value), fmt.Sprint(getBIOS(t, client).Attributes[attribute.Name]), attribute.Name)
	}
}

func conformanceBIOSUnknownAttribute(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": map[string]any{"NoSuchAttribute": "value"}})
	if resp != nil {
		resp.Body.Close()
	}

	redfishErr := &schemas.Error{}
	require.ErrorAs(t, err, &redfishErr)
	require.Equal(t, p.BIOS.Errors.UnknownAttribute.Status, redfishErr.HTTPReturnedStatusCode)
	require.Len(t, redfishErr.ExtendedInfos, 1)
	require.Equal(t, p.BIOS.Errors.UnknownAttribute.MessageID, redfishErr.ExtendedInfos[0].MessageID)
}

func getBIOS(t *testing.T, client *gofish.APIClient) *schemas.Bios {
	t.Helper()

	systems, err := client.Service.Systems()
	require.NoError(t, err)
	require.Len(t, systems, 1)

	bios, err := systems[0].Bios()
	require.NoError(t, err)

	return bios
}

// otherValue returns a valid value of the attribute that differs from its default.
func otherValue(attribute profile.Attribute) any {
	switch attribute.Type {
	case profile.AttributeTypeEnumeration:
		for _, value := range attribute.Values {
			if value != attribute.DefaultValue() {
				return value
			}
		}

	case profile.AttributeTypeInteger:
		value, ok := attribute.DefaultValue().(int64)
		if ok && attribute.Max != nil && value >= *attribute.Max {
			return value - 1
		}

		return value + 1

	case profile.AttributeTypeBoolean:
		value, ok := attribute.DefaultValue().(bool)
		return ok && !value
	}

	return "conformance"
}
