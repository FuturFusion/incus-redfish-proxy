package api

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/uefi"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// secureBootEnableKey holds the SecureBootEnable value requested on a running instance. The next reset applies it.
const secureBootEnableKey = redfishConfigPrefix + "secureboot.enable"

// nvramExtension is the Incus API extension providing access to the UEFI variables.
const nvramExtension = "instance_nvram"

// defaultVariableAttributes are the attributes used when a signature database is created from scratch.
var defaultVariableAttributes = []string{
	"NON_VOLATILE",
	"BOOTSERVICE_ACCESS",
	"RUNTIME_ACCESS",
	"TIME_BASED_AUTHENTICATED_WRITE_ACCESS",
}

// secureBootDatabase maps a Redfish SecureBootDatabase ID to an UEFI variable.
type secureBootDatabase struct {
	id   string
	guid string
	name string
}

// secureBootDatabases are the UEFI signature databases exposed over Redfish.
var secureBootDatabases = []secureBootDatabase{
	{id: "PK", guid: uefi.EfiGlobalVariableGuid, name: "PK"},
	{id: "KEK", guid: uefi.EfiGlobalVariableGuid, name: "KEK"},
	{id: "db", guid: uefi.EfiImageSecurityDatabaseGuid, name: "db"},
	{id: "dbx", guid: uefi.EfiImageSecurityDatabaseGuid, name: "dbx"},
}

// lookupSecureBootDatabase returns the signature database with the given Redfish ID.
func lookupSecureBootDatabase(databaseID string) (secureBootDatabase, bool) {
	for _, db := range secureBootDatabases {
		if db.id == databaseID {
			return db, true
		}
	}

	return secureBootDatabase{}, false
}

// signatureEntry mirrors an entry of the Incus EFI signature list dissector.
type signatureEntry struct {
	Owner string `json:"owner"`
	Data  []byte `json:"data"`
}

// signatureList mirrors a node of the Incus EFI signature list dissector.
type signatureList struct {
	Type    string           `json:"type"`
	Header  []byte           `json:"header,omitempty"`
	Entries []signatureEntry `json:"entries"`
}

// signatureRef is a signature list entry together with its Redfish ID.
type signatureRef struct {
	id        string
	entry     signatureEntry
	entryType string

	// hash identifies the content of the entry, whatever the style of the Redfish ID is.
	hash string
}

// signatureID derives a content based Redfish ID, the fingerprint for certificates.
func signatureID(entryType string, data []byte) string {
	hash := sha256.New()

	if entryType != "x509" {
		_, _ = hash.Write([]byte(entryType + "\x00"))
	}

	_, _ = hash.Write(data)

	return hex.EncodeToString(hash.Sum(nil))
}

// signatureTypeGUIDs maps the Incus signature type names to the corresponding UEFI GUIDs.
var signatureTypeGUIDs = map[string]string{
	"pkcs7":          uefi.EfiCertPkcs7Guid,
	"rsa2048":        uefi.EfiCertRsa2048Guid,
	"rsa2048-sha1":   uefi.EfiCertRsa2048Sha1Guid,
	"rsa2048-sha256": uefi.EfiCertRsa2048Sha256Guid,
	"sha1":           uefi.EfiCertSha1Guid,
	"sha224":         uefi.EfiCertSha224Guid,
	"sha256":         uefi.EfiCertSha256Guid,
	"sha384":         uefi.EfiCertSha384Guid,
	"sha512":         uefi.EfiCertSha512Guid,
	"sm3":            uefi.EfiCertSm3Guid,
	"x509":           uefi.EfiCertX509Guid,
	"x509-sha256":    uefi.EfiCertX509Sha256Guid,
	"x509-sha384":    uefi.EfiCertX509Sha384Guid,
	"x509-sha512":    uefi.EfiCertX509Sha512Guid,
	"x509-sm3":       uefi.EfiCertX509Sm3Guid,
}

// signatureTypeName returns the UEFI define name for an Incus signature type name.
func signatureTypeName(signatureType string) string {
	guid, ok := signatureTypeGUIDs[signatureType]
	if !ok {
		return signatureType
	}

	return uefi.GUIDName(guid)
}

// signatureTypeFromName returns the Incus signature type name for an UEFI define name.
func signatureTypeFromName(name string) (string, bool) {
	for signatureType, guid := range signatureTypeGUIDs {
		if strings.EqualFold(uefi.GUIDName(guid), name) || strings.EqualFold(signatureType, name) {
			return signatureType, true
		}
	}

	return "", false
}

// decodeSignatureLists converts the dissected variable data into typed signature lists.
func decodeSignatureLists(data any) ([]signatureList, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode UEFI signature lists: %w", err)
	}

	lists := []signatureList{}

	err = json.Unmarshal(raw, &lists)
	if err != nil {
		return nil, fmt.Errorf("decode UEFI signature lists: %w", err)
	}

	return lists, nil
}

