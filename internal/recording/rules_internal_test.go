package recording

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		exchange Exchange

		want value
	}{
		{
			name:     "no content",
			exchange: Exchange{Sequence: 1, Status: 204},
			want:     value{yaml: `"204"`, seqs: []int64{1}},
		},
		{
			name:     "accepted",
			exchange: Exchange{Sequence: 2, Status: 202},
			want:     value{yaml: `"202-task"`, seqs: []int64{2}},
		},
		{
			name:     "ok with a message",
			exchange: Exchange{Sequence: 3, Status: 200, ResponseBody: `{"@Message.ExtendedInfo": [{"MessageId": "ExtendedError.1.2.RebootRequired"}]}`},
			want:     value{yaml: `"200"`, seqs: []int64{3}, note: "ExtendedError.1.2.RebootRequired"},
		},
		{
			name:     "ok with a message wrapped in an error",
			exchange: Exchange{Sequence: 5, Status: 200, ResponseBody: `{"error": {"code": "iLO.0.10.ExtendedInfo", "@Message.ExtendedInfo": [{"MessageId": "Base.1.18.Success"}]}}`},
			want:     value{yaml: `"200-error"`, seqs: []int64{5}, note: "Base.1.18.Success"},
		},
		{
			name:     "ok without a body",
			exchange: Exchange{Sequence: 4, Status: 200},
			want:     value{yaml: `"200"`, seqs: []int64{4}},
		},
		{
			name:     "created",
			exchange: Exchange{Sequence: 6, Status: 201, ResponseBody: `{"@Message.ExtendedInfo": [{"MessageId": "Base.1.18.Created"}]}`},
			want:     value{yaml: `"204"`, seqs: []int64{6}, verify: true, note: "answered 201, Base.1.18.Created"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := response(tc.exchange)

			require.Equal(t, tc.want, got, "drawn response")
		})
	}
}

func TestPowerDelay(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)

	system := func(seq int64, second int, state string) Exchange {
		return Exchange{
			Sequence: seq, Timestamp: start.Add(time.Duration(second) * time.Second), Method: "GET",
			URL: "https://bmc.example.com/redfish/v1/Systems/1", Status: 200, ResponseBody: `{"PowerState": "` + state + `"}`,
		}
	}

	reset := func(seq int64, second int, resetType string) Exchange {
		return Exchange{
			Sequence: seq, Timestamp: start.Add(time.Duration(second) * time.Second), Method: "POST",
			URL: "https://bmc.example.com/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", RequestBody: `{"ResetType": "` + resetType + `"}`, Status: 204,
		}
	}

	tests := []struct {
		name      string
		exchanges []Exchange

		want value
	}{
		{
			name:      "no reset",
			exchanges: []Exchange{system(1, 0, "Off")},
			want:      value{yaml: "0s"},
		},
		{
			name:      "reset keeps the power state",
			exchanges: []Exchange{system(1, 0, "Off"), reset(2, 1, "ForceOff"), system(3, 5, "Off")},
			want:      value{yaml: "0s"},
		},
		{
			name:      "graceful shutdown waits for the operating system",
			exchanges: []Exchange{system(1, 0, "On"), reset(2, 1, "GracefulShutdown"), system(3, 60, "Off")},
			want:      value{yaml: "0s"},
		},
		{
			name:      "first poll shows the new state",
			exchanges: []Exchange{system(1, 0, "Off"), reset(2, 1, "On"), system(3, 13, "On")},
			want:      value{yaml: "0s", seqs: []int64{2, 3}, verify: true, note: "the power state became On 0s to 12s after the reset, bounded by the polls"},
		},
		{
			name: "polls bound the delay from both sides",
			exchanges: []Exchange{
				system(1, 0, "Off"), reset(2, 1, "On"), system(3, 4, "Off"), system(4, 9, "On"),
				reset(5, 10, "ForceOff"), system(6, 15, "Off"),
			},
			want: value{yaml: "3s", seqs: []int64{2, 4, 5, 6}, verify: true, note: "the power state became On 3s to 8s and Off 0s to 5s after the reset, bounded by the polls"},
		},
		{
			name: "power off is faster than the lower bound of power on",
			exchanges: []Exchange{
				system(1, 0, "Off"), reset(2, 1, "On"), system(3, 13, "Off"), system(4, 30, "On"),
				reset(5, 31, "ForceOff"), system(6, 36, "Off"),
			},
			want: value{yaml: "5s", seqs: []int64{2, 4, 5, 6}, verify: true, note: "the power state became On 12s to 29s and Off 0s to 5s after the reset, bounded by the polls"},
		},
		{
			name: "the delay differs between resets",
			exchanges: []Exchange{
				system(1, 0, "Off"), reset(2, 1, "On"), system(3, 11, "Off"), system(4, 13, "On"),
				reset(5, 14, "ForceOff"), system(6, 15, "Off"),
				reset(7, 20, "On"), system(8, 25, "On"),
			},
			want: value{yaml: "1s", seqs: []int64{2, 4, 5, 6, 7, 8}, verify: true, note: "the power state became On 10s to 5s and Off 0s to 1s after the reset, bounded by the polls; the delay differs between resets"},
		},
		{
			name: "transitional state",
			exchanges: []Exchange{
				system(1, 0, "Off"), reset(2, 1, "On"), system(3, 13, "Reset"), system(4, 33, "On"),
			},
			want: value{yaml: "12s", seqs: []int64{2, 4}, verify: true, note: "the power state became On 12s to 32s after the reset, bounded by the polls; the BMC reported Reset in between"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.powerDelay(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.want, d.values["power.delay"], "power.delay")
		})
	}
}

