package profile_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestLoad_BIOSRegistry(t *testing.T) {
	t.Parallel()

	header := "version: 1\nname: custom\nbios:\n  registry:\n"

	tests := []struct {
		name     string
		registry string

		assertErr require.ErrorAssertionFunc
	}{
		{
			name: "success",
			registry: `    id: TestRegistry.v1_0_0
    attributes:
      - {name: Mode, type: Enumeration, values: [A, B], default: B}
      - {name: Label, type: String, read_only: true}
      - {name: Threshold, type: Integer, min: 0, max: 10, default: 5}
      - {name: Turbo, type: Boolean, default: true}
      - {name: Secure, type: Enumeration, values: [On, Off], maps_to: security.secureboot, value_map: {On: "true", Off: "false"}}
`,

			assertErr: require.NoError,
		},
		{
			name:     "error - attributes without id",
			registry: "    attributes: [{name: Mode, type: String}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - malformed id",
			registry: "    id: TestRegistry\n",

			assertErr: require.Error,
		},
		{
			name:     "error - duplicate name",
			registry: "    id: T.v1_0_0\n    attributes: [{name: Mode, type: String}, {name: Mode, type: String}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - incus name",
			registry: "    id: T.v1_0_0\n    attributes: [{name: incus.config.foo, type: String}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - unknown type",
			registry: "    id: T.v1_0_0\n    attributes: [{name: Mode, type: Float}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - enumeration without values",
			registry: "    id: T.v1_0_0\n    attributes: [{name: Mode, type: Enumeration}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - default not in values",
			registry: "    id: T.v1_0_0\n    attributes: [{name: Mode, type: Enumeration, values: [A], default: B}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - integer default not an integer",
			registry: "    id: T.v1_0_0\n    attributes: [{name: N, type: Integer, default: x}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - min greater than max",
			registry: "    id: T.v1_0_0\n    attributes: [{name: N, type: Integer, min: 5, max: 1}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - negative min",
			registry: "    id: T.v1_0_0\n    attributes: [{name: N, type: Integer, min: -1}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - bounds on a string",
			registry: "    id: T.v1_0_0\n    attributes: [{name: N, type: String, max: 1}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - value map missing a value",
			registry: "    id: T.v1_0_0\n    attributes: [{name: S, type: Enumeration, values: [On, Off], maps_to: security.secureboot, value_map: {On: \"true\"}}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - value map not one to one",
			registry: "    id: T.v1_0_0\n    attributes: [{name: S, type: Enumeration, values: [On, Off], maps_to: security.secureboot, value_map: {On: \"true\", Off: \"true\"}}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - maps to proxy state",
			registry: "    id: T.v1_0_0\n    attributes: [{name: S, type: String, maps_to: user.redfish.bios.x}]\n",

			assertErr: require.Error,
		},
		{
			name:     "success - requires",
			registry: "    id: T.v1_0_0\n    attributes: [{name: M, type: Enumeration, values: [A, B]}, {name: O, type: Boolean, requires: {attribute: M, value: B}}]\n",

			assertErr: require.NoError,
		},
		{
			name:     "error - requires unknown attribute",
			registry: "    id: T.v1_0_0\n    attributes: [{name: O, type: Boolean, requires: {attribute: M, value: B}}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - chained requirement",
			registry: "    id: T.v1_0_0\n    attributes: [{name: M, type: Enumeration, values: [A, B]}, {name: O, type: Boolean, requires: {attribute: M, value: B}}, {name: P, type: String, requires: {attribute: O, value: true}}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - requires without value",
			registry: "    id: T.v1_0_0\n    attributes: [{name: M, type: Enumeration, values: [A, B]}, {name: O, type: Boolean, requires: {attribute: M}}]\n",

			assertErr: require.Error,
		},
		{
			name:     "error - requires invalid value",
			registry: "    id: T.v1_0_0\n    attributes: [{name: M, type: Enumeration, values: [A, B]}, {name: O, type: Boolean, requires: {attribute: M, value: C}}]\n",

			assertErr: require.Error,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), "profile.yaml")

			err := os.WriteFile(filename, []byte(header+tc.registry), 0o600)
			require.NoError(t, err, "write the profile file")

			_, err = profile.Load(filename)
			tc.assertErr(t, err, "load the profile")
		})
	}
}

