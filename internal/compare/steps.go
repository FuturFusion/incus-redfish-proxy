package compare

import (
	"encoding/json"
	"time"

	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

// step is one entry of the deployment state history.
type step struct {
	State     string    `json:"state"`
	EnteredAt time.Time `json:"entered_at"`
	Retries   int       `json:"retries"`
	Error     string    `json:"error"`

	Duration time.Duration `json:"-"`
}

// StepSide is one side of an aligned deployment step.
type StepSide struct {
	Entered  bool
	Retries  int
	Error    string
	Duration time.Duration
}

// StepRow is one aligned deployment step.
type StepRow struct {
	State     string
	Reference StepSide
	Candidate StepSide
	Flags     []string
}

// steps returns the deployment state history. Without a history, it derives the steps from the exchanges.
func steps(rec *recording.Recording) []step {
	var history []step

	err := json.Unmarshal(rec.Manifest.History, &history)
	if err != nil || len(history) == 0 {
		history = nil

		for _, e := range rec.Exchanges {
			if len(history) == 0 || history[len(history)-1].State != e.DeploymentState {
				history = append(history, step{State: e.DeploymentState, EnteredAt: e.Timestamp})
			}
		}
	}

	_, end := bounds(rec)

	for i := range history {
		next := end
		if i+1 < len(history) {
			next = history[i+1].EnteredAt
		}

		history[i].Duration = max(next.Sub(history[i].EnteredAt), 0)
	}

	return history
}

func stepRows(reference []step, candidate []step) ([]StepRow, Parity) {
	matches := align(reference, candidate, func(a, b step) bool { return a.State == b.State })

	rows := make([]StepRow, 0, len(matches))

	for _, m := range matches {
		var row StepRow

		if m.reference >= 0 {
			s := reference[m.reference]
			row.State = s.State
			row.Reference = StepSide{Entered: true, Retries: s.Retries, Error: s.Error, Duration: s.Duration}
		}

		if m.candidate >= 0 {
			s := candidate[m.candidate]
			row.State = s.State
			row.Candidate = StepSide{Entered: true, Retries: s.Retries, Error: s.Error, Duration: s.Duration}
		}

		row.Flags = stepFlags(row)
		rows = append(rows, row)
	}

	referenceStates := stateSet(reference)
	candidateStates := stateSet(candidate)

	p := Parity{Label: "Steps", Unit: "states on both sides"}

	for state := range union(referenceStates, candidateStates) {
		p.Total++

		if referenceStates[state] && candidateStates[state] {
			p.Both++
		}
	}

	return rows, p
}

func stepFlags(row StepRow) []string {
	var flags []string

	switch {
	case !row.Candidate.Entered:
		return []string{"reference only"}

	case !row.Reference.Entered:
		return []string{"candidate only"}
	}

	if row.Reference.Retries != row.Candidate.Retries {
		flags = append(flags, "retries differ")
	}

	switch {
	case row.Reference.Error != "" && row.Candidate.Error == "":
		flags = append(flags, "error on reference only")

	case row.Reference.Error == "" && row.Candidate.Error != "":
		flags = append(flags, "error on candidate only")
	}

	return flags
}

func stateSet(history []step) map[string]bool {
	states := map[string]bool{}
	for _, s := range history {
		states[s.State] = true
	}

	return states
}

func union(a map[string]bool, b map[string]bool) map[string]bool {
	all := make(map[string]bool, len(a)+len(b))

	for key := range a {
		all[key] = true
	}

	for key := range b {
		all[key] = true
	}

	return all
}

// startState comes before the first state of a deployment, so the first state is a transition too.
const startState = "start"

// Transitions lists the changes from one deployment state to the next that only one side makes.
// A loop or a reverted step shows up here, even if both sides enter the same states.
type Transitions struct {
	ReferenceOnly []string
	CandidateOnly []string
}

// transitions returns the distinct transitions, like "check-bios → apply-bios", in the order of their first appearance.
func transitions(history []step) []string {
	var result []string

	seen := map[string]bool{}
	previous := startState

	for _, s := range history {
		transition := previous + " → " + s.State
		if !seen[transition] {
			seen[transition] = true
			result = append(result, transition)
		}

		previous = s.State
	}

	return result
}

func transitionRows(reference []step, candidate []step) (Transitions, Parity) {
	referenceTransitions, candidateTransitions := transitions(reference), transitions(candidate)
	inReference, inCandidate := toSet(referenceTransitions), toSet(candidateTransitions)

	var t Transitions

	p := Parity{Label: "Transitions", Unit: "state transitions on both sides"}

	for _, transition := range referenceTransitions {
		p.Total++

		if inCandidate[transition] {
			p.Both++
		} else {
			t.ReferenceOnly = append(t.ReferenceOnly, transition)
		}
	}

	for _, transition := range candidateTransitions {
		if !inReference[transition] {
			p.Total++

			t.CandidateOnly = append(t.CandidateOnly, transition)
		}
	}

	return t, p
}

func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}

	return set
}
