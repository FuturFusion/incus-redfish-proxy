package recording_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

		assert         func(t *testing.T, p profile.Profile)
		wantNoEvidence int
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
			wantNoEvidence: 33,
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
			wantNoEvidence: 21,
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
			wantNoEvidence: 24,
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
			wantNoEvidence: 22,
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
			wantNoEvidence: 25,
		},
		{
			name:  "patch media",
			input: "testdata/patch-media.jsonl",
			assert: func(t *testing.T, p profile.Profile) {
				t.Helper()

				require.Equal(t, profile.MediaInsertPatch, p.VirtualMedia.Insert, "virtual_media.insert")
				require.True(t, p.BIOS.TaskResource, "bios.task_resource")
			},
			wantNoEvidence: 31,
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
			data, noEvidence, err := recording.NewDraft(rec, "draft").Render()

			// Assert
			require.NoError(t, err, "render the draft")
			require.Equal(t, string(goldenFile(t, golden, data)), string(data), "golden draft %s, update with -update", golden)
			require.Len(t, noEvidence, tc.wantNoEvidence, "keys without evidence")

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