func TestLoad_BIOS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		bios string

		assertErr require.ErrorAssertionFunc
	}{
		{name: "success - task monitor", bios: "{patch_response: 202-taskmonitor}", assertErr: require.NoError},
		{name: "success - task", bios: "{patch_response: 202-task, task_resource: true}", assertErr: require.NoError},
		{name: "success - no content", bios: "{patch_response: \"204\"}", assertErr: require.NoError},
		{name: "error - task without task resource", bios: "{patch_response: 202-task}", assertErr: require.Error},
		{name: "error - unknown", bios: "{patch_response: \"201\"}", assertErr: require.Error},
		{name: "success - pending", bios: "{settings_object: true, pending: true, apply_time: {supported: [OnReset, Immediate], default: OnReset}}", assertErr: require.NoError},
		{name: "success - default without supported", bios: "{apply_time: {default: Immediate}}", assertErr: require.NoError},
		{name: "error - pending without settings object", bios: "{pending: true}", assertErr: require.Error},
		{name: "error - on reset without pending", bios: "{settings_object: true, apply_time: {supported: [OnReset]}}", assertErr: require.Error},
		{name: "error - default on reset without pending", bios: "{apply_time: {default: OnReset}}", assertErr: require.Error},
		{name: "error - apply time not implemented", bios: "{apply_time: {supported: [AtMaintenanceWindowStart]}}", assertErr: require.Error},
		{name: "error - unknown apply time", bios: "{apply_time: {supported: [Later]}}", assertErr: require.Error},
		{name: "error - default not supported", bios: "{settings_object: true, pending: true, apply_time: {supported: [OnReset], default: Immediate}}", assertErr: require.Error},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), "profile.yaml")

			err := os.WriteFile(filename, []byte("version: 1\nname: custom\nbios: "+tc.bios+"\n"), 0o600)
			require.NoError(t, err, "write the profile file")

			_, err = profile.Load(filename)
			tc.assertErr(t, err, "load the profile")
		})
	}
}

func TestLoad_ResolvesBIOS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		bios string

		wantApplyTime  profile.ApplyTime
		wantAttributes []profile.Attribute
	}{
		{
			name: "immediate without pending",
			bios: "{}",

			wantApplyTime:  profile.ApplyTimeImmediate,
			wantAttributes: []profile.Attribute{},
		},
		{
			name: "on reset with pending",
			bios: "{settings_object: true, pending: true}",

			wantApplyTime:  profile.ApplyTimeOnReset,
			wantAttributes: []profile.Attribute{},
		},
		{
			name: "explicit default stays",
			bios: "{settings_object: true, pending: true, apply_time: {default: Immediate}}",

			wantApplyTime:  profile.ApplyTimeImmediate,
			wantAttributes: []profile.Attribute{},
		},
		{
			name: "integer requirement",
			bios: "{registry: {id: T.v1_0_0, attributes: [{name: N, type: Integer}, {name: O, type: Boolean, requires: {attribute: N, value: 3}}]}}",

			wantApplyTime: profile.ApplyTimeImmediate,
			wantAttributes: []profile.Attribute{
				{Name: "N", Type: profile.AttributeTypeInteger},
				{Name: "O", Type: profile.AttributeTypeBoolean, Requires: &profile.Requirement{Attribute: "N", Value: int64(3)}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			filename := filepath.Join(t.TempDir(), "profile.yaml")

			err := os.WriteFile(filename, []byte("version: 1\nname: custom\nbios: "+tc.bios+"\n"), 0o600)
			require.NoError(t, err, "write the profile file")

			// Run test
			p, err := profile.Load(filename)

			// Assert
			require.NoError(t, err, "load the profile")
			require.Equal(t, tc.wantApplyTime, p.BIOS.ApplyTime.Default, "the empty default apply time is resolved")
			require.Equal(t, tc.wantAttributes, p.BIOS.Registry.Attributes, "a required value has the type of the stored values")
		})
	}
}

func TestLoad_SecureBoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		secureBoot string

		assertErr require.ErrorAssertionFunc
	}{
		{name: "success - read only with reset keys", secureBoot: "{database_write: false, reset_keys: {supported: true, types: [DeletePK], response: 202-task}}", assertErr: require.NoError},
		{name: "success - sequential with prefix", secureBoot: "{certificate_id_style: sequential, certificate_id_prefix: Certificate}", assertErr: require.NoError},
		{name: "error - reset to default keys", secureBoot: "{reset_keys: {types: [ResetAllKeysToDefault]}}", assertErr: require.Error},
		{name: "error - unknown reset keys type", secureBoot: "{reset_keys: {types: [DeleteKEK]}}", assertErr: require.Error},
		{name: "error - reset keys without types", secureBoot: "{reset_keys: {supported: true, types: []}}", assertErr: require.Error},
		{name: "error - unknown reset keys response", secureBoot: "{reset_keys: {response: \"200\"}}", assertErr: require.Error},
		{name: "error - prefix with hash", secureBoot: "{certificate_id_prefix: Certificate}", assertErr: require.Error},
		{name: "error - prefix not usable in a URI", secureBoot: "{certificate_id_style: sequential, certificate_id_prefix: a/b}", assertErr: require.Error},
		{name: "error - unknown certificate id style", secureBoot: "{certificate_id_style: uuid}", assertErr: require.Error},
		{name: "error - database write error not an error", secureBoot: "{database_write_error: {status: 200, message_id: Base.1.18.ActionNotSupported}}", assertErr: require.Error},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), "profile.yaml")

			err := os.WriteFile(filename, []byte("version: 1\nname: custom\nsecure_boot: "+tc.secureBoot+"\n"), 0o600)
			require.NoError(t, err, "write the profile file")

			_, err = profile.Load(filename)
			tc.assertErr(t, err, "load the profile")
		})
	}
}

