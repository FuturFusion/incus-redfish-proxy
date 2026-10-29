package recording

import (
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
	systemPathPattern       = regexp.MustCompile(`^/redfish/v1/systems/[^/]+$`)
	certificatesPathPattern = regexp.MustCompile(`/securebootdatabases/[^/]+/(certificates|signatures)$`)
	virtualMediaPathPattern = regexp.MustCompile(`/virtualmedia/[^/]+$`)
	sequentialIDPattern     = regexp.MustCompile(`^([A-Za-z_-]*?)([0-9]+)$`)
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

func taskResponse(e Exchange) string {
	if e.Status == http.StatusAccepted {
		return quote("202-task")
	}

	return quote("204")
}

func (d *Draft) bios(rec *Recording) {
	task, ok := rec.first(isGET(isTask))
	if ok {
		d.set("bios.task_resource", observed("true", task))
	}

	patches := rec.where(func(e Exchange) bool { return isBIOSPatch(e) && e.Succeeded() })
	if len(patches) == 0 {
		return
	}

	patch := patches[0]
	settingsObject := !isBIOS(patch.Path())
	d.set("bios.settings_object", observed(yamlBool(settingsObject), patch))

	d.biosPatchResponse(patch)

	pending := d.biosPending(rec, patch)
	if pending && !settingsObject {
		d.set("bios.settings_object", inferred("true", patch).withNote("the proxy keeps pending attributes in a settings object"))
	}

	d.biosApplyTime(rec, patch, pending)

	for _, p := range patches {
		system, ok := rec.lastBefore(p, isGET(isSystem))
		if ok && powerState(system) == "On" {
			d.set("bios.patch_requires_power_off", observed("false", system, p))

			break
		}
	}

	d.biosRegistry(rec, patches)
}

func (d *Draft) biosPatchResponse(patch Exchange) {
	switch patch.Status {
	case http.StatusAccepted:
		if isTask(strings.ToLower(patch.ResponseHeader("Location"))) {
			d.set("bios.patch_response", observed(string(profile.PatchResponseTask), patch))
			d.set("bios.task_resource", observed("true", patch))

			return
		}

		d.set("bios.patch_response", observed(string(profile.PatchResponseTaskMonitor), patch))

	case http.StatusNoContent:
		d.set("bios.patch_response", observed(quote(string(profile.PatchResponseNoContent)), patch))

	default:
		d.set("bios.patch_response", inferred(quote(string(profile.PatchResponseOK)), patch).withNote("answered %d", patch.Status))
	}
}

// biosPending compares the attributes of the PATCH with the next GET of the Bios resource before a reset.
func (d *Draft) biosPending(rec *Recording, patch Exchange) bool {
	var request struct {
		Attributes map[string]any `json:"Attributes"`
	}

	err := patch.RequestJSON(&request)
	if err != nil {
		return false
	}

	for _, e := range rec.Exchanges {
		if e.Sequence <= patch.Sequence {
			continue
		}

		if isResetPost(e) {
			return false
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

			return pending
		}
	}

	return false
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

	var supported, dropped []string

	for _, applyTime := range bios.Settings.SupportedApplyTimes {
		switch {
		case applyTime == string(profile.ApplyTimeImmediate), applyTime == string(profile.ApplyTimeOnReset) && pending:
			supported = append(supported, applyTime)

		default:
			dropped = append(dropped, applyTime)
		}
	}

	v := observed(yamlList(supported), get)
	if len(dropped) > 0 {
		v = inferred(yamlList(supported), get).withNote("dropped %s, OnReset needs pending", strings.Join(dropped, ", "))
	}

	d.set("bios.apply_time.supported", v)

	var request struct {
		ApplyTime struct {
			ApplyTime string `json:"ApplyTime"`
		} `json:"@Redfish.SettingsApplyTime"`
	}

	err := patch.RequestJSON(&request)
	if err == nil && slices.Contains(supported, request.ApplyTime.ApplyTime) {
		d.set("bios.apply_time.default", observed(request.ApplyTime.ApplyTime, patch).withNote("as asked for by Operations Center"))
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

	for _, e := range rec.where(isGET(func(p string) bool { return strings.Contains(p, "/registries/") })) {
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
	}

	if len(d.attributes) > 0 {
		d.set("bios.registry.id", value{yaml: draftRegistryID, seqs: seqs, note: "vendor registry IDs are not copied"})
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

		shape, valid := errorShape(post)
		if valid {
			d.set("secure_boot.database_write_error", observed(shape, post))
		}
	}

	d.resetKeys(rec)
	d.certificateIDs(rec)
}

func (d *Draft) resetKeys(rec *Recording) {
	var secureBoot struct {
		Actions struct {
			ResetKeys struct {
				Target          string   `json:"target"`
				AllowableValues []string `json:"ResetKeysType@Redfish.AllowableValues"`
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

	post, ok := rec.first(func(e Exchange) bool {
		return e.Method == http.MethodPost && strings.HasSuffix(e.Path(), "/actions/secureboot.resetkeys") && e.Succeeded()
	})
	if ok {
		d.set("secure_boot.reset_keys.response", observed(taskResponse(post), post))
	}
}

// certificateIDs tells sequential IDs like Certificate1 apart from hashes.
func (d *Draft) certificateIDs(rec *Recording) {
	var seqs []Exchange

	var ids []string

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
		for _, member := range collection.Members {
			ids = append(ids, path.Base(member.ODataID))
		}
	}

	if len(ids) == 0 {
		return
	}

	prefix := ""

	for i, id := range ids {
		match := sequentialIDPattern.FindStringSubmatch(id)
		if match == nil || (i > 0 && match[1] != prefix) {
			d.set("secure_boot.certificate_id_style", observed(string(profile.CertificateIDStyleHash), seqs...))

			return
		}

		prefix = match[1]
	}

	d.set("secure_boot.certificate_id_style", observed(string(profile.CertificateIDStyleSequential), seqs...))
	d.set("secure_boot.certificate_id_prefix", observed(quote(prefix), seqs...))
}

func (d *Draft) virtualMedia(rec *Recording) {
	insert, ok := rec.first(func(e Exchange) bool { return e.Succeeded() && insertKind(e) != "" })
	if ok {
		kind := insertKind(insert)
		d.set("virtual_media.insert", observed(string(kind), insert))

		switch kind {
		case profile.MediaInsertOEM:
			d.set("virtual_media.insert_response", observed(taskResponse(insert), insert))

			action := oemAction(insert)
			if profile.IsOEMName(action) {
				d.set("virtual_media.oem_action", observed(action, insert))
			}

		case profile.MediaInsertStandard:
			d.set("virtual_media.insert_response", observed(taskResponse(insert), insert))
		}
	}

	var system struct {
		Boot struct {
			AllowableValues []string `json:"BootSourceOverrideEnabled@Redfish.AllowableValues"`
		} `json:"Boot"`
	}

	get, ok := rec.first(func(e Exchange) bool {
		return isGET(isSystem)(e) && e.ResponseJSON(&system) == nil && len(system.Boot.AllowableValues) > 0
	})
	if ok {
		d.set("virtual_media.boot_override_once", observed(yamlBool(slices.Contains(system.Boot.AllowableValues, "Once")), get))
	}
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
		d.set("power.response", observed(taskResponse(post), post))
	}

	d.notSettled(rec)
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
		var request struct {
			ResetType string `json:"ResetType"`
		}

		return isResetPost(e) && e.Succeeded() && e.RequestJSON(&request) == nil &&
			(request.ResetType == string(profile.ResetTypeOn) || request.ResetType == string(profile.ResetTypeForceOn))
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
		return
	}

	seconds := time.Duration(math.Ceil(window.Seconds())) * time.Second
	d.set("power.not_settled_window", inferred(seconds.String(), evidence...).withNote("time to the last refusal, a lower bound"))

	shape, ok := errorShape(refusal)
	if ok {
		d.set("power.not_settled_error", observed(shape, refusal))
	}
}
