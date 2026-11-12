package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/uefi"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/internal/util/testing/boom"
)

func TestRedfishServer_OKAnswers(t *testing.T) {
	t.Parallel()

	const (
		resetPath      = systemPath + "/Actions/ComputerSystem.Reset"
		resetKeysPath  = systemPath + "/SecureBoot/Actions/SecureBoot.ResetKeys"
		secureBootPath = systemPath + "/SecureBoot"
	)

	stopped := incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}}
	running := incusapi.Instance{Status: "Running", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}}
	inserted := incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{
		"boot-media": {"type": "disk", "pool": "default", "source": "test-instance-boot-media.iso"},
	}}}

	success := "Base.1.18.Success"
	resetRequired := "Base.1.18.ResetRequired"

	resetKeys := func(response profile.ResetKeysResponse, applies profile.ResetKeysApplies) func(p *profile.Profile) {
		return func(p *profile.Profile) {
			p.SecureBoot.ResetKeys = profile.ResetKeys{Supported: true, Types: []profile.ResetKeysType{profile.ResetKeysTypeDeleteAllKeys}, Response: response, Applies: applies}
		}
	}

	patch := func(response profile.ResourcePatchResponse) func(p *profile.Profile) {
		return func(p *profile.Profile) {
			p.Responses.Patch = response
		}
	}

	tests := []struct {
		name     string
		profile  func(p *profile.Profile)
		instance incusapi.Instance
		method   string
		path     string
		body     string

		wantAnswer doneAnswer
	}{
		{
			name:     "reset - 200",
			profile:  func(p *profile.Profile) { p.Power.Response = profile.PowerResponseOK },
			instance: stopped,
			method:   http.MethodPost,
			path:     resetPath,
			body:     `{"ResetType": "On"}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: success},
		},
		{
			name:     "reset - 200 with error",
			profile:  func(p *profile.Profile) { p.Power.Response = profile.PowerResponseOKError },
			instance: stopped,
			method:   http.MethodPost,
			path:     resetPath,
			body:     `{"ResetType": "On"}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: success, inError: true},
		},
		{
			name:     "insert action - 200",
			profile:  func(p *profile.Profile) { p.VirtualMedia.InsertResponse = profile.InsertResponseOK },
			instance: stopped,
			method:   http.MethodPost,
			path:     virtualMediaPath + "/Actions/VirtualMedia.InsertMedia",
			body:     `{"Image": "{image}"}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: success},
		},
		{
			name:     "eject action - 200 with error",
			profile:  func(p *profile.Profile) { p.VirtualMedia.InsertResponse = profile.InsertResponseOKError },
			instance: inserted,
			method:   http.MethodPost,
			path:     virtualMediaPath + "/Actions/VirtualMedia.EjectMedia",
			body:     `{}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: success, inError: true},
		},
		{
			name:     "eject action - 204 with a task answer for the insert",
			profile:  func(p *profile.Profile) { p.VirtualMedia.InsertResponse = profile.InsertResponseTask },
			instance: inserted,
			method:   http.MethodPost,
			path:     virtualMediaPath + "/Actions/VirtualMedia.EjectMedia",
			body:     `{}`,

			wantAnswer: doneAnswer{status: http.StatusNoContent},
		},
		{
			name:     "reset keys immediately - 200",
			profile:  resetKeys(profile.ResetKeysResponseOK, profile.ResetKeysAppliesImmediately),
			instance: stopped,
			method:   http.MethodPost,
			path:     resetKeysPath,
			body:     `{"ResetKeysType": "DeleteAllKeys"}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: success},
		},
		{
			name:     "reset keys on reset - 200 with error",
			profile:  resetKeys(profile.ResetKeysResponseOKError, profile.ResetKeysAppliesOnReset),
			instance: running,
			method:   http.MethodPost,
			path:     resetKeysPath,
			body:     `{"ResetKeysType": "DeleteAllKeys"}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: resetRequired, inError: true},
		},
		{
			name:     "reset keys on reset - 204",
			profile:  resetKeys(profile.ResetKeysResponseNoContent, profile.ResetKeysAppliesOnReset),
			instance: running,
			method:   http.MethodPost,
			path:     resetKeysPath,
			body:     `{"ResetKeysType": "DeleteAllKeys"}`,

			wantAnswer: doneAnswer{status: http.StatusNoContent},
		},
		{
			name:     "system patch - 204",
			profile:  patch(profile.ResourcePatchResponseNoContent),
			instance: stopped,
			method:   http.MethodPatch,
			path:     systemPath,
			body:     `{"Boot": {"BootSourceOverrideEnabled": "Disabled"}}`,

			wantAnswer: doneAnswer{status: http.StatusNoContent},
		},
		{
			name:     "system patch - 200",
			profile:  patch(profile.ResourcePatchResponseOK),
			instance: stopped,
			method:   http.MethodPatch,
			path:     systemPath,
			body:     `{"Boot": {"BootSourceOverrideEnabled": "Disabled"}}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: success},
		},
		{
			name:     "system patch - 200 with error",
			profile:  patch(profile.ResourcePatchResponseOKError),
			instance: stopped,
			method:   http.MethodPatch,
			path:     systemPath,
			body:     `{"Boot": {"BootSourceOverrideEnabled": "Disabled"}}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: success, inError: true},
		},
		{
			name:     "system patch - 200 with the resource",
			profile:  patch(profile.ResourcePatchResponseResource),
			instance: stopped,
			method:   http.MethodPatch,
			path:     systemPath,
			body:     `{"Boot": {"BootSourceOverrideEnabled": "Disabled"}}`,

			wantAnswer: doneAnswer{status: http.StatusOK, odataID: systemPath},
		},
		{
			name:     "system patch without boot - 200",
			profile:  patch(profile.ResourcePatchResponseOK),
			instance: stopped,
			method:   http.MethodPatch,
			path:     systemPath,
			body:     `{}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: success},
		},
		{
			name:     "secure boot patch - 200 with the resource",
			profile:  patch(profile.ResourcePatchResponseResource),
			instance: stopped,
			method:   http.MethodPatch,
			path:     secureBootPath,
			body:     `{"SecureBootEnable": true}`,

			wantAnswer: doneAnswer{status: http.StatusOK, odataID: secureBootPath},
		},
		{
			name:     "secure boot patch on a stopped instance - 200",
			profile:  patch(profile.ResourcePatchResponseOK),
			instance: stopped,
			method:   http.MethodPatch,
			path:     secureBootPath,
			body:     `{"SecureBootEnable": true}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: success},
		},
		{
			name: "secure boot patch on a running instance - 200 with error",
			profile: func(p *profile.Profile) {
				p.Responses.Patch = profile.ResourcePatchResponseOKError
				p.SecureBoot.EnableRequiresPowerOff = false
			},
			instance: running,
			method:   http.MethodPatch,
			path:     secureBootPath,
			body:     `{"SecureBootEnable": true}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: resetRequired, inError: true},
		},
		{
			name:     "secure boot patch without enable - 200",
			profile:  patch(profile.ResourcePatchResponseOK),
			instance: stopped,
			method:   http.MethodPatch,
			path:     secureBootPath,
			body:     `{}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: success},
		},
		{
			name:     "virtual media patch insert - 200 with error",
			profile:  patch(profile.ResourcePatchResponseOKError),
			instance: stopped,
			method:   http.MethodPatch,
			path:     virtualMediaPath,
			body:     `{"Image": "{image}", "Inserted": true}`,

			wantAnswer: doneAnswer{status: http.StatusOK, messageID: success, inError: true},
		},
		{
			name:     "virtual media patch eject - 200 with the resource",
			profile:  patch(profile.ResourcePatchResponseResource),
			instance: inserted,
			method:   http.MethodPatch,
			path:     virtualMediaPath,
			body:     `{"Inserted": false}`,

			wantAnswer: doneAnswer{status: http.StatusOK, odataID: virtualMediaPath},
		},
		{
			name:     "virtual media patch eject - 204",
			profile:  patch(profile.ResourcePatchResponseNoContent),
			instance: inserted,
			method:   http.MethodPatch,
			path:     virtualMediaPath,
			body:     `{"Inserted": false}`,

			wantAnswer: doneAnswer{status: http.StatusNoContent},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("image"))
			}))
			t.Cleanup(image.Close)

			p := profile.Default()
			tc.profile(&p)

			client := setup(t, fakeIncus(tc.instance), api.WithProfile(p), api.WithMediaMounter(&fakeMounter{}))

			body := strings.ReplaceAll(tc.body, "{image}", image.URL)

			// Run test
			resp, err := client.RunRawRequestWithHeaders(tc.method, tc.path, strings.NewReader(body), "application/json", nil)

			// Assert
			require.Equal(t, tc.wantAnswer, doneAnswerOf(t, resp, err), "answer of %s %s", tc.method, tc.path)
		})
	}
}

