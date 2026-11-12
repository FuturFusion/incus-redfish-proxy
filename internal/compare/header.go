package compare

import (
	"fmt"
	"time"

	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

// completedState is the final state of a successful deployment.
const completedState = "completed"

// Side describes one recording.
type Side struct {
	Model       string
	BMCFirmware string
	BIOS        string
	OCVersion   string
	FinalState  string
	Exchanges   int

	// Truncated counts the exchanges with a truncated request or response body.
	Truncated int
	Duration  time.Duration
}

// Header describes both recordings and warns about differences that are not caused by the profile.
type Header struct {
	Reference Side
	Candidate Side
	Warnings  []string
}

func header(reference *recording.Recording, candidate *recording.Recording) Header {
	h := Header{
		Reference: side(reference),
		Candidate: side(candidate),
	}

	if h.Reference.OCVersion != h.Candidate.OCVersion {
		h.Warnings = append(h.Warnings, fmt.Sprintf("The Operations Center versions differ (%s and %s). Differences can come from changes of Operations Center and not from the profile.", h.Reference.OCVersion, h.Candidate.OCVersion))
	}

	for _, s := range []struct {
		name string
		side Side
	}{{"reference", h.Reference}, {"candidate", h.Candidate}} {
		if s.side.FinalState != completedState {
			h.Warnings = append(h.Warnings, fmt.Sprintf("The %s has the final state %q, not %q.", s.name, s.side.FinalState, completedState))
		}

		if s.side.Truncated > 0 {
			h.Warnings = append(h.Warnings, fmt.Sprintf("The %s has %d truncated exchanges. Body based checks skip them.", s.name, s.side.Truncated))
		}
	}

	return h
}

func side(rec *recording.Recording) Side {
	m := rec.Manifest

	s := Side{
		Model:       m.BMCData.ServerModel,
		BMCFirmware: m.BMCData.BMCFirmwareVersion,
		BIOS:        m.BMCData.ServerBIOSVersion,
		OCVersion:   m.OperationsCenterVersion,
		FinalState:  m.FinalState,
		Exchanges:   len(rec.Exchanges),
	}

	for _, e := range rec.Exchanges {
		if e.RequestTruncated || e.ResponseTruncated {
			s.Truncated++
		}
	}

	start, end := bounds(rec)
	s.Duration = max(end.Sub(start), 0)

	return s
}

// bounds returns the start and the end of a recording. It uses the exchanges, if the manifest lacks a time.
func bounds(rec *recording.Recording) (time.Time, time.Time) {
	start, end := rec.Manifest.StartedAt, rec.Manifest.FinishedAt

	if len(rec.Exchanges) > 0 {
		if start.IsZero() {
			start = rec.Exchanges[0].Timestamp
		}

		if end.IsZero() {
			end = rec.Exchanges[len(rec.Exchanges)-1].Timestamp
		}
	}

	return start, end
}
