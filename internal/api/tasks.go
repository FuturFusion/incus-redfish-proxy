package api

import (
	"slices"
	"sync"

	"github.com/google/uuid"
)

// taskHistory is the number of tasks whose task monitor is kept around, older ones answer 404.
const taskHistory = 16

// taskRegistry keeps track of the tasks handed out for operations a client may follow by a task monitor.
type taskRegistry struct {
	mu  sync.Mutex
	ids []string
}

// add registers a completed task and returns its ID.
func (t *taskRegistry) add() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	id := uuid.NewString()

	t.ids = append(t.ids, id)
	if len(t.ids) > taskHistory {
		t.ids = t.ids[len(t.ids)-taskHistory:]
	}

	return id
}

// has reports whether the task is still known.
func (t *taskRegistry) has(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return slices.Contains(t.ids, id)
}
