package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/uefi"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/api/mock"
	"github.com/FuturFusion/incus-redfish-proxy/internal/media"
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
// A behavior with variants has one scenario per variant, so every scenario runs a straight sequence.
var conformanceScenarios = []conformanceScenario{
	{name: "identity", applies: always, run: conformanceIdentity},
	{name: "bios-registry", applies: hasRegistry, run: conformanceBIOSRegistry},
	{name: "bios-registry-write-immediate", applies: allOf(hasRegistry, appliesAtOnce), run: conformanceBIOSRegistryWrite(getBIOSAttributes)},
	{name: "bios-registry-write-staged", applies: allOf(hasRegistry, stages), run: conformanceBIOSRegistryWrite(getBIOSSettings)},
	{name: "bios-unknown-attribute", applies: always, run: conformanceBIOSUnknownAttribute},
	{name: "bios-patch-response-task-monitor-immediate", applies: allOf(patchResponse(profile.PatchResponseTaskMonitor), appliesAtOnce), run: conformanceBIOSPatchTask("^/redfish/v1/TaskMonitors/", http.StatusOK)},
	{name: "bios-patch-response-task-monitor-staged", applies: allOf(patchResponse(profile.PatchResponseTaskMonitor), stages), run: conformanceBIOSPatchTask("^/redfish/v1/TaskMonitors/", http.StatusAccepted)},
	{name: "bios-patch-response-task", applies: patchResponse(profile.PatchResponseTask), run: conformanceBIOSPatchTask("^/redfish/v1/TaskService/Tasks/", http.StatusOK)},
	{name: "bios-patch-response-200", applies: patchResponse(profile.PatchResponseOK), run: conformanceBIOSPatchNoTask(http.StatusOK)},
	{name: "bios-patch-response-204", applies: patchResponse(profile.PatchResponseNoContent), run: conformanceBIOSPatchNoTask(http.StatusNoContent)},
	{name: "task-service-absent", applies: not(hasTaskResource), run: conformanceTaskServiceAbsent},
	{name: "task-service-immediate", applies: allOf(hasTaskResource, appliesAtOnce), run: conformanceTaskService(schemas.CompletedTaskState)},
	{name: "task-service-staged", applies: allOf(hasTaskResource, stages), run: conformanceTaskService(schemas.RunningTaskState)},
	{name: "bios-immediate", applies: appliesAtOnce, run: conformanceBIOSImmediate},
	{name: "bios-stage-and-apply", applies: hasStagedAttribute, run: conformanceBIOSStageAndApply},
	{name: "bios-requires-immediate", applies: allOf(hasRequirement, appliesAtOnce), run: conformanceBIOSRequires("Stopped", writeBIOS)},
	{name: "bios-requires-staged", applies: allOf(hasRequirement, stages), run: conformanceBIOSRequires("Running", writeBIOSAndReset)},
	{name: "secure-boot-write", applies: databaseWritable, run: conformanceSecureBootWrite},
	{name: "secure-boot-write-refused", applies: not(databaseWritable), run: conformanceSecureBootWriteRefused},
	{name: "virtual-media-actions", applies: always, run: conformanceVirtualMediaActions},
	{name: "virtual-media-insert-standard", applies: mediaInsert(profile.MediaInsertStandard), run: conformanceVirtualMediaInsert(http.MethodPost, standardInsertTarget, actionInsertBody)},
	{name: "virtual-media-insert-oem", applies: mediaInsert(profile.MediaInsertOEM), run: conformanceVirtualMediaInsert(http.MethodPost, oemInsertTarget, actionInsertBody)},
	{name: "virtual-media-insert-patch", applies: mediaInsert(profile.MediaInsertPatch), run: conformanceVirtualMediaInsert(http.MethodPatch, patchInsertTarget, patchInsertBody)},
	{name: "virtual-media-hidden-paths", applies: not(mediaInsert(profile.MediaInsertStandard)), run: conformanceVirtualMediaHiddenPaths},
	{name: "secure-boot-reset-keys", applies: resetKeysSupported, run: conformanceSecureBootResetKeys},
	{name: "secure-boot-reset-keys-absent", applies: not(resetKeysSupported), run: conformanceSecureBootResetKeysAbsent},
	{name: "power-reset-types", applies: always, run: conformancePowerResetTypes},
	{name: "power-reset-type-refused", applies: hasUnlistedResetType, run: conformancePowerResetTypeRefused},
	{name: "power-settle", applies: func(p profile.Profile) bool { return p.Power.NotSettledWindow > 0 }, run: conformancePowerSettle},
	{name: "boot-override-continuous", applies: not(bootOverrideOnce), run: conformanceBootOverride(schemas.ContinuousBootSourceOverrideEnabled, schemas.ContinuousBootSourceOverrideEnabled)},
	{name: "boot-override-once", applies: bootOverrideOnce, run: conformanceBootOverride(schemas.OnceBootSourceOverrideEnabled, schemas.DisabledBootSourceOverrideEnabled)},
	{name: "overlay", applies: func(p profile.Profile) bool { return len(p.Overlay) > 0 }, run: conformanceOverlay},
}

