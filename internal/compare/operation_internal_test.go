package compare

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

func TestOperations(t *testing.T) {
	t.Parallel()

	system := `{"@odata.type": "#ComputerSystem.v1_13_0.ComputerSystem"}`

	tests := []struct {
		name      string
		exchanges []recording.Exchange

		want []Operation
	}{
		{
			name: "system URIs of all vendors",
			exchanges: []recording.Exchange{
				{Method: "GET", URL: "https://bmc/redfish/v1/Systems/System.Embedded.1", Status: 200, ResponseBody: system},
				{Method: "GET", URL: "https://bmc/redfish/v1/Systems/1/", Status: 200, ResponseBody: system},
				{Method: "GET", URL: "https://bmc/redfish/v1/Systems/instance-a", Status: 200, ResponseBody: system},
			},
			want: []Operation{
				{Method: "GET", Kind: "ComputerSystem", Outcome: "200"},
				{Method: "GET", Kind: "ComputerSystem", Outcome: "200"},
				{Method: "GET", Kind: "ComputerSystem", Outcome: "200"},
			},
		},
		{
			name: "actions and OEM actions",
			exchanges: []recording.Exchange{
				{Method: "POST", URL: "https://bmc/redfish/v1/Managers/1/VirtualMedia/2/Actions/VirtualMedia.InsertMedia/", Status: 204},
				{Method: "POST", URL: "https://bmc/redfish/v1/Managers/1/VirtualMedia/2/Actions/Oem/Hpe/HpeiLOVirtualMedia.InsertVirtualMedia/", Status: 200},
				{Method: "POST", URL: "https://bmc/redfish/v1/Managers/m/VirtualMedia/cd/Actions/Oem/Incus.InsertVirtualMedia", Status: 204},
			},
			want: []Operation{
				{Method: "POST", Kind: "VirtualMedia.InsertMedia", Outcome: "204"},
				{Method: "POST", Kind: "Oem.InsertVirtualMedia", Outcome: "200"},
				{Method: "POST", Kind: "Oem.InsertVirtualMedia", Outcome: "204"},
			},
		},
		{
			name: "type of a PATCH and a 204 answer from the GET",
			exchanges: []recording.Exchange{
				{Method: "PATCH", URL: "https://bmc/redfish/v1/Systems/1/Bios/Settings", Status: 204},
				{Method: "GET", URL: "https://bmc/redfish/v1/Systems/1/Bios/Settings/", Status: 200, ResponseBody: `{"@odata.type": "#Bios.v1_2_0.Bios"}`},
			},
			want: []Operation{
				{Method: "PATCH", Kind: "Bios", Outcome: "204"},
				{Method: "GET", Kind: "Bios", Outcome: "200"},
			},
		},
		{
			name: "path template without a type",
			exchanges: []recording.Exchange{
				{Method: "GET", URL: "https://bmc/redfish/v1/Systems/System.Embedded.1/SecureBoot/SecureBootDatabases/KEK/Certificates/CustSecbootpolicy.2", Status: 404},
				{Method: "GET", URL: "https://bmc/redfish/v1/TaskService/Tasks/JID_942490679844", Status: 200, ResponseTruncated: true, ResponseBody: "{"},
			},
			want: []Operation{
				{Method: "GET", Kind: "/redfish/v1/systems/{id}/secureboot/securebootdatabases/kek/certificates/{id}", Outcome: "404"},
				{Method: "GET", Kind: "/redfish/v1/taskservice/tasks/{id}", Outcome: "200"},
			},
		},
		{
			name: "message IDs without version",
			exchanges: []recording.Exchange{
				{Method: "GET", URL: "https://bmc/redfish/v1/Systems/1/Bios", Status: 503, ResponseBody: `{"error": {"code": "Base.1.8.GeneralError", "@Message.ExtendedInfo": [{"MessageId": "IDRAC.2.8.SYS518"}]}}`},
				{Method: "PATCH", URL: "https://bmc/redfish/v1/Systems/1", Status: 200, ResponseBody: `{"@Message.ExtendedInfo": [{"MessageId": "Base.1.18.Success"}]}`},
				{Method: "POST", URL: "https://bmc/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", Status: 400, ResponseBody: `{"error": {"code": "iLO.0.10.InvalidOperationForSystemState"}}`},
			},
			want: []Operation{
				{Method: "GET", Kind: "/redfish/v1/systems/{id}/bios", Outcome: "503 IDRAC.SYS518"},
				{Method: "PATCH", Kind: "/redfish/v1/systems/{id}", Outcome: "200 Base.Success"},
				{Method: "POST", Kind: "ComputerSystem.Reset", Outcome: "400 iLO.InvalidOperationForSystemState"},
			},
		},
		{
			name: "task monitors of all vendors",
			exchanges: []recording.Exchange{
				{Method: "PATCH", URL: "https://bmc/redfish/v1/Systems/System.Embedded.1/Bios/Settings", Status: 202, ResponseHeaders: map[string][]string{"Location": {"/redfish/v1/TaskService/Tasks/JID_942490679844"}}},
				{Method: "GET", URL: "https://bmc/redfish/v1/TaskService/Tasks/JID_942490679844", Status: 200, ResponseBody: `{"@odata.type": "#Task.v1_4_3.Task"}`},
				{Method: "PATCH", URL: "https://bmc/redfish/v1/Systems/server01/Bios/Settings", Status: 202, ResponseHeaders: map[string][]string{"Location": {"https://bmc/redfish/v1/TaskMonitors/1/"}}},
				{Method: "GET", URL: "https://bmc/redfish/v1/TaskMonitors/1", Status: 202},
			},
			want: []Operation{
				{Method: "PATCH", Kind: "/redfish/v1/systems/{id}/bios/settings", Outcome: "202"},
				{Method: "GET", Kind: taskMonitorKind, Outcome: "200"},
				{Method: "PATCH", Kind: "/redfish/v1/systems/{id}/bios/settings", Outcome: "202"},
				{Method: "GET", Kind: taskMonitorKind, Outcome: "202"},
			},
		},
		{
			name: "transport errors",
			exchanges: []recording.Exchange{
				{Method: "GET", URL: "https://bmc/redfish/v1/", TransportError: "context deadline exceeded"},
				{Method: "GET", URL: "https://bmc/redfish/v1/"},
			},
			want: []Operation{
				{Method: "GET", Kind: "/redfish/v1", Outcome: transportError},
				{Method: "GET", Kind: "/redfish/v1", Outcome: transportError},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			rec := &recording.Recording{Exchanges: tc.exchanges}

			// Run test
			got := operations(rec)

			// Assert
			require.Equal(t, tc.want, got, "operations")
		})
	}
}

func TestRuns(t *testing.T) {
	t.Parallel()

	// Setup
	poll := Operation{State: "wait", Method: "GET", Kind: "ComputerSystem", Outcome: "200"}
	reset := Operation{State: "wait", Method: "POST", Kind: "ComputerSystem.Reset", Outcome: "204"}

	// Run test
	got := runs([]Operation{poll, poll, poll, reset, poll})

	// Assert
	require.Equal(t, []Run{{Operation: poll, Count: 3}, {Operation: reset, Count: 1}, {Operation: poll, Count: 1}}, got, "consecutive equal operations collapse")
	require.Equal(t, "GET ComputerSystem 200 ×3", got[0].String(), "a poll names its count")
	require.Equal(t, "POST ComputerSystem.Reset 204", got[1].String(), "a single operation has no count")
}

func TestAlign(t *testing.T) {
	t.Parallel()

	// Setup
	equal := func(a, b string) bool { return a == b }

	// Run test
	got := align([]string{"a", "b", "c", "d"}, []string{"a", "c", "e"}, equal)

	// Assert
	require.Equal(t, []match{{0, 0}, {1, -1}, {2, 1}, {3, -1}, {-1, 2}}, got, "longest common subsequence a, c")
}
