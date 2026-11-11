package recording

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func certificatesAt(seq int64, at time.Duration, count int) Exchange {
	return Exchange{
		Sequence: seq, Timestamp: cycleStart.Add(at), Method: "GET", URL: "https://bmc.example.com/redfish/v1/Systems/1/SecureBoot/SecureBootDatabases/db/Certificates",
		Status: 200, ResponseBody: `{"Members@odata.count": ` + strconv.Itoa(count) + `}`,
	}
}

func certificatePostAt(seq int64, at time.Duration) Exchange {
	return Exchange{
		Sequence: seq, Timestamp: cycleStart.Add(at), Method: "POST", URL: "https://bmc.example.com/redfish/v1/Systems/1/SecureBoot/SecureBootDatabases/db/Certificates",
		Status: 204,
	}
}

func TestEmptyAfterPowerOn(t *testing.T) {
	t.Parallel()

	const noWrite = "Operations Center wrote no key database"

	const noEmpty = "no key database read empty after a power on"

	tests := []struct {
		name      string
		exchanges []Exchange

		wantTimes value
		wantFor   value
	}{
		{
			name:      "no write",
			exchanges: []Exchange{certificatesAt(1, 0, 1)},
			wantTimes: value{yaml: "0", reason: noWrite},
			wantFor:   value{yaml: "0s", reason: noWrite},
		},
		{
			name: "full after the power on",
			exchanges: []Exchange{
				certificatePostAt(1, 0), certificatesAt(2, time.Second, 1),
				resetAt(3, time.Minute, "On", 204), certificatesAt(4, 3*time.Minute, 1),
			},
			wantTimes: value{yaml: "0", seqs: []int64{1}, note: noEmpty},
			wantFor:   value{yaml: "0s", reason: noEmpty},
		},
		{
			name: "empty after the first two power ons like iDRAC",
			exchanges: []Exchange{
				certificatePostAt(1, 0), certificatesAt(2, time.Second, 1),
				resetAt(3, time.Minute, "On", 204), certificatesAt(4, 5*time.Minute, 0),
				resetAt(5, 6*time.Minute, "ForceOff", 204), certificatesAt(6, 7*time.Minute, 1),
				resetAt(7, 8*time.Minute, "On", 204), certificatesAt(8, 11*time.Minute, 0),
				resetAt(9, 12*time.Minute, "ForceOff", 204), certificatesAt(10, 13*time.Minute, 1),
				resetAt(11, 14*time.Minute, "On", 204), certificatesAt(12, 17*time.Minute, 1),
			},
			wantTimes: value{yaml: "2", seqs: []int64{3, 4, 7, 8}, note: "counted from the last write, seq 1"},
			wantFor: value{
				yaml: "4m0s", seqs: []int64{3, 4, 7, 8}, verify: true,
				note: "the last empty read came 4m0s after the power on, the end is not recorded",
			},
		},
		{
			name: "an empty database before the power on stays empty",
			exchanges: []Exchange{
				certificatePostAt(1, 0), certificatesAt(2, time.Second, 0),
				resetAt(3, time.Minute, "On", 204), certificatesAt(4, 3*time.Minute, 0),
			},
			wantTimes: value{yaml: "0", seqs: []int64{1}, note: noEmpty},
			wantFor:   value{yaml: "0s", reason: noEmpty},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			d := &Draft{values: defaultValues()}

			// Run test
			d.emptyAfterPowerOn(&Recording{Exchanges: tc.exchanges})

			// Assert
			require.Equal(t, tc.wantTimes, d.values["secure_boot.empty_after_power_on.times"], "secure_boot.empty_after_power_on.times")
			require.Equal(t, tc.wantFor, d.values["secure_boot.empty_after_power_on.for"], "secure_boot.empty_after_power_on.for")
		})
	}
}
