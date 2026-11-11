package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	t.Parallel()

	const fixtures = "../../internal/recording/testdata/"

	tests := []struct {
		name string
		args func(t *testing.T, out string) []string

		assertErr  require.ErrorAssertionFunc
		wantStdout string
		wantStderr string
		wantFile   string
	}{
		{
			name:       "report to standard output",
			args:       func(*testing.T, string) []string { return []string{fixtures + "generic", fixtures + "oem-media"} },
			assertErr:  require.NoError,
			wantStdout: "- Steps: 3 of 7 states on both sides\n",
		},
		{
			name: "verbose report to a file",
			args: func(_ *testing.T, out string) []string {
				return []string{"--verbose", "--out", out, fixtures + "generic", fixtures + "deferred"}
			},
			assertErr: require.NoError,
			wantFile:  "### apply-bios\n",
		},
		{
			name: "reference directory with a profile",
			args: func(*testing.T, string) []string {
				return []string{"../../profiles/hpe-ilo5", "../../profiles/hpe-ilo5"}
			},
			assertErr:  require.NoError,
			wantStdout: "  - profile.yaml: ",
		},
		{
			name: "reference file without a profile",
			args: func(*testing.T, string) []string {
				return []string{fixtures + "patch-media.jsonl", fixtures + "generic"}
			},
			assertErr:  require.NoError,
			wantStdout: "Values and sequence numbers of the keys:",
		},
		{
			name: "invalid profile of the reference",
			args: func(t *testing.T, out string) []string {
				t.Helper()

				dir := filepath.Dir(out)
				data, err := os.ReadFile(fixtures + "generic/manifest.json")
				require.NoError(t, err, "read the manifest")
				require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644), "write the manifest")
				require.NoError(t, os.WriteFile(filepath.Join(dir, "exchanges.jsonl"), nil, 0o644), "write the exchanges")
				require.NoError(t, os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte("version: 2\n"), 0o644), "write the profile")

				return []string{dir, fixtures + "generic"}
			},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "profile.yaml", a...)
			},
		},
		{
			name: "missing arguments",
			args: func(*testing.T, string) []string { return []string{fixtures + "generic"} },
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "expected a reference and a candidate recording", a...)
			},
			wantStderr: "Usage: redfish-recording-compare",
		},
		{
			name: "unknown flag",
			args: func(*testing.T, string) []string { return []string{"--unknown"} },
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "unknown flag", a...)
			},
		},
		{
			name: "unreadable reference",
			args: func(_ *testing.T, out string) []string { return []string{out, fixtures + "generic"} },
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorIs(tt, err, os.ErrNotExist, a...)
			},
		},
		{
			name: "unreadable candidate",
			args: func(_ *testing.T, out string) []string { return []string{fixtures + "generic", out} },
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorIs(tt, err, os.ErrNotExist, a...)
			},
		},
		{
			name: "unwritable report",
			args: func(_ *testing.T, out string) []string {
				return []string{"--out", filepath.Join(out, "missing", "report.md"), fixtures + "generic", fixtures + "generic"}
			},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "write report", a...)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			out := filepath.Join(t.TempDir(), "report.md")

			var stdout, stderr strings.Builder

			// Run test
			err := run(tc.args(t, out), &stdout, &stderr)

			// Assert
			tc.assertErr(t, err, "the error of run")
			require.Contains(t, stdout.String(), tc.wantStdout, "standard output")
			require.Contains(t, stderr.String(), tc.wantStderr, "standard error")

			data, _ := os.ReadFile(out)
			require.Contains(t, string(data), tc.wantFile, "written report")
		})
	}
}