func TestSecureBootEnable(t *testing.T) {
	t.Parallel()

	system := func(seq int64, state string) Exchange {
		return Exchange{
			Sequence: seq, Method: "GET", URL: "https://bmc.example.com/redfish/v1/Systems/1/",
			Status: 200, ResponseBody: `{"PowerState": "` + state + `"}`,
		}
	}

	secureBoot := func(seq int64, enabled string) Exchange {
		return Exchange{
			Sequence: seq, Method: "GET", URL: "https://bmc.example.com/redfish/v1/Systems/1/SecureBoot/",
			Status: 200, ResponseBody: `{"SecureBootEnable": ` + enabled + `}`,
		}
	}

	patch := func(seq int64, status int, messageID string) Exchange {
		return Exchange{
			Sequence: seq, Method: "PATCH", URL: "https://bmc.example.com/redfish/v1/Systems/1/SecureBoot/",
			RequestBody: `{"SecureBootEnable": true}`, Status: status,
			ResponseBody: `{"error": {"@Message.ExtendedInfo": [{"MessageId": "` + messageID + `"}]}}`,
		}
	}

	tests := []struct {
		name      string
		exchanges []Exchange

		want value
	}{
		{
			name:      "accepted while on",
			exchanges: []Exchange{system(1, "On"), patch(2, 200, "Base.1.18.Success")},
			want:      value{yaml: "false", seqs: []int64{1, 2}},
		},
		{
			name:      "refused while on",
			exchanges: []Exchange{system(1, "On"), patch(2, 400, "Base.1.18.PropertyNotWritable"), system(3, "Off"), patch(4, 200, "Base.1.18.Success")},
			want:      value{yaml: "true", seqs: []int64{1, 2}, verify: true, note: "refused while the server was on, Base.1.18.PropertyNotWritable"},
		},
		{
			name:      "not settled refusal",
			exchanges: []Exchange{system(1, "On"), patch(2, 400, "Base.1.18.ResourceInStandby"), system(3, "Off"), patch(4, 200, "Base.1.18.Success")},
			want:      value{yaml: "true", reason: "changed only while the server was off"},
		},
		{
			name:      "changed only while off",
			exchanges: []Exchange{system(1, "Off"), patch(2, 200, "iLO.2.25.SystemResetRequired")},
			want:      value{yaml: "true", reason: "changed only while the server was off"},
		},
		{
			name:      "already enabled",
			exchanges: []Exchange{secureBoot(1, "true")},
			want:      value{yaml: "true", reason: "secure boot already enabled"},
		},
		{
			name:      "not changed",
			exchanges: []Exchange{secureBoot(1, "false")},
			want:      value{yaml: "true", reason: "Operations Center did not change SecureBootEnable"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.secureBootEnable(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.want, d.values["secure_boot.enable_requires_power_off"], "secure_boot.enable_requires_power_off")
		})
	}
}

