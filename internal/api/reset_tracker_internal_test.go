package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"
)

func TestResetTracker_handle(t *testing.T) {
	startedAt := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	restartedAt := startedAt.Add(time.Hour)

	event := func(action string, source string, timestamp time.Time) incusapi.Event {
		metadata, err := json.Marshal(incusapi.EventLifecycle{Action: action, Source: source})
		require.NoError(t, err)

		return incusapi.Event{Type: incusapi.EventTypeLifecycle, Timestamp: timestamp, Metadata: metadata}
	}

	var seen []string

	tracker := &resetTracker{instanceName: "test-instance"}
	tracker.onPowerOn(func(ctx context.Context, e powerEvent) error {
		seen = append(seen, fmt.Sprintf("first %t %s", e.Restart, e.At.Format(time.TimeOnly)))
		return nil
	})
	tracker.onPowerOn(func(ctx context.Context, e powerEvent) error {
		seen = append(seen, "second")
		return errors.New("boom")
	})

	tracker.handle(t.Context(), event(incusapi.EventLifecycleInstanceRestarted, "/1.0/instances/other?project=foo", restartedAt))
	tracker.handle(t.Context(), event(incusapi.EventLifecycleInstanceUpdated, "/1.0/instances/test-instance?project=foo", restartedAt))
	tracker.handle(t.Context(), event(incusapi.EventLifecycleInstanceStopped, "/1.0/instances/test-instance?project=foo", restartedAt))
	require.Zero(t, tracker.last())
	require.Zero(t, tracker.lastPowerOn())
	require.Empty(t, seen)

	tracker.handle(t.Context(), event(incusapi.EventLifecycleInstanceStarted, "/1.0/instances/test-instance?project=foo", startedAt))
	require.Zero(t, tracker.last())
	require.Equal(t, startedAt, tracker.lastPowerOn())
	require.Equal(t, []string{"first false 12:00:00", "second"}, seen)

	tracker.handle(t.Context(), event(incusapi.EventLifecycleInstanceRestarted, "/1.0/instances/test-instance?project=foo", restartedAt))
	require.Equal(t, restartedAt, tracker.last())
	require.Equal(t, restartedAt, tracker.lastPowerOn())
	require.Equal(t, []string{"first false 12:00:00", "second", "first true 13:00:00", "second"}, seen)
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