func TestRedfishServer_SecureBootDatabaseAnswers(t *testing.T) {
	t.Parallel()

	const databases = systemPath + "/SecureBoot/SecureBootDatabases/"

	// Setup
	p := profile.Default()
	p.SecureBoot.CertificateIDStyle = profile.CertificateIDStyleSequential
	p.SecureBoot.CertificateIDPrefix = "Certificate"
	p.SecureBoot.DatabaseAddResponse = profile.DatabaseAddResponseNoContent
	p.SecureBoot.DatabaseDeleteResponse = profile.DatabaseDeleteResponseOKError

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	_, certificate := testCertificate(t, "added")

	// Run test
	resp, err := client.Post(databases+"KEK/Certificates", map[string]any{"CertificateString": certificate, "CertificateType": "PEM"})
	added := doneAnswerOf(t, resp, err)
	location := resp.Header.Get("Location")

	listed, err := schemas.ListReferencedCertificates(client, databases+"KEK/Certificates")
	require.NoError(t, err, "list the certificates")

	resp, err = client.RunRawRequestWithHeaders(http.MethodDelete, databases+"KEK/Certificates/Certificate1", nil, "", nil)
	deleted := doneAnswerOf(t, resp, err)

	left, err := schemas.ListReferencedCertificates(client, databases+"KEK/Certificates")
	require.NoError(t, err, "list the certificates")

	// Assert
	require.Equal(t, doneAnswer{status: http.StatusNoContent}, added, "the POST answers 204 without a body")
	require.Empty(t, location, "the POST answers without a location")
	require.Len(t, listed, 1, "the collection lists the new certificate")
	require.Equal(t, "Certificate1", listed[0].ID, "the new certificate has the next sequential ID")
	require.Equal(t, doneAnswer{status: http.StatusOK, messageID: "Base.1.18.ResetRequired", inError: true}, deleted, "the DELETE answers 200 with the messages inside an error object")
	require.Empty(t, left, "the DELETE removes the certificate")
}