func allOf(predicates ...func(profile.Profile) bool) func(profile.Profile) bool {
	return func(p profile.Profile) bool {
		for _, predicate := range predicates {
			if !predicate(p) {
				return false
			}
		}

		return true
	}
}

func not(predicate func(profile.Profile) bool) func(profile.Profile) bool {
	return func(p profile.Profile) bool { return !predicate(p) }
}

func patchResponse(response profile.PatchResponse) func(profile.Profile) bool {
	return func(p profile.Profile) bool { return p.BIOS.PatchResponse == response }
}

func mediaInsert(insert profile.MediaInsert) func(profile.Profile) bool {
	return func(p profile.Profile) bool { return p.VirtualMedia.Insert == insert }
}

func hasTaskResource(p profile.Profile) bool {
	return p.BIOS.TaskResource
}

func databaseWritable(p profile.Profile) bool {
	return p.SecureBoot.DatabaseWrite
}

func bootOverrideOnce(p profile.Profile) bool {
	return p.VirtualMedia.BootOverrideOnce
}

func hasUnlistedResetType(p profile.Profile) bool {
	_, ok := unlistedResetType(p)
	return ok
}

// unlistedResetType returns a reset type the handler implements, but the profile does not list.
func unlistedResetType(p profile.Profile) (profile.ResetType, bool) {
	if len(p.Power.ResetTypes) == 0 {
		return "", false
	}

	for _, resetType := range []profile.ResetType{profile.ResetTypeOn, profile.ResetTypeForceOn, profile.ResetTypeGracefulShutdown, profile.ResetTypeForceOff, profile.ResetTypeGracefulRestart, profile.ResetTypeForceRestart} {
		if !slices.Contains(p.Power.ResetTypes, resetType) {
			return resetType, true
		}
	}

	return "", false
}

func resetKeysSupported(p profile.Profile) bool {
	return p.SecureBoot.ResetKeys.Supported
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
	require.NoError(t, err, "list the test profiles")

	names := append(profile.List(), files...)

	profiles := make([]profile.Profile, 0, len(names))
	for _, name := range names {
		p, err := profile.Load(name)
		require.NoError(t, err, "load the profile %s", name)

		profiles = append(profiles, p)
	}

	return profiles
}

func TestProfileConformance(t *testing.T) {
	t.Parallel()

	for _, p := range conformanceProfiles(t) {
		t.Run(p.Name, func(t *testing.T) {
			t.Parallel()

			for _, scenario := range applicableScenarios(p) {
				t.Run(scenario.name, func(t *testing.T) {
					t.Parallel()

					scenario.run(t, p)
				})
			}
		})
	}
}

// applicableScenarios returns the scenarios that are meaningful for the profile.
func applicableScenarios(p profile.Profile) []conformanceScenario {
	return slices.DeleteFunc(slices.Clone(conformanceScenarios), func(scenario conformanceScenario) bool { return !scenario.applies(p) })
}

