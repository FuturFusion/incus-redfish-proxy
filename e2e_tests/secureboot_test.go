//go:build e2e

package e2e_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"testing"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/uefi"
	"github.com/stretchr/testify/require"
)

const secureBootEnableKey = "user.redfish.secureboot.enable"

func (p *proxy) secureBootPath() string {
	return p.systemPath() + "/SecureBoot"
}

// secureBoot is the part of the SecureBoot resource that the tests check.
type secureBoot struct {
	Enable      bool   `json:"SecureBootEnable"`
	CurrentBoot string `json:"SecureBootCurrentBoot"`
	Mode        string `json:"SecureBootMode"`
}

func (p *proxy) secureBoot(t *testing.T) secureBoot {
	t.Helper()

	resp := p.do(t, http.MethodGet, p.secureBootPath(), nil)
	require.Equal(t, http.StatusOK, resp.status, "GET the secure boot resource: %s", resp.body)

	var state secureBoot

	resp.decode(t, &state)

	return state
}

func (p *proxy) enableSecureBoot(t *testing.T) response {
	t.Helper()

	return p.do(t, http.MethodPatch, p.secureBootPath(), map[string]any{"SecureBootEnable": true})
}

func (p *proxy) resetKeys(t *testing.T, resetKeysType string) response {
	t.Helper()

	return p.do(t, http.MethodPost, p.secureBootPath()+"/Actions/SecureBoot.ResetKeys", map[string]string{"ResetKeysType": resetKeysType})
}

// nvramVariable reads a UEFI (Unified Extensible Firmware Interface) variable of the VM directly from Incus. It returns nil, if the variable does not exist.
func nvramVariable(t *testing.T, vm string, guid string, name string) []byte {
	t.Helper()

	variable, _, err := env.client.GetInstanceNVRAMGUIDVar(vm, guid, name)
	if incusapi.StatusErrorCheck(err, http.StatusNotFound) {
		return nil
	}

	require.NoError(t, err, "read the UEFI variable %s", name)

	return variable.Binary
}

// newCertificate returns a self signed certificate as PEM and DER.
func newCertificate(t *testing.T) (string, []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "create the key")

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "incus-redfish-proxy e2e " + t.Name()},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err, "create the certificate")

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), der
}

// addToDB adds the certificate to the signature database db, which holds the allowed signers.
func (p *proxy) addToDB(t *testing.T, certificate string) response {
	t.Helper()

	return p.do(t, http.MethodPost, p.secureBootPath()+"/SecureBootDatabases/db/Certificates", map[string]any{
		"CertificateString": certificate,
		"CertificateType":   "PEM",
	})
}

func TestSecureBoot_Generic(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "generic")
	certificate, der := newCertificate(t)
	db := func() []byte { return nvramVariable(t, vm, uefi.EfiImageSecurityDatabaseGuid, "db") }

	// Run test
	state := p.secureBoot(t)

	// Assert
	require.Equal(t, secureBoot{Enable: false, CurrentBoot: "Disabled", Mode: "SetupMode"}, state, "the firmware without secure boot has no platform key")

	// Run test: add a certificate to db of the stopped VM.
	resp := p.addToDB(t, certificate)

	// Assert
	require.Equal(t, http.StatusCreated, resp.status, "POST the certificate: %s", resp.body)

	location := resp.header.Get("Location")
	require.NotEmpty(t, location, "the answer names the new certificate")
	require.True(t, bytes.Contains(db(), der), "the certificate is in the UEFI variable db")
	require.Equal(t, http.StatusOK, p.do(t, http.MethodGet, location, nil).status, "GET the new certificate")

	// Run test: the same certificate again.
	resp = p.addToDB(t, certificate)

	// Assert
	require.Equal(t, http.StatusConflict, resp.status, "POST the certificate again: %s", resp.body)

	// Run test
	resp = p.do(t, http.MethodDelete, location, nil)

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "DELETE the certificate: %s", resp.body)
	require.Nil(t, db(), "deleting the only certificate removes the UEFI variable db")

	// Run test: a running VM refuses database writes and SecureBootEnable.
	require.Equal(t, http.StatusNoContent, p.reset(t, "On").status, "power on")

	addResp := p.addToDB(t, certificate)
	enableResp := p.enableSecureBoot(t)

	// Assert
	require.Equal(t, http.StatusPreconditionFailed, addResp.status, "POST on a running VM: %s", addResp.body)
	require.Equal(t, http.StatusPreconditionFailed, enableResp.status, "SecureBootEnable on a running VM: %s", enableResp.body)
	require.Equal(t, "false", instanceConfig(t, vm)["security.secureboot"], "the refused SecureBootEnable changes nothing")

	// Run test: enable secure boot on the stopped VM, with a certificate in db.
	require.Equal(t, http.StatusNoContent, p.reset(t, "ForceOff").status, "power off")
	require.Equal(t, http.StatusCreated, p.addToDB(t, certificate).status, "POST the certificate")

	resp = p.enableSecureBoot(t)

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "SecureBootEnable: %s", resp.body)
	require.Equal(t, "true", instanceConfig(t, vm)["security.secureboot"], "SecureBootEnable is the Incus config")
	require.Equal(t, secureBoot{Enable: true, CurrentBoot: "Disabled", Mode: "UserMode"}, p.secureBoot(t), "Incus copies the firmware variables with the enrolled keys")
	require.False(t, bytes.Contains(db(), der), "the new firmware variables drop the certificate added over Redfish")

	// Run test
	require.Equal(t, http.StatusNoContent, p.reset(t, "On").status, "power on")

	// Assert
	require.Equal(t, "Enabled", p.secureBoot(t).CurrentBoot, "the VM boots with secure boot")
}

