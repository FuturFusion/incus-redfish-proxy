package recording

import (
	"fmt"
	"maps"
	"math"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// deferredState is the deployment state, in which Operations Center writes the deferred BIOS attributes.
const deferredState = "apply-bios-deferred"

// draftRegistryID names the registry of the synthetic attributes. Vendor registry IDs are not copied.
const draftRegistryID = "DraftBiosAttributeRegistry.v1_0_0"

var (
	systemPathPattern        = regexp.MustCompile(`^/redfish/v1/systems/[^/]+$`)
	certificatesPathPattern  = regexp.MustCompile(`/securebootdatabases/[^/]+/(certificates|signatures)$`)
	databaseEntryPathPattern = regexp.MustCompile(`/securebootdatabases/[^/]+/(certificates|signatures)/[^/]+$`)
	virtualMediaPathPattern  = regexp.MustCompile(`/virtualmedia/[^/]+$`)
	sequentialIDPattern      = regexp.MustCompile(`^([A-Za-z_.-]*?)([0-9]+)$`)
)

// where returns the exchanges that match.
func (r *Recording) where(match func(Exchange) bool) []Exchange {
	var exchanges []Exchange

	for _, e := range r.Exchanges {
		if match(e) {
			exchanges = append(exchanges, e)
		}
	}

	return exchanges
}

// first returns the first exchange that matches.
func (r *Recording) first(match func(Exchange) bool) (Exchange, bool) {
	index := slices.IndexFunc(r.Exchanges, match)
	if index < 0 {
		return Exchange{}, false
	}

	return r.Exchanges[index], true
}

// firstAfter returns the first exchange after e that matches.
func (r *Recording) firstAfter(e Exchange, match func(Exchange) bool) (Exchange, bool) {
	return r.first(func(candidate Exchange) bool { return candidate.Sequence > e.Sequence && match(candidate) })
}

// lastBefore returns the last exchange before e that matches.
func (r *Recording) lastBefore(e Exchange, match func(Exchange) bool) (Exchange, bool) {
	var found Exchange

	ok := false

	for _, candidate := range r.Exchanges {
		if candidate.Sequence >= e.Sequence {
			break
		}

		if match(candidate) {
			found, ok = candidate, true
		}
	}

	return found, ok
}

func isGET(match func(string) bool) func(Exchange) bool {
	return func(e Exchange) bool { return e.Method == http.MethodGet && e.Succeeded() && match(e.Path()) }
}

func isBIOS(p string) bool { return strings.HasSuffix(p, "/bios") }

// isBIOSWrite matches the Bios resource and its settings object, whatever the vendor calls it.
func isBIOSWrite(p string) bool {
	return isBIOS(p) || strings.HasSuffix(path.Dir(p), "/bios")
}

func isSystem(p string) bool { return systemPathPattern.MatchString(p) }

func isReset(p string) bool { return strings.HasSuffix(p, "/actions/computersystem.reset") }

func isSecureBoot(p string) bool { return strings.HasSuffix(p, "/secureboot") }

func isTask(p string) bool { return strings.Contains(p, "/taskservice/tasks/") }

func isBIOSPatch(e Exchange) bool { return e.Method == http.MethodPatch && isBIOSWrite(e.Path()) }

func isResetPost(e Exchange) bool { return e.Method == http.MethodPost && isReset(e.Path()) }

// errorShape returns the status and message ID of a Redfish error answer, if they are valid in a profile.
func errorShape(e Exchange) (string, bool) {
	messageID := messageID(e)

	err := profile.ErrorShape{Status: e.Status, MessageID: messageID}.Validate()
	if err != nil {
		return "", false
	}

	return yamlError(e.Status, messageID), true
}

func messageID(e Exchange) string {
	var body struct {
		Error struct {
			Code         string `json:"code"`
			ExtendedInfo []struct {
				MessageID string `json:"MessageId"`
			} `json:"@Message.ExtendedInfo"`
		} `json:"error"`
	}

	err := e.ResponseJSON(&body)
	if err != nil {
		return ""
	}

	if len(body.Error.ExtendedInfo) > 0 {
		return body.Error.ExtendedInfo[0].MessageID
	}

	return body.Error.Code
}

// response draws the answer of an action: 202-task, 204, 200 or 200-error.
func response(e Exchange) value {
	if e.Status == http.StatusAccepted {
		return observed(quote(string(profile.PowerResponseTask)), e)
	}

	return doneResponse(e)
}

// doneResponse draws the answer to a request that is done: 204, or 200 with the messages at the top level or inside an error object.
// A profile knows no other status, so it becomes 204 with a note. The note names the first message ID.
func doneResponse(e Exchange) value {
	id := successMessageID(e)

	var v value

	switch e.Status {
	case http.StatusNoContent:
		return observed(quote(string(profile.PowerResponseNoContent)), e)

	case http.StatusOK:
		v = observed(quote(okResponse(e)), e)
		if id != "" {
			v = v.withNote("%s", id)
		}

		return v
	}

	v = inferred(quote(string(profile.PowerResponseNoContent)), e).withNote("answered %d", e.Status)
	if id != "" {
		v.note += ", " + id
	}

	return v
}

// okResponse tells a 200 answer with the messages inside an error object from one with top level messages.
func okResponse(e Exchange) string {
	var body struct {
		Error *struct{} `json:"error"`
	}

	err := e.ResponseJSON(&body)
	if err == nil && body.Error != nil {
		return profile.AnswerOKError
	}

	return profile.AnswerOK
}

// patchResponse draws the answer to a PATCH: like doneResponse, or 200-resource for a 200 answer with the resource.
func patchResponse(e Exchange) value {
	var resource struct {
		ODataID string `json:"@odata.id"`
		ID      string `json:"Id"`
	}

	err := e.ResponseJSON(&resource)
	if e.Status == http.StatusOK && err == nil && (resource.ODataID != "" || resource.ID != "") {
		return observed(quote(string(profile.ResourcePatchResponseResource)), e)
	}

	return doneResponse(e)
}

// successMessageID returns the first message ID that a successful answer carries.
// Some BMCs wrap even a success in an error object.
func successMessageID(e Exchange) string {
	type extendedInfo []struct {
		MessageID string `json:"MessageId"`
	}

	var body struct {
		ExtendedInfo extendedInfo `json:"@Message.ExtendedInfo"`
		Error        struct {
			ExtendedInfo extendedInfo `json:"@Message.ExtendedInfo"`
		} `json:"error"`
	}

	err := e.ResponseJSON(&body)
	if err != nil {
		return ""
	}

	for _, info := range []extendedInfo{body.ExtendedInfo, body.Error.ExtendedInfo} {
		if len(info) > 0 {
			return info[0].MessageID
		}
	}

	return ""
}

func (d *Draft) bios(rec *Recording) {
	task, ok := rec.first(isTaskResource)
	if ok {
		d.set("bios.task_resource", observed("true", task))
	}

	settingsPending := d.biosSettings(rec)

	patches := rec.where(func(e Exchange) bool { return isBIOSPatch(e) && e.Succeeded() })
	if len(patches) == 0 {
		d.biosApplyTime(rec, Exchange{}, settingsPending)
		d.explain("Operations Center sent no BIOS PATCH",
			"bios.patch_response", "bios.task_resource", "bios.settings_object", "bios.pending",
			"bios.apply_time.supported", "bios.apply_time.default", "bios.patch_requires_power_off",
			"bios.registry.id", "bios.errors.unknown_attribute", "bios.errors.deferred_rejected")

		return
	}

	patch := patches[0]
	settingsObject := !isBIOS(patch.Path())
	d.set("bios.settings_object", observed(yamlBool(settingsObject), patch))

	d.biosPatchResponse(rec, patch)
	d.explain("no GET answered a task resource", "bios.task_resource")

	pending, compared := d.biosPending(rec, patch)
	if !compared {
		pending = settingsPending
	}

	if pending && !settingsObject {
		d.set("bios.settings_object", inferred("true", patch).withNote("the proxy keeps pending attributes in a settings object"))
	}

	d.biosApplyTime(rec, patch, pending)

	sentWhileOff := false

	for _, p := range patches {
		system, ok := rec.lastBefore(p, isGET(isSystem))
		if ok && powerState(system) == "On" {
			d.set("bios.patch_requires_power_off", observed("false", system, p))

			break
		}

		sentWhileOff = sentWhileOff || ok && powerState(system) == "Off"
	}

	if sentWhileOff {
		d.explain("BIOS PATCHes sent only while the server was off", "bios.patch_requires_power_off")
	}

	d.explain("no write to an unknown attribute was recorded", "bios.errors.unknown_attribute")

	d.biosRegistry(rec, patches)
}

// isTaskResource matches a GET that answers a task resource. Some BMCs answer like a task monitor below TaskService.
func isTaskResource(e Exchange) bool {
	var task struct {
		TaskState string `json:"TaskState"`
	}

	return isGET(isTask)(e) && e.ResponseJSON(&task) == nil && task.TaskState != ""
}

func (d *Draft) biosPatchResponse(rec *Recording, patch Exchange) {
	switch patch.Status {
	case http.StatusAccepted:
		location := strings.TrimSuffix(strings.ToLower(patch.ResponseHeader("Location")), "/")
		if isTask(location) {
			polls := rec.where(func(e Exchange) bool { return isGET(isTask)(e) && e.Path() == location })

			task, ok := rec.first(func(e Exchange) bool { return isTaskResource(e) && e.Path() == location })
			if ok {
				d.set("bios.patch_response", observed(string(profile.PatchResponseTask), patch, task))
				d.set("bios.task_resource", observed("true", patch, task))

				return
			}

			if len(polls) == 0 {
				d.set("bios.patch_response", observed(string(profile.PatchResponseTask), patch))
				d.set("bios.task_resource", observed("true", patch))

				return
			}

			d.set("bios.patch_response", observed(string(profile.PatchResponseTaskMonitor), patch, polls[0]).withNote("the task URI answers like a task monitor, without a task body"))

			return
		}

		d.set("bios.patch_response", observed(string(profile.PatchResponseTaskMonitor), patch))

	case http.StatusNoContent:
		d.set("bios.patch_response", observed(quote(string(profile.PatchResponseNoContent)), patch))

	default:
		d.set("bios.patch_response", inferred(quote(string(profile.PatchResponseOK)), patch).withNote("answered %d", patch.Status))
	}
}

// biosSettings reads @Redfish.Settings of the Bios resource, which needs no PATCH.
// It reports whether a PATCH stages the attributes. A settings object holds the future settings, so it implies this.
func (d *Draft) biosSettings(rec *Recording) bool {
	var bios struct {
		Settings struct {
			SettingsObject struct {
				ODataID string `json:"@odata.id"`
			} `json:"SettingsObject"`
			SupportedApplyTimes []string `json:"SupportedApplyTimes"`
		} `json:"@Redfish.Settings"`
	}

	get, ok := rec.first(func(e Exchange) bool {
		return isGET(isBIOS)(e) && e.ResponseJSON(&bios) == nil && bios.Settings.SettingsObject.ODataID != ""
	})
	if !ok {
		return false
	}

	d.set("bios.settings_object", observed("true", get))

	note := "a settings object holds the future settings"
	if slices.Equal(bios.Settings.SupportedApplyTimes, []string{string(profile.ApplyTimeOnReset)}) {
		note = "only OnReset is supported"
	}

	d.set("bios.pending", inferred("true", get).withNote("%s", note))

	return true
}

// biosPending compares the attributes of the PATCH with the next GET of the Bios resource before a reset.
// It reports whether the attributes are pending and whether a GET allowed the comparison.
func (d *Draft) biosPending(rec *Recording, patch Exchange) (bool, bool) {
	var request struct {
		Attributes map[string]any `json:"Attributes"`
	}

	err := patch.RequestJSON(&request)
	if err != nil {
		return false, false
	}

	for _, e := range rec.Exchanges {
		if e.Sequence <= patch.Sequence {
			continue
		}

		if isResetPost(e) {
			return false, false
		}

		if !isGET(isBIOS)(e) {
			continue
		}

		var bios struct {
			Attributes map[string]any `json:"Attributes"`
		}

		err := e.ResponseJSON(&bios)
		if err != nil {
			continue
		}

		compared, pending := false, false

		for name, want := range request.Attributes {
			got, ok := bios.Attributes[name]
			if ok {
				compared = true
				pending = pending || !reflect.DeepEqual(got, want)
			}
		}

		if compared {
			d.set("bios.pending", observed(yamlBool(pending), patch, e))

			return pending, true
		}
	}

	return false, false
}

func (d *Draft) biosApplyTime(rec *Recording, patch Exchange, pending bool) {
	var bios struct {
		Settings struct {
			SupportedApplyTimes []string `json:"SupportedApplyTimes"`
		} `json:"@Redfish.Settings"`
	}

	get, ok := rec.first(func(e Exchange) bool {
		return isGET(isBIOS)(e) && e.ResponseJSON(&bios) == nil && len(bios.Settings.SupportedApplyTimes) > 0
	})
	if !ok {
		return
	}

	var supported, unknown []string

	onResetDropped := false

	for _, applyTime := range bios.Settings.SupportedApplyTimes {
		switch {
		case applyTime == string(profile.ApplyTimeImmediate), applyTime == string(profile.ApplyTimeOnReset) && pending:
			supported = append(supported, applyTime)

		case applyTime == string(profile.ApplyTimeOnReset):
			onResetDropped = true

		default:
			unknown = append(unknown, applyTime)
		}
	}

	var notes []string

	if len(unknown) > 0 {
		notes = append(notes, fmt.Sprintf("dropped %s, the proxy knows only Immediate and OnReset", strings.Join(unknown, ", ")))
	}

	if onResetDropped {
		notes = append(notes, "dropped OnReset, it needs pending")
	}

	v := observed(yamlList(supported), get)
	if len(notes) > 0 {
		v = inferred(yamlList(supported), get).withNote("%s", strings.Join(notes, "; "))
	}

	d.set("bios.apply_time.supported", v)

	var request struct {
		ApplyTime struct {
			ApplyTime string `json:"ApplyTime"`
		} `json:"@Redfish.SettingsApplyTime"`
	}

	err := patch.RequestJSON(&request)

	switch {
	case err == nil && slices.Contains(supported, request.ApplyTime.ApplyTime):
		d.set("bios.apply_time.default", observed(request.ApplyTime.ApplyTime, patch).withNote("as asked for by Operations Center"))

	case len(supported) == 1:
		d.set("bios.apply_time.default", inferred(supported[0], get).withNote("the only supported apply time"))
	}
}

func powerState(e Exchange) string {
	var system struct {
		PowerState string `json:"PowerState"`
	}

	err := e.ResponseJSON(&system)
	if err != nil {
		return ""
	}

	return system.PowerState
}

// registryEntry is an attribute of a recorded attribute registry.
type registryEntry struct {
	AttributeName string `json:"AttributeName"`
	Type          string `json:"Type"`
	Value         []struct {
		ValueName string `json:"ValueName"`
	} `json:"Value"`
	LowerBound *int64 `json:"LowerBound"`
	UpperBound *int64 `json:"UpperBound"`
	seq        int64
}

func recordedRegistry(rec *Recording) map[string]registryEntry {
	entries := map[string]registryEntry{}

	// The registry has no fixed location. Dell serves it below the Bios resource.
	for _, e := range rec.where(func(e Exchange) bool {
		return isGET(func(string) bool { return true })(e) && strings.Contains(e.ResponseBody, "RegistryEntries")
	}) {
		var registry struct {
			RegistryEntries struct {
				Attributes []registryEntry `json:"Attributes"`
			} `json:"RegistryEntries"`
		}

		err := e.ResponseJSON(&registry)
		if err != nil {
			continue
		}

		for _, entry := range registry.RegistryEntries.Attributes {
			entry.seq = e.Sequence
			entries[entry.AttributeName] = entry
		}
	}

	return entries
}

// synthetic is one synthetic attribute, which stands for the vendor attributes of one type.
type synthetic struct {
	attributeType profile.AttributeType
	entry         *registryEntry
	vendorNames   []string
	seqs          []int64
}

func attributeType(value any, entry *registryEntry) profile.AttributeType {
	if entry != nil {
		switch profile.AttributeType(entry.Type) {
		case profile.AttributeTypeEnumeration:
			if len(entry.Value) > 0 {
				return profile.AttributeTypeEnumeration
			}

		case profile.AttributeTypeInteger, profile.AttributeTypeBoolean:
			return profile.AttributeType(entry.Type)
		}
	}

	switch value.(type) {
	case bool:
		return profile.AttributeTypeBoolean

	case float64:
		return profile.AttributeTypeInteger
	}

	return profile.AttributeTypeString
}

// biosRegistry adds one synthetic attribute per type the PATCHes carry. Vendor names stay in the comments.
func (d *Draft) biosRegistry(rec *Recording, patches []Exchange) {
	registry := recordedRegistry(rec)
	byType := map[profile.AttributeType]*synthetic{}

	var order []profile.AttributeType

	var deferred *synthetic

	for _, patch := range patches {
		var request struct {
			Attributes map[string]any `json:"Attributes"`
		}

		err := patch.RequestJSON(&request)
		if err != nil {
			continue
		}

		for _, name := range slices.Sorted(maps.Keys(request.Attributes)) {
			var entry *registryEntry

			recorded, ok := registry[name]
			if ok {
				entry = &recorded
			}

			attrType := attributeType(request.Attributes[name], entry)

			if patch.DeploymentState == deferredState {
				if deferred == nil {
					deferred = &synthetic{attributeType: attrType, entry: entry}
				}

				deferred.add(name, patch, entry)

				continue
			}

			s, ok := byType[attrType]
			if !ok {
				s = &synthetic{attributeType: attrType, entry: entry}
				byType[attrType] = s
				order = append(order, attrType)
			}

			s.add(name, patch, entry)
		}
	}

	var seqs []int64

	for _, attrType := range order {
		s := byType[attrType]
		d.attributes = append(d.attributes, s.value("Draft"+string(attrType), ""))
		seqs = append(seqs, s.seqs...)
	}

	if deferred != nil {
		requirement := value{yaml: `{name: DraftRequirement, type: Enumeration, values: ["Standard", "Performance"], default: "Standard"}`, seqs: deferred.seqs, verify: true, note: "stands for the attribute the deferred ones depend on"}
		d.attributes = append(d.attributes, requirement, deferred.value("DraftDeferred", ", requires: {attribute: DraftRequirement, value: Performance}"))
		seqs = append(seqs, deferred.seqs...)

		d.biosDeferredRejected(rec)
		d.explain("no deferred attribute was refused", "bios.errors.deferred_rejected")
	} else {
		d.explain("Operations Center deferred no attribute", "bios.errors.deferred_rejected")
	}

	if len(d.attributes) > 0 {
		v := value{yaml: draftRegistryID, seqs: seqs, note: "vendor registry IDs are not copied"}
		if len(registry) == 0 {
			v = v.addNote("the attribute registry was not recorded, so the types are guessed")
		}

		d.set("bios.registry.id", v)
	}
}

func (s *synthetic) add(name string, patch Exchange, entry *registryEntry) {
	if !slices.Contains(s.vendorNames, name) {
		s.vendorNames = append(s.vendorNames, name)
	}

	s.seqs = append(s.seqs, patch.Sequence)
	if entry != nil {
		s.seqs = append(s.seqs, entry.seq)
	}
}

func (s *synthetic) value(name string, extra string) value {
	fields := []string{"name: " + name, "type: " + string(s.attributeType)}

	if s.entry != nil && profile.AttributeType(s.entry.Type) == s.attributeType {
		fields = append(fields, s.registryFields()...)
	}

	v := value{
		yaml:   "{" + strings.Join(fields, ", ") + extra + "}",
		seqs:   s.seqs,
		verify: s.entry == nil || extra != "",
		note:   "vendor attributes " + strings.Join(s.vendorNames, ", "),
	}

	return v
}

// registryFields copies the values and bounds of the recorded registry entry, as far as a profile accepts them.
func (s *synthetic) registryFields() []string {
	switch s.attributeType {
	case profile.AttributeTypeEnumeration:
		var values []string

		for _, v := range s.entry.Value {
			if !slices.Contains(values, quote(v.ValueName)) {
				values = append(values, quote(v.ValueName))
			}
		}

		return []string{"values: " + yamlList(values)}

	case profile.AttributeTypeInteger:
		lower, upper := s.entry.LowerBound, s.entry.UpperBound
		if lower == nil || *lower < 0 || upper == nil || *upper < *lower {
			return nil
		}

		return []string{"min: " + strconv.FormatInt(*lower, 10), "max: " + strconv.FormatInt(*upper, 10)}
	}

	return nil
}

func (d *Draft) biosDeferredRejected(rec *Recording) {
	for _, e := range rec.where(func(e Exchange) bool { return isBIOSPatch(e) && e.Status >= 400 }) {
		shape, ok := errorShape(e)
		if ok {
			d.set("bios.errors.deferred_rejected", observed(shape, e))

			return
		}
	}
}

func (d *Draft) secureBoot(rec *Recording) {
	post, ok := rec.first(func(e Exchange) bool {
		return e.Method == http.MethodPost && certificatesPathPattern.MatchString(e.Path()) && e.Status != 0
	})
	if ok {
		d.set("secure_boot.database_write", observed(yamlBool(post.Succeeded()), post))
		d.explain("no database write was refused", "secure_boot.database_write_error")

		shape, valid := errorShape(post)
		if valid {
			d.set("secure_boot.database_write_error", observed(shape, post))
		}
	}

	d.databaseAnswers(rec)
	d.resetKeys(rec)
	d.certificateIDs(rec)

	databaseKeys := []string{
		"secure_boot.database_write", "secure_boot.database_write_error",
		"secure_boot.database_add_response", "secure_boot.database_delete_response",
		"secure_boot.certificate_id_style", "secure_boot.certificate_id_prefix",
	}

	switch {
	case rec.Manifest.Request.SecureBootEnrollmentMedia:
		d.explain("keys enrolled from media, no database write", databaseKeys...)

	case rec.Manifest.Request.SkipSecureBootCertificates:
		d.explain("certificates skipped by the deployment request", databaseKeys...)
	}

	d.explain("Operations Center added no database entry", "secure_boot.database_add_response")
	d.explain("Operations Center deleted no database entry", "secure_boot.database_delete_response")

	d.secureBootEnable(rec)
}

// databaseAnswers draws the answers to the first accepted POST and DELETE of a database entry.
func (d *Draft) databaseAnswers(rec *Recording) {
	post, ok := rec.first(func(e Exchange) bool {
		return e.Method == http.MethodPost && certificatesPathPattern.MatchString(e.Path()) && e.Succeeded()
	})
	if ok {
		v := inferred(quote(string(profile.DatabaseAddResponseCreated)), post).withNote("answered %d", post.Status)

		switch post.Status {
		case http.StatusCreated, http.StatusNoContent:
			v = observed(quote(strconv.Itoa(post.Status)), post)
		}

		d.set("secure_boot.database_add_response", v)
	}

	deleted, ok := rec.first(func(e Exchange) bool {
		return e.Method == http.MethodDelete && databaseEntryPathPattern.MatchString(e.Path()) && e.Succeeded()
	})
	if ok {
		d.set("secure_boot.database_delete_response", doneResponse(deleted))
	}
}

// secureBootEnable draws enable_requires_power_off from the SecureBootEnable PATCHes sent while the server was on.
func (d *Draft) secureBootEnable(rec *Recording) {
	const key = "secure_boot.enable_requires_power_off"

	patches := rec.where(func(e Exchange) bool {
		var request struct {
			SecureBootEnable *bool `json:"SecureBootEnable"`
		}

		return e.Method == http.MethodPatch && isSecureBoot(e.Path()) && e.Status != 0 &&
			e.RequestJSON(&request) == nil && request.SecureBootEnable != nil
	})

	var refused []Exchange

	for _, patch := range patches {
		system, ok := rec.lastBefore(patch, isGET(isSystem))
		if !ok || powerState(system) != "On" {
			continue
		}

		if patch.Succeeded() {
			d.set(key, observed("false", system, patch))

			return
		}

		if !profile.IsNotSettledMessageID(messageID(patch)) {
			refused = append(refused, system, patch)
		}
	}

	if len(refused) > 0 {
		d.set(key, inferred("true", refused...).withNote("refused while the server was on, %s", messageID(refused[1])))

		return
	}

	if len(patches) > 0 {
		d.explain("changed only while the server was off", key)

		return
	}

	var secureBoot struct {
		SecureBootEnable bool `json:"SecureBootEnable"`
	}

	_, enabled := rec.first(func(e Exchange) bool {
		return isGET(isSecureBoot)(e) && e.ResponseJSON(&secureBoot) == nil && secureBoot.SecureBootEnable
	})
	if enabled {
		d.explain("secure boot already enabled", key)

		return
	}

	d.explain("Operations Center did not change SecureBootEnable", key)
}

func (d *Draft) resetKeys(rec *Recording) {
	var secureBoot struct {
		Actions struct {
			ResetKeys struct {
				Target          string   `json:"target"`
				AllowableValues []string `json:"ResetKeysType@Redfish.AllowableValues"`
				ActionInfo      string   `json:"@Redfish.ActionInfo"`
			} `json:"#SecureBoot.ResetKeys"`
		} `json:"Actions"`
	}

	get, ok := rec.first(func(e Exchange) bool { return isGET(isSecureBoot)(e) && e.ResponseJSON(&secureBoot) == nil })
	if !ok {
		return
	}

	action := secureBoot.Actions.ResetKeys
	d.set("secure_boot.reset_keys.supported", observed(yamlBool(action.Target != ""), get))

	var types []string

	for _, resetKeysType := range action.AllowableValues {
		if resetKeysType == string(profile.ResetKeysTypeDeleteAllKeys) || resetKeysType == string(profile.ResetKeysTypeDeletePK) {
			types = append(types, resetKeysType)
		}
	}

	if len(types) > 0 {
		v := observed(yamlList(types), get)
		if len(types) < len(action.AllowableValues) {
			v = v.withNote("published %s", strings.Join(action.AllowableValues, ", "))
		}

		d.set("secure_boot.reset_keys.types", v)
	}

	posts := rec.where(func(e Exchange) bool {
		return e.Method == http.MethodPost && strings.HasSuffix(e.Path(), "/actions/secureboot.resetkeys") && e.Succeeded()
	})
	if len(posts) == 0 {
		d.explain("Operations Center sent no ResetKeys", "secure_boot.reset_keys.response", "secure_boot.reset_keys.applies")

		return
	}

	d.set("secure_boot.reset_keys.response", response(posts[0]))

	// A profile with applies on_reset needs supported.
	if action.Target != "" {
		d.resetKeysApplies(rec, posts[0])
	}

	if len(action.AllowableValues) == 0 {
		d.usedResetKeysTypes(posts, action.ActionInfo != "")
	}
}

// resetKeysApplies draws when the reset of the keys takes effect from the first SecureBoot read after the reset.
func (d *Draft) resetKeysApplies(rec *Recording, post Exchange) {
	const key = "secure_boot.reset_keys.applies"

	var secureBoot struct {
		SecureBootMode string `json:"SecureBootMode"`
	}

	get, ok := rec.firstAfter(post, func(e Exchange) bool { return isGET(isSecureBoot)(e) && e.ResponseJSON(&secureBoot) == nil })
	if ok {
		_, poweredOn := rec.firstAfter(post, func(e Exchange) bool {
			return e.Sequence < get.Sequence && isResetPost(e) && e.Succeeded() && powersOn(e)
		})

		switch {
		case secureBoot.SecureBootMode == "UserMode":
			d.set(key, observed(string(profile.ResetKeysAppliesOnReset), post, get))

			return

		case secureBoot.SecureBootMode == "SetupMode" && !poweredOn:
			d.set(key, observed(string(profile.ResetKeysAppliesImmediately), post, get))

			return
		}
	}

	id := successMessageID(post)
	for _, suffix := range []string{".ResetRequired", ".RebootRequired", ".SystemResetRequired"} {
		if strings.HasSuffix(id, suffix) {
			d.set(key, inferred(string(profile.ResetKeysAppliesOnReset), post).withNote("%s", id))

			return
		}
	}

	d.explain("no read of SecureBoot shows when the reset takes effect", key)
}

// powersOn reports whether a reset ends with the server on.
func powersOn(e Exchange) bool {
	switch resetType(e) {
	case profile.ResetTypeOn, profile.ResetTypeForceOn, profile.ResetTypeGracefulRestart, profile.ResetTypeForceRestart:
		return true
	}

	return false
}

// usedResetKeysTypes draws the reset keys types from the requests the BMC accepted, if it publishes none.
func (d *Draft) usedResetKeysTypes(posts []Exchange, actionInfo bool) {
	var types []string

	for _, post := range posts {
		var request struct {
			ResetKeysType string `json:"ResetKeysType"`
		}

		err := post.RequestJSON(&request)
		if err != nil || slices.Contains(types, request.ResetKeysType) {
			continue
		}

		if request.ResetKeysType == string(profile.ResetKeysTypeDeleteAllKeys) || request.ResetKeysType == string(profile.ResetKeysTypeDeletePK) {
			types = append(types, request.ResetKeysType)
		}
	}

	if len(types) == 0 {
		return
	}

	note := "accepted by the BMC, no allowable values published"
	if actionInfo {
		note = "accepted by the BMC, allowable values published through @Redfish.ActionInfo, not recorded"
	}

	d.set("secure_boot.reset_keys.types", inferred(yamlList(types), posts...).withNote("%s", note))
}

// numberedPerDatabase reports whether the first reads of two collections share a number.
// Later reads do not count, because a reset of the keys restarts the count.
func numberedPerDatabase(reads []Exchange) bool {
	owner := map[string]string{}
	read := map[string]bool{}

	for _, e := range reads {
		if read[e.Path()] {
			continue
		}

		read[e.Path()] = true

		var collection struct {
			Members []struct {
				ODataID string `json:"@odata.id"`
			} `json:"Members"`
		}

		_ = e.ResponseJSON(&collection)

		for _, m := range collection.Members {
			number := sequentialIDPattern.FindStringSubmatch(path.Base(m.ODataID))[2]

			o, ok := owner[number]
			if ok && o != e.Path() {
				return true
			}

			owner[number] = e.Path()
		}
	}

	return false
}

// certificateIDs tells sequential IDs like Certificate1 apart from hashes.
func (d *Draft) certificateIDs(rec *Recording) {
	type member struct{ id, collection string }

	var seqs []Exchange

	var members []member

	for _, e := range rec.where(isGET(certificatesPathPattern.MatchString)) {
		var collection struct {
			Members []struct {
				ODataID string `json:"@odata.id"`
			} `json:"Members"`
		}

		err := e.ResponseJSON(&collection)
		if err != nil || len(collection.Members) == 0 {
			continue
		}

		seqs = append(seqs, e)
		for _, m := range collection.Members {
			members = append(members, member{id: path.Base(m.ODataID), collection: e.Path()})
		}
	}

	if len(members) == 0 {
		return
	}

	prefix := ""

	for i, m := range members {
		match := sequentialIDPattern.FindStringSubmatch(m.id)
		if match == nil || (i > 0 && match[1] != prefix) {
			d.set("secure_boot.certificate_id_style", observed(string(profile.CertificateIDStyleHash), seqs...))

			return
		}

		prefix = match[1]
	}

	style := observed(string(profile.CertificateIDStyleSequential), seqs...)
	if numberedPerDatabase(seqs) {
		style = style.withNote("numbered per database, the proxy numbers all databases with one counter")
	}

	d.set("secure_boot.certificate_id_style", style)
	d.set("secure_boot.certificate_id_prefix", observed(quote(prefix), seqs...))
}

// responses draws the answer to a PATCH from the first accepted PATCH of Systems, SecureBoot or VirtualMedia.
func (d *Draft) responses(rec *Recording) {
	patch, ok := rec.first(func(e Exchange) bool {
		p := e.Path()

		return e.Method == http.MethodPatch && e.Succeeded() && (isSystem(p) || isSecureBoot(p) || virtualMediaPathPattern.MatchString(p))
	})
	if !ok {
		d.explain("Operations Center sent no PATCH of Systems, SecureBoot or VirtualMedia", "responses.patch")

		return
	}

	d.set("responses.patch", patchResponse(patch))
}

func (d *Draft) virtualMedia(rec *Recording) {
	insert, ok := rec.first(func(e Exchange) bool { return e.Succeeded() && insertKind(e) != "" })
	if ok {
		kind := insertKind(insert)
		d.set("virtual_media.insert", refusedFirst(rec, insert, observed(string(kind), insert)))

		switch kind {
		case profile.MediaInsertOEM:
			d.set("virtual_media.insert_response", response(insert))

			action := oemAction(insert)
			if profile.IsOEMName(action) {
				d.set("virtual_media.oem_action", observed(action, insert))
			}

		case profile.MediaInsertStandard:
			d.set("virtual_media.insert_response", response(insert))

		case profile.MediaInsertPatch:
			v := d.values["virtual_media.insert_response"]
			v.fixed, v.note = true, fmt.Sprintf("not used with insert patch, the BMC answered %d", insert.Status)
			d.set("virtual_media.insert_response", v)
		}

		if kind != profile.MediaInsertOEM {
			v := d.values["virtual_media.oem_action"]
			v.fixed, v.note = true, "not used with insert "+string(kind)
			d.set("virtual_media.oem_action", v)
		}
	}

	d.transferMethod(rec)
	d.bootOverrideOnce(rec)
}

// refusedFirst names the parameters of a refused try before the accepted insert.
func refusedFirst(rec *Recording, insert Exchange, v value) value {
	refused, ok := rec.lastBefore(insert, func(e Exchange) bool {
		return e.Method == insert.Method && e.Path() == insert.Path() && e.Status >= http.StatusBadRequest
	})
	if !ok {
		return v
	}

	v.seqs = append(v.seqs, refused.Sequence)

	accepted := requestKeys(insert)

	var dropped []string

	for _, key := range requestKeys(refused) {
		if !slices.Contains(accepted, key) {
			dropped = append(dropped, key)
		}
	}

	if len(dropped) == 0 {
		return v.addNote("the BMC refused a first try, %s", messageID(refused))
	}

	return v.addNote("the BMC refused the parameter %s, %s", strings.Join(dropped, ", "), messageID(refused))
}

// requestKeys returns the sorted top level keys of the request body.
func requestKeys(e Exchange) []string {
	var request map[string]any

	err := e.RequestJSON(&request)
	if err != nil {
		return nil
	}

	return slices.Sorted(maps.Keys(request))
}

// bootOverrideOnce reads the published override modes. Without them, an accepted PATCH with Once is the evidence.
func (d *Draft) bootOverrideOnce(rec *Recording) {
	const key = "virtual_media.boot_override_once"

	var system struct {
		Boot struct {
			AllowableValues []string `json:"BootSourceOverrideEnabled@Redfish.AllowableValues"`
		} `json:"Boot"`
	}

	get, ok := rec.first(func(e Exchange) bool {
		return isGET(isSystem)(e) && e.ResponseJSON(&system) == nil && len(system.Boot.AllowableValues) > 0
	})
	if ok {
		v := observed(yamlBool(slices.Contains(system.Boot.AllowableValues, "Once")), get)
		if !slices.Contains(system.Boot.AllowableValues, "Continuous") {
			v = v.withNote("published %s", strings.Join(system.Boot.AllowableValues, ", "))
		}

		d.set(key, v)

		return
	}

	patch, ok := rec.first(func(e Exchange) bool {
		var request struct {
			Boot struct {
				BootSourceOverrideEnabled string `json:"BootSourceOverrideEnabled"`
			} `json:"Boot"`
		}

		return e.Method == http.MethodPatch && isSystem(e.Path()) && e.Succeeded() &&
			e.RequestJSON(&request) == nil && request.Boot.BootSourceOverrideEnabled == "Once"
	})
	if ok {
		d.set(key, observed("true", patch).withNote("accepted in a PATCH, no allowable values published"))

		return
	}

	d.explain("no allowable values published, no PATCH with Once", key)
}

// transferMethod draws the delivery from the TransferMethod the BMC reports for inserted media.
func (d *Draft) transferMethod(rec *Recording) {
	var media struct {
		Inserted       bool   `json:"Inserted"`
		TransferMethod string `json:"TransferMethod"`
	}

	e, ok := rec.first(func(e Exchange) bool {
		return e.Succeeded() && virtualMediaPathPattern.MatchString(e.Path()) &&
			e.ResponseJSON(&media) == nil && media.Inserted
	})
	if !ok {
		return
	}

	if media.TransferMethod == "" {
		d.explain("the BMC reports no TransferMethod", "virtual_media.delivery")

		return
	}

	delivery := profile.DeliveryFullFetch
	if media.TransferMethod == "Stream" {
		delivery = profile.DeliveryStream
	}

	d.set("virtual_media.delivery", inferred(string(delivery), e).withNote("BMC reports TransferMethod %s", media.TransferMethod))
}

// insertKind tells the three ways Operations Center inserts media apart.
func insertKind(e Exchange) profile.MediaInsert {
	p := e.Path()

	var request struct {
		Image *string `json:"Image"`
	}

	if e.RequestJSON(&request) != nil || request.Image == nil || !strings.Contains(p, "/virtualmedia/") {
		return ""
	}

	switch {
	case e.Method == http.MethodPost && strings.HasSuffix(p, "/actions/virtualmedia.insertmedia"):
		return profile.MediaInsertStandard

	case e.Method == http.MethodPost && strings.Contains(p, "/actions/"):
		return profile.MediaInsertOEM

	case e.Method == http.MethodPatch && virtualMediaPathPattern.MatchString(p):
		return profile.MediaInsertPatch
	}

	return ""
}

// oemAction returns the action name of an OEM action target like .../Actions/Oem/Vendor.InsertVirtualMedia.
func oemAction(e Exchange) string {
	u, err := url.Parse(e.URL)
	if err != nil {
		return ""
	}

	base := path.Base(u.Path)

	return base[strings.LastIndex(base, ".")+1:]
}

func (d *Draft) power(rec *Recording) {
	var system struct {
		Actions struct {
			Reset struct {
				AllowableValues []string `json:"ResetType@Redfish.AllowableValues"`
			} `json:"#ComputerSystem.Reset"`
		} `json:"Actions"`
	}

	get, ok := rec.first(func(e Exchange) bool {
		return isGET(isSystem)(e) && e.ResponseJSON(&system) == nil && len(system.Actions.Reset.AllowableValues) > 0
	})
	if ok {
		d.resetTypes(get, system.Actions.Reset.AllowableValues)
	}

	post, ok := rec.first(func(e Exchange) bool { return isResetPost(e) && e.Succeeded() })
	if ok {
		d.set("power.response", response(post))
	}

	d.redundantResets(rec)
	d.notSettled(rec)
	d.powerDelay(rec)
}

// redundantResetKeys are the profile keys of a reset to the power state the server has already, by target power state.
var redundantResetKeys = map[string]string{
	"Off": "power.redundant_reset_errors.off",
	"On":  "power.redundant_reset_errors.on",
}

// redundantResets draws the answer to a reset to the power state the server has already.
// An accepted reset gives null, a refused one its error shape.
func (d *Draft) redundantResets(rec *Recording) {
	for _, e := range rec.where(isResetPost) {
		target := targetPowerState(e)

		key, ok := redundantResetKeys[target]
		if !ok || e.Status == 0 || len(d.values[key].seqs) > 0 {
			continue
		}

		system, ok := rec.lastBefore(e, isGET(isSystem))
		if !ok || powerState(system) != target {
			continue
		}

		if e.Succeeded() {
			d.set(key, observed("null", system, e).withNote("accepted %s while %s", resetType(e), target))

			continue
		}

		shape, ok := errorShape(e)
		if ok {
			d.set(key, observed(shape, system, e).withNote("refused %s while %s", resetType(e), target))
		}
	}

	d.explain("Operations Center sent no reset to the current power state", slices.Sorted(maps.Values(redundantResetKeys))...)
}

func (d *Draft) resetTypes(get Exchange, published []string) {
	known := []profile.ResetType{
		profile.ResetTypeOn, profile.ResetTypeForceOn, profile.ResetTypeGracefulShutdown,
		profile.ResetTypeForceOff, profile.ResetTypeGracefulRestart, profile.ResetTypeForceRestart,
	}

	var types []string

	for _, resetType := range published {
		if slices.Contains(known, profile.ResetType(resetType)) {
			types = append(types, resetType)
		}
	}

	if len(types) == 0 {
		return
	}

	v := observed(yamlList(types), get)
	if len(types) < len(published) {
		v = v.withNote("published %s", strings.Join(published, ", "))
	}

	d.set("power.reset_types", v)
}

// notSettled measures the time from a power on to the last refusal that Operations Center waits on.
func (d *Draft) notSettled(rec *Recording) {
	isPowerOn := func(e Exchange) bool {
		return isResetPost(e) && e.Succeeded() && targetPowerState(e) == "On"
	}

	var window time.Duration

	var evidence []Exchange

	var refusal Exchange

	for _, e := range rec.where(func(e Exchange) bool { return e.Status >= 400 && profile.IsNotSettledMessageID(messageID(e)) }) {
		powerOn, ok := rec.lastBefore(e, isPowerOn)
		if !ok {
			continue
		}

		window = max(window, e.Timestamp.Sub(powerOn.Timestamp))
		evidence = append(evidence, powerOn, e)
		refusal = e
	}

	if len(evidence) == 0 {
		d.explain("no write was refused after a power on", "power.not_settled_window", "power.not_settled_error")

		return
	}

	seconds := time.Duration(math.Ceil(window.Seconds())) * time.Second
	d.set("power.not_settled_window", inferred(seconds.String(), evidence...).withNote("time to the last refusal, a lower bound"))

	shape, ok := errorShape(refusal)
	if ok {
		d.set("power.not_settled_error", observed(shape, refusal))
	}
}

func resetType(e Exchange) profile.ResetType {
	var request struct {
		ResetType profile.ResetType `json:"ResetType"`
	}

	err := e.RequestJSON(&request)
	if err != nil {
		return ""
	}

	return request.ResetType
}

// targetPowerState returns the power state a reset leads to without the help of the operating system.
// It is empty for a reset that keeps the power state or that waits for the operating system.
func targetPowerState(e Exchange) string {
	switch resetType(e) {
	case profile.ResetTypeOn, profile.ResetTypeForceOn:
		return "On"

	case profile.ResetTypeForceOff:
		return "Off"
	}

	return ""
}

// delayBounds are the bounds of the delay of the resets to one power state.
type delayBounds struct {
	lower, upper time.Duration
}

// powerDelay bounds the time from a reset to the change of the power state.
// The polls of Operations Center give the bounds: the last poll with the old state and the first poll with the new one.
// The bounds intersect per power state, because a power on can take longer than a power off.
func (d *Draft) powerDelay(rec *Recording) {
	bounds := map[string]*delayBounds{}

	var evidence []Exchange

	var transitional []string

	for _, post := range rec.where(func(e Exchange) bool { return isResetPost(e) && e.Succeeded() }) {
		target := targetPowerState(post)

		before, ok := rec.lastBefore(post, isGET(isSystem))
		if target == "" || !ok || powerState(before) == target {
			continue
		}

		var lower time.Duration

		for _, e := range rec.Exchanges {
			if e.Sequence <= post.Sequence || !isGET(isSystem)(e) {
				continue
			}

			elapsed := e.Timestamp.Sub(post.Timestamp)

			state := powerState(e)
			if state == target {
				b, ok := bounds[target]
				if !ok {
					b = &delayBounds{upper: elapsed}
					bounds[target] = b
				}

				b.lower, b.upper = max(b.lower, lower), min(b.upper, elapsed)
				evidence = append(evidence, post, e)

				break
			}

			lower = max(lower, elapsed)

			if state != "" && state != powerState(before) && !slices.Contains(transitional, state) {
				transitional = append(transitional, state)
			}
		}
	}

	if len(evidence) == 0 {
		return
	}

	// The profile has one delay. The largest lower bound fits every power state, unless it exceeds an upper bound.
	var lower time.Duration

	upper := time.Duration(math.MaxInt64)

	var ranges []string

	differs := false

	for _, target := range []string{"On", "Off"} {
		b, ok := bounds[target]
		if !ok {
			continue
		}

		b.lower = b.lower.Truncate(time.Second)
		b.upper = time.Duration(math.Ceil(b.upper.Seconds())) * time.Second
		lower, upper = max(lower, b.lower), min(upper, b.upper)
		differs = differs || b.lower > b.upper
		ranges = append(ranges, fmt.Sprintf("%s %s to %s", target, b.lower, b.upper))
	}

	v := inferred(min(lower, upper).String(), evidence...).
		withNote("the power state became %s after the reset, bounded by the polls", strings.Join(ranges, " and "))

	if differs {
		v = v.addNote("the delay differs between resets")
	}

	if len(transitional) > 0 {
		v = v.addNote("the BMC reported %s in between", strings.Join(transitional, ", "))
	}

	d.set("power.delay", v)
}