// fakeIncus returns an Incus client keeping the instance and its UEFI (Unified Extensible Firmware Interface) variables, so the tests read writes back.
func fakeIncus(instance incusapi.Instance) *mock.IncusClientMock {
	var mu sync.Mutex

	nvram := map[string]map[string]*incusapi.InstanceNVRAMVariable{}

	clone := func(put incusapi.InstancePut) incusapi.InstancePut {
		put.Config = maps.Clone(put.Config)
		put.Devices = maps.Clone(put.Devices)

		return put
	}

	return &mock.IncusClientMock{
		HasExtensionFunc: func(extension string) bool {
			return extension == "instance_nvram"
		},
		GetInstanceNVRAMGUIDFunc: func(name string, guid string) (map[string]*incusapi.InstanceNVRAMVariable, error) {
			mu.Lock()
			defer mu.Unlock()

			return maps.Clone(nvram[guid]), nil
		},
		GetInstanceNVRAMGUIDVarFunc: func(name string, guid string, varName string) (*incusapi.InstanceNVRAMVariable, string, error) {
			mu.Lock()
			defer mu.Unlock()

			variable, ok := nvram[guid][varName]
			if !ok {
				return nil, "", incusapi.StatusErrorf(http.StatusNotFound, "UEFI variable not found")
			}

			return variable, "", nil
		},
		UpdateInstanceNVRAMGUIDVarFunc: func(name string, guid string, varName string, data incusapi.InstanceNVRAMVariablePut, ETag string) error {
			mu.Lock()
			defer mu.Unlock()

			if nvram[guid] == nil {
				nvram[guid] = map[string]*incusapi.InstanceNVRAMVariable{}
			}

			// Incus reports the encoded variable as binary, only its presence matters here.
			nvram[guid][varName] = &incusapi.InstanceNVRAMVariable{InstanceNVRAMVariablePut: data, Binary: []byte{1}}

			return nil
		},
		DeleteInstanceNVRAMGUIDVarFunc: func(name string, guid string, varName string) error {
			mu.Lock()
			defer mu.Unlock()

			_, ok := nvram[guid][varName]
			if !ok {
				return incusapi.StatusErrorf(http.StatusNotFound, "UEFI variable not found")
			}

			delete(nvram[guid], varName)

			return nil
		},
		CreateStoragePoolVolumeFromISOFunc: func(pool string, args incusclient.StorageVolumeBackupArgs) (incusclient.Operation, error) {
			_, err := io.Copy(io.Discard, args.BackupFile)
			if err != nil {
				return nil, err
			}

			return &mock.IncusOperationMock{WaitFunc: func() error { return nil }}, nil
		},
		DeleteStoragePoolVolumeFunc: func(pool string, volType string, name string) error {
			return nil
		},
		UpdateInstanceStateFunc: func(name string, state incusapi.InstanceStatePut, ETag string) (incusclient.Operation, error) {
			mu.Lock()
			defer mu.Unlock()

			instance.Status = "Running"
			if state.Action == "stop" {
				instance.Status = "Stopped"
			}

			return &mock.IncusOperationMock{WaitFunc: func() error { return nil }}, nil
		},
		GetInstanceStateFunc: func(name string) (*incusapi.InstanceState, string, error) {
			mu.Lock()
			defer mu.Unlock()

			return &incusapi.InstanceState{Status: instance.Status}, "", nil
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
	require.NoError(t, err, "list the systems")
	require.Len(t, systems, 1, "the proxy serves one system")

	require.Equal(t, "linuxcontainers.org", systems[0].Manufacturer, "the manufacturer names Incus")
	require.Equal(t, "Incus ("+p.Name+")", systems[0].Model, "the model names the profile")
}

func conformanceBIOSRegistry(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	bios := getBIOS(t, client)
	require.Equal(t, p.BIOS.Registry.ID, bios.AttributeRegistry, "Bios names the registry of the profile")

	published := registryAttributes(t, client, bios.AttributeRegistry)

	for _, attribute := range p.BIOS.Registry.Attributes {
		require.True(t, published[attribute.Name], "the registry lists %s", attribute.Name)
	}

	// bios-requires covers the attributes with a requirement.
	for _, attribute := range attributesWithoutRequirement(p) {
		require.Equal(t, fmt.Sprint(attribute.DefaultValue()), fmt.Sprint(bios.Attributes[attribute.Name]), "Bios publishes the default of %s", attribute.Name)
	}
}

// conformanceBIOSRegistryWrite writes every writable attribute and reads it back from where the profile keeps it.
func conformanceBIOSRegistryWrite(read func(t *testing.T, client *gofish.APIClient) map[string]any) func(t *testing.T, p profile.Profile) {
	return func(t *testing.T, p profile.Profile) {
		t.Helper()

		client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

		for _, attribute := range writableAttributes(p) {
			value := otherValue(attribute)

			writeBIOS(t, client, map[string]any{attribute.Name: value})

			require.Equal(t, fmt.Sprint(value), fmt.Sprint(read(t, client)[attribute.Name]), "the write of %s takes effect", attribute.Name)
		}
	}
}

// attributesWithoutRequirement returns the attributes, which Bios always publishes.
func attributesWithoutRequirement(p profile.Profile) []profile.Attribute {
	return slices.DeleteFunc(slices.Clone(p.BIOS.Registry.Attributes), func(attribute profile.Attribute) bool { return attribute.Requires != nil })
}

// writableAttributes returns the attributes a client can write without a requirement.
func writableAttributes(p profile.Profile) []profile.Attribute {
	return slices.DeleteFunc(slices.Clone(p.BIOS.Registry.Attributes), func(attribute profile.Attribute) bool { return attribute.ReadOnly || attribute.Requires != nil })
}

func getBIOSAttributes(t *testing.T, client *gofish.APIClient) map[string]any {
	t.Helper()

	return getBIOS(t, client).Attributes
}

func conformanceBIOSUnknownAttribute(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": map[string]any{"NoSuchAttribute": "value"}})
	answer := answerOf(t, resp, err)

	require.Equal(t, p.BIOS.Errors.UnknownAttribute.Status, answer.status, "status of bios.errors.unknown_attribute")
	require.Equal(t, p.BIOS.Errors.UnknownAttribute.MessageID, answer.messageID, "message of bios.errors.unknown_attribute")
}

// conformanceBIOSPatchTask checks a PATCH, which answers with a task, and the first poll of that task.
func conformanceBIOSPatchTask(wantLocation string, wantPollStatus int) func(t *testing.T, p profile.Profile) {
	return func(t *testing.T, p profile.Profile) {
		t.Helper()

		client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

		status, location := patchBIOS(t, client)

		require.Equal(t, http.StatusAccepted, status, "a task answer is 202")
		require.Regexp(t, wantLocation, location, "the location points to the task")

		resp, err := client.Get(location)
		require.NoError(t, err, "poll the task")
		resp.Body.Close()

		// A task monitor answers 202 while its task runs. A task resource always answers 200.
		require.Equal(t, wantPollStatus, resp.StatusCode, "status of the first poll")
	}
}

// conformanceBIOSPatchNoTask checks a PATCH, which answers without a task.
func conformanceBIOSPatchNoTask(wantStatus int) func(t *testing.T, p profile.Profile) {
	return func(t *testing.T, p profile.Profile) {
		t.Helper()

		client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

		status, location := patchBIOS(t, client)

		require.Equal(t, wantStatus, status, "status of bios.patch_response")
		require.Empty(t, location, "an answer without a task has no location")
	}
}

func conformanceTaskServiceAbsent(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	resp, err := client.Get("/redfish/v1/TaskService/Tasks")

	require.Equal(t, http.StatusNotFound, answerOf(t, resp, err).status, "the task collection exists only with bios.task_resource")
}

// conformanceTaskService checks that the task collection lists the task of a BIOS PATCH in the given state.
func conformanceTaskService(wantState schemas.TaskState) func(t *testing.T, p profile.Profile) {
	return func(t *testing.T, p profile.Profile) {
		t.Helper()

		client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

		tasks, err := client.Service.Tasks()
		require.NoError(t, err, "read the task service")

		patchBIOS(t, client)

		listed, err := tasks.Tasks()
		require.NoError(t, err, "list the tasks")
		require.NotEmpty(t, listed, "the collection lists the task of the PATCH")
		require.Equal(t, wantState, listed[len(listed)-1].TaskState, "state of the task of the PATCH")
	}
}

func conformanceBIOSImmediate(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	patchBIOS(t, client)

	require.Equal(t, "2", getBIOS(t, client).Attributes["incus.config.limits.cpu"], "the PATCH takes effect at once")
}

func conformanceBIOSStageAndApply(t *testing.T, p profile.Profile) {
	t.Helper()

	attribute, _ := stagedAttribute(p)
	value := otherValue(attribute)

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Running"}), api.WithProfile(p))

	resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": map[string]any{attribute.Name: value}})
	require.NoError(t, err, "stage the attribute")
	resp.Body.Close()

	location := resp.Header.Get("Location")

	require.Equal(t, fmt.Sprint(attribute.DefaultValue()), fmt.Sprint(getBIOS(t, client).Attributes[attribute.Name]), "Bios holds the current value")
	require.Equal(t, fmt.Sprint(value), fmt.Sprint(getBIOSSettings(t, client)[attribute.Name]), "Bios/Settings holds the staged value")
	require.True(t, taskRunning(t, client, location), "the task runs until the reset")

	resetSystem(t, client, profile.ResetTypeForceRestart)

	require.Equal(t, fmt.Sprint(value), fmt.Sprint(getBIOS(t, client).Attributes[attribute.Name]), "the reset applies the staged value")
	require.Empty(t, getBIOSSettings(t, client), "nothing is staged after the reset")
	require.False(t, taskRunning(t, client, location), "the reset completes the task")
}

// conformanceBIOSRequires checks a deferred attribute on an instance with the given status.
// write writes attributes and puts them into effect the way the profile needs.
func conformanceBIOSRequires(status string, write func(t *testing.T, client *gofish.APIClient, attributes map[string]any)) func(t *testing.T, p profile.Profile) {
	return func(t *testing.T, p profile.Profile) {
		t.Helper()

		dependent, _ := dependentAttribute(p)
		requirement := map[string]any{dependent.Requires.Attribute: dependent.Requires.Value}
		value := otherValue(dependent)

		client := setup(t, fakeIncus(incusapi.Instance{Status: status}), api.WithProfile(p))

		bios := getBIOS(t, client)
		require.NotContains(t, bios.Attributes, dependent.Name, "Bios hides the attribute until its requirement is in effect")
		require.True(t, registryAttributes(t, client, bios.AttributeRegistry)[dependent.Name], "the registry lists the attribute")

		// A requirement written in the same PATCH is not in effect yet, so nothing of the PATCH is taken.
		resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": map[string]any{dependent.Requires.Attribute: dependent.Requires.Value, dependent.Name: value}})
		answer := answerOf(t, resp, err)

		require.Equal(t, p.BIOS.Errors.DeferredRejected.Status, answer.status, "status of bios.errors.deferred_rejected")
		require.Equal(t, p.BIOS.Errors.DeferredRejected.MessageID, answer.messageID, "message of bios.errors.deferred_rejected")
		require.NotContains(t, getBIOSSettings(t, client), dependent.Requires.Attribute, "a refused PATCH stages nothing")

		write(t, client, requirement)
		require.Equal(t, fmt.Sprint(dependent.DefaultValue()), fmt.Sprint(getBIOS(t, client).Attributes[dependent.Name]), "Bios publishes the attribute once its requirement is in effect")

		write(t, client, map[string]any{dependent.Name: value})
		require.Equal(t, fmt.Sprint(value), fmt.Sprint(getBIOS(t, client).Attributes[dependent.Name]), "the attribute is writable once its requirement is in effect")
	}
}

func conformanceSecureBootWrite(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	_, certificate := testCertificate(t, "conformance")

	resp, err := client.Post("/redfish/v1/Systems/test-instance/SecureBoot/SecureBootDatabases/db/Certificates", map[string]any{"CertificateString": certificate, "CertificateType": "PEM"})

	require.Equal(t, http.StatusCreated, answerOf(t, resp, err).status, "a writable database accepts a certificate")
}

func conformanceSecureBootWriteRefused(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	_, certificate := testCertificate(t, "conformance")

	resp, err := client.Post("/redfish/v1/Systems/test-instance/SecureBoot/SecureBootDatabases/db/Certificates", map[string]any{"CertificateString": certificate, "CertificateType": "PEM"})
	answer := answerOf(t, resp, err)

	require.Equal(t, p.SecureBoot.DatabaseWriteError.Status, answer.status, "status of secure_boot.database_write_error")
	require.Equal(t, p.SecureBoot.DatabaseWriteError.MessageID, answer.messageID, "message of secure_boot.database_write_error")
}

func conformanceSecureBootResetKeys(t *testing.T, p profile.Profile) {
	t.Helper()

	incusClient := fakeIncus(incusapi.Instance{Status: "Stopped"})
	client := setup(t, incusClient, api.WithProfile(p))

	// Enroll a platform key the way the guest does, not over Redfish.
	err := incusClient.UpdateInstanceNVRAMGUIDVar("test-instance", uefi.EfiGlobalVariableGuid, "PK", incusapi.InstanceNVRAMVariablePut{}, "")
	require.NoError(t, err, "enroll the platform key")

	secureBoot := getSecureBoot(t, client)
	require.Equal(t, schemas.UserModeSecureBootModeType, secureBoot.SecureBootMode, "a platform key puts the firmware into user mode")
	require.Equal(t, p.SecureBoot.ResetKeys.Types, resetKeysAllowableValues(t, secureBoot), "the action publishes the reset keys types of the profile")

	// Reset the keys the way Operations Center does.
	_, err = secureBoot.ResetKeys(schemas.ResetKeysType(p.SecureBoot.ResetKeys.Types[0]))
	require.NoError(t, err, "reset the keys")

	require.Equal(t, schemas.SetupModeSecureBootModeType, getSecureBoot(t, client).SecureBootMode, "the reset puts the firmware into setup mode")
}

func conformanceSecureBootResetKeysAbsent(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	require.Empty(t, resetKeysAllowableValues(t, getSecureBoot(t, client)), "SecureBoot publishes no reset keys action")

	resp, err := client.Post("/redfish/v1/Systems/test-instance/SecureBoot/Actions/SecureBoot.ResetKeys", map[string]any{"ResetKeysType": "DeleteAllKeys"})
	closeBody(resp)

	require.Error(t, err, "the proxy refuses the reset keys action")
}

func getSecureBoot(t *testing.T, client *gofish.APIClient) *schemas.SecureBoot {
	t.Helper()

	systems, err := client.Service.Systems()
	require.NoError(t, err, "list the systems")
	require.Len(t, systems, 1, "the proxy serves one system")

	secureBoot, err := systems[0].SecureBoot()
	require.NoError(t, err, "read the SecureBoot resource")

	return secureBoot
}

// resetKeysAllowableValues returns the reset keys types published on the SecureBoot resource, nil without the action.
func resetKeysAllowableValues(t *testing.T, secureBoot *schemas.SecureBoot) []profile.ResetKeysType {
	t.Helper()

	var resource struct {
		Actions struct {
			ResetKeys struct {
				AllowableValues []profile.ResetKeysType `json:"ResetKeysType@Redfish.AllowableValues"`
			} `json:"#SecureBoot.ResetKeys"`
		}
	}

	err := json.Unmarshal(secureBoot.RawData, &resource)
	require.NoError(t, err, "decode the SecureBoot resource")

	return resource.Actions.ResetKeys.AllowableValues
}

const virtualMediaPath = "/redfish/v1/Managers/bmc1/VirtualMedia/CD"

// conformanceVirtualMediaInsert inserts virtual media the way Operations Center does for the insert variant of the profile.
func conformanceVirtualMediaInsert(method string, target func(t *testing.T, client *gofish.APIClient) string, body func(image string) map[string]any) func(t *testing.T, p profile.Profile) {
	return func(t *testing.T, p profile.Profile) {
		t.Helper()

		image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("image"))
		}))
		t.Cleanup(image.Close)

		incusClient := fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}})
		client := setup(t, incusClient, api.WithProfile(p), api.WithMediaMounter(&fakeMounter{}))

		insertTarget := target(t, client)

		payload, err := json.Marshal(body(image.URL))
		require.NoError(t, err, "encode the insert request")

		resp, err := client.RunRawRequestWithHeaders(method, insertTarget, bytes.NewReader(payload), "application/json", nil)
		require.NoError(t, err, "insert the virtual media")
		resp.Body.Close()

		waitForTask(t, client, resp.Header.Get("Location"))

		require.Equal(t, new(true), getVirtualMedia(t, client).Inserted, "virtual media is inserted through %s %s", method, insertTarget)

		instance, _, err := incusClient.GetInstance("test-instance")
		require.NoError(t, err, "read the instance")

		_, volume := instance.Devices["boot-media"]["pool"]
		require.Equal(t, p.VirtualMedia.Delivery == profile.DeliveryFullFetch, volume, "only a full fetch uses a storage volume")
	}
}

