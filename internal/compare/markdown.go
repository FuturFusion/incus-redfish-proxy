package compare

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// maxSeqs bounds the sequence numbers named in one detail line.
const maxSeqs = 5

// shortDifferences are the labels of the differences in the key table. The details below the table use the full text.
var shortDifferences = map[string]string{
	DiffReferenceProfile:   "ref≠profile",
	DiffCandidateProfile:   "cand≠profile",
	DiffReferenceCandidate: "ref≠cand",
}

// Markdown renders the report. Verbose adds the aligned runs of every step.
// The tables hold a summary with aligned columns, so the report is readable without a Markdown renderer.
// The details follow below each table.
func (r Report) Markdown(verbose bool) []byte {
	var b strings.Builder

	b.WriteString("# Recording comparison\n\n")
	b.WriteString("## Summary\n\n")

	for _, p := range r.Parities.List() {
		b.WriteString("- " + p.String() + "\n")
	}

	r.writeHeader(&b)
	r.writeSteps(&b)
	r.writeOperations(&b, verbose)
	r.writeErrors(&b)
	r.writeKeys(&b)
	r.writeTiming(&b)

	return []byte(b.String())
}

func (r Report) writeHeader(b *strings.Builder) {
	ref, cand := r.Header.Reference, r.Header.Candidate

	b.WriteString("\n## Header\n\n")
	table(b, []string{"", "Reference", "Candidate"}, [][]string{
		{"Server model", ref.Model, cand.Model},
		{"BMC firmware", ref.BMCFirmware, cand.BMCFirmware},
		{"BIOS", ref.BIOS, cand.BIOS},
		{"Operations Center", ref.OCVersion, cand.OCVersion},
		{"Final state", ref.FinalState, cand.FinalState},
		{"Exchanges", strconv.Itoa(ref.Exchanges), strconv.Itoa(cand.Exchanges)},
		{"Truncated exchanges", strconv.Itoa(ref.Truncated), strconv.Itoa(cand.Truncated)},
		{"Duration", duration(ref.Duration), duration(cand.Duration)},
	})

	if len(r.Header.Warnings) > 0 {
		b.WriteString("\nWarnings:\n\n")

		for _, w := range r.Header.Warnings {
			b.WriteString("- " + w + "\n")
		}
	}
}

func (r Report) writeSteps(b *strings.Builder) {
	b.WriteString("\n## Deployment steps\n\n")
	b.WriteString(r.Parities.Steps.String() + "\n")
	b.WriteString(r.Parities.Transitions.String() + "\n\n")

	rows := make([][]string, 0, len(r.Steps))
	for _, s := range r.Steps {
		rows = append(rows, []string{s.State, entered(s.Reference), entered(s.Candidate), strings.Join(s.Flags, ", ")})
	}

	table(b, []string{"State", "Ref", "Cand", "Flags"}, rows)

	var details []string

	for _, s := range r.Steps {
		if s.Reference.Retries == 0 && s.Candidate.Retries == 0 && s.Reference.Error == "" && s.Candidate.Error == "" {
			continue
		}

		lines := []string{"- " + code(s.State) + ":"}
		if s.Reference == s.Candidate {
			lines = append(lines, stepDetail("both", s.Reference)...)
		} else {
			lines = append(lines, stepDetail("reference", s.Reference)...)
			lines = append(lines, stepDetail("candidate", s.Candidate)...)
		}

		details = append(details, strings.Join(lines, "\n"))
	}

	writeDetails(b, "Retries and errors of the steps:", details)

	transitions := make([]string, 0, len(r.Transitions.ReferenceOnly)+len(r.Transitions.CandidateOnly))
	for _, t := range r.Transitions.ReferenceOnly {
		transitions = append(transitions, "- reference only: "+t)
	}

	for _, t := range r.Transitions.CandidateOnly {
		transitions = append(transitions, "- candidate only: "+t)
	}

	writeDetails(b, "State transitions on one side only:", transitions)
}

