package recording

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

//go:embed draft.yaml.tmpl
var draftTemplate string

// commentColumn aligns the comments like in the generic profile.
const commentColumn = 36

// maxSeqs bounds the sequence numbers named in one comment.
const maxSeqs = 5

// value is the YAML text of one profile key and the exchanges it is drawn from.
type value struct {
	yaml   string
	seqs   []int64
	verify bool
	note   string

	// fixed marks a key that no recording can show: a setting of the proxy, or a key the BMC does not use.
	fixed bool

	// reason tells why the recording holds no evidence.
	reason string
}

func observed(yaml string, exchanges ...Exchange) value {
	v := value{yaml: yaml}
	for _, e := range exchanges {
		v.seqs = append(v.seqs, e.Sequence)
	}

	return v
}

func inferred(yaml string, exchanges ...Exchange) value {
	v := observed(yaml, exchanges...)
	v.verify = true

	return v
}

func (v value) withNote(format string, args ...any) value {
	v.note = fmt.Sprintf(format, args...)

	return v
}

// addNote appends to the note of the value.
func (v value) addNote(format string, args ...any) value {
	note := fmt.Sprintf(format, args...)
	if v.note != "" {
		note = v.note + "; " + note
	}

	v.note = note

	return v
}

func (v value) comment() string {
	var parts []string

	seqs := slices.Compact(slices.Sorted(slices.Values(v.seqs)))

	switch {
	case len(seqs) == 0 && v.fixed:

	case len(seqs) == 0:
		parts = append(parts, "no evidence")

		if v.reason != "" {
			parts = append(parts, v.reason)
		}

	case len(seqs) > maxSeqs:
		parts = append(parts, fmt.Sprintf("seq %s and %d more", joinSeqs(seqs[:maxSeqs]), len(seqs)-maxSeqs))

	default:
		parts = append(parts, "seq "+joinSeqs(seqs))
	}

	if v.verify {
		parts = append(parts, "TODO verify")
	}

	if v.note != "" {
		parts = append(parts, v.note)
	}

	return strings.Join(parts, "; ")
}

func joinSeqs(seqs []int64) string {
	texts := make([]string, 0, len(seqs))
	for _, seq := range seqs {
		texts = append(texts, strconv.FormatInt(seq, 10))
	}

	return strings.Join(texts, ", ")
}

// Gap is a profile key that keeps its default, because the recording holds no evidence for it.
type Gap struct {
	Key string

	// Reason tells why the recording holds no evidence, empty if the draft does not know.
	Reason string
}

// Draft is a profile drawn from a recording.
type Draft struct {
	name        string
	description string
	header      []string
	values      map[string]value
	attributes  []value
}

// NewDraft draws a profile with the given name from the recording.
func NewDraft(rec *Recording, name string) *Draft {
	d := &Draft{
		name:   name,
		values: defaultValues(),
	}

	d.describe(rec.Manifest)
	d.bios(rec)
	d.secureBoot(rec)
	d.virtualMedia(rec)
	d.power(rec)
	d.responses(rec)

	return d
}

func (d *Draft) set(key string, v value) {
	_, ok := d.values[key]
	if !ok {
		panic(fmt.Sprintf("unknown profile key %q", key))
	}

	d.values[key] = v
}

// explain gives a reason to the keys that still have no evidence.
func (d *Draft) explain(reason string, keys ...string) {
	for _, key := range keys {
		v, ok := d.values[key]
		if !ok {
			panic(fmt.Sprintf("unknown profile key %q", key))
		}

		if len(v.seqs) == 0 && !v.fixed && v.reason == "" {
			v.reason = reason
			d.values[key] = v
		}
	}
}

// describe names the firmware, never the identity of the server.
func (d *Draft) describe(m Manifest) {
	d.description = "Draft from a BMC recording."
	d.header = []string{"Draft profile from a BMC recording. Review every value and resolve each TODO verify."}

	if m.RecordingID != "" {
		d.header = append(d.header, fmt.Sprintf("Recording %s, made by Operations Center %s.", m.RecordingID, m.OperationsCenterVersion))
	}

	var firmware []string

	if m.BMCData.ServerModel != "" {
		firmware = append(firmware, "server model "+m.BMCData.ServerModel)
	}

	if m.BMCData.BMCFirmwareVersion != "" {
		firmware = append(firmware, "BMC firmware "+m.BMCData.BMCFirmwareVersion)
	}

	if m.BMCData.ServerBIOSVersion != "" {
		firmware = append(firmware, "BIOS "+m.BMCData.ServerBIOSVersion)
	}

	if len(firmware) > 0 {
		text := strings.Join(firmware, ", ")
		d.description = fmt.Sprintf("Draft from a BMC recording of %s.", text)
		d.header = append(d.header, "Recorded with "+text+".")
	}
}

