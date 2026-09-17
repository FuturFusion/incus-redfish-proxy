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
	"strings"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/uefi"
)

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

// signatureRef locates a single entry within a set of signature lists.
type signatureRef struct {
	id         string
	entry      signatureEntry
	entryType  string
	listIndex  int
	entryIndex int
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

// collectSignatures returns the entries of the given signature lists, either the X.509
// certificates or everything else. The two views are disjoint and together cover all entries.
func collectSignatures(lists []signatureList, certificates bool) []signatureRef {
	refs := []signatureRef{}

	for listIndex, list := range lists {
		if (list.Type == "x509") != certificates {
			continue
		}

		for entryIndex, entry := range list.Entries {
			refs = append(refs, signatureRef{
				id:         fmt.Sprintf("%d", len(refs)+1),
				entry:      entry,
				entryType:  list.Type,
				listIndex:  listIndex,
				entryIndex: entryIndex,
			})
		}
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

// removeSignature drops an entry from the signature lists, dropping the list once it is empty.
func removeSignature(lists []signatureList, ref signatureRef) []signatureList {
	list := lists[ref.listIndex]
	list.Entries = slices.Delete(list.Entries, ref.entryIndex, ref.entryIndex+1)

	if len(list.Entries) == 0 {
		return slices.Delete(lists, ref.listIndex, ref.listIndex+1)
	}

	lists[ref.listIndex] = list

	return lists
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