func stepDetail(name string, s StepSide) []string {
	if !s.Entered {
		return nil
	}

	lines := []string{fmt.Sprintf("  - %s: retries %d", name, s.Retries)}
	if s.Error != "" {
		lines = append(lines, "    - error: "+s.Error)
	}

	return lines
}

func (r Report) writeOperations(b *strings.Builder, verbose bool) {
	b.WriteString("\n## Operations per step\n\n")
	b.WriteString(r.Parities.Operations.String() + "\n\n")
	b.WriteString("The kind columns count pairs of method and kind. The run columns count runs and exchanges.\n")
	b.WriteString("Only steps with exchanges on both sides count for the parity.\n\n")

	rows := make([][]string, 0, len(r.Operations))
	for _, s := range r.Operations {
		rows = append(rows, []string{
			s.State,
			strconv.Itoa(len(s.Both)),
			strconv.Itoa(len(s.ReferenceOnly)),
			strconv.Itoa(len(s.CandidateOnly)),
			fmt.Sprintf("%d / %d", s.Reference.Runs, s.Reference.Exchanges),
			fmt.Sprintf("%d / %d", s.Candidate.Runs, s.Candidate.Exchanges),
		})
	}

	table(b, []string{"State", "Both", "Ref only", "Cand only", "Ref runs", "Cand runs"}, rows)

	var details []string

	for _, s := range r.Operations {
		if len(s.ReferenceOnly) == 0 && len(s.CandidateOnly) == 0 {
			continue
		}

		lines := []string{"- " + code(s.State) + ":"}
		for _, k := range s.ReferenceOnly {
			lines = append(lines, "  - reference only: "+k)
		}

		for _, k := range s.CandidateOnly {
			lines = append(lines, "  - candidate only: "+k)
		}

		details = append(details, strings.Join(lines, "\n"))
	}

	writeDetails(b, "Operations on one side only:", details)

	if !verbose {
		return
	}

	for _, s := range r.Operations {
		b.WriteString("\n### " + s.State + "\n\n")

		aligned := make([][]string, 0, len(s.Aligned))
		for _, a := range s.Aligned {
			aligned = append(aligned, []string{run(a.Reference), run(a.Candidate)})
		}

		table(b, []string{"Reference", "Candidate"}, aligned)
	}
}

func (r Report) writeErrors(b *strings.Builder) {
	b.WriteString("\n## Errors and retries\n\n")
	b.WriteString(r.Parities.Errors.String() + "\n\n")

	rows := make([][]string, 0, len(r.Errors))
	for _, e := range r.Errors {
		flag := ""

		switch {
		case e.Candidate == 0:
			flag = "reference only"

		case e.Reference == 0:
			flag = "candidate only"
		}

		rows = append(rows, []string{e.State, e.Operation, e.Outcome, strconv.Itoa(e.Reference), strconv.Itoa(e.Candidate), flag})
	}

	table(b, []string{"State", "Operation", "Outcome", "Ref", "Cand", "Flags"}, rows)
}

func (r Report) writeKeys(b *strings.Builder) {
	b.WriteString("\n## Profile keys\n\n")
	b.WriteString(r.Parities.Keys.String() + "\n\n")

	rows := make([][]string, 0, len(r.Keys))
	for _, k := range r.Keys {
		labels := make([]string, 0, len(k.Differences))
		for _, d := range k.Differences {
			labels = append(labels, shortDifferences[d])
		}

		rows = append(rows, []string{k.Key, string(k.Reference.Status), string(k.Candidate.Status), strings.Join(labels, ", ")})
	}

	table(b, []string{"Key", "Ref", "Cand", "Differences"}, rows)

	details := make([]string, 0, len(r.Keys))
	for _, k := range r.Keys {
		details = append(details, r.keyDetail(k))
	}

	writeDetails(b, "Values and sequence numbers of the keys:", details)
}