// collectSignatures returns the deduplicated entries, either the X.509 certificates or everything else.
func collectSignatures(lists []signatureList, certificates bool) []signatureRef {
	refs := []signatureRef{}
	seen := map[string]bool{}

	for _, list := range lists {
		if (list.Type == "x509") != certificates {
			continue
		}

		for _, entry := range list.Entries {
			id := signatureID(list.Type, entry.Data)
			if seen[id] {
				continue
			}

			seen[id] = true

			refs = append(refs, signatureRef{
				id:        id,
				entry:     entry,
				entryType: list.Type,
				hash:      id,
			})
		}
	}

	return refs
}

// signatureRefs collects the entries with the Redfish IDs in the style of the profile.
func (s redfishServer) signatureRefs(lists []signatureList, certificates bool) []signatureRef {
	refs := collectSignatures(lists, certificates)
	if s.profile.SecureBoot.CertificateIDStyle != profile.CertificateIDStyleSequential {
		return refs
	}

	for i := range refs {
		refs[i].id = s.profile.SecureBoot.CertificateIDPrefix + strconv.Itoa(i+1)
	}

	return refs
}

// lookupSignature returns the entry with the given Redfish ID.
func lookupSignature(refs []signatureRef, id string) (signatureRef, bool) {
	for _, ref := range refs {
		if ref.id == id {
			return ref, true
		}
	}

	return signatureRef{}, false
}

// removeSignature drops all entries with the given content hash, and lists left empty.
func removeSignature(lists []signatureList, hash string, certificates bool) []signatureList {
	remaining := make([]signatureList, 0, len(lists))

	for _, list := range lists {
		if (list.Type == "x509") != certificates {
			remaining = append(remaining, list)
			continue
		}

		entries := make([]signatureEntry, 0, len(list.Entries))

		for _, entry := range list.Entries {
			if signatureID(list.Type, entry.Data) != hash {
				entries = append(entries, entry)
			}
		}

		if len(entries) == 0 {
			continue
		}

		list.Entries = entries
		remaining = append(remaining, list)
	}

	return remaining
}

// derToPEM encodes a DER certificate as PEM.
func derToPEM(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// pemToDER decodes the first certificate of a PEM blob into its canonical DER encoding.
func pemToDER(data string) ([]byte, error) {
	block, _ := pem.Decode([]byte(data))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no PEM encoded certificate found")
	}

	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}

	return certificate.Raw, nil
}

// fingerprint returns the SHA-256 fingerprint of a certificate in the usual colon separated form.
func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, 0, len(sum))

	for _, b := range sum {
		parts = append(parts, strings.ToUpper(hex.EncodeToString([]byte{b})))
	}

	return strings.Join(parts, ":")
}

// getSignatureDatabase fetches and decodes an UEFI signature database. A database which
// has not been enrolled yet is reported as empty.
func (s redfishServer) getSignatureDatabase(db secureBootDatabase) ([]signatureList, *incusapi.InstanceNVRAMVariable, string, error) {
	variable, etag, err := s.client.GetInstanceNVRAMGUIDVar(s.instanceName, db.guid, db.name)
	if incusapi.StatusErrorCheck(err, http.StatusNotFound) {
		return []signatureList{}, nil, "", nil
	}

	if err != nil {
		return nil, nil, "", err
	}

	// Incus silently leaves the dissected data unset if the variable could not be parsed.
	if variable.Data == nil && len(variable.Binary) > 0 {
		return nil, nil, "", statusErrorf(http.StatusInternalServerError, "UEFI variable %q does not contain a valid signature list", db.name)
	}

	lists, err := decodeSignatureLists(variable.Data)
	if err != nil {
		return nil, nil, "", statusErrorf(http.StatusInternalServerError, "%s", err.Error())
	}

	return lists, variable, etag, nil
}

// putSignatureDatabase writes the signature lists back, deleting the variable once it is empty.
func (s redfishServer) putSignatureDatabase(db secureBootDatabase, lists []signatureList, variable *incusapi.InstanceNVRAMVariable, etag string) error {
	if len(lists) == 0 {
		return s.client.DeleteInstanceNVRAMGUIDVar(s.instanceName, db.guid, db.name)
	}

	put := incusapi.InstanceNVRAMVariablePut{
		Data:       lists,
		Attributes: defaultVariableAttributes,
	}

	if variable != nil {
		put.Attributes = variable.Attributes
		put.Timestamp = variable.Timestamp
	}

	if put.Timestamp == nil && slices.Contains(put.Attributes, "TIME_BASED_AUTHENTICATED_WRITE_ACCESS") {
		put.Timestamp = ref(time.Now().UTC())
	}

	return s.client.UpdateInstanceNVRAMGUIDVar(s.instanceName, db.guid, db.name, put, etag)
}

// respondNVRAMError writes the response for an error reported while accessing the UEFI
// variables, forwarding the status code Incus itself used where that is meaningful.
func respondNVRAMError(w http.ResponseWriter, err error) {
	var statusErr *statusError

	if errors.As(err, &statusErr) {
		responseErrWithMessage(w, statusErr.status, statusErr.Error())
		return
	}

	status, ok := incusapi.StatusErrorMatch(err, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed)
	if !ok {
		status = http.StatusInternalServerError
	}

	responseErrWithMessage(w, status, err.Error())
}

