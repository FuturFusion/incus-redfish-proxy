package recording

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var cycleStart = time.Date(2026, 11, 9, 6, 0, 0, 0, time.UTC)

func systemAt(seq int64, at time.Duration, state string) Exchange {
	return Exchange{
		Sequence: seq, Timestamp: cycleStart.Add(at), Method: "GET", URL: "https://bmc.example.com/redfish/v1/Systems/1/",
		Status: 200, ResponseBody: `{"PowerState": "` + state + `"}`,
	}
}

func resetAt(seq int64, at time.Duration, resetType string, status int) Exchange {
	return Exchange{
		Sequence: seq, Timestamp: cycleStart.Add(at), Method: "POST", URL: "https://bmc.example.com/redfish/v1/Systems/1/Actions/ComputerSystem.Reset/",
		RequestBody: `{"ResetType": "` + resetType + `"}`, Status: status,
	}
}

// resetKeysAt sends ResetKeys at the start of the recording.
func resetKeysAt(seq int64) Exchange {
	return Exchange{
		Sequence: seq, Timestamp: cycleStart, Method: "POST", URL: "https://bmc.example.com/redfish/v1/Systems/1/SecureBoot/Actions/SecureBoot.ResetKeys/",
		RequestBody: `{"ResetKeysType": "DeleteAllKeys"}`, Status: 200,
	}
}

func enableAt(seq int64, at time.Duration) Exchange {
	return Exchange{
		Sequence: seq, Timestamp: cycleStart.Add(at), Method: "PATCH", URL: "https://bmc.example.com/redfish/v1/Systems/1/SecureBoot/",
		RequestBody: `{"SecureBootEnable": true}`, Status: 200,
	}
}

func secureBootAt(seq int64, enable bool, mode string) Exchange {
	body := `{"SecureBootEnable": false, "SecureBootMode": "` + mode + `"}`
	if enable {
		body = `{"SecureBootEnable": true, "SecureBootMode": "` + mode + `"}`
	}

	return Exchange{
		Sequence: seq, Method: "GET", URL: "https://bmc.example.com/redfish/v1/Systems/1/SecureBoot/",
		Status: 200, ResponseBody: body,
	}
}

func TestPowerCycle(t *testing.T) {
	t.Parallel()

	const noChange = "Operations Center changed no secure boot setting"

	const noCycle = "no power cycle followed a secure boot change"

	tests := []struct {
		name      string
		exchanges []Exchange

		wantAfter    value
		wantOffAfter value
		wantOffFor   value
	}{
		{
			name:         "no secure boot change",
			exchanges:    []Exchange{resetAt(1, 0, "On", 200), systemAt(2, 10*time.Second, "On")},
			wantAfter:    value{yaml: "[]", reason: noChange},
			wantOffAfter: value{yaml: "0s", reason: noChange},
			wantOffFor:   value{yaml: "0s", reason: noChange},
		},
		{
			name: "no power cycle",
			exchanges: []Exchange{
				resetKeysAt(1), resetAt(2, time.Second, "On", 200), systemAt(3, 10*time.Second, "On"),
				resetAt(4, 3*time.Minute, "ForceOff", 200), systemAt(5, 190*time.Second, "Off"),
			},
			wantAfter:    value{yaml: "[]", seqs: []int64{1}, note: noCycle},
			wantOffAfter: value{yaml: "0s", reason: noCycle},
			wantOffFor:   value{yaml: "0s", reason: noCycle},
		},
		{
			name: "power cycle after reset keys and after enable",
			exchanges: []Exchange{
				resetKeysAt(1), resetAt(2, time.Second, "On", 200),
				systemAt(3, 117*time.Second, "On"), systemAt(4, 125*time.Second, "Off"),
				resetAt(5, 126*time.Second, "ForceOff", 400), systemAt(6, 138*time.Second, "Off"), systemAt(7, 158*time.Second, "On"),
				resetAt(8, 160*time.Second, "ForceOff", 200), systemAt(9, 170*time.Second, "Off"),
				enableAt(10, 200*time.Second), resetAt(11, 201*time.Second, "On", 200),
				systemAt(12, 310*time.Second, "On"), systemAt(13, 330*time.Second, "Off"), systemAt(14, 350*time.Second, "On"),
			},
			wantAfter: value{yaml: "[reset_keys, enable]", seqs: []int64{1, 2, 3, 4, 6, 7, 10, 11, 12, 13, 14}},
			wantOffAfter: value{
				yaml: "1m56s", seqs: []int64{1, 2, 3, 4, 6, 7, 10, 11, 12, 13, 14}, verify: true,
				note: "the power off came 1m56s to 2m4s after the power on, bounded by the polls",
			},
			wantOffFor: value{
				yaml: "13s", seqs: []int64{1, 2, 3, 4, 6, 7, 10, 11, 12, 13, 14}, verify: true,
				note: "the system stayed off for 13s to 40s, bounded by the polls",
			},
		},
		{
			name: "a requested power off is no cycle",
			exchanges: []Exchange{
				enableAt(1, 0), resetAt(2, time.Second, "On", 200),
				systemAt(3, 60*time.Second, "On"), resetAt(4, 61*time.Second, "ForceOff", 200), systemAt(5, 70*time.Second, "Off"),
				resetAt(6, 80*time.Second, "On", 200), systemAt(7, 90*time.Second, "On"),
			},
			wantAfter:    value{yaml: "[]", seqs: []int64{1}, note: noCycle},
			wantOffAfter: value{yaml: "0s", reason: noCycle},
			wantOffFor:   value{yaml: "0s", reason: noCycle},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.powerCycle(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.wantAfter, d.values["secure_boot.power_cycle.after"], "secure_boot.power_cycle.after")
			require.Equal(t, tc.wantOffAfter, d.values["secure_boot.power_cycle.off_after"], "secure_boot.power_cycle.off_after")
			require.Equal(t, tc.wantOffFor, d.values["secure_boot.power_cycle.off_for"], "secure_boot.power_cycle.off_for")
		})
	}
}

func TestResetKeysDisables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		exchanges []Exchange

		want value
	}{
		{
			name:      "no ResetKeys",
			exchanges: []Exchange{secureBootAt(1, true, "UserMode")},
			want:      value{yaml: "false", reason: "Operations Center sent no ResetKeys"},
		},
		{
			name:      "not read before",
			exchanges: []Exchange{resetKeysAt(1), secureBootAt(2, false, "SetupMode")},
			want:      value{yaml: "false", reason: "SecureBoot was not read before the ResetKeys"},
		},
		{
			name:      "setup mode not seen",
			exchanges: []Exchange{secureBootAt(1, true, "UserMode"), resetKeysAt(2), secureBootAt(3, true, "UserMode")},
			want:      value{yaml: "false", reason: "no read of SecureBoot shows setup mode"},
		},
		{
			name:      "switched off like HPE",
			exchanges: []Exchange{secureBootAt(1, true, "UserMode"), resetKeysAt(2), secureBootAt(3, true, "UserMode"), secureBootAt(4, false, "SetupMode")},
			want:      value{yaml: "true", seqs: []int64{1, 2, 4}},
		},
		{
			name:      "kept on like Lenovo",
			exchanges: []Exchange{secureBootAt(1, true, "UserMode"), resetKeysAt(2), secureBootAt(3, true, "SetupMode")},
			want:      value{yaml: "false", seqs: []int64{1, 2, 3}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.resetKeysDisables(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.want, d.values["secure_boot.reset_keys.disables_secure_boot"], "secure_boot.reset_keys.disables_secure_boot")
		})
	}
}
