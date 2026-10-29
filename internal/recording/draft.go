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

func (v value) comment() string {
	var parts []string

	seqs := slices.Compact(slices.Sorted(slices.Values(v.seqs)))

	switch {
	case len(seqs) == 0:
		parts = append(parts, "no evidence")

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

	return d
}

func (d *Draft) set(key string, v value) {
	_, ok := d.values[key]
	if !ok {
		panic(fmt.Sprintf("unknown profile key %q", key))
	}

	d.values[key] = v
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
// It decodes the draft as a profile before it returns it, so the draft is always valid.
func (d *Draft) Render() ([]byte, []string, error) {
	var noEvidence []string

	funcs := template.FuncMap{
		"quote": quote,
		"field": func(key string) (string, error) {
			v, ok := d.values[key]
			if !ok {
				return "", fmt.Errorf("unknown profile key %q", key)
			}

			if len(v.seqs) == 0 {
				noEvidence = append(noEvidence, key)
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

	return buf.Bytes(), noEvidence, nil
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
		"bios.patch_response":              {yaml: string(profile.PatchResponseTaskMonitor)},
		"bios.task_resource":               {yaml: "false"},
		"bios.settings_object":             {yaml: "false"},
		"bios.pending":                     {yaml: "false"},
		"bios.apply_time.supported":        {yaml: "[]"},
		"bios.apply_time.default":          {yaml: `""`},
		"bios.patch_requires_power_off":    {yaml: "true"},
		"bios.registry.id":                 {yaml: `""`},
		"bios.errors.unknown_attribute":    {yaml: yamlError(400, "Base.1.18.PropertyUnknown")},
		"bios.errors.deferred_rejected":    {yaml: yamlError(400, "Base.1.18.PropertyUnknown")},
		"secure_boot.database_write":       {yaml: "true"},
		"secure_boot.reset_keys.types":     {yaml: yamlList([]string{string(profile.ResetKeysTypeDeleteAllKeys), string(profile.ResetKeysTypeDeletePK)})},
		"virtual_media.insert":             {yaml: string(profile.MediaInsertStandard)},
		"virtual_media.oem_vendor":         {yaml: "Incus", note: "never a real vendor"},
		"virtual_media.oem_action":         {yaml: "InsertVirtualMedia"},
		"virtual_media.insert_response":    {yaml: quote(string(profile.InsertResponseNoContent))},
		"virtual_media.boot_override_once": {yaml: "false"},
		"virtual_media.delivery": {
			yaml:   string(profile.DeliveryFullFetch),
			verify: true,
			note:   "range requests against the media URL are not recorded",
		},
		"virtual_media.stream.read_ahead":       {yaml: "4MiB"},
		"virtual_media.stream.cache":            {yaml: "64MiB"},
		"virtual_media.stream.fallback":         {yaml: string(profile.StreamFallbackFullFetch)},
		"secure_boot.database_write_error":      {yaml: yamlError(405, "Base.1.18.ActionNotSupported")},
		"secure_boot.reset_keys.supported":      {yaml: "false"},
		"secure_boot.reset_keys.response":       {yaml: quote(string(profile.ResetKeysResponseNoContent))},
		"secure_boot.certificate_id_style":      {yaml: string(profile.CertificateIDStyleHash)},
		"secure_boot.certificate_id_prefix":     {yaml: `""`},
		"secure_boot.enable_requires_power_off": {yaml: "true"},
		"power.reset_types":                     {yaml: "[]"},
		"power.response":                        {yaml: quote(string(profile.PowerResponseNoContent))},
		"power.not_settled_window":              {yaml: "0s"},
		"power.not_settled_error":               {yaml: yamlError(400, "Base.1.18.ResourceInStandby")},
		"power.delay":                           {yaml: "0s"},
		"timing.scale":                          {yaml: "1.0"},
	}
}
