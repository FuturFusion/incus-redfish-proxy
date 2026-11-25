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

		assertErr    require.ErrorAssertionFunc
		assertStdout func(t *testing.T, got string)
		assertStderr func(t *testing.T, got string)
	}{
		{
			name:         "help",
			args:         []string{"--help"},
			assertErr:    require.NoError,
			assertStdout: contains("Usage: incus-redfish-proxy [flags] <instance-name>"),
			assertStderr: empty,
		},
		{
			name:         "help shorthand",
			args:         []string{"-h"},
			assertErr:    require.NoError,
			assertStdout: contains("Usage: incus-redfish-proxy [flags] <instance-name>"),
			assertStderr: empty,
		},
		{
			name:         "list profiles",
			args:         []string{"--list-profiles"},
			assertErr:    require.NoError,
			assertStdout: contains("generic\t"),
			assertStderr: empty,
		},
		{
			name: "missing instance name",
			args: []string{},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "missing argument: instance-name", a...)
			},
			assertStdout: empty,
			assertStderr: contains("Usage: incus-redfish-proxy"),
		},
		{
			name: "empty instance name",
			args: []string{""},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "instance name must not be empty", a...)
			},
			assertStdout: empty,
			assertStderr: empty,
		},
		{
			name: "unknown flag",
			args: []string{"--unknown"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "unknown flag", a...)
			},
			assertStdout: empty,
			assertStderr: contains("Usage: incus-redfish-proxy"),
		},
		{
			name: "invalid timing scale",
			args: []string{"--timing-scale", "abc", "instance"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "invalid argument", a...)
			},
			assertStdout: empty,
			assertStderr: contains("Usage: incus-redfish-proxy"),
		},
		{
			name: "negative timing scale",
			args: []string{"--timing-scale", "-1", "instance"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "timing.scale -1 must be a finite number greater than 0", a...)
			},
			assertStdout: empty,
			assertStderr: empty,
		},
		{
			name: "unknown profile",
			args: []string{"--profile", "unknown", "instance"},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "load profile", a...)
			},
			assertStdout: empty,
			assertStderr: empty,
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
			tc.assertStdout(t, stdout.String())
			tc.assertStderr(t, stderr.String())
		})
	}
}

func empty(t *testing.T, got string) {
	t.Helper()

	require.Empty(t, got, "output stream")
}

func contains(want string) func(t *testing.T, got string) {
	return func(t *testing.T, got string) {
		t.Helper()

		require.Contains(t, got, want, "output stream")
	}
}