func standardInsertTarget(t *testing.T, client *gofish.APIClient) string {
	t.Helper()

	return virtualMediaActions(t, client).InsertMedia.Target
}

func oemInsertTarget(t *testing.T, client *gofish.APIClient) string {
	t.Helper()

	return findOEMInsertTarget(virtualMediaActions(t, client).Oem, 3)
}

func patchInsertTarget(*testing.T, *gofish.APIClient) string {
	return virtualMediaPath
}

func actionInsertBody(image string) map[string]any {
	return map[string]any{"Image": image}
}

func patchInsertBody(image string) map[string]any {
	return map[string]any{"Image": image, "Inserted": true}
}

func conformanceVirtualMediaActions(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}}), api.WithProfile(p))

	actions := virtualMediaActions(t, client)

	require.Equal(t, p.VirtualMedia.Insert == profile.MediaInsertStandard, actions.InsertMedia.Target != "", "the standard action is only published for standard")
	require.Equal(t, p.VirtualMedia.Insert == profile.MediaInsertOEM, findOEMInsertTarget(actions.Oem, 3) != "", "the OEM action is only published for oem")
}

func conformanceVirtualMediaHiddenPaths(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}}), api.WithProfile(p))

	requests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: virtualMediaPath + "/InsertMediaActionInfo"},
		{method: http.MethodPost, path: virtualMediaPath + "/Actions/VirtualMedia.InsertMedia"},
	}

	for _, request := range requests {
		resp, err := client.RunRawRequestWithHeaders(request.method, request.path, strings.NewReader(`{"Image":"http://localhost/image.iso"}`), "application/json", nil)

		require.Equal(t, http.StatusNotFound, answerOf(t, resp, err).status, "%s %s is hidden without the standard action", request.method, request.path)
	}
}

