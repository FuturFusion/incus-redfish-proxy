package recording

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMediaLocations(t *testing.T) {
	t.Parallel()

	resource := func(seq int64, path string, media string) Exchange {
		body := `{}`
		if media != "" {
			body = `{"VirtualMedia": {"@odata.id": "` + media + `"}}`
		}

		return Exchange{Sequence: seq, Method: "GET", URL: "https://bmc.example.com" + path, Status: 200, ResponseBody: body}
	}

	insert := func(seq int64, path string) Exchange {
		return Exchange{
			Sequence: seq, Method: "POST", URL: "https://bmc.example.com" + path + "/Actions/VirtualMedia.InsertMedia",
			RequestBody: `{"Image": "https://images.example/boot.iso"}`, Status: 204,
		}
	}

	const noInsert = "Operations Center inserted no media"

	tests := []struct {
		name      string
		exchanges []Exchange

		wantLocations value
		wantID        value
	}{
		{
			name: "manager only like iLO",
			exchanges: []Exchange{
				resource(1, "/redfish/v1/Systems/1/", ""), resource(2, "/redfish/v1/Managers/1/", "/redfish/v1/Managers/1/VirtualMedia/"),
				insert(3, "/redfish/v1/Managers/1/VirtualMedia/2"),
			},
			wantLocations: value{yaml: "[managers]", seqs: []int64{3, 2}},
			wantID:        value{yaml: `"2"`, seqs: []int64{3}},
		},
		{
			name: "both, the insert below the system first like iDRAC",
			exchanges: []Exchange{
				resource(1, "/redfish/v1/Systems/System.Embedded.1", "/redfish/v1/Systems/System.Embedded.1/VirtualMedia"),
				resource(2, "/redfish/v1/Managers/iDRAC.Embedded.1", "/redfish/v1/Managers/iDRAC.Embedded.1/VirtualMedia"),
				insert(3, "/redfish/v1/Systems/System.Embedded.1/VirtualMedia/1"),
			},
			wantLocations: value{yaml: "[systems, managers]", seqs: []int64{3, 1, 2}},
			wantID:        value{yaml: `"1"`, seqs: []int64{3}},
		},
		{
			name: "both, the insert below the manager first",
			exchanges: []Exchange{
				resource(1, "/redfish/v1/Systems/1", "/redfish/v1/Systems/1/VirtualMedia"),
				resource(2, "/redfish/v1/Managers/1", "/redfish/v1/Managers/1/VirtualMedia"),
				insert(3, "/redfish/v1/Managers/1/VirtualMedia/EXT1"),
			},
			wantLocations: value{yaml: "[managers, systems]", seqs: []int64{3, 1, 2}},
			wantID:        value{yaml: `"EXT1"`, seqs: []int64{3}},
		},
		{
			name: "no insert",
			exchanges: []Exchange{
				resource(1, "/redfish/v1/Systems/1", "/redfish/v1/Systems/1/VirtualMedia"),
				resource(2, "/redfish/v1/Managers/1", ""),
			},
			wantLocations: value{yaml: "[systems]", seqs: []int64{1}},
			wantID:        value{yaml: `"CD"`, reason: noInsert},
		},
		{
			name:          "manager not read",
			exchanges:     []Exchange{resource(1, "/redfish/v1/Systems/1", "/redfish/v1/Systems/1/VirtualMedia")},
			wantLocations: value{yaml: "[managers]", reason: "the system and the manager were not read"},
			wantID:        value{yaml: `"CD"`, reason: noInsert},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.virtualMedia(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.wantLocations, d.values["virtual_media.locations"], "virtual_media.locations")
			require.Equal(t, tc.wantID, d.values["virtual_media.id"], "virtual_media.id")
		})
	}
}

func TestMediaTypes(t *testing.T) {
	t.Parallel()

	mediaAt := func(seq int64, id string, mediaTypes string) Exchange {
		return Exchange{
			Sequence: seq, Method: "GET", URL: "https://bmc.example.com/redfish/v1/Systems/1/VirtualMedia/" + id,
			Status: 200, ResponseBody: `{"MediaTypes": ` + mediaTypes + `}`,
		}
	}

	insert := Exchange{
		Sequence: 3, Method: "PATCH", URL: "https://bmc.example.com/redfish/v1/Systems/1/VirtualMedia/EXT1",
		RequestBody: `{"Image": "https://example.com/image.raw", "Inserted": true}`, Status: 200,
	}

	tests := []struct {
		name      string
		exchanges []Exchange
		inserted  bool

		want value
	}{
		{
			name:      "no read",
			exchanges: []Exchange{},
			want:      value{yaml: "[CD, DVD]", reason: "no read of a virtual media device shows its media types"},
		},
		{
			name:      "the inserted device like XCC",
			exchanges: []Exchange{mediaAt(1, "RDOC1", `["CD", "DVD"]`), mediaAt(2, "EXT1", `["CD", "DVD", "Floppy", "USBStick"]`), insert},
			inserted:  true,
			want:      value{yaml: "[CD, DVD, USBStick]", seqs: []int64{2}, note: "dropped Floppy, the proxy knows only CD, DVD and USBStick"},
		},
		{
			name:      "the first device without an insert",
			exchanges: []Exchange{mediaAt(1, "1", `["Floppy", "USBStick"]`), mediaAt(2, "2", `["CD", "DVD"]`)},
			want:      value{yaml: "[USBStick]", seqs: []int64{1}, note: "dropped Floppy, the proxy knows only CD, DVD and USBStick"},
		},
		{
			name:      "no known media type",
			exchanges: []Exchange{mediaAt(1, "1", `["Floppy"]`)},
			want:      value{yaml: "[CD, DVD]", reason: "the device offers no media type that the proxy knows"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.mediaTypes(&Recording{Exchanges: tc.exchanges}, insert, tc.inserted)

			// Assert
			require.Equal(t, tc.want, d.values["virtual_media.media_types"], "virtual_media.media_types")
		})
	}
}
