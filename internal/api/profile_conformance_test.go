package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	{name: "secure-boot-write", applies: always, run: conformanceSecureBootWrite},
	{name: "virtual-media-insert", applies: always, run: conformanceVirtualMediaInsert},
	{name: "virtual-media-hidden-paths", applies: always, run: conformanceVirtualMediaHiddenPaths},
	{name: "secure-boot-reset-keys", applies: resetKeysSupported, run: conformanceSecureBootResetKeys},
	{name: "secure-boot-reset-keys-absent", applies: func(p profile.Profile) bool { return !resetKeysSupported(p) }, run: conformanceSecureBootResetKeysAbsent},
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

func conformanceSecureBootWrite(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	_, certificate := testCertificate(t, "conformance")

	resp, err := client.Post("/redfish/v1/Systems/test-instance/SecureBoot/SecureBootDatabases/db/Certificates", map[string]any{"CertificateString": certificate, "CertificateType": "PEM"})
	if resp != nil {
		resp.Body.Close()
	}

	if p.SecureBoot.DatabaseWrite {
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		return
	}

	redfishErr := &schemas.Error{}
	require.ErrorAs(t, err, &redfishErr)
	require.Equal(t, p.SecureBoot.DatabaseWriteError.Status, redfishErr.HTTPReturnedStatusCode)
	require.Len(t, redfishErr.ExtendedInfos, 1)
	require.Equal(t, p.SecureBoot.DatabaseWriteError.MessageID, redfishErr.ExtendedInfos[0].MessageID)
}

func conformanceSecureBootResetKeys(t *testing.T, p profile.Profile) {
	t.Helper()

	incusClient := fakeIncus(incusapi.Instance{Status: "Stopped"})
	client := setup(t, incusClient, api.WithProfile(p))

	// Enroll a platform key the way the guest does, not over Redfish.
	err := incusClient.UpdateInstanceNVRAMGUIDVar("test-instance", uefi.EfiGlobalVariableGuid, "PK", incusapi.InstanceNVRAMVariablePut{}, "")
	require.NoError(t, err)

	secureBoot := getSecureBoot(t, client)
	require.Equal(t, schemas.UserModeSecureBootModeType, secureBoot.SecureBootMode)
	require.Equal(t, p.SecureBoot.ResetKeys.Types, resetKeysAllowableValues(t, secureBoot))

	// Reset the keys the way Operations Center does.
	_, err = secureBoot.ResetKeys(schemas.ResetKeysType(p.SecureBoot.ResetKeys.Types[0]))
	require.NoError(t, err)

	require.Equal(t, schemas.SetupModeSecureBootModeType, getSecureBoot(t, client).SecureBootMode)
}

func conformanceSecureBootResetKeysAbsent(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	require.Empty(t, resetKeysAllowableValues(t, getSecureBoot(t, client)))

	resp, err := client.Post("/redfish/v1/Systems/test-instance/SecureBoot/Actions/SecureBoot.ResetKeys", map[string]any{"ResetKeysType": "DeleteAllKeys"})
	if resp != nil {
		resp.Body.Close()
	}

	require.Error(t, err)
}

func getSecureBoot(t *testing.T, client *gofish.APIClient) *schemas.SecureBoot {
	t.Helper()

	systems, err := client.Service.Systems()
	require.NoError(t, err)
	require.Len(t, systems, 1)

	secureBoot, err := systems[0].SecureBoot()
	require.NoError(t, err)

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
	require.NoError(t, err)

	return resource.Actions.ResetKeys.AllowableValues
}

const virtualMediaPath = "/redfish/v1/Managers/bmc1/VirtualMedia/CD"

func conformanceVirtualMediaInsert(t *testing.T, p profile.Profile) {
	t.Helper()

	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("image"))
	}))
	t.Cleanup(image.Close)

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}}), api.WithProfile(p))

	method, target, body := insertRequest(t, client, image.URL)

	payload, err := json.Marshal(body)
	require.NoError(t, err)

	resp, err := client.RunRawRequestWithHeaders(method, target, bytes.NewReader(payload), "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()

	location := resp.Header.Get("Location")
	for range 100 {
		if location == "" {
			break
		}

		resp, err = client.Get(location)
		require.NoError(t, err)
		resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	require.Equal(t, new(true), getVirtualMedia(t, client).Inserted, "virtual media is inserted through %s %s", method, target)
}

// insertRequest picks the insert request with the rules of Operations Center: the standard action, an OEM action or a PATCH.
func insertRequest(t *testing.T, client *gofish.APIClient, image string) (string, string, map[string]any) {
	t.Helper()

	actions := virtualMediaActions(t, client)

	standard := actions.InsertMedia.Target
	if standard != "" {
		return http.MethodPost, standard, map[string]any{"Image": image}
	}

	oem := findOEMInsertTarget(actions.Oem, 3)
	if oem != "" {
		return http.MethodPost, oem, map[string]any{"Image": image}
	}

	return http.MethodPatch, virtualMediaPath, map[string]any{"Image": image, "Inserted": true}
}

func conformanceVirtualMediaHiddenPaths(t *testing.T, p profile.Profile) {
	t.Helper()

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}}), api.WithProfile(p))

	actions := virtualMediaActions(t, client)

	standard := p.VirtualMedia.Insert == profile.MediaInsertStandard
	require.Equal(t, standard, actions.InsertMedia.Target != "", "the standard action is only published for standard")
	require.Equal(t, p.VirtualMedia.Insert == profile.MediaInsertOEM, findOEMInsertTarget(actions.Oem, 3) != "", "the OEM action is only published for oem")

	if standard {
		return
	}

	for _, path := range []string{virtualMediaPath + "/InsertMediaActionInfo", virtualMediaPath + "/Actions/VirtualMedia.InsertMedia"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "InsertMedia") {
			method = http.MethodPost
		}

		resp, err := client.RunRawRequestWithHeaders(method, path, strings.NewReader(`{"Image":"http://localhost/image.iso"}`), "application/json", nil)
		if resp != nil {
			resp.Body.Close()
		}

		redfishErr := &schemas.Error{}
		require.ErrorAs(t, err, &redfishErr, path)
		require.Equal(t, http.StatusNotFound, redfishErr.HTTPReturnedStatusCode, path)
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
	require.NoError(t, err)

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
	require.NoError(t, err)

	return virtualMedia
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