func TestRedfishServer_SecureBootSignatureAnswers(t *testing.T) {
	t.Parallel()

	const signatures = systemPath + "/SecureBoot/SecureBootDatabases/dbx/Signatures"

	// Setup
	p := profile.Default()
	p.SecureBoot.DatabaseAddResponse = profile.DatabaseAddResponseNoContent
	p.SecureBoot.DatabaseDeleteResponse = profile.DatabaseDeleteResponseOK

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	// Run test
	resp, err := client.Post(signatures, map[string]any{"SignatureType": "sha256", "SignatureTypeRegistry": "UEFI", "SignatureString": strings.Repeat("ab", 32)})
	added := doneAnswerOf(t, resp, err)
	location := resp.Header.Get("Location")

	listed, err := schemas.ListReferencedSignatures(client, signatures)
	require.NoError(t, err, "list the signatures")
	require.Len(t, listed, 1, "the collection lists the new signature")

	resp, err = client.RunRawRequestWithHeaders(http.MethodDelete, listed[0].ODataID, nil, "", nil)
	deleted := doneAnswerOf(t, resp, err)

	left, err := schemas.ListReferencedSignatures(client, signatures)
	require.NoError(t, err, "list the signatures")

	// Assert
	require.Equal(t, doneAnswer{status: http.StatusNoContent}, added, "the POST answers 204 without a body")
	require.Empty(t, location, "the POST answers without a location")
	require.Equal(t, doneAnswer{status: http.StatusOK, messageID: "Base.1.18.ResetRequired"}, deleted, "the DELETE answers 200 with top level messages")
	require.Empty(t, left, "the DELETE removes the signature")
}