func TestSecureBoot_NoDatabaseWrite(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "no-secure-boot-write")
	certificate, _ := newCertificate(t)
	kek := func() []byte { return nvramVariable(t, vm, uefi.EfiGlobalVariableGuid, "KEK") }

	resp := p.enableSecureBoot(t)
	require.Equal(t, http.StatusNoContent, resp.status, "SecureBootEnable: %s", resp.body)
	require.Equal(t, "UserMode", p.secureBoot(t).Mode, "the firmware with secure boot has a platform key")
	require.NotNil(t, kek(), "the firmware with secure boot has a KEK")

	// Run test
	resp = p.addToDB(t, certificate)

	// Assert
	require.Equal(t, http.StatusMethodNotAllowed, resp.status, "POST the certificate: %s", resp.body)
	require.Equal(t, "Base.1.18.ActionNotSupported", resp.messageID(t), "message of the refused write")

	// Run test: a running VM refuses the reset of the keys.
	require.Equal(t, http.StatusNoContent, p.reset(t, "On").status, "power on")

	resp = p.resetKeys(t, "DeletePK")

	// Assert
	require.Equal(t, http.StatusPreconditionFailed, resp.status, "ResetKeys on a running VM: %s", resp.body)

	// Run test: DeletePK keeps the other databases.
	require.Equal(t, http.StatusNoContent, p.reset(t, "ForceOff").status, "power off")

	resp = p.resetKeys(t, "DeletePK")

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "ResetKeys DeletePK: %s", resp.body)
	require.Equal(t, "SetupMode", p.secureBoot(t).Mode, "without the platform key the firmware is in setup mode")
	require.NotNil(t, kek(), "DeletePK keeps the KEK")

	// Run test
	resp = p.resetKeys(t, "DeleteAllKeys")

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "ResetKeys DeleteAllKeys: %s", resp.body)
	require.Nil(t, kek(), "DeleteAllKeys removes the KEK")
	require.Nil(t, nvramVariable(t, vm, uefi.EfiImageSecurityDatabaseGuid, "db"), "DeleteAllKeys removes db")
	require.Equal(t, "SetupMode", p.secureBoot(t).Mode, "the firmware stays in setup mode")
}

func TestSecureBoot_EnableOnReset(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "testdata/profiles/e2e-secureboot-running.yaml")
	require.Equal(t, http.StatusNoContent, p.reset(t, "On").status, "power on")

	// Run test: SecureBootEnable on a running VM is staged.
	resp := p.enableSecureBoot(t)

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "SecureBootEnable: %s", resp.body)

	config := instanceConfig(t, vm)
	require.Equal(t, "true", config[secureBootEnableKey], "the request is staged")
	require.Equal(t, "false", config["security.secureboot"], "the request is not in effect")
	require.Equal(t, secureBoot{Enable: true, CurrentBoot: "Disabled", Mode: "SetupMode"}, p.secureBoot(t), "SecureBootEnable shows the request, the current boot the setting in effect")

	// Run test: a reset through the proxy applies it.
	require.Equal(t, http.StatusNoContent, p.reset(t, "ForceRestart").status, "ForceRestart")

	// Assert
	config = instanceConfig(t, vm)
	require.Equal(t, "true", config["security.secureboot"], "the reset applies the request")
	require.NotContains(t, config, secureBootEnableKey, "nothing is staged any more")
	require.Equal(t, "Running", instanceStatus(t, vm), "the VM runs after the reset")
	require.Equal(t, secureBoot{Enable: true, CurrentBoot: "Enabled", Mode: "UserMode"}, p.secureBoot(t), "the VM boots with secure boot")
}