func (r Report) keyDetail(k KeyRow) string {
	lines := []string{"- " + code(k.Key) + ":"}

	if k.Reference.Value == k.Candidate.Value {
		lines = append(lines, "  - both drafts: "+code(k.Reference.Value))
	} else {
		lines = append(lines,
			"  - reference draft: "+code(k.Reference.Value),
			"  - candidate draft: "+code(k.Candidate.Value),
		)
	}

	if r.HasProfile {
		lines = append(lines, "  - profile.yaml: "+code(k.Profile))
	}

	if len(k.Reference.Seqs) > 0 {
		lines = append(lines, "  - reference seq: "+seqs(k.Reference.Seqs))
	}

	if len(k.Candidate.Seqs) > 0 {
		lines = append(lines, "  - candidate seq: "+seqs(k.Candidate.Seqs))
	}

	for _, d := range k.Differences {
		lines = append(lines, "  - "+d)
	}

	return strings.Join(lines, "\n")
}

func (r Report) writeTiming(b *strings.Builder) {
	b.WriteString("\n## Timing\n\n")
	b.WriteString("Informational only. The VM boots faster than a real server, and the delays of the proxy are settings.\n\n")

	rows := make([][]string, 0, len(r.Timing))
	for _, t := range r.Timing {
		ratio := ""
		if t.Ratio > 0 {
			ratio = strconv.FormatFloat(t.Ratio, 'f', 2, 64)
		}

		rows = append(rows, []string{
			t.State,
			stepDuration(StepSide{Entered: t.ReferenceEntered, Duration: t.Reference}),
			stepDuration(StepSide{Entered: t.CandidateEntered, Duration: t.Candidate}),
			ratio,
		})
	}

	table(b, []string{"State", "Reference", "Candidate", "Ratio"}, rows)
}

func writeDetails(b *strings.Builder, title string, details []string) {
	if len(details) == 0 {
		return
	}

	b.WriteString("\n" + title + "\n\n")

	for _, d := range details {
		b.WriteString(d + "\n")
	}
}

// table writes a table with aligned columns.
func table(b *strings.Builder, header []string, rows [][]string) {
	if len(rows) == 0 {
		b.WriteString("None.\n")

		return
	}

	cells := make([][]string, 0, len(rows)+1)
	for _, row := range append([][]string{header}, rows...) {
		escaped := make([]string, 0, len(row))
		for _, cell := range row {
			cell = strings.ReplaceAll(cell, "|", `\|`)
			escaped = append(escaped, strings.ReplaceAll(cell, "\n", " "))
		}

		cells = append(cells, escaped)
	}

	widths := make([]int, len(header))
	for _, row := range cells {
		for i, cell := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell), 3)
		}
	}

	separator := make([]string, len(header))
	for i, width := range widths {
		separator[i] = strings.Repeat("-", width)
	}

	writeRow(b, cells[0], widths)
	writeRow(b, separator, widths)

	for _, row := range cells[1:] {
		writeRow(b, row, widths)
	}
}

func writeRow(b *strings.Builder, cells []string, widths []int) {
	b.WriteString("|")

	for i, cell := range cells {
		b.WriteString(" " + cell + strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell)) + " |")
	}

	b.WriteString("\n")
}

func duration(d time.Duration) string {
	return d.Round(time.Second).String()
}

func entered(s StepSide) string {
	if s.Entered {
		return "yes"
	}

	return "no"
}

func stepDuration(s StepSide) string {
	if !s.Entered {
		return ""
	}

	return duration(s.Duration)
}

func run(r *Run) string {
	if r == nil {
		return ""
	}

	return r.String()
}

func code(s string) string {
	if s == "" {
		return ""
	}

	return "`" + s + "`"
}

func seqs(values []int64) string {
	texts := make([]string, 0, min(len(values), maxSeqs))
	for _, v := range values[:min(len(values), maxSeqs)] {
		texts = append(texts, strconv.FormatInt(v, 10))
	}

	text := strings.Join(texts, ", ")
	if len(values) > maxSeqs {
		text += fmt.Sprintf(" and %d more", len(values)-maxSeqs)
	}

	return text
}