func TestRedfishServer_SecureBootResetKeysOnResetRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   string
		requests []string

		wantStored string
		wantKept   []string
	}{
		{
			name:     "running instance",
			status:   "Running",
			requests: []string{"DeletePK"},

			wantStored: "DeletePK",
			wantKept:   []string{"PK", "KEK"},
		},
		{
			name:     "stopped instance",
			status:   "Stopped",
			requests: []string{"DeletePK"},

			wantStored: "DeletePK",
			wantKept:   []string{"PK", "KEK"},
		},
		{
			name:     "delete all keys replaces delete platform key",
			status:   "Running",
			requests: []string{"DeletePK", "DeleteAllKeys"},

			wantStored: "DeleteAllKeys",
			wantKept:   []string{"PK", "KEK"},
		},
		{
			name:     "delete platform key does not replace delete all keys",
			status:   "Running",
			requests: []string{"DeleteAllKeys", "DeletePK"},

			wantStored: "DeleteAllKeys",
			wantKept:   []string{"PK", "KEK"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			p := profile.Default()
			p.SecureBoot.ResetKeys = profile.ResetKeys{Supported: true, Types: []profile.ResetKeysType{profile.ResetKeysTypeDeleteAllKeys, profile.ResetKeysTypeDeletePK}, Response: profile.ResetKeysResponseNoContent, Applies: profile.ResetKeysAppliesOnReset}

			incusClient := fakeIncus(incusapi.Instance{Status: tc.status})
			enrollKeys(t, incusClient)

			client := setup(t, incusClient, api.WithProfile(p))

			// Run test
			for _, request := range tc.requests {
				resp, err := client.Post(systemPath+"/SecureBoot/Actions/SecureBoot.ResetKeys", map[string]any{"ResetKeysType": request})
				require.NoError(t, err, "request the reset of the keys with %s", request)
				resp.Body.Close()
			}

			// Assert
			instance, _, err := incusClient.GetInstance("test-instance")
			require.NoError(t, err, "read the instance")

			require.Equal(t, tc.wantStored, instance.Config["user.redfish.secureboot.reset_keys"], "the stored request")
			require.Equal(t, tc.wantKept, nvramNames(incusClient, keyVariables), "the request deletes no keys")
			require.Equal(t, schemas.UserModeSecureBootModeType, getSecureBoot(t, client).SecureBootMode, "the firmware stays in user mode")
		})
	}
}