type virtualMediaActionsResource struct {
	InsertMedia struct {
		Target string `json:"target"`
	} `json:"#VirtualMedia.InsertMedia"`
	Oem map[string]any `json:"Oem"`
}

func virtualMediaActions(t *testing.T, client *gofish.APIClient) virtualMediaActionsResource {
	t.Helper()

	var resource struct {
		Actions virtualMediaActionsResource `json:"Actions"`
	}

	err := json.Unmarshal(getVirtualMedia(t, client).RawData, &resource)
	require.NoError(t, err, "decode the actions of the virtual media")

	return resource.Actions
}

// findOEMInsertTarget searches the OEM actions for one named InsertVirtualMedia, like Operations Center does.
func findOEMInsertTarget(oem map[string]any, depth int) string {
	if depth <= 0 {
		return ""
	}

	for name, value := range oem {
		member, ok := value.(map[string]any)
		if !ok {
			continue
		}

		if !strings.HasPrefix(name, "#") {
			target := findOEMInsertTarget(member, depth-1)
			if target != "" {
				return target
			}

			continue
		}

		target, ok := member["target"].(string)
		if strings.HasSuffix(name, ".InsertVirtualMedia") && ok {
			return target
		}
	}

	return ""
}

func getVirtualMedia(t *testing.T, client *gofish.APIClient) *schemas.VirtualMedia {
	t.Helper()

	virtualMedia, err := schemas.GetVirtualMedia(client, virtualMediaPath)
	require.NoError(t, err, "read the virtual media")

	return virtualMedia
}

