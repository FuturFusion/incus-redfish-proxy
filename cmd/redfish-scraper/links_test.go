package main

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractODataIDs(t *testing.T) {
	tests := []struct {
		name string
		v    any

		want []string
	}{
		{
			name: "no odata id",
			v:    map[string]any{"Name": "foo"},

			want: nil,
		},
		{
			name: "top level odata id",
			v:    map[string]any{"@odata.id": "/redfish/v1/Systems/1"},

			want: []string{"/redfish/v1/Systems/1"},
		},
		{
			name: "fragment is stripped",
			v:    map[string]any{"@odata.id": "/redfish/v1/Systems/1#/Foo"},

			want: []string{"/redfish/v1/Systems/1"},
		},
		{
			name: "nested in Links, Actions, Oem and Attributes",
			v: map[string]any{
				"Links": map[string]any{
					"Chassis": []any{
						map[string]any{"@odata.id": "/redfish/v1/Chassis/1"},
					},
				},
				"Actions": map[string]any{
					"#ComputerSystem.Reset": map[string]any{
						"target": "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
					},
				},
				"Oem": map[string]any{
					"Vendor": map[string]any{"@odata.id": "/redfish/v1/Oem/Vendor"},
				},
				"Attributes": map[string]any{
					"@odata.id": "/redfish/v1/Systems/1/Bios",
				},
			},

			want: []string{
				"/redfish/v1/Chassis/1",
				"/redfish/v1/Oem/Vendor",
				"/redfish/v1/Systems/1/Bios",
			},
		},
		{
			name: "collection members",
			v: map[string]any{
				"Members": []any{
					map[string]any{"@odata.id": "/redfish/v1/Systems/1"},
					map[string]any{"@odata.id": "/redfish/v1/Systems/2"},
				},
			},

			want: []string{
				"/redfish/v1/Systems/1",
				"/redfish/v1/Systems/2",
			},
		},
		{
			name: "non-string odata id is ignored",
			v:    map[string]any{"@odata.id": 42},

			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractODataIDs(tc.v)

			require.ElementsMatch(t, tc.want, got)
		})
	}
}

func TestParseLinkHeader(t *testing.T) {
	tests := []struct {
		name   string
		values []string

		want []string
	}{
		{
			name:   "no headers",
			values: nil,

			want: nil,
		},
		{
			name:   "single entry",
			values: []string{"</redfish/v1/Systems/Self/NetworkInterfaces>; path=/NetworkInterfaces"},

			want: []string{"/redfish/v1/Systems/Self/NetworkInterfaces"},
		},
		{
			name:   "multiple comma separated entries in one header",
			values: []string{"</a>; rel=foo, </b>; rel=bar"},

			want: []string{"/a", "/b"},
		},
		{
			name:   "multiple headers",
			values: []string{"</a>; rel=foo", "</b>; rel=bar"},

			want: []string{"/a", "/b"},
		},
		{
			name:   "malformed entry is skipped",
			values: []string{"not-a-link, </b>; rel=bar"},

			want: []string{"/b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLinkHeader(tc.values)

			require.Equal(t, tc.want, got)
		})
	}
}

func TestSameEndpoint(t *testing.T) {
	base, err := url.Parse("https://bmc.example.com:8443")
	require.NoError(t, err)

	tests := []struct {
		name string
		raw  string

		wantPath string
		wantOK   bool
	}{
		{
			name: "relative path",
			raw:  "/redfish/v1/Systems/1",

			wantPath: "/redfish/v1/Systems/1",
			wantOK:   true,
		},
		{
			name: "absolute url same host",
			raw:  "https://bmc.example.com:8443/redfish/v1/Systems/1",

			wantPath: "/redfish/v1/Systems/1",
			wantOK:   true,
		},
		{
			name: "absolute url different host",
			raw:  "https://other.example.com/redfish/v1/Systems/1",

			wantPath: "",
			wantOK:   false,
		},
		{
			name: "invalid url",
			raw:  "://not-a-url",

			wantPath: "",
			wantOK:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotPath, gotOK := sameEndpoint(base, tc.raw)

			require.Equal(t, tc.wantOK, gotOK)
			require.Equal(t, tc.wantPath, gotPath)
		})
	}
}

func TestResourceDir(t *testing.T) {
	tests := []struct {
		name   string
		outDir string
		path   string

		want      string
		assertErr require.ErrorAssertionFunc
	}{
		{
			name:   "simple path",
			outDir: "/tmp/out",
			path:   "/redfish/v1/Systems/1",

			want:      "/tmp/out/redfish/v1/Systems/1",
			assertErr: require.NoError,
		},
		{
			name:   "trailing slash",
			outDir: "/tmp/out",
			path:   "/redfish/v1/",

			want:      "/tmp/out/redfish/v1",
			assertErr: require.NoError,
		},
		{
			name:   "path traversal is rejected",
			outDir: "/tmp/out",
			path:   "/redfish/v1/../../../../etc/cron.d/evil",

			want:      "",
			assertErr: require.Error,
		},
		{
			name:   "embedded dot-dot segment is rejected",
			outDir: "/tmp/out",
			path:   "/redfish/v1/Systems/../../../etc/passwd",

			want:      "",
			assertErr: require.Error,
		},
		{
			name:   "empty segment is rejected",
			outDir: "/tmp/out",
			path:   "/redfish/v1//Systems",

			want:      "",
			assertErr: require.Error,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resourceDir(tc.outDir, tc.path)

			tc.assertErr(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
