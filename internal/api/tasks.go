package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

const taskHistory = 16

const taskRetryAfter = 2 * time.Second

const (
	taskMonitorsPath = "/redfish/v1/TaskMonitors"
	taskServicePath  = "/redfish/v1/TaskService"
	tasksPath        = taskServicePath + "/Tasks"
)

type task struct {
	id        string
	state     TaskV174TaskState
	startedAt time.Time
	endedAt   time.Time

	// target is the resource the task acts on. Only the task monitor of a completed BIOS task answers with it.
	target string

	messages []redfishMessage
}

type taskRegistry struct {
	mu    sync.Mutex
	tasks []*task
}

func (t *taskRegistry) remove(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.tasks = slices.DeleteFunc(t.tasks, func(candidate *task) bool { return candidate.id == id })
}

func (t *taskRegistry) create(target string, state TaskV174TaskState) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now().UTC()

	created := &task{
		id:        uuid.NewString(),
		state:     state,
		startedAt: now,
		target:    target,
	}

	if taskEnded(state) {
		created.endedAt = now
	}

	t.tasks = append(t.tasks, created)

	// A client still waits for a running task.
	for len(t.tasks) > taskHistory {
		index := slices.IndexFunc(t.tasks, func(candidate *task) bool { return taskEnded(candidate.state) })
		if index < 0 {
			break
		}

		t.tasks = slices.Delete(t.tasks, index, index+1)
	}

	return created.id
}

func (t *taskRegistry) get(id string) (task, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	index := slices.IndexFunc(t.tasks, func(candidate *task) bool { return candidate.id == id })
	if index < 0 {
		return task{}, false
	}

	return *t.tasks[index], true
}

func (t *taskRegistry) complete(id string) {
	t.end(id, TaskV174TaskStateCompleted)
}

func (t *taskRegistry) fail(id string, messages ...redfishMessage) {
	t.end(id, TaskV174TaskStateException, messages...)
}

func (t *taskRegistry) end(id string, state TaskV174TaskState, messages ...redfishMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, candidate := range t.tasks {
		if candidate.id != id || taskEnded(candidate.state) {
			continue
		}

		candidate.state = state
		candidate.endedAt = time.Now().UTC()
		candidate.messages = append(candidate.messages, messages...)
	}
}

func (t *taskRegistry) list() []task {
	t.mu.Lock()
	defer t.mu.Unlock()

	tasks := make([]task, 0, len(t.tasks))
	for _, candidate := range t.tasks {
		tasks = append(tasks, *candidate)
	}

	return tasks
}

func taskEnded(state TaskV174TaskState) bool {
	return state == TaskV174TaskStateCompleted || state == TaskV174TaskStateException
}

func taskMonitorPath(id string) string {
	return taskMonitorsPath + "/" + id
}

func taskPath(id string) string {
	return tasksPath + "/" + id
}

func taskResource(t task) TaskV174Task {
	percentComplete := int64(0)
	if taskEnded(t.state) {
		percentComplete = 100
	}

	messages := make([]MessageV140Message, 0, len(t.messages))
	for _, m := range t.messages {
		messages = append(messages, MessageV140Message{
			MessageID:         ref(m.MessageID),
			Message:           refNonZero(m.Message),
			MessageArgs:       refNonEmpty(m.MessageArgs),
			RelatedProperties: refNonEmpty(m.RelatedProperties),
		})
	}

	resource := TaskV174Task{
		OdataID:         ref(taskPath(t.id)),
		OdataType:       ref("#Task.v1_7_4.Task"),
		ID:              t.id,
		Name:            fmt.Sprintf("Task %s", t.id),
		TaskState:       ref(t.state),
		StartTime:       ref(t.startedAt),
		TaskMonitor:     ref(taskMonitorPath(t.id)),
		PercentComplete: ref(percentComplete),
		Messages:        refNonEmpty(messages),
	}

	if !t.endedAt.IsZero() {
		resource.EndTime = ref(t.endedAt)
	}

	return resource
}

// responseTaskRunning answers a task monitor of a task, which has not ended yet.
func responseTaskRunning(w http.ResponseWriter, t task) {
	w.Header().Set("Location", taskMonitorPath(t.id))
	w.Header().Set("Retry-After", strconv.Itoa(int(taskRetryAfter.Seconds())))
	w.WriteHeader(http.StatusAccepted)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(taskResource(t))
}

type taskService struct {
	OdataID        string       `json:"@odata.id"`
	OdataType      string       `json:"@odata.type"`
	ID             string       `json:"Id"`
	Name           string       `json:"Name"`
	ServiceEnabled bool         `json:"ServiceEnabled"`
	Tasks          OdataV4IdRef `json:"Tasks"`
}

func (s redfishServer) tasksLink() *OdataV4IdRef {
	if !s.profile.BIOS.TaskResource {
		return nil
	}

	return &OdataV4IdRef{OdataID: ref(taskServicePath)}
}

func (s redfishServer) registerTaskRoutes(mux *http.ServeMux) {
	// The task service is only added, if the profile exposes it.
	// Every task body links to its task resource.
	mux.HandleFunc("GET "+tasksPath+"/{TaskId}", func(w http.ResponseWriter, r *http.Request) {
		s.getTask(w, r.PathValue("TaskId"))
	})

	if !s.profile.BIOS.TaskResource {
		return
	}

	mux.HandleFunc("GET "+taskServicePath, s.getTaskService)
	mux.HandleFunc("GET "+tasksPath, s.getTasks)
}

func (s redfishServer) getTaskService(w http.ResponseWriter, r *http.Request) {
	response(w, taskService{
		OdataID:        taskServicePath,
		OdataType:      "#TaskService.v1_2_1.TaskService",
		ID:             "TaskService",
		Name:           "Task Service",
		ServiceEnabled: true,
		Tasks:          OdataV4IdRef{OdataID: ref(tasksPath)},
	})
}

func (s redfishServer) getTasks(w http.ResponseWriter, r *http.Request) {
	tasks := s.tasks.list()

	members := make([]OdataV4IdRef, 0, len(tasks))
	for _, t := range tasks {
		members = append(members, OdataV4IdRef{OdataID: ref(taskPath(t.id))})
	}

	response(w, resourceCollection{
		OdataID:           tasksPath,
		OdataType:         "#TaskCollection.TaskCollection",
		Name:              "Task Collection",
		Members:           members,
		MembersOdataCount: len(members),
	})
}

func (s redfishServer) getTask(w http.ResponseWriter, taskID string) {
	t, ok := s.tasks.get(taskID)
	if !ok {
		responseErr(w, http.StatusNotFound)
		return
	}

	response(w, taskResource(t))
}
