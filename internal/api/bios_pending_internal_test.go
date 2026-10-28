package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

func TestBiosStore_applyTime(t *testing.T) {
	tests := []struct {
		name      string
		bios      profile.BIOS
		requested *settingsApplyTime

		want      profile.ApplyTime
		assertErr require.ErrorAssertionFunc
	}{
		{
			name: "success - default",
			bios: profile.BIOS{Pending: true, ApplyTime: profile.ApplyTimes{Default: profile.ApplyTimeImmediate}},

			want:      profile.ApplyTimeImmediate,
			assertErr: require.NoError,
		},
		{
			name:      "success - requested without supported is ignored",
			bios:      profile.BIOS{Pending: true, ApplyTime: profile.ApplyTimes{Default: profile.ApplyTimeImmediate}},
			requested: &settingsApplyTime{ApplyTime: profile.ApplyTimeOnReset},

			want:      profile.ApplyTimeImmediate,
			assertErr: require.NoError,
		},
		{
			name:      "success - requested",
			bios:      profile.BIOS{Pending: true, ApplyTime: profile.ApplyTimes{Supported: []profile.ApplyTime{profile.ApplyTimeOnReset, profile.ApplyTimeImmediate}}},
			requested: &settingsApplyTime{ApplyTime: profile.ApplyTimeImmediate},

			want:      profile.ApplyTimeImmediate,
			assertErr: require.NoError,
		},
		{
			name:      "error - requested not supported",
			bios:      profile.BIOS{Pending: true, ApplyTime: profile.ApplyTimes{Supported: []profile.ApplyTime{profile.ApplyTimeOnReset}}},
			requested: &settingsApplyTime{ApplyTime: profile.ApplyTimeImmediate},

			want: "",
			assertErr: func(tt require.TestingT, err error, a ...any) {
				var statusErr *statusError
				require.ErrorAs(tt, err, &statusErr, a...)
				require.Equal(tt, baseRegistry+"PropertyValueNotInList", statusErr.messages[0].MessageID, a...)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := biosStore{bios: tc.bios}.applyTime(tc.requested)

			tc.assertErr(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// stateClient keeps the instance, so writes and power actions can be read back.
type stateClient struct {
	IncusClient

	mu       sync.Mutex
	instance incusapi.Instance
	actions  []string

	// updateErr fails an update, if set. It may change the instance to simulate a concurrent writer.
	updateErr func(instance *incusapi.Instance) error
}

func (c *stateClient) GetInstance(name string) (*incusapi.Instance, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current := c.instance
	current.Config = maps.Clone(c.instance.Config)

	return &current, "", nil
}

func (c *stateClient) UpdateInstance(name string, put incusapi.InstancePut, ETag string) (incusclient.Operation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.updateErr != nil {
		err := c.updateErr(&c.instance)
		if err != nil {
			return nil, err
		}
	}

	c.instance.InstancePut = put

	return doneOperation{}, nil
}

func (c *stateClient) UpdateInstanceState(name string, state incusapi.InstanceStatePut, ETag string) (incusclient.Operation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.actions = append(c.actions, state.Action)

	c.instance.Status = "Running"
	if state.Action == "stop" {
		c.instance.Status = "Stopped"
	}

	return doneOperation{}, nil
}

type doneOperation struct {
	incusclient.Operation
}

func (doneOperation) Wait() error {
	return nil
}

func TestRedfishServer_applyPendingBIOS(t *testing.T) {
	t.Parallel()

	p, err := profile.Load("testdata/profiles/pending.yaml")
	require.NoError(t, err, "load the profile")

	client := &stateClient{instance: incusapi.Instance{Status: "Running", InstancePut: incusapi.InstancePut{Config: map[string]string{"security.secureboot": "true"}}}}
	server := NewRedfishServer("test-instance", client, WithProfile(p))

	taskID, err := server.stageBIOS(map[string]any{"Mode": "Performance", "SecureBootControl": "Disabled"}, profile.ApplyTimeOnReset)
	require.NoError(t, err, "stage the BIOS attributes")

	// A restart by the guest applies the proxy state only, the instance already runs.
	metadata, err := json.Marshal(incusapi.EventLifecycle{Action: incusapi.EventLifecycleInstanceRestarted, Source: "/1.0/instances/test-instance"})
	require.NoError(t, err, "marshal the event metadata")
	server.resets.handle(t.Context(), incusapi.Event{Type: incusapi.EventTypeLifecycle, Timestamp: time.Now(), Metadata: metadata})

	staged, err := server.bios.pending(&client.instance)
	require.NoError(t, err, "read the staged attributes")
	require.Equal(t, "Performance", client.instance.Config[biosConfigPrefix+"Mode"], "the guest restart applies Mode")
	require.Equal(t, "true", client.instance.Config["security.secureboot"], "the guest restart leaves secure boot")
	require.Equal(t, map[string]any{"SecureBootControl": "Disabled"}, staged.Attributes, "the attributes that need a power cycle stay staged")

	task, _ := server.tasks.get(taskID)
	require.Equal(t, TaskV174TaskStateRunning, task.state, "the task waits for the attributes left")

	// A reset by the proxy stops the instance for the attributes left.
	rec := httptest.NewRecorder()
	server.PostRedfishV1SystemsComputerSystemIDActionsComputerSystemReset(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ResetType":"ForceRestart"}`)), "test-instance")
	require.Equal(t, http.StatusNoContent, rec.Code, "the reset succeeds")

	require.Equal(t, []string{"stop", "start"}, client.actions, "the reset power cycles the instance")
	require.Equal(t, "false", client.instance.Config["security.secureboot"], "the power cycle applies SecureBootControl")
	require.NotContains(t, client.instance.Config, biosPendingKey, "the power cycle clears the staged attributes")
	require.Contains(t, client.instance.Config, biosAppliedKey, "the power cycle records the applied attributes")

	task, _ = server.tasks.get(taskID)
	require.Equal(t, TaskV174TaskStateCompleted, task.state, "the task completes after the power cycle")

	// A refused value drops the staged attributes and fails the task.
	taskID, err = server.stageBIOS(map[string]any{"Mode": "Standard"}, profile.ApplyTimeOnReset)
	require.NoError(t, err, "stage the second BIOS attributes")

	client.instance.Config[biosPendingKey] = strings.Replace(client.instance.Config[biosPendingKey], "Standard", "Unknown", 1)

	err = server.applyPendingBIOS(true)
	require.NoError(t, err, "a refused value does not fail the apply")

	require.NotContains(t, client.instance.Config, biosPendingKey, "the refused value drops the staged attributes")
	require.Equal(t, "Performance", client.instance.Config[biosConfigPrefix+"Mode"], "Mode keeps the last applied value")

	task, _ = server.tasks.get(taskID)
	require.Equal(t, TaskV174TaskStateException, task.state, "the refused value fails the task")
	require.Equal(t, baseRegistry+"PropertyValueNotInList", task.messages[0].MessageID, "the task reports the refused value")
}

func TestRedfishServer_stageBIOSConflict(t *testing.T) {
	// Setup
	p, err := profile.Load("testdata/profiles/pending.yaml")
	require.NoError(t, err)

	client := &stateClient{instance: incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Config: map[string]string{}}}}
	server := NewRedfishServer("test-instance", client, WithProfile(p))

	concurrentTaskID := server.tasks.create(server.biosPath(), TaskV174TaskStateRunning)
	concurrent := incusapi.InstancePut{Config: map[string]string{}}
	err = server.bios.setPending(&concurrent, &pendingBIOS{TaskID: concurrentTaskID, ApplyTime: profile.ApplyTimeOnReset, Attributes: map[string]any{"SecureBootControl": "Disabled"}})
	require.NoError(t, err)

	conflicts := 1
	client.updateErr = func(instance *incusapi.Instance) error {
		if conflicts == 0 {
			return nil
		}

		conflicts--
		instance.Config = maps.Clone(concurrent.Config)

		return incusapi.StatusErrorf(http.StatusPreconditionFailed, "conflict")
	}

	// Run test
	taskID, err := server.stageBIOS(map[string]any{"Mode": "Performance"}, profile.ApplyTimeOnReset)

	// Assert
	require.NoError(t, err)
	require.Equal(t, concurrentTaskID, taskID, "the retry joins the task of the concurrent PATCH")
	require.Len(t, server.tasks.list(), 1, "the task of the lost attempt is dropped")

	staged, err := server.bios.pending(&client.instance)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"Mode": "Performance", "SecureBootControl": "Disabled"}, staged.Attributes, "the retry merges into the concurrent attributes")
}