// writeBIOS writes the attributes.
func writeBIOS(t *testing.T, client *gofish.APIClient, attributes map[string]any) {
	t.Helper()

	resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": attributes})
	require.NoError(t, err, "write the attributes")
	resp.Body.Close()
}

// writeBIOSAndReset writes the attributes and resets the instance, which applies the staged attributes.
func writeBIOSAndReset(t *testing.T, client *gofish.APIClient, attributes map[string]any) {
	t.Helper()

	writeBIOS(t, client, attributes)
	resetSystem(t, client, profile.ResetTypeForceRestart)
}

// registryAttributes discovers the attribute registry the way Operations Center does and returns the names it lists.
func registryAttributes(t *testing.T, client *gofish.APIClient, registryID string) map[string]bool {
	t.Helper()

	files, err := client.Service.Registries()
	require.NoError(t, err, "list the registries")

	var registry *schemas.AttributeRegistry

	for _, file := range files {
		if file.Registry != registryID || len(file.Location) == 0 {
			continue
		}

		registry, err = schemas.GetAttributeRegistry(client, file.Location[0].URI)
		require.NoError(t, err, "read the attribute registry")
	}

	require.NotNil(t, registry, "the registries list %s", registryID)

	published := map[string]bool{}
	for _, attribute := range registry.RegistryEntries.Attributes {
		published[attribute.AttributeName] = true
	}

	return published
}

