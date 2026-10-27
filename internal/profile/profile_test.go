package profile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

func TestLoad_Embedded(t *testing.T) {
	t.Parallel()

	names := profile.List()
	require.Contains(t, names, profile.DefaultName, "the default profile is embedded")

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := profile.Load(name)
			require.NoError(t, err, "load the embedded profile")
			require.Equal(t, name, p.Name, "the profile name matches the embedded name")
		})
	}
}

func TestLoad_Default(t *testing.T) {
	t.Parallel()

	generic, err := profile.Load(profile.DefaultName)
	require.NoError(t, err, "load the default profile by name")

	empty, err := profile.Load("")
	require.NoError(t, err, "load the profile with an empty name")

	require.Equal(t, profile.Default(), generic, "the default name loads the default profile")
	require.Equal(t, profile.Default(), empty, "an empty name loads the default profile")
}

func TestLoad_UnknownEmbedded(t *testing.T) {
	t.Parallel()

	_, err := profile.Load("does-not-exist")
	require.ErrorContains(t, err, profile.DefaultName, "the error lists the known profiles")
}

func TestLoad_File(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string

		assertErr require.ErrorAssertionFunc
	}{
		{
			name:    "success",
			content: "version: 1\nname: custom\ndescription: A custom profile.\n",

			assertErr: require.NoError,
		},
		{
			name:    "error - empty",
			content: "",

			assertErr: require.Error,
		},
		{
			name:    "error - unknown key",
			content: "version: 1\nname: custom\nunknown: true\n",

			assertErr: require.Error,
		},
		{
			name:    "error - wrong version",
			content: "version: 2\nname: custom\n",

			assertErr: require.Error,
		},
		{
			name:    "error - invalid name",
			content: "version: 1\nname: Custom Profile\n",

			assertErr: require.Error,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), "profile.yaml")

			err := os.WriteFile(filename, []byte(tc.content), 0o600)
			require.NoError(t, err, "write the profile file")

			_, err = profile.Load(filename)
			tc.assertErr(t, err, "load the profile")
		})
	}
}

func TestErrorShape_Validate(t *testing.T) {
	tests := []struct {
		name  string
		shape profile.ErrorShape

		assertErr require.ErrorAssertionFunc
	}{
		{
			name:  "success",
			shape: profile.ErrorShape{Status: 400, MessageID: "Base.1.18.PropertyUnknown"},

			assertErr: require.NoError,
		},
		{
			name:  "error - status not an error",
			shape: profile.ErrorShape{Status: 200, MessageID: "Base.1.18.PropertyUnknown"},

			assertErr: require.Error,
		},
		{
			name:  "error - malformed message ID",
			shape: profile.ErrorShape{Status: 400, MessageID: "PropertyUnknown"},

			assertErr: require.Error,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.assertErr(t, tc.shape.Validate())
		})
	}
}