func TestRefusedFirst(t *testing.T) {
	t.Parallel()

	insert := func(seq int64, status int, body string) Exchange {
		return Exchange{
			Sequence: seq, Method: "POST", URL: "https://bmc.example.com/redfish/v1/Managers/1/VirtualMedia/2/Actions/VirtualMedia.InsertMedia/",
			RequestBody: body, Status: status, ResponseBody: `{"error": {"@Message.ExtendedInfo": [{"MessageId": "Base.1.18.ActionParameterUnknown"}]}}`,
		}
	}

	tests := []struct {
		name      string
		exchanges []Exchange

		want value
	}{
		{
			name:      "accepted at once",
			exchanges: []Exchange{insert(1, 204, `{"Image": "x"}`)},
			want:      value{yaml: "standard", seqs: []int64{1}},
		},
		{
			name:      "refused parameter",
			exchanges: []Exchange{insert(1, 400, `{"Image": "x", "MediaType": "CD"}`), insert(2, 200, `{"Image": "x"}`)},
			want:      value{yaml: "standard", seqs: []int64{2, 1}, note: "the BMC refused the parameter MediaType, Base.1.18.ActionParameterUnknown"},
		},
		{
			name:      "refused the same request",
			exchanges: []Exchange{insert(1, 400, `{"Image": "x"}`), insert(2, 204, `{"Image": "x"}`)},
			want:      value{yaml: "standard", seqs: []int64{2, 1}, note: "the BMC refused a first try, Base.1.18.ActionParameterUnknown"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			rec := &Recording{Exchanges: tc.exchanges}
			accepted := tc.exchanges[len(tc.exchanges)-1]

			// Run test
			got := refusedFirst(rec, accepted, observed("standard", accepted))

			// Assert
			require.Equal(t, tc.want, got, "virtual_media.insert")
		})
	}
}

func TestRedundantResets(t *testing.T) {
	t.Parallel()

	system := func(seq int64, state string) Exchange {
		return Exchange{
			Sequence: seq, Method: "GET", URL: "https://bmc.example.com/redfish/v1/Systems/1/",
			Status: 200, ResponseBody: `{"PowerState": "` + state + `"}`,
		}
	}

	reset := func(seq int64, resetType string, status int) Exchange {
		return Exchange{
			Sequence: seq, Method: "POST", URL: "https://bmc.example.com/redfish/v1/Systems/1/Actions/ComputerSystem.Reset/",
			RequestBody: `{"ResetType": "` + resetType + `"}`, Status: status,
			ResponseBody: `{"error": {"@Message.ExtendedInfo": [{"MessageId": "iLO.2.25.InvalidOperationForSystemState"}]}}`,
		}
	}

	const reason = "Operations Center sent no reset to the current power state"

	tests := []struct {
		name      string
		exchanges []Exchange

		wantOff value
		wantOn  value
	}{
		{
			name:      "accepted while in the target state",
			exchanges: []Exchange{system(1, "Off"), reset(2, "ForceOff", 204)},
			wantOff:   value{yaml: "null", seqs: []int64{1, 2}, note: "accepted ForceOff while Off"},
			wantOn:    value{yaml: "null", reason: reason},
		},
		{
			name:      "refused for another reason",
			exchanges: []Exchange{system(1, "On"), reset(2, "ForceOff", 400)},
			wantOff:   value{yaml: "null", reason: reason},
			wantOn:    value{yaml: "null", reason: reason},
		},
		{
			name:      "refused while in the target state",
			exchanges: []Exchange{system(1, "Off"), reset(2, "ForceOff", 400), system(3, "On"), reset(4, "On", 409)},
			wantOff:   value{yaml: "{status: 400, message_id: iLO.2.25.InvalidOperationForSystemState}", seqs: []int64{1, 2}, note: "refused ForceOff while Off"},
			wantOn:    value{yaml: "{status: 409, message_id: iLO.2.25.InvalidOperationForSystemState}", seqs: []int64{3, 4}, note: "refused On while On"},
		},
		{
			name:      "the first evidence wins",
			exchanges: []Exchange{system(1, "Off"), reset(2, "ForceOff", 400), system(3, "Off"), reset(4, "ForceOff", 204)},
			wantOff:   value{yaml: "{status: 400, message_id: iLO.2.25.InvalidOperationForSystemState}", seqs: []int64{1, 2}, note: "refused ForceOff while Off"},
			wantOn:    value{yaml: "null", reason: reason},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.redundantResets(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.wantOff, d.values["power.redundant_reset_errors.off"], "power.redundant_reset_errors.off")
			require.Equal(t, tc.wantOn, d.values["power.redundant_reset_errors.on"], "power.redundant_reset_errors.on")
		})
	}
}