func TestRedfishServer_SecureBootResetKeysOnResetApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    string
		request   string
		resetType profile.ResetType

		wantStored  string
		wantKept    []string
		wantActions []string
	}{
		{
			name:      "on",
			status:    "Stopped",
			request:   "DeleteAllKeys",
			resetType: profile.ResetTypeOn,

			wantKept:    []string{},
			wantActions: []string{"start"},
		},
		{
			name:      "force restart",
			status:    "Running",
			request:   "DeletePK",
			resetType: profile.ResetTypeForceRestart,

			wantKept:    []string{"KEK"},
			wantActions: []string{"stop", "start"},
		},
		{
			name:      "graceful restart",
			status:    "Running",
			request:   "DeleteAllKeys",
			resetType: profile.ResetTypeGracefulRestart,

			wantKept:    []string{},
			wantActions: []string{"stop", "start"},
		},
		{
			name:      "force off keeps the request",
			status:    "Running",
			request:   "DeleteAllKeys",
			resetType: profile.ResetTypeForceOff,

			wantStored:  "DeleteAllKeys",
			wantKept:    []string{"PK", "KEK"},
			wantActions: []string{"stop"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			p := profile.Default()
			p.SecureBoot.ResetKeys = profile.ResetKeys{Supported: true, Types: []profile.ResetKeysType{profile.ResetKeysTypeDeleteAllKeys, profile.ResetKeysTypeDeletePK}, Response: profile.ResetKeysResponseNoContent, Applies: profile.ResetKeysAppliesOnReset}

			incusClient := fakeIncus(incusapi.Instance{Status: tc.status, InstancePut: incusapi.InstancePut{Config: map[string]string{"user.redfish.secureboot.reset_keys": tc.request}}})
			enrollKeys(t, incusClient)

			client := setup(t, incusClient, api.WithProfile(p))

			// Run test
			resetSystem(t, client, tc.resetType)

			// Assert
			instance, _, err := incusClient.GetInstance("test-instance")
			require.NoError(t, err, "read the instance")

			calls := incusClient.UpdateInstanceStateCalls()

			actions := make([]string, 0, len(calls))
			for _, call := range calls {
				actions = append(actions, call.State.Action)
			}

			require.Equal(t, tc.wantStored, instance.Config["user.redfish.secureboot.reset_keys"], "the request left for the next reset")
			require.Equal(t, tc.wantKept, nvramNames(incusClient, keyVariables), "the keys left")
			require.Equal(t, tc.wantActions, actions, "the keys change on a stopped instance only")
		})
	}
}

func TestRedfishServer_SecureBootResetKeysOnResetRestartsSequentialIDs(t *testing.T) {
	t.Parallel()

	const certificates = systemPath + "/SecureBoot/SecureBootDatabases/db/Certificates"

	// Setup
	p := profile.Default()
	p.SecureBoot.CertificateIDStyle = profile.CertificateIDStyleSequential
	p.SecureBoot.ResetKeys = profile.ResetKeys{Supported: true, Types: []profile.ResetKeysType{profile.ResetKeysTypeDeleteAllKeys}, Response: profile.ResetKeysResponseNoContent, Applies: profile.ResetKeysAppliesOnReset}

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p))

	add := func(commonName string) string {
		t.Helper()

		_, certificate := testCertificate(t, commonName)

		resp, err := client.Post(certificates, map[string]any{"CertificateString": certificate, "CertificateType": "PEM"})
		require.NoError(t, err, "add the certificate %s", commonName)
		resp.Body.Close()

		return resp.Header.Get("Location")
	}

	// Run test
	first := add("first")
	second := add("second")

	resp, err := client.Post(systemPath+"/SecureBoot/Actions/SecureBoot.ResetKeys", map[string]any{"ResetKeysType": "DeleteAllKeys"})
	require.NoError(t, err, "request the reset of the keys")
	resp.Body.Close()

	// A write while the request waits goes to the NVRAM, the next reset deletes it.
	pending := add("pending")

	resetSystem(t, client, profile.ResetTypeOn)

	listed, err := schemas.ListReferencedCertificates(client, certificates)
	require.NoError(t, err, "list the certificates")

	resetSystem(t, client, profile.ResetTypeForceOff)

	afterReset := add("after")

	// Assert
	require.Equal(t, certificates+"/1", first, "the first certificate gets the first number")
	require.Equal(t, certificates+"/2", second, "the count continues")
	require.Equal(t, certificates+"/3", pending, "a write while the request waits continues the count")
	require.Empty(t, listed, "the reset deletes all certificates")
	require.Equal(t, certificates+"/1", afterReset, "the delayed reset restarts the count")
}

// keyVariables are the UEFI variables enrollKeys writes.
var keyVariables = []struct{ guid, name string }{{uefi.EfiGlobalVariableGuid, "PK"}, {uefi.EfiGlobalVariableGuid, "KEK"}}

