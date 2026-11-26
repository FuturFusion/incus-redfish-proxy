//go:build e2e

package e2e_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type logEntry struct {
	ID                string `json:"Id"`
	Created           string `json:"Created"`
	Message           string `json:"Message"`
	MessageID         string `json:"MessageId"`
	AdditionalDataURI string `json:"AdditionalDataURI"`
}

func (p *proxy) logEntries(t *testing.T, logServiceID string) []logEntry {
	t.Helper()

	resp := p.do(t, http.MethodGet, p.systemPath()+"/LogServices/"+logServiceID+"/Entries", nil)
	require.Equal(t, http.StatusOK, resp.status, "GET the entries of %s: %s", logServiceID, resp.body)

	var collection struct {
		Members []logEntry `json:"Members"`
	}

	resp.decode(t, &collection)

	return collection.Members
}

func (p *proxy) consoleLog(t *testing.T) []byte {
	t.Helper()

	diagnostic := p.logEntries(t, "Diagnostic")

	console := slices.IndexFunc(diagnostic, func(entry logEntry) bool { return entry.ID == "SerialConsole" })
	require.NotEqual(t, -1, console, "the diagnostic log has the console log")

	resp := p.do(t, http.MethodGet, diagnostic[console].AdditionalDataURI, nil)
	require.Equal(t, http.StatusOK, resp.status, "GET the console log: %s", resp.body)

	return resp.body
}

func TestLogServices(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "generic")

	// Run
	resp := p.reset(t, "On")
	require.Equal(t, http.StatusNoContent, resp.status, "power on: %s", resp.body)

	// Stop only after the firmware wrote to the console. Otherwise the console log can be empty.
	eventually(t, 30*time.Second, func() bool {
		return len(p.consoleLog(t)) > 0
	}, "the firmware writes to the console")

	resp = p.reset(t, "ForceOff")
	require.Equal(t, http.StatusNoContent, resp.status, "power off: %s", resp.body)

	// Assert
	eventually(t, 30*time.Second, func() bool {
		entries := p.logEntries(t, "EventLog")

		started := slices.ContainsFunc(entries, func(entry logEntry) bool { return entry.MessageID == "IncusLifecycle.1.0.InstanceStarted" })
		stopped := slices.ContainsFunc(entries, func(entry logEntry) bool { return entry.MessageID == "IncusLifecycle.1.0.InstanceStopped" })

		return started && stopped
	}, "the event log shows the start and the stop of the VM")

	eventually(t, 30*time.Second, func() bool {
		return slices.ContainsFunc(p.logEntries(t, "HypervisorLog"), func(entry logEntry) bool {
			return strings.HasPrefix(entry.Message, "SHUTDOWN") && entry.Created != ""
		})
	}, "the hypervisor log shows the shutdown of QEMU")

	require.NotEmpty(t, p.consoleLog(t), "Incus keeps the console log after the stop")
}