func TestCertificateIDs(t *testing.T) {
	t.Parallel()

	collection := func(seq int64, database string, ids ...string) Exchange {
		body := `{"Members": [`
		for i, id := range ids {
			if i > 0 {
				body += ", "
			}

			body += `{"@odata.id": "/redfish/v1/Systems/1/SecureBoot/SecureBootDatabases/` + database + `/Certificates/` + id + `"}`
		}

		return Exchange{
			Sequence: seq, Method: "GET", URL: "https://bmc.example.com/redfish/v1/Systems/1/SecureBoot/SecureBootDatabases/" + database + "/Certificates",
			Status: 200, ResponseBody: body + "]}",
		}
	}

	tests := []struct {
		name      string
		exchanges []Exchange

		wantStyle  value
		wantPrefix value
	}{
		{
			name:       "hash",
			exchanges:  []Exchange{collection(1, "db", "a1b2c3d4e5f6")},
			wantStyle:  value{yaml: "hash", seqs: []int64{1}},
			wantPrefix: value{yaml: `""`},
		},
		{
			name:       "numbered per database",
			exchanges:  []Exchange{collection(1, "KEK", "Certificate1"), collection(2, "db", "Certificate1", "Certificate2")},
			wantStyle:  value{yaml: "sequential", seqs: []int64{1, 2}, note: "numbered per database, the proxy numbers all databases with one counter"},
			wantPrefix: value{yaml: `"Certificate"`, seqs: []int64{1, 2}},
		},
		{
			name: "numbered across databases with a dotted prefix, restarted by a reset",
			exchanges: []Exchange{
				collection(1, "KEK", "Policy.2", "Policy.3"), collection(2, "db", "Policy.4"),
				collection(3, "KEK", "Policy.2"), collection(4, "db", "Policy.3"),
			},
			wantStyle:  value{yaml: "sequential", seqs: []int64{1, 2, 3, 4}},
			wantPrefix: value{yaml: `"Policy."`, seqs: []int64{1, 2, 3, 4}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.certificateIDs(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.wantStyle, d.values["secure_boot.certificate_id_style"], "secure_boot.certificate_id_style")
			require.Equal(t, tc.wantPrefix, d.values["secure_boot.certificate_id_prefix"], "secure_boot.certificate_id_prefix")
		})
	}
}

func TestBIOSPatchResponse(t *testing.T) {
	t.Parallel()

	patch := func(status int, location string) Exchange {
		return Exchange{
			Sequence: 1, Method: "PATCH", URL: "https://bmc.example.com/redfish/v1/Systems/1/Bios/Settings",
			Status: status, ResponseHeaders: map[string][]string{"Location": {location}},
		}
	}

	poll := func(seq int64, status int, body string) Exchange {
		return Exchange{
			Sequence: seq, Method: "GET", URL: "https://bmc.example.com/redfish/v1/TaskService/Tasks/JID_1",
			Status: status, ResponseBody: body,
		}
	}

	tests := []struct {
		name      string
		exchanges []Exchange

		wantResponse value
		wantTask     value
	}{
		{
			name:         "task resource",
			exchanges:    []Exchange{patch(202, "/redfish/v1/TaskService/Tasks/JID_1"), poll(2, 200, `{"TaskState": "Completed"}`)},
			wantResponse: value{yaml: "202-task", seqs: []int64{1, 2}},
			wantTask:     value{yaml: "true", seqs: []int64{1, 2}},
		},
		{
			name:         "task URI without polls",
			exchanges:    []Exchange{patch(202, "/redfish/v1/TaskService/Tasks/JID_1")},
			wantResponse: value{yaml: "202-task", seqs: []int64{1}},
			wantTask:     value{yaml: "true", seqs: []int64{1}},
		},
		{
			name:         "task URI that answers like a task monitor",
			exchanges:    []Exchange{patch(202, "/redfish/v1/TaskService/Tasks/JID_1"), poll(2, 202, ""), poll(3, 200, "")},
			wantResponse: value{yaml: "202-taskmonitor", seqs: []int64{1, 2}, note: "the task URI answers like a task monitor, without a task body"},
			wantTask:     value{yaml: "false"},
		},
		{
			name:         "task monitor",
			exchanges:    []Exchange{patch(202, "/redfish/v1/TaskMonitors/1")},
			wantResponse: value{yaml: "202-taskmonitor", seqs: []int64{1}},
			wantTask:     value{yaml: "false"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.biosPatchResponse(&Recording{Exchanges: tc.exchanges}, tc.exchanges[0])

			// Assert
			require.Equal(t, tc.wantResponse, d.values["bios.patch_response"], "bios.patch_response")
			require.Equal(t, tc.wantTask, d.values["bios.task_resource"], "bios.task_resource")
		})
	}
}

