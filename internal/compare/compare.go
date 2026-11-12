// Package compare compares two baseboard management controller (BMC) recordings of Operations Center.
// It tells how close a profile is to the real server it is drawn from.
package compare

import (
	"fmt"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

// Parity counts the items of a section that both sides share.
type Parity struct {
	Label string
	Both  int
	Total int

	// Unit names the counted items, for example "states on both sides".
	Unit string
}

// String returns the parity line, for example "Steps: 14 of 16 states on both sides".
func (p Parity) String() string {
	return fmt.Sprintf("%s: %d of %d %s", p.Label, p.Both, p.Total, p.Unit)
}

// Full reports whether both sides share every item.
func (p Parity) Full() bool {
	return p.Both == p.Total
}

// Parities holds the parity of each section. Timing has no parity.
type Parities struct {
	Steps       Parity
	Transitions Parity
	Operations  Parity
	Errors      Parity
	Keys        Parity
}

// List returns the parities in the order of the sections.
func (p Parities) List() []Parity {
	return []Parity{p.Steps, p.Transitions, p.Operations, p.Errors, p.Keys}
}

// Report holds the result of each section.
type Report struct {
	Header      Header
	Parities    Parities
	Steps       []StepRow
	Transitions Transitions
	Operations  []StepOperations
	Errors      []ErrorRow
	Keys        []KeyRow
	Timing      []TimingRow

	// HasProfile tells whether the key rows hold the values of a profile.yaml.
	HasProfile bool
}

// Compare compares the candidate recording with the reference recording.
// The profile is optional.
func Compare(reference *recording.Recording, candidate *recording.Recording, p *profile.Profile) Report {
	referenceSteps := steps(reference)
	candidateSteps := steps(candidate)

	r := Report{
		Header:     header(reference, candidate),
		HasProfile: p != nil,
	}

	r.Steps, r.Parities.Steps = stepRows(referenceSteps, candidateSteps)
	r.Transitions, r.Parities.Transitions = transitionRows(referenceSteps, candidateSteps)
	r.Operations, r.Parities.Operations = stepOperations(reference, candidate, r.Steps)
	r.Errors, r.Parities.Errors = errorRows(reference, candidate)

	var warning string

	r.Keys, r.Parities.Keys, warning = keyRows(reference, candidate, p)
	if warning != "" {
		r.Header.Warnings = append(r.Header.Warnings, warning)
	}

	r.Timing = timingRows(r.Steps, r.Header)

	return r
}
