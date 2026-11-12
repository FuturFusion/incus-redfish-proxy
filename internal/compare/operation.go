package compare

import (
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

const transportError = "transport error"

// taskMonitorKind is the kind of a path that a 202 answer names in its Location header.
// Vendors use different paths and bodies for it, for example /redfish/v1/TaskService/Tasks/JID_1 or /redfish/v1/TaskMonitors/1.
const taskMonitorKind = "TaskMonitor"

// versionPattern matches the registry version in a message ID like IDRAC.2.8.SYS518.
var versionPattern = regexp.MustCompile(`\.\d+(\.\d+)*\.`)

// idParents are the path segments, whose next segment is an ID that differs between vendors.
var idParents = map[string]bool{
	"systems":      true,
	"managers":     true,
	"virtualmedia": true,
	"processors":   true,
	"tasks":        true,
	"jobs":         true,
	"certificates": true,
	"signatures":   true,
}

// Operation is an exchange without the vendor specific parts.
type Operation struct {
	// State is the deployment state of the exchange.
	State  string
	Method string

	// Kind is the resource kind, for example ComputerSystem, Bios or VirtualMedia.InsertMedia.
	Kind string

	// Outcome is the status and the message ID, for example "400 Base.PropertyUnknown", or "transport error".
	Outcome string
}

// methodKind is the pair of method and kind, for example "GET ComputerSystem".
func (o Operation) methodKind() string {
	return o.Method + " " + o.Kind
}

// String returns the operation without its state.
func (o Operation) String() string {
	return o.methodKind() + " " + o.Outcome
}

// Run is a sequence of equal operations of one step. A run with more than one operation is a poll.
type Run struct {
	Operation
	Count int
}

// String returns the run, for example "GET ComputerSystem 200 ×12".
func (r Run) String() string {
	if r.Count == 1 {
		return r.Operation.String()
	}

	return r.Operation.String() + " ×" + strconv.Itoa(r.Count)
}

// operations normalizes every exchange to an operation.
func operations(rec *recording.Recording) []Operation {
	types := resourceTypes(rec)

	for path := range taskMonitors(rec) {
		types[path] = taskMonitorKind
	}

	ops := make([]Operation, 0, len(rec.Exchanges))
	for _, e := range rec.Exchanges {
		ops = append(ops, Operation{
			State:   e.DeploymentState,
			Method:  e.Method,
			Kind:    kind(e, types),
			Outcome: outcome(e),
		})
	}

	return ops
}

// resourceTypes maps each path to the type of its resource. A PATCH or a 204 answer has no type, so the GET of the same path gives it.
func resourceTypes(rec *recording.Recording) map[string]string {
	types := map[string]string{}

	for _, e := range rec.Exchanges {
		if !e.Succeeded() || e.ResponseTruncated {
			continue
		}

		var body struct {
			Type string `json:"@odata.type"`
		}

		err := e.ResponseJSON(&body)
		if err != nil || body.Type == "" {
			continue
		}

		_, ok := types[e.Path()]
		if !ok {
			types[e.Path()] = typeName(body.Type)
		}
	}

	return types
}

// taskMonitors returns the paths that 202 answers name in their Location header, in the form of Exchange.Path.
func taskMonitors(rec *recording.Recording) map[string]bool {
	paths := map[string]bool{}

	for _, e := range rec.Exchanges {
		if e.Status != http.StatusAccepted {
			continue
		}

		location, err := url.Parse(e.ResponseHeader("Location"))
		if err != nil || location.Path == "" {
			continue
		}

		paths[strings.TrimSuffix(strings.ToLower(location.Path), "/")] = true
	}

	return paths
}

// typeName removes the namespace and its version: #ComputerSystem.v1_13_0.ComputerSystem becomes ComputerSystem.
func typeName(odataType string) string {
	name, _, _ := strings.Cut(strings.TrimPrefix(odataType, "#"), ".")

	return name
}

func kind(e recording.Exchange, types map[string]string) string {
	action, ok := actionKind(e)
	if ok {
		return action
	}

	name, ok := types[e.Path()]
	if ok {
		return name
	}

	return pathTemplate(e.Path())
}

// actionKind returns the action segment of the path. OEM actions lose their vendor:
// Oem/Hpe/HpeiLOVirtualMedia.InsertVirtualMedia and Oem/Incus.InsertVirtualMedia become Oem.InsertVirtualMedia.
func actionKind(e recording.Exchange) (string, bool) {
	u, err := url.Parse(e.URL)
	if err != nil {
		return "", false
	}

	segments := strings.Split(strings.Trim(u.Path, "/"), "/")

	i := slices.IndexFunc(segments, func(s string) bool { return strings.EqualFold(s, "actions") })
	if i < 0 || i+1 >= len(segments) {
		return "", false
	}

	if strings.EqualFold(segments[i+1], "oem") {
		last := segments[len(segments)-1]

		return "Oem." + last[strings.LastIndex(last, ".")+1:], true
	}

	return segments[i+1], true
}

// pathTemplate replaces the IDs in the lower case path by {id}.
func pathTemplate(path string) string {
	segments := strings.Split(path, "/")

	for i := 1; i < len(segments); i++ {
		if idParents[segments[i-1]] {
			segments[i] = "{id}"
		}
	}

	return strings.Join(segments, "/")
}

// outcome returns the status and the first message ID without its registry version.
func outcome(e recording.Exchange) string {
	if e.TransportError != "" || e.Status == 0 {
		return transportError
	}

	status := strconv.Itoa(e.Status)

	id := messageID(e)
	if id == "" {
		return status
	}

	return status + " " + versionPattern.ReplaceAllString(id, ".")
}

type message struct {
	MessageID string `json:"MessageId"`
}

func messageID(e recording.Exchange) string {
	var body struct {
		Error struct {
			Code         string    `json:"code"`
			ExtendedInfo []message `json:"@Message.ExtendedInfo"`
		} `json:"error"`
		ExtendedInfo []message `json:"@Message.ExtendedInfo"`
	}

	err := e.ResponseJSON(&body)
	if err != nil {
		return ""
	}

	switch {
	case len(body.Error.ExtendedInfo) > 0:
		return body.Error.ExtendedInfo[0].MessageID

	case len(body.ExtendedInfo) > 0:
		return body.ExtendedInfo[0].MessageID
	}

	return body.Error.Code
}

// runs collapses consecutive equal operations.
func runs(ops []Operation) []Run {
	var result []Run

	for _, op := range ops {
		if len(result) > 0 && result[len(result)-1].Operation == op {
			result[len(result)-1].Count++

			continue
		}

		result = append(result, Run{Operation: op, Count: 1})
	}

	return result
}

// RunCount counts the runs and the exchanges of one side of a step.
type RunCount struct {
	Runs      int
	Exchanges int
}

// AlignedRun pairs a run of the reference with a run of the candidate. A side is nil, if it lacks the run.
type AlignedRun struct {
	Reference *Run
	Candidate *Run
}

// StepOperations compares the operations of one deployment step.
type StepOperations struct {
	State string

	// Both, ReferenceOnly and CandidateOnly hold pairs of method and kind, for example "GET ComputerSystem".
	Both          []string
	ReferenceOnly []string
	CandidateOnly []string

	Reference RunCount
	Candidate RunCount
	Aligned   []AlignedRun

	// Counted tells whether the step counts for the parity. A step with exchanges on one side only already counts in the steps section.
	Counted bool
}

func stepOperations(reference *recording.Recording, candidate *recording.Recording, steps []StepRow) ([]StepOperations, Parity) {
	referenceOps := byState(operations(reference))
	candidateOps := byState(operations(candidate))

	var order []string

	seen := map[string]bool{}
	add := func(state string) {
		if !seen[state] {
			seen[state] = true
			order = append(order, state)
		}
	}

	for _, row := range steps {
		add(row.State)
	}

	for _, e := range reference.Exchanges {
		add(e.DeploymentState)
	}

	for _, e := range candidate.Exchanges {
		add(e.DeploymentState)
	}

	p := Parity{Label: "Operations", Unit: "method and kind pairs on both sides"}

	var result []StepOperations

	for _, state := range order {
		refOps, candOps := referenceOps[state], candidateOps[state]
		if len(refOps) == 0 && len(candOps) == 0 {
			continue
		}

		s := StepOperations{State: state, Counted: len(refOps) > 0 && len(candOps) > 0}

		refKinds, candKinds := methodKinds(refOps), methodKinds(candOps)
		for _, k := range slices.Sorted(maps.Keys(union(refKinds, candKinds))) {
			switch {
			case refKinds[k] && candKinds[k]:
				s.Both = append(s.Both, k)

			case refKinds[k]:
				s.ReferenceOnly = append(s.ReferenceOnly, k)

			default:
				s.CandidateOnly = append(s.CandidateOnly, k)
			}
		}

		refRuns, candRuns := runs(refOps), runs(candOps)
		s.Reference = RunCount{Runs: len(refRuns), Exchanges: len(refOps)}
		s.Candidate = RunCount{Runs: len(candRuns), Exchanges: len(candOps)}

		for _, m := range align(refRuns, candRuns, func(a, b Run) bool { return a.Operation == b.Operation }) {
			var aligned AlignedRun

			if m.reference >= 0 {
				aligned.Reference = &refRuns[m.reference]
			}

			if m.candidate >= 0 {
				aligned.Candidate = &candRuns[m.candidate]
			}

			s.Aligned = append(s.Aligned, aligned)
		}

		if s.Counted {
			p.Both += len(s.Both)
			p.Total += len(s.Both) + len(s.ReferenceOnly) + len(s.CandidateOnly)
		}

		result = append(result, s)
	}

	return result, p
}

func byState(ops []Operation) map[string][]Operation {
	states := map[string][]Operation{}
	for _, op := range ops {
		states[op.State] = append(states[op.State], op)
	}

	return states
}

func methodKinds(ops []Operation) map[string]bool {
	kinds := map[string]bool{}
	for _, op := range ops {
		kinds[op.methodKind()] = true
	}

	return kinds
}
