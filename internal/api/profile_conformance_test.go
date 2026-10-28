package api_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"strings"
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
	{name: "bios-patch-response", applies: always, run: conformanceBIOSPatchResponse},
	{name: "task-service", applies: always, run: conformanceTaskService},
	{name: "bios-immediate", applies: appliesAtOnce, run: conformanceBIOSImmediate},
	{name: "bios-stage-and-apply", applies: hasStagedAttribute, run: conformanceBIOSStageAndApply},
	{name: "bios-requires", applies: hasRequirement, run: conformanceBIOSRequires},
}

func always(profile.Profile) bool {
	return true
}

func hasRegistry(p profile.Profile) bool {
	return p.BIOS.Registry.ID != ""
}

// stages reports whether a PATCH without an apply time stages the attributes.
func stages(p profile.Profile) bool {
	return p.BIOS.ApplyTime.Default == profile.ApplyTimeOnReset
}

func appliesAtOnce(p profile.Profile) bool {
	return !stages(p)
}

func hasStagedAttribute(p profile.Profile) bool {
	_, ok := stagedAttribute(p)
	return stages(p) && ok
}

func hasRequirement(p profile.Profile) bool {
	_, ok := dependentAttribute(p)
	return ok
}

// dependentAttribute returns a writable attribute with a requirement and the attribute it requires.
func dependentAttribute(p profile.Profile) (profile.Attribute, bool) {
	for _, attribute := range p.BIOS.Registry.Attributes {
		if !attribute.ReadOnly && attribute.Requires != nil {
			return attribute, true
		}
	}

	return profile.Attribute{}, false
}

