package api

import (
	"encoding/json"
	"testing"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"
)

func TestResetTracker_handle(t *testing.T) {
	restartedAt := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	event := func(action string, source string, timestamp time.Time) incusapi.Event {
		metadata, err := json.Marshal(incusapi.EventLifecycle{Action: action, Source: source})
		require.NoError(t, err)

		return incusapi.Event{Type: incusapi.EventTypeLifecycle, Timestamp: timestamp, Metadata: metadata}
	}

	tracker := &resetTracker{instanceName: "test-instance"}

	tracker.handle(event(incusapi.EventLifecycleInstanceRestarted, "/1.0/instances/other?project=foo", restartedAt))
	tracker.handle(event(incusapi.EventLifecycleInstanceUpdated, "/1.0/instances/test-instance?project=foo", restartedAt))
	require.Zero(t, tracker.last())

	tracker.handle(event(incusapi.EventLifecycleInstanceRestarted, "/1.0/instances/test-instance?project=foo", restartedAt))
	require.Equal(t, restartedAt, tracker.last())
}

type instanceStateClient struct {
	IncusClient

	state *incusapi.InstanceState
}

func (c instanceStateClient) GetInstanceState(name string) (*incusapi.InstanceState, string, error) {
	return c.state, "", nil
}

func TestRedfishServer_lastResetTime(t *testing.T) {
	startedAt := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	restartedAt := startedAt.Add(time.Hour)

	server := NewRedfishServer("test-instance", instanceStateClient{state: &incusapi.InstanceState{StartedAt: startedAt}})
	instance := &incusapi.Instance{Status: "Running"}

	resetTime, err := server.lastResetTime(instance)
	require.NoError(t, err)
	require.Equal(t, startedAt, *resetTime)

	server.resets.lastRestartAt = restartedAt

	resetTime, err = server.lastResetTime(instance)
	require.NoError(t, err)
	require.Equal(t, restartedAt, *resetTime)
}