// Render returns the draft as a profile file and the keys that have no evidence in the recording.
// Keys that no recording can show are not gaps.
// It decodes the draft as a profile before it returns it, so the draft is always valid.
func (d *Draft) Render() ([]byte, []Gap, error) {
	var gaps []Gap

	funcs := template.FuncMap{
		"quote": quote,
		"field": func(key string) (string, error) {
			v, ok := d.values[key]
			if !ok {
				return "", fmt.Errorf("unknown profile key %q", key)
			}

			if len(v.seqs) == 0 && !v.fixed {
				gaps = append(gaps, Gap{Key: key, Reason: v.reason})
			}

			depth := strings.Count(key, ".")
			name := key[strings.LastIndex(key, ".")+1:]

			return line(depth, name+": "+v.yaml, v.comment()), nil
		},
		"attributes": func() string {
			if len(d.attributes) == 0 {
				return line(2, "attributes: []", "")
			}

			lines := []string{line(2, "attributes:", "")}
			for _, attribute := range d.attributes {
				lines = append(lines, line(3, "- "+attribute.yaml, attribute.comment()))
			}

			return strings.Join(lines, "\n")
		},
	}

	tmpl, err := template.New("draft").Funcs(funcs).Parse(draftTemplate)
	if err != nil {
		return nil, nil, err
	}

	var buf bytes.Buffer

	err = tmpl.Execute(&buf, map[string]any{
		"Name":        d.name,
		"Description": d.description,
		"Header":      d.header,
	})
	if err != nil {
		return nil, nil, err
	}

	_, err = profile.Parse(buf.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("draft is not a valid profile: %w", err)
	}

	return buf.Bytes(), gaps, nil
}

func line(depth int, text string, comment string) string {
	text = strings.Repeat("  ", depth) + text
	if comment == "" {
		return text
	}

	return fmt.Sprintf("%-*s # %s", commentColumn-1, text, comment)
}

// quote returns a YAML double quoted string. JSON strings are valid YAML.
func quote(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}

	return string(data)
}

func yamlBool(b bool) string {
	return strconv.FormatBool(b)
}

func yamlList(items []string) string {
	return "[" + strings.Join(items, ", ") + "]"
}

func yamlError(status int, messageID string) string {
	return fmt.Sprintf("{status: %d, message_id: %s}", status, messageID)
}

