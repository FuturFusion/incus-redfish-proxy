package compare

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

func TestLookup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tree func(t *testing.T) map[string]any
		key  string

		want any
	}{
		{
			name: "equal YAML in different text",
			tree: func(t *testing.T) map[string]any {
				t.Helper()

				return parsedTree(t, "version: 1\nname: a\ntiming:\n  scale: 1.0\npower:\n  delay: 1m30s\n")
			},
			key:  "power.delay",
			want: map[string]any{"on": "1m30s", "off": "1m30s"},
		},
		{
			name: "duration in seconds",
			tree: func(t *testing.T) map[string]any {
				t.Helper()

				return parsedTree(t, "version: 1\nname: a\npower:\n  delay: {on: 90s, off: 0s}\n")
			},
			key:  "power.delay",
			want: map[string]any{"on": "1m30s", "off": "0s"},
		},
		{
			name: "error shape",
			tree: func(t *testing.T) map[string]any {
				t.Helper()

				return parsedTree(t, "version: 1\nname: a\n")
			},
			key:  "power.not_settled_error",
			want: map[string]any{"status": 400, "message_id": "Base.1.18.ResourceInStandby"},
		},
		{
			name: "registry ID by presence",
			tree: func(t *testing.T) map[string]any {
				t.Helper()

				return parsedTree(t, "version: 1\nname: a\nbios:\n  registry:\n    id: VendorRegistry.v1_0_0\n    attributes:\n      - {name: A, type: Boolean}\n")
			},
			key:  "bios.registry.id",
			want: true,
		},
		{
			name: "BIOS attributes by type",
			tree: func(t *testing.T) map[string]any {
				t.Helper()

				return parsedTree(t, `version: 1
name: a
bios:
  registry:
    id: R.v1_0_0
    attributes:
      - {name: Mode, type: Enumeration, values: ["A", "B"]}
      - {name: Cores, type: Integer, requires: {attribute: Mode, value: B}}
      - {name: Sriov, type: Boolean}
`)
			},
			key:  recording.AttributesKey,
			want: []string{"Boolean", "Enumeration", "Integer requires"},
		},
		{
			name: "missing key",
			tree: func(t *testing.T) map[string]any {
				t.Helper()

				return parsedTree(t, "version: 1\nname: a\n")
			},
			key:  "power.delay.unknown",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			tree := tc.tree(t)

			// Run test
			got := lookup(tree, tc.key)

			// Assert
			require.Equal(t, tc.want, got, "value of %s", tc.key)
		})
	}
}

func TestPollSeen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value any

		want any
	}{
		{name: "delays", key: "power.delay", value: map[string]any{"on": "12s", "off": "0s"}, want: map[string]any{"on": true, "off": false}},
		{name: "power cycle", key: "secure_boot.power_cycle.off_for", value: "12s", want: true},
		{name: "no power cycle", key: "secure_boot.power_cycle.off_after", value: "0s", want: false},
		{name: "other key", key: "power.response", value: "204", want: "204"},
		{name: "missing delay", key: "power.delay", value: nil, want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Run test
			got := pollSeen(tc.key, tc.value)

			// Assert
			require.Equal(t, tc.want, got, "comparable value of %s", tc.key)
		})
	}
}

func parsedTree(t *testing.T, text string) map[string]any {
	t.Helper()

	p, err := profile.Parse([]byte(text))
	require.NoError(t, err, "parse the profile")

	tree, err := profileTree(p)
	require.NoError(t, err, "marshal the profile")

	return tree
}

func TestKeyRows(t *testing.T) {
	t.Parallel()

	// Setup
	generic := openFixture(t, "generic")
	oemMedia := openFixture(t, "oem-media")

	p, err := profile.Load("../recording/testdata/generic.profile.yaml")
	require.NoError(t, err, "load the profile")

	// Run test
	rows, parity, warning := keyRows(generic, oemMedia, &p)

	// Assert
	require.Empty(t, warning, "both drafts succeed")

	byKey := map[string]KeyRow{}
	for _, row := range rows {
		byKey[row.Key] = row
	}

	require.NotContains(t, byKey, "virtual_media.stream.cache", "fixed keys are skipped")
	require.NotContains(t, byKey, "timing.scale", "fixed keys are skipped")

	insert := byKey["virtual_media.insert"]
	require.True(t, insert.Counted, "virtual_media.insert is observed on both sides")
	require.Equal(t, []string{DiffCandidateProfile, DiffReferenceCandidate}, insert.Differences, "the OEM insert differs")

	notSettled := byKey["power.not_settled_window"]
	require.False(t, notSettled.Counted, "a gap on one side is not counted")
	require.Equal(t, recording.DraftGap, notSettled.Reference.Status, "gap on the reference side")
	require.Equal(t, []string{DiffCandidateProfile, DiffReferenceCandidate}, notSettled.Differences, "a gap is shown")

	require.Equal(t, Parity{Label: "Keys", Both: 1, Total: 6, Unit: "profile keys equal in both drafts"}, parity, "parity")
}

func TestKeyRowsWithoutProfile(t *testing.T) {
	t.Parallel()

	// Setup
	generic := openFixture(t, "generic")

	// Run test
	rows, parity, warning := keyRows(generic, generic, nil)

	// Assert
	require.Empty(t, warning, "both drafts succeed")
	require.True(t, parity.Full(), "a recording equals itself")

	for _, row := range rows {
		require.Empty(t, row.Profile, "no profile value for %s", row.Key)
		require.Empty(t, row.Differences, "no difference for %s", row.Key)
	}
}

func openFixture(t *testing.T, name string) *recording.Recording {
	t.Helper()

	rec, err := recording.Open("../recording/testdata/" + name)
	require.NoError(t, err, "open the fixture %s", name)

	return rec
}