// databaseWritable answers the profile's error, if the secure boot databases are read only over Redfish.
// Callers check it before the NVRAM (non-volatile memory) extension, so the answer does not depend on the Incus version.
func (s redfishServer) databaseWritable(w http.ResponseWriter) bool {
	if s.profile.SecureBoot.DatabaseWrite {
		return true
	}

	shape := s.profile.SecureBoot.DatabaseWriteError
	responseRedfishError(w, shape.Status, "the secure boot databases are read only", redfishMessage{MessageID: shape.MessageID, Message: "the secure boot databases are read only"})

	return false
}

// secureBootPath is the URI of the SecureBoot resource.
func (s redfishServer) secureBootPath() string {
	return fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot", s.instanceName)
}

// secureBootResponse is the SecureBoot resource, SecureBootV121Actions has no allowable values for the reset keys action.
type secureBootResponse struct {
	SecureBootV121SecureBoot

	Actions *secureBootActions `json:"Actions,omitempty"`
}

type secureBootActions struct {
	ResetKeys resetKeysAction `json:"#SecureBoot.ResetKeys"`
}

type resetKeysAction struct {
	Target          string                  `json:"target"`
	AllowableValues []profile.ResetKeysType `json:"ResetKeysType@Redfish.AllowableValues"`
}

// secureBootActions returns the actions of the SecureBoot resource, nil without the reset keys action.
func (s redfishServer) secureBootActions() *secureBootActions {
	if !s.profile.SecureBoot.ResetKeys.Supported {
		return nil
	}

	return &secureBootActions{
		ResetKeys: resetKeysAction{
			Target:          s.secureBootPath() + "/Actions/SecureBoot.ResetKeys",
			AllowableValues: s.profile.SecureBoot.ResetKeys.Types,
		},
	}
}

// registerSecureBootRoutes adds the reset keys action, which is not part of the generated routes.
func (s redfishServer) registerSecureBootRoutes(mux *http.ServeMux) {
	if !s.profile.SecureBoot.ResetKeys.Supported {
		return
	}

	mux.HandleFunc("POST /redfish/v1/Systems/{ComputerSystemId}/SecureBoot/Actions/SecureBoot.ResetKeys", func(w http.ResponseWriter, r *http.Request) {
		s.postSecureBootResetKeys(w, r, r.PathValue("ComputerSystemId"))
	})
}

func (s redfishServer) postSecureBootResetKeys(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	request := struct {
		ResetKeysType profile.ResetKeysType `json:"ResetKeysType"`
	}{}

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	if !slices.Contains(s.profile.SecureBoot.ResetKeys.Types, request.ResetKeysType) {
		message := fmt.Sprintf("reset keys type %q is not supported", request.ResetKeysType)
		responseRedfishError(w, http.StatusBadRequest, message, redfishMessage{
			MessageID:   baseRegistry + "ActionParameterValueNotInList",
			Message:     message,
			MessageArgs: []string{string(request.ResetKeysType), "ResetKeysType", "SecureBoot.ResetKeys"},
		})

		return
	}

	if !s.hasNVRAMSupport(w) {
		return
	}

	err = s.resetKeys(request.ResetKeysType)
	if err != nil {
		respondNVRAMError(w, err)
		return
	}

	if s.profile.SecureBoot.ResetKeys.Response == profile.ResetKeysResponseTask {
		responseAccepted(w, taskMonitorPath(s.tasks.create(s.secureBootPath(), TaskV174TaskStateCompleted)))
		return
	}

	responseNoContent(w)
}

// resetKeys deletes the key databases of the reset type, which puts the firmware into setup mode.
func (s redfishServer) resetKeys(resetKeysType profile.ResetKeysType) error {
	instance, _, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		return err
	}

	// Incus only allows changes to the UEFI variables of a stopped instance.
	if instance.Status != "Stopped" {
		return statusErrorf(http.StatusPreconditionFailed, "instance %q is not stopped", s.instanceName)
	}

	databases := secureBootDatabases
	if resetKeysType == profile.ResetKeysTypeDeletePK {
		pk, _ := lookupSecureBootDatabase("PK")
		databases = []secureBootDatabase{pk}
	}

	for _, db := range databases {
		err := s.client.DeleteInstanceNVRAMGUIDVar(s.instanceName, db.guid, db.name)
		if err != nil && !incusapi.StatusErrorCheck(err, http.StatusNotFound) {
			return err
		}
	}

	return nil
}

// applySecureBootEnable applies the SecureBootEnable requested on a running instance, once the instance is stopped.
func (s redfishServer) applySecureBootEnable() error {
	return s.updateInstance(func(instance *incusapi.Instance, put *incusapi.InstancePut) (bool, error) {
		enable, ok := put.Config[secureBootEnableKey]
		if !ok {
			return false, nil
		}

		put.Config["security.secureboot"] = enable
		delete(put.Config, secureBootEnableKey)

		return true, nil
	})
}