func TestLoad_VirtualMedia(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		virtualMedia string

		assertErr require.ErrorAssertionFunc
	}{
		{name: "success - oem with task", virtualMedia: "{insert: oem, oem_vendor: Test, oem_action: InsertVirtualMedia, insert_response: 202-task}", assertErr: require.NoError},
		{name: "success - patch", virtualMedia: "{insert: patch}", assertErr: require.NoError},
		{name: "error - unknown insert", virtualMedia: "{insert: upload}", assertErr: require.Error},
		{name: "error - oem vendor not usable in a URI", virtualMedia: "{insert: oem, oem_vendor: a/b}", assertErr: require.Error},
		{name: "error - empty oem action", virtualMedia: "{insert: oem, oem_action: \"\"}", assertErr: require.Error},
		{name: "error - unknown insert response", virtualMedia: "{insert_response: \"200\"}", assertErr: require.Error},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), "profile.yaml")

			err := os.WriteFile(filename, []byte("version: 1\nname: custom\nvirtual_media: "+tc.virtualMedia+"\n"), 0o600)
			require.NoError(t, err, "write the profile file")

			_, err = profile.Load(filename)
			tc.assertErr(t, err, "load the profile")
		})
	}
}

func TestLoad_Power(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sections string

		assertErr      require.ErrorAssertionFunc
		wantWindow     time.Duration
		wantScaled     time.Duration
		wantResetTypes []profile.ResetType
		wantWarnings   int
	}{
		{
			name:     "success - defaults",
			sections: "",

			assertErr:      require.NoError,
			wantResetTypes: []profile.ResetType{},
		},
		{
			name:     "success - window scaled",
			sections: "power: {reset_types: [On, ForceOff], response: 202-task, not_settled_window: 20s, delay: 1m}\ntiming: {scale: 0.1}",

			assertErr:      require.NoError,
			wantWindow:     20 * time.Second,
			wantScaled:     2 * time.Second,
			wantResetTypes: []profile.ResetType{profile.ResetTypeOn, profile.ResetTypeForceOff},
		},
		{
			name:     "success - error OC does not recognize",
			sections: "power: {not_settled_window: 5s, not_settled_error: {status: 409, message_id: Base.1.18.ResourceInUse}}",

			assertErr:      require.NoError,
			wantWindow:     5 * time.Second,
			wantScaled:     5 * time.Second,
			wantResetTypes: []profile.ResetType{},
			wantWarnings:   1,
		},
		{name: "error - unknown reset type", sections: "power: {reset_types: [Nmi]}", assertErr: require.Error},
		{name: "error - unknown response", sections: "power: {response: \"200\"}", assertErr: require.Error},
		{name: "error - duration without unit", sections: "power: {delay: 5}", assertErr: require.Error},
		{name: "error - negative window", sections: "power: {not_settled_window: -1s}", assertErr: require.Error},
		{name: "error - negative delay", sections: "power: {delay: -1s}", assertErr: require.Error},
		{name: "error - malformed error", sections: "power: {not_settled_error: {status: 200, message_id: Base.1.18.ResourceInStandby}}", assertErr: require.Error},
		{name: "error - zero scale", sections: "timing: {scale: 0}", assertErr: require.Error},
		{name: "error - NaN scale", sections: "timing: {scale: .nan}", assertErr: require.Error},
		{name: "error - infinite scale", sections: "timing: {scale: .inf}", assertErr: require.Error},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), "profile.yaml")

			err := os.WriteFile(filename, []byte("version: 1\nname: custom\n"+tc.sections+"\n"), 0o600)
			require.NoError(t, err, "write the profile file")

			p, err := profile.Load(filename)
			tc.assertErr(t, err, "load the profile")

			require.Equal(t, tc.wantWindow, p.Power.NotSettledWindow, "the not settled window")
			require.Equal(t, tc.wantScaled, p.Scaled(p.Power.NotSettledWindow), "the window is multiplied by timing.scale")
			require.Equal(t, tc.wantResetTypes, p.Power.ResetTypes, "the supported reset types")
			require.Len(t, p.Warnings(), tc.wantWarnings, "the number of warnings")
		})
	}
}

