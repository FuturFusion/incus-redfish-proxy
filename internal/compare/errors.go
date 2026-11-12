package compare

import (
	"cmp"
	"slices"

	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

// retryOutcome marks a transport retry of a request.
const retryOutcome = "retry"

// ErrorRow counts one error outcome of one operation in one step.
type ErrorRow struct {
	State string

	// Operation is the pair of method and kind, for example "POST ComputerSystem.Reset".
	Operation string
	Outcome   string
	Reference int
	Candidate int
}

type errorKey struct {
	state     string
	operation string
	outcome   string
}

// errorRows lists the non 2xx answers, the transport errors and the transport retries.
func errorRows(reference *recording.Recording, candidate *recording.Recording) ([]ErrorRow, Parity) {
	rows := map[errorKey]*ErrorRow{}

	count := func(rec *recording.Recording, field func(*ErrorRow) *int) {
		ops := operations(rec)

		for i, e := range rec.Exchanges {
			op := ops[i]

			var outcomes []string

			if !e.Succeeded() {
				outcomes = append(outcomes, op.Outcome)
			}

			if e.Attempt > 0 {
				outcomes = append(outcomes, retryOutcome)
			}

			for _, o := range outcomes {
				key := errorKey{state: op.State, operation: op.methodKind(), outcome: o}

				row, ok := rows[key]
				if !ok {
					row = &ErrorRow{State: key.state, Operation: key.operation, Outcome: key.outcome}
					rows[key] = row
				}

				*field(row)++
			}
		}
	}

	count(reference, func(r *ErrorRow) *int { return &r.Reference })
	count(candidate, func(r *ErrorRow) *int { return &r.Candidate })

	order := stateOrder(reference, candidate)

	result := make([]ErrorRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, *row)
	}

	slices.SortFunc(result, func(a, b ErrorRow) int {
		return cmp.Or(
			cmp.Compare(order[a.State], order[b.State]),
			cmp.Compare(a.State, b.State),
			cmp.Compare(a.Operation, b.Operation),
			cmp.Compare(a.Outcome, b.Outcome),
		)
	})

	p := Parity{Label: "Errors", Unit: "error outcomes of the reference also in the candidate"}

	for _, row := range result {
		if row.Reference > 0 {
			p.Total++

			if row.Candidate > 0 {
				p.Both++
			}
		}
	}

	return result, p
}

// stateOrder numbers the states in the order the exchanges first enter them.
func stateOrder(recordings ...*recording.Recording) map[string]int {
	order := map[string]int{}

	for _, rec := range recordings {
		for _, e := range rec.Exchanges {
			_, ok := order[e.DeploymentState]
			if !ok {
				order[e.DeploymentState] = len(order)
			}
		}
	}

	return order
}