// taskRunning follows the location of a PATCH answer with a task.
func taskRunning(t *testing.T, client *gofish.APIClient, location string) bool {
	t.Helper()

	resp, err := client.Get(location)
	require.NoError(t, err, "poll the task")

	defer resp.Body.Close()

	// A completed task monitor answers with the Bios resource, which has no task state.
	body := struct{ TaskState schemas.TaskState }{}
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err, "decode the task")

	return resp.StatusCode == http.StatusAccepted || body.TaskState == schemas.RunningTaskState
}

// getBIOSSettings returns the attributes of the settings object.
func getBIOSSettings(t *testing.T, client *gofish.APIClient) map[string]any {
	t.Helper()

	resp, err := client.Get("/redfish/v1/Systems/test-instance/Bios/Settings")
	require.NoError(t, err, "read the settings object")

	defer resp.Body.Close()

	settings := struct{ Attributes map[string]any }{}
	err = json.NewDecoder(resp.Body).Decode(&settings)
	require.NoError(t, err, "decode the settings object")

	return settings.Attributes
}

// patchBIOS writes one attribute every profile accepts and returns the status and location of the answer.
func patchBIOS(t *testing.T, client *gofish.APIClient) (int, string) {
	t.Helper()

	resp, err := client.Patch("/redfish/v1/Systems/test-instance/Bios/Settings", map[string]any{"Attributes": map[string]any{"incus.config.limits.cpu": "2"}})
	require.NoError(t, err, "write limits.cpu")

	defer resp.Body.Close()

	return resp.StatusCode, resp.Header.Get("Location")
}

func getBIOS(t *testing.T, client *gofish.APIClient) *schemas.Bios {
	t.Helper()

	systems, err := client.Service.Systems()
	require.NoError(t, err, "list the systems")
	require.Len(t, systems, 1, "the proxy serves one system")

	bios, err := systems[0].Bios()
	require.NoError(t, err, "read the Bios resource")

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

const systemPath = "/redfish/v1/Systems/test-instance"

func conformancePowerResetTypes(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	var resource struct {
		Actions struct {
			Reset struct {
				AllowableValues []profile.ResetType `json:"ResetType@Redfish.AllowableValues"`
			} `json:"#ComputerSystem.Reset"`
		} `json:"Actions"`
	}

	err := json.Unmarshal(getSystem(t, client).RawData, &resource)
	require.NoError(t, err, "decode the system")

	require.ElementsMatch(t, p.Power.ResetTypes, resource.Actions.Reset.AllowableValues, "the reset action publishes the reset types of the profile")
}

func conformancePowerResetTypeRefused(t *testing.T, p profile.Profile) {
	t.Helper()

	resetType, _ := unlistedResetType(p)

	incusClient := fakeIncus(incusapi.Instance{Status: "Stopped"})
	client := setup(t, incusClient, api.WithProfile(p))

	resp, err := client.Post(systemPath+"/Actions/ComputerSystem.Reset", map[string]any{"ResetType": resetType})
	answer := answerOf(t, resp, err)

	require.Equal(t, http.StatusBadRequest, answer.status, "a reset type outside of the profile is refused")
	require.Equal(t, "Base.1.18.ActionParameterValueNotInList", answer.messageID, "message of a refused reset type")
	require.Equal(t, []string{"#/ResetType"}, answer.relatedProperties, "the message names the reset type")
	require.Empty(t, incusClient.UpdateInstanceStateCalls(), "a refused reset type leaves the instance")
}

func conformancePowerSettle(t *testing.T, p profile.Profile) {
	t.Helper()

	var mu sync.Mutex
	now := time.Now()

	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		return now
	}

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p), api.WithClock(clock))

	resetSystem(t, client, profile.ResetTypeOn)

	resp, err := patchBoot(client, schemas.DisabledBootSourceOverrideEnabled)
	answer := answerOf(t, resp, err)

	require.Equal(t, p.Power.NotSettledError.Status, answer.status, "a write right after the power on is refused")
	require.Equal(t, p.Power.NotSettledError.MessageID, answer.messageID, "message of power.not_settled_error")
	require.Equal(t, "On", string(getSystem(t, client).PowerState), "reads pass in the window")

	mu.Lock()
	now = now.Add(p.Scaled(p.Power.NotSettledWindow))
	mu.Unlock()

	resp, err = patchBoot(client, schemas.DisabledBootSourceOverrideEnabled)
	require.NoError(t, err, "a write after the window passes")
	resp.Body.Close()
}

