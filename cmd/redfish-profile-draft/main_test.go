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

	tests := []struct {
		name string
		args func(out string) []string

		assertErr  require.ErrorAssertionFunc
		wantStdout string
		wantStderr string
		wantFile   string
	}{
		{
			name:       "draft to standard output",
			args:       func(string) []string { return []string{"--name", "oem", "../../internal/recording/testdata/oem-media"} },
			assertErr:  require.NoError,
			wantStdout: "name: oem\n",
			wantStderr: "Read 6 exchanges. These keys keep their default for lack of evidence:\n  bios.patch_response\n",
		},
		{
			name:       "draft to a file",
			args:       func(out string) []string { return []string{"--out", out, "../../internal/recording/testdata/generic"} },
			assertErr:  require.NoError,
			wantStderr: "Read 9 exchanges.",
			wantFile:   "name: draft\n",
		},
		{
			name: "invalid name",
			args: func(string) []string {
				return []string{"--name", "Not Valid", "../../internal/recording/testdata/empty"}
			},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "draft is not a valid profile", a...)
			},
		},
		{
			name: "missing recording argument",
			args: func(string) []string { return nil },
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "expected exactly one recording", a...)
			},
			wantStderr: "Usage: redfish-profile-draft",
		},
		{
			name: "unknown flag",
			args: func(string) []string { return []string{"--unknown"} },
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "unknown flag", a...)
			},
		},
		{
			name: "missing recording",
			args: func(out string) []string { return []string{out} },
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorIs(tt, err, os.ErrNotExist, a...)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			out := filepath.Join(t.TempDir(), "profile.yaml")

			var stdout, stderr strings.Builder

			// Run test
			err := run(tc.args(out), &stdout, &stderr)

			// Assert
			tc.assertErr(t, err, "the error of run")
			require.Contains(t, stdout.String(), tc.wantStdout, "standard output")
			require.Contains(t, stderr.String(), tc.wantStderr, "standard error")

			data, _ := os.ReadFile(out)
			require.Contains(t, string(data), tc.wantFile, "written draft")
		})
	}
}