func TestPatchResponse(t *testing.T) {
	t.Parallel()

	patch := func(seq int64, path string, status int, body string) Exchange {
		return Exchange{
			Sequence: seq, Method: "PATCH", URL: "https://bmc.example.com/redfish/v1/" + path,
			RequestBody: `{}`, Status: status, ResponseBody: body,
		}
	}

	tests := []struct {
		name      string
		exchanges []Exchange

		want value
	}{
		{
			name:      "no patch",
			exchanges: []Exchange{patch(1, "Systems/1/Bios/Settings", 200, `{"@odata.id": "/redfish/v1/Systems/1/Bios/Settings"}`)},
			want:      value{yaml: `"204"`, reason: "Operations Center sent no PATCH of Systems, SecureBoot or VirtualMedia"},
		},
		{
			name:      "no content",
			exchanges: []Exchange{patch(1, "Systems/1/SecureBoot", 204, "")},
			want:      value{yaml: `"204"`, seqs: []int64{1}},
		},
		{
			name:      "resource",
			exchanges: []Exchange{patch(1, "Managers/1/VirtualMedia/CD1", 200, `{"@odata.id": "/redfish/v1/Managers/1/VirtualMedia/CD1", "Id": "CD1"}`)},
			want:      value{yaml: `"200-resource"`, seqs: []int64{1}},
		},
		{
			name:      "top level messages",
			exchanges: []Exchange{patch(1, "Systems/1", 200, `{"@Message.ExtendedInfo": [{"MessageId": "Base.1.12.Success"}, {"MessageId": "IDRAC.2.8.SYS413"}]}`)},
			want:      value{yaml: `"200"`, seqs: []int64{1}, note: "Base.1.12.Success"},
		},
		{
			name: "messages inside error after a refused patch",
			exchanges: []Exchange{
				patch(1, "Systems/1", 400, `{"error": {"@Message.ExtendedInfo": [{"MessageId": "Base.1.18.PropertyUnknown"}]}}`),
				patch(2, "Systems/1", 200, `{"error": {"code": "iLO.0.10.ExtendedInfo", "@Message.ExtendedInfo": [{"MessageId": "Base.1.18.Success"}]}}`),
			},
			want: value{yaml: `"200-error"`, seqs: []int64{2}, note: "Base.1.18.Success"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.responses(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.want, d.values["responses.patch"], "responses.patch")
		})
	}
}

func TestDatabaseAnswers(t *testing.T) {
	t.Parallel()

	const certificates = "https://bmc.example.com/redfish/v1/Systems/1/SecureBoot/SecureBootDatabases/db/Certificates"

	post := func(seq int64, status int) Exchange {
		return Exchange{Sequence: seq, Method: "POST", URL: certificates, RequestBody: `{}`, Status: status}
	}

	deleted := func(seq int64, status int, body string) Exchange {
		return Exchange{Sequence: seq, Method: "DELETE", URL: certificates + "/CustPolicy.1", Status: status, ResponseBody: body}
	}

	tests := []struct {
		name      string
		exchanges []Exchange

		wantAdd    value
		wantDelete value
	}{
		{
			name:       "no writes",
			exchanges:  []Exchange{},
			wantAdd:    value{yaml: `"201"`},
			wantDelete: value{yaml: `"204"`},
		},
		{
			name:       "standard answers",
			exchanges:  []Exchange{post(1, 201), deleted(2, 204, "")},
			wantAdd:    value{yaml: `"201"`, seqs: []int64{1}},
			wantDelete: value{yaml: `"204"`, seqs: []int64{2}},
		},
		{
			name: "answers of Dell after a refused write",
			exchanges: []Exchange{
				post(1, 400), post(2, 204),
				deleted(3, 200, `{"error": {"@Message.ExtendedInfo": [{"MessageId": "IDRAC.2.8.SYS430"}, {"MessageId": "Base.1.12.ResetRequired"}]}}`),
			},
			wantAdd:    value{yaml: `"204"`, seqs: []int64{2}},
			wantDelete: value{yaml: `"200-error"`, seqs: []int64{3}, note: "IDRAC.2.8.SYS430"},
		},
		{
			name:       "unknown answers",
			exchanges:  []Exchange{post(1, 200), deleted(2, 202, "")},
			wantAdd:    value{yaml: `"201"`, seqs: []int64{1}, verify: true, note: "answered 200"},
			wantDelete: value{yaml: `"204"`, seqs: []int64{2}, verify: true, note: "answered 202"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.databaseAnswers(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.wantAdd, d.values["secure_boot.database_add_response"], "secure_boot.database_add_response")
			require.Equal(t, tc.wantDelete, d.values["secure_boot.database_delete_response"], "secure_boot.database_delete_response")
		})
	}
}

