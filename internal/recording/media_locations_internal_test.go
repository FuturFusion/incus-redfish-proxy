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
