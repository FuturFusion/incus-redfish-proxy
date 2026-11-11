package compare

import "time"

// minRatioDuration is the shortest reference duration with a ratio. Below it, the ratio is noise.
const minRatioDuration = time.Second

// totalState names the timing row of the whole deployment.
const totalState = "total"

// TimingRow compares the duration of one step, or of the whole deployment.
type TimingRow struct {
	State     string
	Reference time.Duration
	Candidate time.Duration

	// ReferenceEntered and CandidateEntered tell whether the side entered the step.
	ReferenceEntered bool
	CandidateEntered bool

	// Ratio is the candidate duration divided by the reference duration. It is 0 if a side lacks the step or the reference took less than a second.
	Ratio float64
}

func timingRows(steps []StepRow, h Header) []TimingRow {
	rows := make([]TimingRow, 0, len(steps)+1)

	for _, s := range steps {
		rows = append(rows, timingRow(s.State, s.Reference, s.Candidate))
	}

	return append(rows, timingRow(totalState, StepSide{Entered: true, Duration: h.Reference.Duration}, StepSide{Entered: true, Duration: h.Candidate.Duration}))
}

func timingRow(state string, reference StepSide, candidate StepSide) TimingRow {
	row := TimingRow{
		State:            state,
		Reference:        reference.Duration,
		Candidate:        candidate.Duration,
		ReferenceEntered: reference.Entered,
		CandidateEntered: candidate.Entered,
	}

	if reference.Entered && candidate.Entered && reference.Duration >= minRatioDuration {
		row.Ratio = float64(candidate.Duration) / float64(reference.Duration)
	}

	return row
}
