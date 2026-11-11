package compare

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

// Differences of a key row.
const (
	// DiffReferenceProfile is a modeling decision. The README of the profile must list it.
	DiffReferenceProfile = "reference draft ≠ profile.yaml"

	// DiffCandidateProfile is a bug in the proxy, or a draft rule that does not understand the proxy answers.
	DiffCandidateProfile = "candidate draft ≠ profile.yaml"

	// DiffReferenceCandidate is behavior that Operations Center sees differently.
	DiffReferenceCandidate = "reference draft ≠ candidate draft"
)

const registryIDKey = "bios.registry.id"

// pollBoundKeys are drawn from polls. A draft only tells whether a poll saw the change.
// So the drafts compare by that, and the value of a profile, which is a setting, does not compare to them.
var pollBoundKeys = []string{
	"power.delay",
	"secure_boot.power_cycle.off_after",
	"secure_boot.power_cycle.off_for",
	"secure_boot.empty_after_power_on.for",
	"secure_boot.reset_keys.unreachable.after",
	"secure_boot.reset_keys.unreachable.for",
}

// KeySide is the drawn value of a profile key on one side.
type KeySide struct {
	Value  string
	Status recording.DraftStatus
	Seqs   []int64
}

// KeyRow compares one profile key.
type KeyRow struct {
	Key       string
	Reference KeySide
	Candidate KeySide

	// Profile is the value of the profile.yaml, empty without a profile.
	Profile     string
	Differences []string

	// Counted tells whether the row counts for the parity. A gap or a fixed key on either side is not counted.
	Counted bool
}

// drawn is a draft as values per key and as a parsed profile.
type drawn struct {
	values  map[string]recording.DraftValue
	profile map[string]any
}

func draw(rec *recording.Recording) (drawn, error) {
	draft := recording.NewDraft(rec, "draft")

	data, _, err := draft.Render()
	if err != nil {
		return drawn{}, err
	}

	p, err := profile.Parse(data)
	if err != nil {
		return drawn{}, err
	}

	tree, err := profileTree(p)
	if err != nil {
		return drawn{}, err
	}

	return drawn{values: draft.Values(), profile: tree}, nil
}

// profileTree marshals the profile and parses it back, so all values compare in their YAML form.
func profileTree(p profile.Profile) (map[string]any, error) {
	data, err := yaml.Marshal(p)
	if err != nil {
		return nil, err
	}

	var tree map[string]any

	err = yaml.Unmarshal(data, &tree)
	if err != nil {
		return nil, err
	}

	return tree, nil
}

// pollSeen reduces the durations of a draft to whether a poll saw them, which is a duration above 0.
func pollSeen(key string, value any) any {
	if !slices.Contains(pollBoundKeys, key) {
		return value
	}

	switch v := value.(type) {
	case string:
		return v != "0s"

	case map[string]any:
		seen := map[string]any{}
		for name, duration := range v {
			seen[name] = duration != "0s"
		}

		return seen
	}

	return value
}

// lookup returns the value of a dotted key.
func lookup(tree map[string]any, key string) any {
	var node any = tree

	for part := range strings.SplitSeq(key, ".") {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}

		node = m[part]
	}

	switch key {
	case recording.AttributesKey:
		return attributeShapes(node)

	case registryIDKey:
		// Drafts never copy the vendor registry ID, so only its presence matters.
		return node != nil && node != ""
	}

	return node
}

// attributeShapes reduces the synthetic BIOS attributes to their types and requirements. The names are synthetic by design.
func attributeShapes(node any) any {
	list, ok := node.([]any)
	if !ok {
		return []string{}
	}

	shapes := make([]string, 0, len(list))

	for _, item := range list {
		attribute, ok := item.(map[string]any)
		if !ok {
			continue
		}

		shape := fmt.Sprint(attribute["type"])
		if attribute["requires"] != nil {
			shape += " requires"
		}

		shapes = append(shapes, shape)
	}

	slices.Sort(shapes)

	return shapes
}

func display(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	return string(data)
}

// keyRows compares the profile keys drawn from both recordings. It returns a warning, if a draft fails.
func keyRows(reference *recording.Recording, candidate *recording.Recording, p *profile.Profile) ([]KeyRow, Parity, string) {
	parity := Parity{Label: "Keys", Unit: "profile keys equal in both drafts"}

	ref, err := draw(reference)
	if err != nil {
		return nil, parity, "The keys section is empty, the draft of the reference failed: " + err.Error()
	}

	cand, err := draw(candidate)
	if err != nil {
		return nil, parity, "The keys section is empty, the draft of the candidate failed: " + err.Error()
	}

	var profileTreeValue map[string]any

	if p != nil {
		profileTreeValue, err = profileTree(*p)
		if err != nil {
			return nil, parity, "The profile column is empty, the profile does not marshal: " + err.Error()
		}
	}

	var rows []KeyRow

	for _, key := range slices.Sorted(maps.Keys(ref.values)) {
		refValue, candValue := ref.values[key], cand.values[key]

		// A draft fixes a key that no recording can show, or that the BMC does not use.
		if refValue.Status == recording.DraftFixed && candValue.Status == recording.DraftFixed {
			continue
		}

		refEvidence, candEvidence := hasEvidence(refValue.Status), hasEvidence(candValue.Status)

		refTree, candTree := lookup(ref.profile, key), lookup(cand.profile, key)

		row := KeyRow{
			Key:       key,
			Reference: KeySide{Value: display(refTree), Status: refValue.Status, Seqs: refValue.Seqs},
			Candidate: KeySide{Value: display(candTree), Status: candValue.Status, Seqs: candValue.Seqs},
			Counted:   refEvidence && candEvidence,
		}

		if profileTreeValue != nil {
			profileValue := lookup(profileTreeValue, key)
			row.Profile = display(profileValue)

			comparable := !slices.Contains(pollBoundKeys, key)

			if comparable && refEvidence && !reflect.DeepEqual(refTree, profileValue) {
				row.Differences = append(row.Differences, DiffReferenceProfile)
			}

			if comparable && candEvidence && !reflect.DeepEqual(candTree, profileValue) {
				row.Differences = append(row.Differences, DiffCandidateProfile)
			}
		}

		equal := reflect.DeepEqual(pollSeen(key, refTree), pollSeen(key, candTree))
		if !equal {
			row.Differences = append(row.Differences, DiffReferenceCandidate)
		}

		if row.Counted {
			parity.Total++

			if equal {
				parity.Both++
			}
		}

		rows = append(rows, row)
	}

	return rows, parity, ""
}

func hasEvidence(status recording.DraftStatus) bool {
	return status == recording.DraftObserved || status == recording.DraftInferred
}