// defaultValues are the values of the generic profile.
func defaultValues() map[string]value {
	return map[string]value{
		"bios.patch_response":                    {yaml: string(profile.PatchResponseTaskMonitor)},
		"bios.task_resource":                     {yaml: "false"},
		"bios.settings_object":                   {yaml: "false"},
		"bios.pending":                           {yaml: "false"},
		"bios.apply_time.supported":              {yaml: "[]"},
		"bios.apply_time.default":                {yaml: `""`},
		"bios.patch_requires_power_off":          {yaml: "true"},
		"bios.registry.id":                       {yaml: `""`},
		"bios.errors.unknown_attribute":          {yaml: yamlError(400, "Base.1.18.PropertyUnknown")},
		"bios.errors.deferred_rejected":          {yaml: yamlError(400, "Base.1.18.PropertyUnknown")},
		"secure_boot.database_write":             {yaml: "true"},
		"secure_boot.reset_keys.types":           {yaml: yamlList([]string{string(profile.ResetKeysTypeDeleteAllKeys), string(profile.ResetKeysTypeDeletePK)})},
		"virtual_media.insert":                   {yaml: string(profile.MediaInsertStandard)},
		"virtual_media.oem_vendor":               {yaml: "Incus", fixed: true, note: "proxy setting, never a real vendor"},
		"virtual_media.oem_action":               {yaml: "InsertVirtualMedia"},
		"virtual_media.insert_response":          {yaml: quote(string(profile.InsertResponseNoContent))},
		"virtual_media.boot_override_once":       {yaml: "false"},
		"virtual_media.refused_parameters.names": {yaml: "[]"},
		"virtual_media.refused_parameters.error": {yaml: yamlError(400, "Base.1.18.ActionParameterUnknown")},
		"virtual_media.locations":                {yaml: yamlList([]string{string(profile.MediaLocationManagers)})},
		"virtual_media.id":                       {yaml: quote("CD")},
		"virtual_media.delivery": {
			yaml:   string(profile.DeliveryFullFetch),
			verify: true,
			note:   "range requests against the media URL are not recorded",
		},
		"virtual_media.stream.read_ahead":             {yaml: "4MiB", fixed: true, note: "proxy setting"},
		"virtual_media.stream.cache":                  {yaml: "64MiB", fixed: true, note: "proxy setting"},
		"virtual_media.stream.fallback":               {yaml: string(profile.StreamFallbackFullFetch), fixed: true, note: "proxy setting"},
		"secure_boot.database_write_error":            {yaml: yamlError(405, "Base.1.18.ActionNotSupported")},
		"secure_boot.reset_keys.supported":            {yaml: "false"},
		"secure_boot.reset_keys.response":             {yaml: quote(string(profile.ResetKeysResponseNoContent))},
		"secure_boot.reset_keys.applies":              {yaml: string(profile.ResetKeysAppliesImmediately)},
		"secure_boot.reset_keys.disables_secure_boot": {yaml: "false"},
		"secure_boot.reset_keys.unreachable.after":    {yaml: "0s"},
		"secure_boot.reset_keys.unreachable.for":      {yaml: "0s"},
		"secure_boot.power_cycle.after":               {yaml: "[]"},
		"secure_boot.power_cycle.off_after":           {yaml: "0s"},
		"secure_boot.power_cycle.off_for":             {yaml: "0s"},
		"secure_boot.empty_after_power_on.times":      {yaml: "0"},
		"secure_boot.empty_after_power_on.for":        {yaml: "0s"},
		"secure_boot.database_add_response":           {yaml: quote(string(profile.DatabaseAddResponseCreated))},
		"secure_boot.database_delete_response":        {yaml: quote(string(profile.DatabaseDeleteResponseNoContent))},
		"secure_boot.certificate_id_style":            {yaml: string(profile.CertificateIDStyleHash)},
		"secure_boot.certificate_id_prefix":           {yaml: `""`},
		"secure_boot.enable_requires_power_off":       {yaml: "true"},
		"power.reset_types":                           {yaml: "[]"},
		"power.response":                              {yaml: quote(string(profile.PowerResponseNoContent))},
		"power.not_settled_window":                    {yaml: "0s"},
		"power.not_settled_error":                     {yaml: yamlError(400, "Base.1.18.ResourceInStandby")},
		"power.delay":                                 {yaml: "0s"},
		"power.redundant_reset_errors.off":            {yaml: "null"},
		"power.redundant_reset_errors.on":             {yaml: "null"},
		"power.busy_reads.resources":                  {yaml: "[]"},
		"power.busy_reads.failures":                   {yaml: "0"},
		"power.busy_reads.error":                      {yaml: yamlError(503, "Base.1.18.ServiceTemporarilyUnavailable")},
		"responses.patch":                             {yaml: quote(string(profile.ResourcePatchResponseNoContent))},
		"timing.scale":                                {yaml: "1.0", fixed: true, note: "proxy setting"},
	}
}

// DraftStatus tells where the drawn value of a profile key comes from.
type DraftStatus string

const (
	// DraftObserved is a value the recording shows.
	DraftObserved DraftStatus = "observed"

	// DraftInferred is a value drawn from indirect evidence. A person must verify it.
	DraftInferred DraftStatus = "inferred"

	// DraftFixed is a key that no recording can show, like a proxy setting.
	DraftFixed DraftStatus = "fixed"

	// DraftGap is a key that keeps its default, because the recording holds no evidence.
	DraftGap DraftStatus = "gap"
)

// AttributesKey is the key of the synthetic BIOS attributes in the result of Values.
const AttributesKey = "bios.registry.attributes"

// DraftValue is the drawn value of one profile key.
type DraftValue struct {
	YAML   string
	Seqs   []int64
	Status DraftStatus
}

// Values returns the drawn value of every profile key.
// The synthetic BIOS attributes are one key, AttributesKey.
func (d *Draft) Values() map[string]DraftValue {
	values := make(map[string]DraftValue, len(d.values)+1)

	for key, v := range d.values {
		values[key] = DraftValue{YAML: v.yaml, Seqs: slices.Compact(slices.Sorted(slices.Values(v.seqs))), Status: v.status()}
	}

	attributes := value{}

	items := make([]string, 0, len(d.attributes))
	for _, attribute := range d.attributes {
		items = append(items, attribute.yaml)
		attributes.seqs = append(attributes.seqs, attribute.seqs...)
		attributes.verify = attributes.verify || attribute.verify
	}

	values[AttributesKey] = DraftValue{YAML: yamlList(items), Seqs: slices.Compact(slices.Sorted(slices.Values(attributes.seqs))), Status: attributes.status()}

	return values
}

func (v value) status() DraftStatus {
	switch {
	case v.fixed:
		return DraftFixed

	case len(v.seqs) == 0:
		return DraftGap

	case v.verify:
		return DraftInferred
	}

	return DraftObserved
}