// conformanceBootOverride sets the boot override and checks what is left of it after a power on.
func conformanceBootOverride(enabled schemas.BootSourceOverrideEnabled, wantAfterPowerOn schemas.BootSourceOverrideEnabled) func(t *testing.T, p profile.Profile) {
	return func(t *testing.T, p profile.Profile) {
		t.Helper()

		client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{
			Devices: incusapi.DevicesMap{"boot-media": {"type": "disk", "source": "boot-media"}},
		}}), api.WithProfile(p))

		var resource struct {
			Boot struct {
				AllowableValues []schemas.BootSourceOverrideEnabled `json:"BootSourceOverrideEnabled@Redfish.AllowableValues"`
			} `json:"Boot"`
		}

		err := json.Unmarshal(getSystem(t, client).RawData, &resource)
		require.NoError(t, err, "decode the system")
		require.Equal(t, p.VirtualMedia.BootOverrideOnce, slices.Contains(resource.Boot.AllowableValues, schemas.OnceBootSourceOverrideEnabled), "Once is offered exactly with boot_override_once")

		resp, err := patchBoot(client, enabled)
		require.NoError(t, err, "set the boot override")
		resp.Body.Close()
		require.Equal(t, enabled, getSystem(t, client).Boot.BootSourceOverrideEnabled, "the system shows the boot override")

		resetSystem(t, client, profile.ResetTypeOn)

		require.Equal(t, wantAfterPowerOn, getSystem(t, client).Boot.BootSourceOverrideEnabled, "the power on drops a one time override only")
	}
}

func getSystem(t *testing.T, client *gofish.APIClient) *schemas.ComputerSystem {
	t.Helper()

	system, err := schemas.GetComputerSystem(client, systemPath)
	require.NoError(t, err, "read the system")

	return system
}

func patchBoot(client *gofish.APIClient, enabled schemas.BootSourceOverrideEnabled) (*http.Response, error) {
	return client.Patch(systemPath, map[string]any{"Boot": map[string]any{"BootSourceOverrideEnabled": enabled, "BootSourceOverrideTarget": schemas.CdBootSource}})
}

// resetSystem resets the system and waits for the task, if the reset answers with one.
func resetSystem(t *testing.T, client *gofish.APIClient, resetType profile.ResetType) {
	t.Helper()

	resp, err := client.Post(systemPath+"/Actions/ComputerSystem.Reset", map[string]any{"ResetType": resetType})
	require.NoError(t, err, "reset the system")
	resp.Body.Close()

	waitForTask(t, client, resp.Header.Get("Location"))
}

// waitForTask polls a task monitor until it no longer answers 202, an empty location is not polled.
func waitForTask(t *testing.T, client *gofish.APIClient, location string) {
	t.Helper()

	for range 100 {
		if location == "" {
			return
		}

		resp, err := client.Get(location)
		require.NoError(t, err, "poll the task")
		resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	require.Fail(t, "the task did not end", location)
}

func conformanceOverlay(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Running", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}}), api.WithProfile(p))

	placeholders := strings.NewReplacer("{system}", "test-instance", "{manager}", "bmc1", "{media}", "CD")

	for path, patch := range p.Overlay {
		resp, err := client.Get(strings.TrimSuffix(placeholders.Replace(path), "/"))
		require.NoError(t, err, "read %s", path)

		document := map[string]any{}
		err = json.NewDecoder(resp.Body).Decode(&document)
		resp.Body.Close()
		require.NoError(t, err, "decode %s", path)

		requirePatched(t, document, patch, path)
	}
}

// requirePatched checks the document holds every value of the merge patch and none it deletes.
func requirePatched(t *testing.T, document map[string]any, patch map[string]any, path string) {
	t.Helper()

	for name, value := range patch {
		nested, isObject := value.(map[string]any)

		switch {
		case value == nil:
			require.NotContains(t, document, name, "%s: the overlay deletes %s", path, name)

		case isObject:
			member, ok := document[name].(map[string]any)
			require.True(t, ok, "%s: %s is an object", path, name)
			requirePatched(t, member, nested, path+" "+name)

		default:
			want, err := json.Marshal(value)
			require.NoError(t, err, "encode the patched value")

			got, err := json.Marshal(document[name])
			require.NoError(t, err, "encode the served value")

			require.JSONEq(t, string(want), string(got), "%s: the overlay sets %s", path, name)
		}
	}
}

// fakeMounter records the media it mounts, so the tests need no FUSE.
type fakeMounter struct {
	mu     sync.Mutex
	err    error
	images []string
	opts   []media.Options
	closed int
}

const fakeMediaPath = "/var/lib/incus-redfish-proxy/media/default_test-instance/mnt/boot-media.iso"

func (m *fakeMounter) Mount(ctx context.Context, image string, opts media.Options) (api.MountedMedia, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return nil, m.err
	}

	m.images = append(m.images, image)
	m.opts = append(m.opts, opts)

	return fakeMedia{mounter: m}, nil
}

func (m *fakeMounter) Cleanup() error {
	return nil
}

func (m *fakeMounter) closeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.closed
}

type fakeMedia struct {
	mounter *fakeMounter
}

func (f fakeMedia) Path() string {
	return fakeMediaPath
}

func (f fakeMedia) Close() error {
	f.mounter.mu.Lock()
	defer f.mounter.mu.Unlock()

	f.mounter.closed++

	return nil
}
