package compare_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/compare"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

var update = flag.Bool("update", false, "update the golden files")

func TestCompareGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		reference string
		candidate string
		profile   string
		verbose   bool

		golden string
	}{
		{
			name:      "generic against oem media",
			reference: "generic",
			candidate: "oem-media",
			profile:   "generic.profile.yaml",
			golden:    "generic-oem-media.md",
		},
		{
			name:      "generic against deferred, verbose",
			reference: "generic",
			candidate: "deferred",
			profile:   "generic.profile.yaml",
			verbose:   true,
			golden:    "generic-deferred-verbose.md",
		},
		{
			name:      "generic against deferred, without profile",
			reference: "generic",
			candidate: "deferred",
			golden:    "generic-deferred.md",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			reference := open(t, filepath.Join("../recording/testdata", tc.reference))
			candidate := open(t, filepath.Join("../recording/testdata", tc.candidate))

			var p *profile.Profile

			if tc.profile != "" {
				loaded, err := profile.Load(filepath.Join("../recording/testdata", tc.profile))
				require.NoError(t, err, "load the profile")

				p = &loaded
			}

			// Run test
			data := compare.Compare(reference, candidate, p).Markdown(tc.verbose)

			// Assert
			golden := filepath.Join("testdata", tc.golden)
			require.Equal(t, string(goldenFile(t, golden, data)), string(data), "golden report %s, update with -update", golden)
		})
	}
}

func TestCompareArchivedRecordingsWithThemselves(t *testing.T) {
	t.Parallel()

	archives, err := filepath.Glob("../../profiles/*/recording.jsonl.gz")
	require.NoError(t, err, "find the archived recordings")
	require.NotEmpty(t, archives, "archived recordings exist")

	for _, archive := range archives {
		dir := filepath.Dir(archive)

		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()

			// Setup
			rec := open(t, dir)

			p, err := profile.Load(filepath.Join(dir, "profile.yaml"))
			require.NoError(t, err, "load the profile")

			// Run test
			report := compare.Compare(rec, rec, &p)

			// Assert
			for _, parity := range report.Parities.List() {
				require.True(t, parity.Full(), "full parity: %s", parity)
				require.Positive(t, parity.Total, "the section is not empty: %s", parity)
			}

			for _, row := range report.Keys {
				require.Equal(t, row.Reference, row.Candidate, "the reference draft equals the candidate draft for %s", row.Key)
				require.NotContains(t, row.Differences, compare.DiffReferenceCandidate, "no difference between the drafts for %s", row.Key)
			}
		})
	}
}

func open(t *testing.T, path string) *recording.Recording {
	t.Helper()

	rec, err := recording.Open(path)
	require.NoError(t, err, "open the recording %s", path)

	return rec
}

func goldenFile(t *testing.T, path string, data []byte) []byte {
	t.Helper()

	if *update {
		err := os.WriteFile(path, data, 0o644)
		require.NoError(t, err, "update the golden file")
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "read the golden file")

	return want
}
