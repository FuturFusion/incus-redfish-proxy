package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string

		assertErr  require.ErrorAssertionFunc
		wantStdout string
		wantStderr string
	}{
		{
			name:       "help",
			args:       []string{"--help"},
			assertErr:  require.NoError,
			wantStdout: "Usage: incus-redfish [flags] <command> [arguments]",
		},
		{
			name: "missing command",
			args: []string{},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, `unknown command ""`, a...)
			},
			wantStderr: "Usage: incus-redfish",
		},
		{
			name: "unknown command",
			args: []string{"unknown"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, `unknown command "unknown"`, a...)
			},
			wantStderr: "Usage: incus-redfish",
		},
		{
			name: "unknown flag",
			args: []string{"--unknown"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "unknown flag", a...)
			},
		},
		{
			name: "insert virtual media without file",
			args: []string{"insert-virtual-media"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "missing argument: file to insert as boot media", a...)
			},
			wantStderr: "Usage: incus-redfish",
		},
		{
			name: "set secure boot without enabled state",
			args: []string{"set-secureboot"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "missing argument: enabled state", a...)
			},
			wantStderr: "Usage: incus-redfish",
		},
		{
			name: "delete secure boot certificate without name",
			args: []string{"delete-secureboot-certificate"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "missing argument: certificate name", a...)
			},
			wantStderr: "Usage: incus-redfish",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			var stdout, stderr strings.Builder

			// Run test
			err := run(tc.args, &stdout, &stderr)

			// Assert
			tc.assertErr(t, err, "the error of run")
			require.Contains(t, stdout.String(), tc.wantStdout, "standard output")
			require.Contains(t, stderr.String(), tc.wantStderr, "standard error")
		})
	}
}