func TestResetKeysApplies(t *testing.T) {
	t.Parallel()

	const system = "https://bmc.example.com/redfish/v1/Systems/1"

	secureBoot := func(seq int64, mode string) Exchange {
		return Exchange{
			Sequence: seq, Method: "GET", URL: system + "/SecureBoot", Status: 200,
			ResponseBody: `{"SecureBootMode": "` + mode + `", "Actions": {"#SecureBoot.ResetKeys": {"target": "/redfish/v1/Systems/1/SecureBoot/Actions/SecureBoot.ResetKeys"}}}`,
		}
	}

	resetKeys := func(seq int64, body string) Exchange {
		return Exchange{
			Sequence: seq, Method: "POST", URL: system + "/SecureBoot/Actions/SecureBoot.ResetKeys",
			RequestBody: `{"ResetKeysType": "DeleteAllKeys"}`, Status: 200, ResponseBody: body,
		}
	}

	reset := func(seq int64, resetType string) Exchange {
		return Exchange{Sequence: seq, Method: "POST", URL: system + "/Actions/ComputerSystem.Reset", RequestBody: `{"ResetType": "` + resetType + `"}`, Status: 204}
	}

	rebootRequired := `{"@Message.ExtendedInfo": [{"MessageId": "ExtendedError.1.2.RebootRequired"}]}`

	tests := []struct {
		name      string
		exchanges []Exchange

		want value
	}{
		{
			name:      "no reset of the keys",
			exchanges: []Exchange{secureBoot(1, "UserMode")},
			want:      value{yaml: "immediately", reason: "Operations Center sent no ResetKeys"},
		},
		{
			name:      "user mode after the reset of the keys",
			exchanges: []Exchange{secureBoot(1, "UserMode"), resetKeys(2, ""), reset(3, "ForceOff"), secureBoot(4, "UserMode")},
			want:      value{yaml: "on_reset", seqs: []int64{2, 4}},
		},
		{
			name:      "setup mode before a power on",
			exchanges: []Exchange{secureBoot(1, "UserMode"), resetKeys(2, ""), secureBoot(3, "SetupMode")},
			want:      value{yaml: "immediately", seqs: []int64{2, 3}},
		},
		{
			name:      "setup mode after a power on, message asks for a reboot",
			exchanges: []Exchange{secureBoot(1, "UserMode"), resetKeys(2, rebootRequired), reset(3, "On"), secureBoot(4, "SetupMode")},
			want:      value{yaml: "on_reset", seqs: []int64{2}, verify: true, note: "ExtendedError.1.2.RebootRequired"},
		},
		{
			name:      "no read, message inside error asks for a reset",
			exchanges: []Exchange{secureBoot(1, "UserMode"), resetKeys(2, `{"error": {"@Message.ExtendedInfo": [{"MessageId": "iLO.2.25.SystemResetRequired"}]}}`)},
			want:      value{yaml: "on_reset", seqs: []int64{2}, verify: true, note: "iLO.2.25.SystemResetRequired"},
		},
		{
			name:      "no read, no message",
			exchanges: []Exchange{secureBoot(1, "UserMode"), resetKeys(2, "")},
			want:      value{yaml: "immediately", reason: "no read of SecureBoot shows when the reset takes effect"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.resetKeys(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.want, d.values["secure_boot.reset_keys.applies"], "secure_boot.reset_keys.applies")
		})
	}
}