func TestLoad_Overlay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		overlay string

		assertErr require.ErrorAssertionFunc
		want      profile.Overlay
	}{
		{
			name:    "success",
			overlay: `{"/redfish/v1/Systems/{system}": {Oem: {Test: {Ready: true, Slots: [{Size: 1}]}}, AssetTag: null}}`,

			assertErr: require.NoError,
			want: profile.Overlay{"/redfish/v1/Systems/{system}": {
				"Oem":      map[string]any{"Test": map[string]any{"Ready": true, "Slots": []any{map[string]any{"Size": 1}}}},
				"AssetTag": nil,
			}},
		},
		{name: "error - path outside of the service", overlay: `{"/redfish/v2": {AssetTag: x}}`, assertErr: require.Error},
		{name: "error - unknown placeholder", overlay: `{"/redfish/v1/Chassis/{chassis}": {AssetTag: x}}`, assertErr: require.Error},
		{name: "error - Id changed", overlay: `{"/redfish/v1/Systems/{system}": {Id: other}}`, assertErr: require.Error},
		{name: "error - nested @odata.id changed", overlay: `{"/redfish/v1/Systems/{system}": {Bios: {"@odata.id": /other}}}`, assertErr: require.Error},
		{name: "error - @odata.id in a list changed", overlay: `{"/redfish/v1/Systems/{system}": {Links: {Chassis: [{"@odata.id": /other}]}}}`, assertErr: require.Error},
		{name: "error - paths differ only in a trailing slash", overlay: `{"/redfish/v1/Systems": {Name: a}, "/redfish/v1/Systems/": {Name: b}}`, assertErr: require.Error},
		{name: "error - key not a string", overlay: `{"/redfish/v1/Systems/{system}": {Oem: {1: x}}}`, assertErr: require.Error},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), "profile.yaml")

			err := os.WriteFile(filename, []byte("version: 1\nname: custom\noverlay: "+tc.overlay+"\n"), 0o600)
			require.NoError(t, err, "write the profile file")

			p, err := profile.Load(filename)
			tc.assertErr(t, err, "load the profile")

			require.Equal(t, tc.want, p.Overlay, "the overlay is parsed")
		})
	}
}

func TestLoad_Delivery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		virtualMedia string

		assertErr require.ErrorAssertionFunc
		want      profile.Stream
	}{
		{
			name:         "success - units",
			virtualMedia: "{delivery: prefetch, stream: {read_ahead: 512KiB, cache: 1GiB, fallback: error}}",

			assertErr: require.NoError,
			want:      profile.Stream{ReadAhead: 512 << 10, Cache: 1 << 30, Fallback: profile.StreamFallbackError},
		},
		{
			name:         "success - plain bytes",
			virtualMedia: "{delivery: stream, stream: {read_ahead: 4096, cache: 4MiB}}",

			assertErr: require.NoError,
			want:      profile.Stream{ReadAhead: 4096, Cache: 4 << 20, Fallback: profile.StreamFallbackFullFetch},
		},
		{name: "error - unknown delivery", virtualMedia: "{delivery: nbd}", assertErr: require.Error},
		{name: "error - unknown unit", virtualMedia: "{stream: {read_ahead: 4MB}}", assertErr: require.Error},
		{name: "error - zero size", virtualMedia: "{stream: {read_ahead: 0}}", assertErr: require.Error},
		{name: "error - read ahead below the minimum", virtualMedia: "{stream: {read_ahead: 4095}}", assertErr: require.Error},
		{name: "error - size overflows", virtualMedia: "{stream: {cache: 9007199254740993GiB}}", assertErr: require.Error},
		{name: "error - read ahead above the cache", virtualMedia: "{stream: {read_ahead: 128MiB}}", assertErr: require.Error},
		{name: "error - unknown fallback", virtualMedia: "{stream: {fallback: retry}}", assertErr: require.Error},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			filename := filepath.Join(t.TempDir(), "profile.yaml")

			err := os.WriteFile(filename, []byte("version: 1\nname: custom\nvirtual_media: "+tc.virtualMedia+"\n"), 0o600)
			require.NoError(t, err, "write the profile file")

			p, err := profile.Load(filename)
			tc.assertErr(t, err, "load the profile")

			require.Equal(t, tc.want, p.VirtualMedia.Stream, "the stream settings are parsed")
		})
	}
}

func TestErrorShape_Validate(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

			tc.assertErr(t, tc.shape.Validate(), "validate the error shape")
		})
	}
}
