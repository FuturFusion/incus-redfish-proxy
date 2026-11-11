package compare

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTableAlignsColumns(t *testing.T) {
	t.Parallel()

	// Setup
	var b strings.Builder

	// Run test
	table(&b, []string{"Key", "Differences"}, [][]string{{"a|b", "ref≠cand"}, {"long key", ""}})

	// Assert
	want := "| Key      | Differences |\n" +
		"| -------- | ----------- |\n" +
		"| a\\|b     | ref≠cand    |\n" +
		"| long key |             |\n"
	require.Equal(t, want, b.String(), "padded columns, escaped pipes, runes counted as one column")
}

func TestStepDetails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  StepRow

		want string
	}{
		{
			name: "equal on both sides",
			row: StepRow{
				State:     "wait",
				Reference: StepSide{Entered: true, Error: "timeout"},
				Candidate: StepSide{Entered: true, Error: "timeout"},
			},
			want: "- `wait`:\n  - both: retries 0\n    - error: timeout\n",
		},
		{
			name: "different sides",
			row: StepRow{
				State:     "power-off",
				Reference: StepSide{Entered: true, Retries: 2},
				Candidate: StepSide{Entered: true, Error: "reverted"},
			},
			want: "- `power-off`:\n  - reference: retries 2\n  - candidate: retries 0\n    - error: reverted\n",
		},
		{
			name: "one side only",
			row: StepRow{
				State:     "apply-bios",
				Reference: StepSide{Entered: true, Retries: 1},
			},
			want: "- `apply-bios`:\n  - reference: retries 1\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			var b strings.Builder

			r := Report{Steps: []StepRow{tc.row}}

			// Run test
			r.writeSteps(&b)

			// Assert
			require.Contains(t, b.String(), "Retries and errors of the steps:\n\n"+tc.want, "step details")
		})
	}
}