// enrollKeys writes PK and KEK the way the guest does, not over Redfish.
func enrollKeys(t *testing.T, incusClient api.IncusClient) {
	t.Helper()

	for _, variable := range keyVariables {
		err := incusClient.UpdateInstanceNVRAMGUIDVar("test-instance", variable.guid, variable.name, incusapi.InstanceNVRAMVariablePut{}, "")
		require.NoError(t, err, "enroll %s", variable.name)
	}
}

func TestRedfishServer_SecureBootResetKeysOnResetFailure(t *testing.T) {
	t.Parallel()

	// Setup
	p := profile.Default()
	p.SecureBoot.ResetKeys = profile.ResetKeys{Supported: true, Types: []profile.ResetKeysType{profile.ResetKeysTypeDeleteAllKeys}, Response: profile.ResetKeysResponseNoContent, Applies: profile.ResetKeysAppliesOnReset}

	incusClient := fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Config: map[string]string{"user.redfish.secureboot.reset_keys": "DeleteAllKeys"}}})
	incusClient.DeleteInstanceNVRAMGUIDVarFunc = func(name string, guid string, varName string) error {
		return boom.Error
	}

	client := setup(t, incusClient, api.WithProfile(p))

	// Run test
	resp, err := client.Post(systemPath+"/Actions/ComputerSystem.Reset", map[string]any{"ResetType": profile.ResetTypeOn})
	answer := answerOf(t, resp, err)

	// Assert
	instance, _, err := incusClient.GetInstance("test-instance")
	require.NoError(t, err, "read the instance")

	require.Equal(t, http.StatusInternalServerError, answer.status, "the reset reports the failed deletion")
	require.Equal(t, "DeleteAllKeys", instance.Config["user.redfish.secureboot.reset_keys"], "the request stays for the next reset")
	require.Empty(t, incusClient.UpdateInstanceStateCalls(), "the instance stays stopped")
}

