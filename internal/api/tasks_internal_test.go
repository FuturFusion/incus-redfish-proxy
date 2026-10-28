package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskRegistry(t *testing.T) {
	t.Parallel()

	tasks := &taskRegistry{}

	first := tasks.create("/target", TaskV174TaskStateRunning)

	running, ok := tasks.get(first)
	require.True(t, ok, "a created task is found")
	require.Equal(t, TaskV174TaskStateRunning, running.state, "a created task keeps its state")
	require.Zero(t, running.endedAt, "a running task has no end time")

	tasks.fail(first, redfishMessage{MessageID: "Base.1.18.GeneralError"})
	tasks.complete(first)

	failed, ok := tasks.get(first)
	require.True(t, ok, "an ended task is found")
	require.Equal(t, TaskV174TaskStateException, failed.state, "complete does not change a failed task")
	require.NotZero(t, failed.endedAt, "an ended task has an end time")
	require.Len(t, failed.messages, 1, "a failed task keeps its messages")

	for range taskHistory {
		tasks.create("/target", TaskV174TaskStateCompleted)
	}

	_, ok = tasks.get(first)
	require.False(t, ok, "the oldest ended task is evicted")
	require.Len(t, tasks.list(), taskHistory, "the history stays bounded")

	_, ok = tasks.get("unknown")
	require.False(t, ok, "an unknown task is not found")
}

func TestTaskRegistry_KeepsRunning(t *testing.T) {
	t.Parallel()

	// Setup
	tasks := &taskRegistry{}
	oldestCompleted := tasks.create("/target", TaskV174TaskStateCompleted)
	running := tasks.create("/target", TaskV174TaskStateRunning)

	// Run test
	for range taskHistory - 1 {
		tasks.create("/target", TaskV174TaskStateCompleted)
	}

	// Assert
	_, ok := tasks.get(running)
	require.True(t, ok, "a running task is never evicted")

	_, ok = tasks.get(oldestCompleted)
	require.False(t, ok, "the oldest ended task is evicted first")
	require.Len(t, tasks.list(), taskHistory, "the history stays bounded")
}

func TestRedfishServer_GetRedfishV1TaskMonitorsTaskID_Running(t *testing.T) {
	t.Parallel()

	server := NewRedfishServer("test-instance", nil)
	id := server.tasks.create(server.biosPath(), TaskV174TaskStateRunning)

	rec := httptest.NewRecorder()
	server.GetRedfishV1TaskMonitorsTaskID(rec, httptest.NewRequest(http.MethodGet, taskMonitorPath(id), nil), id)

	require.Equal(t, http.StatusAccepted, rec.Code, "a running task answers 202")
	require.Equal(t, "2", rec.Header().Get("Retry-After"), "the client polls again after 2 seconds")
	require.Equal(t, taskMonitorPath(id), rec.Header().Get("Location"), "the client polls the task monitor")

	resource := TaskV174Task{}
	err := json.NewDecoder(rec.Body).Decode(&resource)
	require.NoError(t, err, "decode the running task")
	require.Equal(t, TaskV174TaskStateRunning, *resource.TaskState, "the task is running")
	require.Equal(t, int64(0), *resource.PercentComplete, "a running task reports no progress")
	require.Nil(t, resource.EndTime, "a running task has no end time")

	server.tasks.fail(id)

	rec = httptest.NewRecorder()
	server.GetRedfishV1TaskMonitorsTaskID(rec, httptest.NewRequest(http.MethodGet, taskMonitorPath(id), nil), id)

	require.Equal(t, http.StatusOK, rec.Code, "an ended task answers 200")

	err = json.NewDecoder(rec.Body).Decode(&resource)
	require.NoError(t, err, "decode the failed task")
	require.Equal(t, TaskV174TaskStateException, *resource.TaskState, "the task failed")
}
