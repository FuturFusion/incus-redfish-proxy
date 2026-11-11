package recording_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

var update = flag.Bool("update", false, "update the golden files")

// identities are the values of the fixtures that name the server and must never reach a draft.
var identities = []string{"server01", "SERIAL123", "4c4c4544", "bmc.example.com", "operations-center.example.com"}

func TestDraft(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string

		assert   func(t *testing.T, p profile.Profile)
		wantGaps int
		wantGap  recording.Gap
	}{
		{
			name:  "empty",
			input: "testdata/empty",
			assert: func(t *testing.T, p profile.Profile) {
				t.Helper()

				want := profile.Default()
				want.Name, want.Description = p.Name, p.Description
				require.Equal(t, want, p, "a draft without evidence equals the generic profile")
			},
			wantGaps: 34,
			wantGap:  recording.Gap{Key: "bios.patch_response", Reason: "Operations Center sent no BIOS PATCH"},
		},
		{
			name:  "generic",
			input: "testdata/generic",
			assert: func(t *testing.T, p profile.Profile) {
				t.Helper()

				require.Equal(t, profile.PatchResponseNoContent, p.BIOS.PatchResponse, "bios.patch_response")
				require.False(t, p.BIOS.SettingsObject, "bios.settings_object")
				require.Len(t, p.BIOS.Registry.Attributes, 3, "one synthetic attribute per type")
				require.Equal(t, []profile.ResetType{"On", "ForceOff", "GracefulShutdown", "ForceRestart"}, p.Power.ResetTypes, "power.reset_types")
			},
			wantGaps: 20,
			wantGap:  recording.Gap{Key: "power.not_settled_window", Reason: "no write was refused after a power on"},
		},
		{
			name:  "deferred",
			input: "testdata/deferred",
			assert: func(t *testing.T, p profile.Profile) {
				t.Helper()

				require.True(t, p.BIOS.Pending, "bios.pending")
				require.Equal(t, []profile.ApplyTime{"OnReset", "Immediate"}, p.BIOS.ApplyTime.Supported, "bios.apply_time.supported")
				require.False(t, p.BIOS.PatchRequiresPowerOff, "bios.patch_requires_power_off")
				require.Equal(t, &profile.Requirement{Attribute: "DraftRequirement", Value: "Performance"}, p.BIOS.Registry.Attributes[3].Requires, "deferred attribute")
				require.Equal(t, profile.ErrorShape{Status: 400, MessageID: "Base.1.12.PropertyUnknown"}, p.BIOS.Errors.DeferredRejected, "bios.errors.deferred_rejected")
			},
			wantGaps: 25,
			wantGap:  recording.Gap{Key: "secure_boot.enable_requires_power_off", Reason: "Operations Center did not change SecureBootEnable"},
		},
		{
			name:  "secure boot",
			input: "testdata/secure-boot",
			assert: func(t *testing.T, p profile.Profile) {
				t.Helper()

				require.False(t, p.SecureBoot.DatabaseWrite, "secure_boot.database_write")
				require.True(t, p.SecureBoot.ResetKeys.Supported, "secure_boot.reset_keys.supported")
				require.Equal(t, profile.ResetKeysResponseTask, p.SecureBoot.ResetKeys.Response, "secure_boot.reset_keys.response")
				require.Equal(t, "Certificate", p.SecureBoot.CertificateIDPrefix, "secure_boot.certificate_id_prefix")
			},
			wantGaps: 23,
			wantGap:  recording.Gap{Key: "bios.pending"},
		},
		{
			name:  "oem media",
			input: "testdata/oem-media",
			assert: func(t *testing.T, p profile.Profile) {
				t.Helper()

				require.Equal(t, profile.MediaInsertOEM, p.VirtualMedia.Insert, "virtual_media.insert")
				require.True(t, p.VirtualMedia.BootOverrideOnce, "virtual_media.boot_override_once")
				require.Equal(t, profile.PowerResponseTask, p.Power.Response, "power.response")
				require.Equal(t, "19s", p.Power.NotSettledWindow.String(), "power.not_settled_window")
			},
			wantGaps: 25,
			wantGap:  recording.Gap{Key: "bios.registry.id", Reason: "Operations Center sent no BIOS PATCH"},
		},
		{
			name:  "patch media",
			input: "testdata/patch-media.jsonl",
			assert: func(t *testing.T, p profile.Profile) {
				t.Helper()

				require.Equal(t, profile.MediaInsertPatch, p.VirtualMedia.Insert, "virtual_media.insert")
				require.True(t, p.BIOS.TaskResource, "bios.task_resource")
			},
			wantGaps: 29,
			wantGap:  recording.Gap{Key: "virtual_media.delivery"},
		},
		{
			name:  "enrollment media",
			input: "testdata/enrollment-media",
			assert: func(t *testing.T, p profile.Profile) {
				t.Helper()

				require.True(t, p.BIOS.SettingsObject, "bios.settings_object from GET Bios without a PATCH")
				require.True(t, p.BIOS.Pending, "bios.pending, OnReset is the only apply time")
				require.Equal(t, []profile.ApplyTime{"OnReset"}, p.BIOS.ApplyTime.Supported, "bios.apply_time.supported")
				require.Equal(t, profile.ApplyTimeOnReset, p.BIOS.ApplyTime.Default, "bios.apply_time.default")
				require.Equal(t, []profile.ResetKeysType{"DeleteAllKeys"}, p.SecureBoot.ResetKeys.Types, "secure_boot.reset_keys.types from the accepted POST")
				require.Equal(t, profile.ResetKeysResponseOK, p.SecureBoot.ResetKeys.Response, "secure_boot.reset_keys.response for a 200 answer")
				require.Equal(t, profile.ResetKeysAppliesOnReset, p.SecureBoot.ResetKeys.Applies, "secure_boot.reset_keys.applies, UserMode after the reset of the keys")
				require.Equal(t, profile.ResourcePatchResponseResource, p.Responses.Patch, "responses.patch, the PATCH answers the resource")
				require.Equal(t, profile.MediaInsertPatch, p.VirtualMedia.Insert, "virtual_media.insert")
				require.Equal(t, profile.DeliveryStream, p.VirtualMedia.Delivery, "virtual_media.delivery from TransferMethod")
				require.Equal(t, 3*time.Second, p.Power.Delay, "power.delay, the lower bound of the polls")
			},
			wantGaps: 17,
			wantGap:  recording.Gap{Key: "secure_boot.database_write", Reason: "keys enrolled from media, no database write"},
		},
		{
			name:  "wrapped messages",
			input: "testdata/wrapped-messages",
			assert: func(t *testing.T, p profile.Profile) {
				t.Helper()

				require.True(t, p.BIOS.SettingsObject, "bios.settings_object")
				require.True(t, p.BIOS.Pending, "bios.pending from the settings object alone")
				require.Empty(t, p.BIOS.ApplyTime.Supported, "bios.apply_time.supported, none published")
				require.Equal(t, profile.MediaInsertStandard, p.VirtualMedia.Insert, "virtual_media.insert after a refused first try")
				require.True(t, p.VirtualMedia.BootOverrideOnce, "virtual_media.boot_override_once from the accepted PATCH")
				require.Equal(t, profile.PowerResponseOKError, p.Power.Response, "power.response, messages inside an error object")
				require.Equal(t, profile.InsertResponseOKError, p.VirtualMedia.InsertResponse, "virtual_media.insert_response")
				require.Equal(t, profile.ResetKeysResponseOKError, p.SecureBoot.ResetKeys.Response, "secure_boot.reset_keys.response")
				require.Equal(t, profile.ResetKeysAppliesOnReset, p.SecureBoot.ResetKeys.Applies, "secure_boot.reset_keys.applies")
				require.Equal(t, profile.ResourcePatchResponseOKError, p.Responses.Patch, "responses.patch")
				require.Equal(t, 9*time.Second, p.Power.Delay, "power.delay, the upper bound of the power off is below the lower bound of the power on")
			},
			wantGaps: 20,
			wantGap:  recording.Gap{Key: "secure_boot.enable_requires_power_off", Reason: "changed only while the server was off"},
		},
		{
			name:  "task monitor",
			input: "testdata/task-monitor",
			assert: func(t *testing.T, p profile.Profile) {
				t.Helper()

				require.Equal(t, profile.PatchResponseTaskMonitor, p.BIOS.PatchResponse, "bios.patch_response, the task URI answers without a task body")
				require.False(t, p.BIOS.TaskResource, "bios.task_resource")
				require.Equal(t, []profile.ApplyTime{"OnReset"}, p.BIOS.ApplyTime.Supported, "bios.apply_time.supported")
				require.Equal(t, profile.AttributeTypeEnumeration, p.BIOS.Registry.Attributes[2].Type, "deferred attribute typed from the registry below the Bios resource")
				require.Equal(t, profile.CertificateIDStyleSequential, p.SecureBoot.CertificateIDStyle, "secure_boot.certificate_id_style")
				require.Equal(t, "CustPolicy.", p.SecureBoot.CertificateIDPrefix, "secure_boot.certificate_id_prefix with a dot")
				require.Equal(t, profile.DatabaseAddResponseNoContent, p.SecureBoot.DatabaseAddResponse, "secure_boot.database_add_response")
				require.Equal(t, profile.DatabaseDeleteResponseOKError, p.SecureBoot.DatabaseDeleteResponse, "secure_boot.database_delete_response")
				require.Equal(t, profile.ResourcePatchResponseOK, p.Responses.Patch, "responses.patch, top level messages")
			},
			wantGaps: 18,
			wantGap:  recording.Gap{Key: "bios.patch_requires_power_off", Reason: "BIOS PATCHes sent only while the server was off"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			rec, err := recording.Open(tc.input)
			require.NoError(t, err, "open the recording")

			golden := filepath.Join("testdata", strings.TrimSuffix(filepath.Base(tc.input), ".jsonl")+".profile.yaml")

			// Run test
			data, gaps, err := recording.NewDraft(rec, "draft").Render()

			// Assert
			require.NoError(t, err, "render the draft")
			require.Equal(t, string(goldenFile(t, golden, data)), string(data), "golden draft %s, update with -update", golden)
			require.Len(t, gaps, tc.wantGaps, "keys without evidence")
			require.Contains(t, gaps, tc.wantGap, "a key without evidence and its reason")

			for _, identity := range identities {
				require.NotContains(t, string(data), identity, "identity of the server")
			}

			p, err := profile.Load(golden)
			require.NoError(t, err, "the golden draft loads as a profile")
			tc.assert(t, p)
		})
	}
}

func TestDraft_InvalidName(t *testing.T) {
	t.Parallel()

	rec, err := recording.Open("testdata/empty")
	require.NoError(t, err, "open the recording")

	_, _, err = recording.NewDraft(rec, "Not Valid").Render()

	require.ErrorContains(t, err, "draft is not a valid profile", "the profile name is validated")
}

func goldenFile(t *testing.T, path string, data []byte) []byte {
	t.Helper()

	if *update {
		writeFile(t, path, data)
	}

	return readFile(t, path)
}

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}