func TestRedfishServer_SecureBootPowerCycle(t *testing.T) {
	t.Parallel()

	const settle = 300 * time.Millisecond

	tests := []struct {
		name       string
		config     map[string]string
		powerCycle profile.PowerCycle

		// change runs before the power on, like Operations Center, which changes secure boot while the system is off.
		change func(t *testing.T, client *gofish.APIClient)

		// during runs while the cycle waits, like a client that changes the power state.
		during func(t *testing.T, client *gofish.APIClient)

		wantActions    []string
		wantSecureBoot string
	}{
		{
			name:       "reset keys - power cycle and secure boot off",
			config:     map[string]string{"user.redfish.secureboot.reset_keys": "DeleteAllKeys", "security.secureboot": "true"},
			powerCycle: profile.PowerCycle{After: []profile.PowerCycleTrigger{profile.PowerCycleTriggerResetKeys}, OffAfter: 50 * time.Millisecond, OffFor: 50 * time.Millisecond},
			change:     func(*testing.T, *gofish.APIClient) {},
			during:     func(*testing.T, *gofish.APIClient) {},

			wantActions:    []string{"start", "stop", "start"},
			wantSecureBoot: "false",
		},
		{
			name:       "reset keys - no power cycle without the trigger",
			config:     map[string]string{"user.redfish.secureboot.reset_keys": "DeleteAllKeys", "security.secureboot": "true"},
			powerCycle: profile.PowerCycle{After: []profile.PowerCycleTrigger{profile.PowerCycleTriggerEnable}, OffAfter: 50 * time.Millisecond, OffFor: 50 * time.Millisecond},
			change:     func(*testing.T, *gofish.APIClient) {},
			during:     func(*testing.T, *gofish.APIClient) {},

			wantActions:    []string{"start"},
			wantSecureBoot: "false",
		},
		{
			name:       "enable - power cycle",
			config:     map[string]string{"security.secureboot": "false"},
			powerCycle: profile.PowerCycle{After: []profile.PowerCycleTrigger{profile.PowerCycleTriggerEnable}, OffAfter: 50 * time.Millisecond, OffFor: 50 * time.Millisecond},
			change:     enableSecureBoot,
			during:     func(*testing.T, *gofish.APIClient) {},

			wantActions:    []string{"start", "stop", "start"},
			wantSecureBoot: "true",
		},
		{
			name:       "enable - no power cycle if secure boot is on already",
			config:     map[string]string{"security.secureboot": "true"},
			powerCycle: profile.PowerCycle{After: []profile.PowerCycleTrigger{profile.PowerCycleTriggerEnable}, OffAfter: 50 * time.Millisecond, OffFor: 50 * time.Millisecond},
			change:     enableSecureBoot,
			during:     func(*testing.T, *gofish.APIClient) {},

			wantActions:    []string{"start"},
			wantSecureBoot: "true",
		},
		{
			name:       "enable - a client powers off before the cycle",
			config:     map[string]string{"security.secureboot": "false"},
			powerCycle: profile.PowerCycle{After: []profile.PowerCycleTrigger{profile.PowerCycleTriggerEnable}, OffAfter: 150 * time.Millisecond, OffFor: 50 * time.Millisecond},
			change:     enableSecureBoot,
			during: func(t *testing.T, client *gofish.APIClient) {
				t.Helper()

				resetSystem(t, client, profile.ResetTypeForceOff)
			},

			wantActions:    []string{"start", "stop"},
			wantSecureBoot: "true",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			p := profile.Default()
			p.SecureBoot.ResetKeys = profile.ResetKeys{
				Supported:          true,
				Types:              []profile.ResetKeysType{profile.ResetKeysTypeDeleteAllKeys},
				Response:           profile.ResetKeysResponseNoContent,
				Applies:            profile.ResetKeysAppliesOnReset,
				DisablesSecureBoot: true,
			}
			p.SecureBoot.PowerCycle = tc.powerCycle

			incusClient := fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Config: tc.config}})
			enrollKeys(t, incusClient)

			client := setup(t, incusClient, api.WithProfile(p))

			tc.change(t, client)

			// Run test
			resetSystem(t, client, profile.ResetTypeOn)
			tc.during(t, client)
			time.Sleep(settle)

			// Assert
			instance, _, err := incusClient.GetInstance("test-instance")
			require.NoError(t, err, "read the instance")

			calls := incusClient.UpdateInstanceStateCalls()

			actions := make([]string, 0, len(calls))
			for _, call := range calls {
				actions = append(actions, call.State.Action)
			}

			require.Equal(t, tc.wantActions, actions, "the power changes, including the cycle")
			require.Equal(t, tc.wantSecureBoot, instance.Config["security.secureboot"], "secure boot after the change")
			require.NotContains(t, instance.Config, "user.redfish.secureboot.power_cycle", "the power on removes the mark")
		})
	}
}

// enableSecureBoot switches secure boot on through Redfish.
func enableSecureBoot(t *testing.T, client *gofish.APIClient) {
	t.Helper()

	resp, err := client.Patch(systemPath+"/SecureBoot", map[string]any{"SecureBootEnable": true})
	require.NoError(t, err, "enable secure boot")
	resp.Body.Close()
}

