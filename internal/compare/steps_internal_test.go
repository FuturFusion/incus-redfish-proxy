package compare

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

var start = time.Date(2026, 11, 9, 6, 0, 0, 0, time.UTC)

func withHistory(t *testing.T, history ...step) *recording.Recording {
	t.Helper()

	for i := range history {
		history[i].EnteredAt = start.Add(time.Duration(i) * time.Minute)
	}

	data, err := json.Marshal(history)
	require.NoError(t, err, "marshal the history")

	return &recording.Recording{Manifest: recording.Manifest{
		StartedAt:  start,
		FinishedAt: start.Add(time.Duration(len(history)) * time.Minute),
		History:    data,
	}}
}

func TestStepRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		reference []step
		candidate []step

		want       []StepRow
		wantParity Parity
	}{
		{
			name:      "state on one side only",
			reference: []step{{State: "check-bios"}, {State: "apply-bios"}, {State: "power-on"}},
			candidate: []step{{State: "check-bios"}, {State: "power-on"}, {State: "wait"}},
			want: []StepRow{
				{State: "check-bios", Reference: StepSide{Entered: true, Duration: time.Minute}, Candidate: StepSide{Entered: true, Duration: time.Minute}},
				{State: "apply-bios", Reference: StepSide{Entered: true, Duration: time.Minute}, Flags: []string{"reference only"}},
				{State: "power-on", Reference: StepSide{Entered: true, Duration: time.Minute}, Candidate: StepSide{Entered: true, Duration: time.Minute}},
				{State: "wait", Candidate: StepSide{Entered: true, Duration: time.Minute}, Flags: []string{"candidate only"}},
			},
			wantParity: Parity{Label: "Steps", Both: 2, Total: 4, Unit: "states on both sides"},
		},
		{
			name:      "retries and errors",
			reference: []step{{State: "power-off", Retries: 2, Error: "reverted"}, {State: "wait"}},
			candidate: []step{{State: "power-off"}, {State: "wait", Error: "timeout"}},
			want: []StepRow{
				{
					State:     "power-off",
					Reference: StepSide{Entered: true, Retries: 2, Error: "reverted", Duration: time.Minute},
					Candidate: StepSide{Entered: true, Duration: time.Minute},
					Flags:     []string{"retries differ", "error on reference only"},
				},
				{
					State:     "wait",
					Reference: StepSide{Entered: true, Duration: time.Minute},
					Candidate: StepSide{Entered: true, Error: "timeout", Duration: time.Minute},
					Flags:     []string{"error on candidate only"},
				},
			},
			wantParity: Parity{Label: "Steps", Both: 2, Total: 2, Unit: "states on both sides"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			reference := steps(withHistory(t, tc.reference...))
			candidate := steps(withHistory(t, tc.candidate...))

			// Run test
			got, gotParity := stepRows(reference, candidate)

			// Assert
			require.Equal(t, tc.want, got, "aligned steps")
			require.Equal(t, tc.wantParity, gotParity, "parity")
		})
	}
}

func TestStepsFromExchanges(t *testing.T) {
	t.Parallel()

	// Setup
	rec := &recording.Recording{Exchanges: []recording.Exchange{
		{DeploymentState: "check-bios", Timestamp: start},
		{DeploymentState: "check-bios", Timestamp: start.Add(time.Second)},
		{DeploymentState: "apply-bios", Timestamp: start.Add(3 * time.Second)},
		{DeploymentState: "apply-bios", Timestamp: start.Add(7 * time.Second)},
	}}

	// Run test
	got := steps(rec)

	// Assert
	require.Equal(t, []step{
		{State: "check-bios", EnteredAt: start, Duration: 3 * time.Second},
		{State: "apply-bios", EnteredAt: start.Add(3 * time.Second), Duration: 4 * time.Second},
	}, got, "without a history, the state changes of the exchanges are the steps")
}

func TestTransitionRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		reference []step
		candidate []step

		want       Transitions
		wantParity Parity
	}{
		{
			name:      "loop on the reference side only",
			reference: []step{{State: "certificates"}, {State: "settled"}, {State: "certificates"}, {State: "settled"}, {State: "install"}},
			candidate: []step{{State: "certificates"}, {State: "settled"}, {State: "install"}},
			want:      Transitions{ReferenceOnly: []string{"settled → certificates"}},
			wantParity: Parity{
				Label: "Transitions", Both: 3, Total: 4, Unit: "state transitions on both sides",
			},
		},
		{
			name:      "reverted step and a step on the candidate side only",
			reference: []step{{State: "power-off"}, {State: "wait"}, {State: "power-off"}, {State: "wait"}},
			candidate: []step{{State: "power-off"}, {State: "wait"}, {State: "verify"}},
			want: Transitions{
				ReferenceOnly: []string{"wait → power-off"},
				CandidateOnly: []string{"wait → verify"},
			},
			wantParity: Parity{
				Label: "Transitions", Both: 2, Total: 4, Unit: "state transitions on both sides",
			},
		},
		{
			name:      "equal",
			reference: []step{{State: "a"}, {State: "b"}},
			candidate: []step{{State: "a"}, {State: "b"}},
			want:      Transitions{},
			wantParity: Parity{
				Label: "Transitions", Both: 2, Total: 2, Unit: "state transitions on both sides",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Run test
			got, gotParity := transitionRows(tc.reference, tc.candidate)

			// Assert
			require.Equal(t, tc.want, got, "transitions on one side only")
			require.Equal(t, tc.wantParity, gotParity, "parity")
		})
	}
}

func TestTimingRows(t *testing.T) {
	t.Parallel()

	// Setup
	steps := []StepRow{
		{State: "short", Reference: StepSide{Entered: true, Duration: 500 * time.Millisecond}, Candidate: StepSide{Entered: true, Duration: time.Minute}},
		{State: "long", Reference: StepSide{Entered: true, Duration: 10 * time.Second}, Candidate: StepSide{Entered: true, Duration: 5 * time.Second}},
		{State: "one side", Reference: StepSide{Entered: true, Duration: 10 * time.Second}},
	}

	h := Header{Reference: Side{Duration: time.Minute}, Candidate: Side{Duration: 2 * time.Minute}}

	// Run test
	got := timingRows(steps, h)

	// Assert
	ratios := make([]float64, 0, len(got))
	for _, row := range got {
		ratios = append(ratios, row.Ratio)
	}

	require.Equal(t, []float64{0, 0.5, 0, 2}, ratios, "no ratio below a second of the reference or for a step on one side only")
}