// stagedAttribute returns a writable attribute kept in the proxy state that a client can stage on a running instance.
func stagedAttribute(p profile.Profile) (profile.Attribute, bool) {
	for _, attribute := range p.BIOS.Registry.Attributes {
		if !attribute.ReadOnly && attribute.MapsTo == "" && attribute.Requires == nil {
			return attribute, true
		}
	}

	return profile.Attribute{}, false
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
		UpdateInstanceStateFunc: func(name string, state incusapi.InstanceStatePut, ETag string) (incusclient.Operation, error) {
			mu.Lock()
			defer mu.Unlock()

			instance.Status = "Running"
			if state.Action == "stop" {
				instance.Status = "Stopped"
			}

			return &mock.IncusOperationMock{WaitFunc: func() error { return nil }}, nil
		},
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

	published := registryAttributes(t, client, bios.AttributeRegistry)

	// Attributes with a requirement are covered by bios-requires.
	for _, attribute := range p.BIOS.Registry.Attributes {
		require.True(t, published[attribute.Name], attribute.Name)

		if attribute.Requires != nil {
			continue
		}

		require.Equal(t, fmt.Sprint(attribute.DefaultValue()), fmt.Sprint(bios.Attributes[attribute.Name]), attribute.Name)
	}

	for _, attribute := range p.BIOS.Registry.Attributes {
		if attribute.ReadOnly || attribute.Requires != nil {
			continue
		}

		value := otherValue(attribute)

		resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": map[string]any{attribute.Name: value}})
		require.NoError(t, err, attribute.Name)
		resp.Body.Close()

		written := getBIOS(t, client).Attributes
		if stages(p) {
			written = getBIOSSettings(t, client)
		}

		require.Equal(t, fmt.Sprint(value), fmt.Sprint(written[attribute.Name]), attribute.Name)
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

func conformanceBIOSPatchResponse(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	status, location := patchBIOS(t, client)

	switch p.BIOS.PatchResponse {
	case profile.PatchResponseTaskMonitor:
		require.Equal(t, http.StatusAccepted, status)
		require.True(t, strings.HasPrefix(location, "/redfish/v1/TaskMonitors/"), location)

	case profile.PatchResponseTask:
		require.Equal(t, http.StatusAccepted, status)
		require.True(t, strings.HasPrefix(location, "/redfish/v1/TaskService/Tasks/"), location)

	case profile.PatchResponseOK:
		require.Equal(t, http.StatusOK, status)
		require.Empty(t, location)

	case profile.PatchResponseNoContent:
		require.Equal(t, http.StatusNoContent, status)
		require.Empty(t, location)
	}

	if location == "" {
		return
	}

	// A change applied at once is completed when it is first polled, a task resource always answers 200.
	expected := http.StatusOK
	if stages(p) && p.BIOS.PatchResponse == profile.PatchResponseTaskMonitor {
		expected = http.StatusAccepted
	}

	resp, err := client.Get(location)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, expected, resp.StatusCode)
}

func conformanceTaskService(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	resp, err := client.Get("/redfish/v1/TaskService/Tasks")
	if !p.BIOS.TaskResource {
		redfishErr := &schemas.Error{}
		require.ErrorAs(t, err, &redfishErr)
		require.Equal(t, http.StatusNotFound, redfishErr.HTTPReturnedStatusCode)

		return
	}

	require.NoError(t, err)
	resp.Body.Close()

	tasks, err := client.Service.Tasks()
	require.NoError(t, err)
	require.NotNil(t, tasks)

	patchBIOS(t, client)

	listed, err := tasks.Tasks()
	require.NoError(t, err)
	require.NotEmpty(t, listed)

	expected := schemas.CompletedTaskState
	if stages(p) {
		expected = schemas.RunningTaskState
	}

	require.Equal(t, expected, listed[len(listed)-1].TaskState)
}

func conformanceBIOSImmediate(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	patchBIOS(t, client)

	require.Equal(t, "2", getBIOS(t, client).Attributes["incus.config.limits.cpu"])
}

func conformanceBIOSStageAndApply(t *testing.T, p profile.Profile) {
	t.Helper()

	attribute, _ := stagedAttribute(p)
	value := otherValue(attribute)

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Running"}), api.WithProfile(p))

	resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": map[string]any{attribute.Name: value}})
	require.NoError(t, err)
	resp.Body.Close()

	location := resp.Header.Get("Location")

	require.Equal(t, fmt.Sprint(attribute.DefaultValue()), fmt.Sprint(getBIOS(t, client).Attributes[attribute.Name]), "Bios holds the current value")
	require.Equal(t, fmt.Sprint(value), fmt.Sprint(getBIOSSettings(t, client)[attribute.Name]), "Bios/Settings holds the staged value")
	require.True(t, taskRunning(t, client, location), "the task runs until the reset")

	resp, err = client.Post("/redfish/v1/Systems/test-instance/Actions/ComputerSystem.Reset", map[string]any{"ResetType": "ForceRestart"})
	require.NoError(t, err)
	resp.Body.Close()

	require.Equal(t, fmt.Sprint(value), fmt.Sprint(getBIOS(t, client).Attributes[attribute.Name]), "the reset applies the staged value")
	require.Empty(t, getBIOSSettings(t, client), "nothing is staged after the reset")
	require.False(t, taskRunning(t, client, location), "the reset completes the task")
}

func conformanceBIOSRequires(t *testing.T, p profile.Profile) {
	t.Helper()

	dependent, _ := dependentAttribute(p)
	requirement := map[string]any{dependent.Requires.Attribute: dependent.Requires.Value}
	value := otherValue(dependent)

	// Staged attributes apply on a restart, others need a stopped instance.
	status := "Stopped"
	if stages(p) {
		status = "Running"
	}

	client := setup(t, fakeIncus(incusapi.Instance{Status: status}), api.WithProfile(p))

	bios := getBIOS(t, client)
	require.NotContains(t, bios.Attributes, dependent.Name, "Bios hides the attribute until its requirement is in effect")
	require.True(t, registryAttributes(t, client, bios.AttributeRegistry)[dependent.Name], "the registry lists the attribute")

	// A requirement written in the same PATCH is not in effect yet, so nothing of the PATCH is taken.
	resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": map[string]any{dependent.Requires.Attribute: dependent.Requires.Value, dependent.Name: value}})
	if resp != nil {
		resp.Body.Close()
	}

	redfishErr := &schemas.Error{}
	require.ErrorAs(t, err, &redfishErr)
	require.Equal(t, p.BIOS.Errors.DeferredRejected.Status, redfishErr.HTTPReturnedStatusCode)
	require.Len(t, redfishErr.ExtendedInfos, 1)
	require.Equal(t, p.BIOS.Errors.DeferredRejected.MessageID, redfishErr.ExtendedInfos[0].MessageID)
	require.NotContains(t, getBIOSSettings(t, client), dependent.Requires.Attribute, "a refused PATCH stages nothing")

	writeAndApply(t, client, p, requirement)
	require.Equal(t, fmt.Sprint(dependent.DefaultValue()), fmt.Sprint(getBIOS(t, client).Attributes[dependent.Name]), "Bios publishes the attribute once its requirement is in effect")

	writeAndApply(t, client, p, map[string]any{dependent.Name: value})
	require.Equal(t, fmt.Sprint(value), fmt.Sprint(getBIOS(t, client).Attributes[dependent.Name]))
}

// writeAndApply writes the attributes and resets the instance, if the profile stages them.
func writeAndApply(t *testing.T, client *gofish.APIClient, p profile.Profile, attributes map[string]any) {
	t.Helper()

	resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": attributes})
	require.NoError(t, err)
	resp.Body.Close()

	if !stages(p) {
		return
	}

	resp, err = client.Post("/redfish/v1/Systems/test-instance/Actions/ComputerSystem.Reset", map[string]any{"ResetType": "ForceRestart"})
	require.NoError(t, err)
	resp.Body.Close()
}

// registryAttributes discovers the attribute registry the way Operations Center does and returns the names it lists.
func registryAttributes(t *testing.T, client *gofish.APIClient, registryID string) map[string]bool {
	t.Helper()

	files, err := client.Service.Registries()
	require.NoError(t, err)

	var registry *schemas.AttributeRegistry

	for _, file := range files {
		if file.Registry != registryID || len(file.Location) == 0 {
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

	return published
}

// taskRunning follows the location of a PATCH answer, which has no location for the 200 and 204 answers.
func taskRunning(t *testing.T, client *gofish.APIClient, location string) bool {
	t.Helper()

	if location == "" {
		return false
	}

	resp, err := client.Get(location)
	require.NoError(t, err)

	defer resp.Body.Close()

	// A completed task monitor answers with the Bios resource, which has no task state.
	body := struct{ TaskState schemas.TaskState }{}
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)

	return resp.StatusCode == http.StatusAccepted || body.TaskState == schemas.RunningTaskState
}

// getBIOSSettings returns the attributes of the settings object.
func getBIOSSettings(t *testing.T, client *gofish.APIClient) map[string]any {
	t.Helper()

	resp, err := client.Get("/redfish/v1/Systems/test-instance/Bios/Settings")
	require.NoError(t, err)

	defer resp.Body.Close()

	settings := struct{ Attributes map[string]any }{}
	err = json.NewDecoder(resp.Body).Decode(&settings)
	require.NoError(t, err)

	return settings.Attributes
}

// patchBIOS writes one attribute every profile accepts and returns the status and location of the answer.
func patchBIOS(t *testing.T, client *gofish.APIClient) (int, string) {
	t.Helper()

	resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": map[string]any{"incus.config.limits.cpu": "2"}})
	require.NoError(t, err)

	defer resp.Body.Close()

	return resp.StatusCode, resp.Header.Get("Location")
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
