package recording

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func hungAt(seq int64, at time.Duration) Exchange {
	return Exchange{
		Sequence: seq, Timestamp: cycleStart.Add(at), Method: "GET", URL: "https://bmc.example.com/redfish/v1/",
		TransportError: "net/http: timeout awaiting response headers",
	}
}

func TestUnreachable(t *testing.T) {
	t.Parallel()

	const noResetKeys = "Operations Center sent no ResetKeys"

	const allAnswered = "the BMC answered every request after the power on"

	tests := []struct {
		name      string
		exchanges []Exchange

		wantAfter value
		wantFor   value
	}{
		{
			name:      "no ResetKeys",
			exchanges: []Exchange{resetAt(1, 0, "On", 204)},
			wantAfter: value{yaml: "0s", reason: noResetKeys},
			wantFor:   value{yaml: "0s", reason: noResetKeys},
		},
		{
			name:      "no power on after the ResetKeys",
			exchanges: []Exchange{resetKeysAt(1)},
			wantAfter: value{yaml: "0s", reason: "no power on followed the ResetKeys"},
			wantFor:   value{yaml: "0s", reason: "no power on followed the ResetKeys"},
		},
		{
			name:      "every request answered like iLO",
			exchanges: []Exchange{resetKeysAt(1), resetAt(2, time.Second, "On", 204), systemAt(3, 90*time.Second, "On")},
			wantAfter: value{yaml: "0s", reason: allAnswered},
			wantFor:   value{yaml: "0s", seqs: []int64{1, 2}, note: allAnswered},
		},
		{
			name: "requests without an answer like XCC",
			exchanges: []Exchange{
				resetKeysAt(1), resetAt(2, time.Second, "On", 204), systemAt(3, 91*time.Second, "On"),
				hungAt(4, 91500*time.Millisecond), hungAt(5, 152*time.Second), hungAt(6, 193*time.Second), systemAt(7, 253*time.Second, "On"),
			},
			wantAfter: value{yaml: "1m30s", seqs: []int64{1, 2, 3, 4}, verify: true, note: "the last answer came 1m30s after the power on"},
			wantFor:   value{yaml: "2m41s", seqs: []int64{4, 6, 7}, verify: true, note: "3 requests got no answer until the next request"},
		},
		{
			name: "one request without an answer at the end of the recording",
			exchanges: []Exchange{
				resetKeysAt(1), resetAt(2, time.Second, "On", 204), systemAt(3, 91*time.Second, "On"), hungAt(4, 92*time.Second),
			},
			wantAfter: value{yaml: "1m30s", seqs: []int64{1, 2, 3, 4}, verify: true, note: "the last answer came 1m30s after the power on"},
			wantFor:   value{yaml: "1s", seqs: []int64{4}, verify: true, note: "1 requests got no answer, the end is not recorded"},
		},
		{
			name: "a hung request after the next reset does not count",
			exchanges: []Exchange{
				resetKeysAt(1), resetAt(2, time.Second, "On", 204), resetAt(3, 10*time.Second, "ForceOff", 204), hungAt(4, 20*time.Second),
			},
			wantAfter: value{yaml: "0s", reason: allAnswered},
			wantFor:   value{yaml: "0s", seqs: []int64{1, 2}, note: allAnswered},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.unreachable(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.wantAfter, d.values["secure_boot.reset_keys.unreachable.after"], "secure_boot.reset_keys.unreachable.after")
			require.Equal(t, tc.wantFor, d.values["secure_boot.reset_keys.unreachable.for"], "secure_boot.reset_keys.unreachable.for")
		})
	}
}

func secureBootReadAt(seq int64, at time.Duration, mode string) Exchange {
	e := secureBootAt(seq, true, mode)
	e.Timestamp = cycleStart.Add(at)

	return e
}

func TestVisibleAfter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		exchanges []Exchange

		want value
	}{
		{
			name:      "no ResetKeys",
			exchanges: []Exchange{resetAt(1, 0, "On", 204)},
			want:      value{yaml: "0s", reason: "Operations Center sent no ResetKeys"},
		},
		{
			name:      "no read in setup mode",
			exchanges: []Exchange{resetKeysAt(1), resetAt(2, time.Second, "On", 204), secureBootReadAt(3, 20*time.Second, "UserMode")},
			want:      value{yaml: "0s", reason: "no read of SecureBoot after the power on shows setup mode"},
		},
		{
			name:      "setup mode at the first read",
			exchanges: []Exchange{resetKeysAt(1), resetAt(2, time.Second, "On", 204), secureBootReadAt(3, 20*time.Second, "SetupMode")},
			want:      value{yaml: "0s", seqs: []int64{1, 2, 3}, note: "the first read after the power on shows setup mode"},
		},
		{
			name: "setup mode later like iLO",
			exchanges: []Exchange{
				resetKeysAt(1), resetAt(2, time.Second, "On", 204), secureBootReadAt(3, 20*time.Second, "UserMode"),
				secureBootReadAt(4, 96*time.Second, "UserMode"), secureBootReadAt(5, 119*time.Second, "SetupMode"),
			},
			want: value{yaml: "1m35s", seqs: []int64{1, 2, 4, 5}, verify: true, note: "setup mode showed 1m35s to 1m58s after the power on, bounded by the reads"},
		},
		{
			name: "a read after the next reset does not count",
			exchanges: []Exchange{
				resetKeysAt(1), resetAt(2, time.Second, "On", 204), resetAt(3, 10*time.Second, "ForceOff", 204), secureBootReadAt(4, 20*time.Second, "SetupMode"),
			},
			want: value{yaml: "0s", reason: "no read of SecureBoot after the power on shows setup mode"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.visibleAfter(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.want, d.values["secure_boot.reset_keys.visible_after"], "secure_boot.reset_keys.visible_after")
		})
	}
}