func TestRedfishServer_EmptyDatabasesAfterPowerOn(t *testing.T) {
	t.Parallel()

	type step struct {
		// advance moves the clock before the step.
		advance time.Duration
		reset   profile.ResetType

		wantCertificates int
	}

	steps := []step{
		{advance: time.Second, reset: profile.ResetTypeOn, wantCertificates: 0},
		{advance: 9 * time.Minute, wantCertificates: 0},
		{advance: time.Minute, wantCertificates: 1},
		{advance: time.Second, reset: profile.ResetTypeForceRestart, wantCertificates: 0},
		{advance: time.Second, reset: profile.ResetTypeForceRestart, wantCertificates: 1},
	}

	// Setup
	p := profile.Default()
	p.SecureBoot.EmptyAfterPowerOn = profile.EmptyAfterPowerOn{Times: 2, For: 10 * time.Minute}

	var mu sync.Mutex

	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		return now
	}

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped"}), api.WithProfile(p), api.WithClock(clock))

	_, certificate := testCertificate(t, "written")

	resp, err := client.Post(keyDatabases+"db/Certificates", map[string]any{"CertificateString": certificate, "CertificateType": "PEM"})
	require.Equal(t, http.StatusCreated, answerOf(t, resp, err).status, "write a certificate")

	certificatesBeforePowerOn := certificateCount(t, client)

	got := make([]int, 0, len(steps))
	want := make([]int, 0, len(steps))

	// Run test
	for _, s := range steps {
		mu.Lock()
		now = now.Add(s.advance)
		mu.Unlock()

		if s.reset != "" {
			resetSystem(t, client, s.reset)
		}

		got = append(got, certificateCount(t, client))
		want = append(want, s.wantCertificates)
	}

	// Assert
	require.Equal(t, 1, certificatesBeforePowerOn, "the stopped instance shows the written certificate")
	require.Equal(t, want, got, "the first 2 power ons after the write read the database empty for 10 minutes")
}

// keyDatabases is the collection of the key databases of the system.
const keyDatabases = systemPath + "/SecureBoot/SecureBootDatabases/"

// certificateCount reads the number of certificates in the db key database.
func certificateCount(t *testing.T, client *gofish.APIClient) int {
	t.Helper()

	resp, err := client.Get(keyDatabases + "db/Certificates")
	require.NoError(t, err, "read the certificates")

	defer resp.Body.Close()

	var collection struct {
		Count int `json:"Members@odata.count"`
	}

	err = json.NewDecoder(resp.Body).Decode(&collection)
	require.NoError(t, err, "decode the certificates")

	return collection.Count
}

func TestRedfishServer_Unreachable(t *testing.T) {
	t.Parallel()

	// Setup
	p := profile.Default()
	p.SecureBoot.ResetKeys = profile.ResetKeys{
		Supported:   true,
		Types:       []profile.ResetKeysType{profile.ResetKeysTypeDeleteAllKeys},
		Response:    profile.ResetKeysResponseNoContent,
		Applies:     profile.ResetKeysAppliesOnReset,
		Unreachable: profile.Unreachable{For: 300 * time.Millisecond},
	}

	incusClient := fakeIncus(incusapi.Instance{Status: "Stopped"})
	enrollKeys(t, incusClient)

	server := httptest.NewServer(api.NewHandler("test-instance", incusClient, api.WithProfile(p)))
	t.Cleanup(server.Close)

	client := &http.Client{Timeout: 100 * time.Millisecond}

	post := func(path string, body string) int {
		resp, err := client.Post(server.URL+path, "application/json", strings.NewReader(body))
		require.NoError(t, err, "POST %s", path)
		resp.Body.Close()

		return resp.StatusCode
	}

	// Run test
	resetKeys := post(systemPath+"/SecureBoot/Actions/SecureBoot.ResetKeys", `{"ResetKeysType": "DeleteAllKeys"}`)
	powerOn := post(systemPath+"/Actions/ComputerSystem.Reset", `{"ResetType": "On"}`)

	respInWindow, errInWindow := client.Get(server.URL + "/redfish/v1/")
	closeBody(respInWindow)

	time.Sleep(300 * time.Millisecond)

	resp, errAfterWindow := client.Get(server.URL + "/redfish/v1/")
	require.NoError(t, errAfterWindow, "a request after the window")
	resp.Body.Close()

	// Assert
	require.Equal(t, http.StatusNoContent, resetKeys, "the key reset waits for the next power on")
	require.Equal(t, http.StatusNoContent, powerOn, "the power on deletes the keys")
	require.ErrorIs(t, errInWindow, context.DeadlineExceeded, "a request in the window only sees the timeout of the client")
	require.Equal(t, http.StatusOK, resp.StatusCode, "the proxy answers after the window")
}
