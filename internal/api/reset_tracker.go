package api

import (
	"context"
	"encoding/json"
	"net/url"
	"sync"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	incusapi "github.com/lxc/incus/v7/shared/api"
)

// eventRetryDelay is how long to wait before reconnecting to the Incus event stream.
const eventRetryDelay = 5 * time.Second

// EventSource provides the Incus event stream.
type EventSource interface {
	GetEventsByType(eventTypes []string) (listener *incusclient.EventListener, err error)
}

// resetTracker records instance restarts, as Incus resets a guest in place when no full restart is needed, leaving StartedAt unchanged.
type resetTracker struct {
	instanceName string

	mu            sync.Mutex
	lastRestartAt time.Time
}

// last returns the time of the last restart observed.
func (t *resetTracker) last() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.lastRestartAt
}

// handle records the event, if it is a restart of the tracked instance.
func (t *resetTracker) handle(event incusapi.Event) {
	if event.Type != incusapi.EventTypeLifecycle {
		return
	}

	lifecycle := incusapi.EventLifecycle{}

	err := json.Unmarshal(event.Metadata, &lifecycle)
	if err != nil {
		return
	}

	if lifecycle.Action != incusapi.EventLifecycleInstanceRestarted {
		return
	}

	source, err := url.Parse(lifecycle.Source)
	if err != nil {
		return
	}

	// The event source is expected to be scoped to the project of the instance.
	if source.Path != "/1.0/instances/"+t.instanceName {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if event.Timestamp.After(t.lastRestartAt) {
		t.lastRestartAt = event.Timestamp
	}
}

// watch feeds the lifecycle events of the source to the tracker, until the context is done.
func (t *resetTracker) watch(ctx context.Context, source EventSource) {
	for {
		listener, err := source.GetEventsByType([]string{incusapi.EventTypeLifecycle})
		if err == nil {
			stop := context.AfterFunc(ctx, listener.Disconnect)

			_, err = listener.AddHandler([]string{incusapi.EventTypeLifecycle}, t.handle)
			if err == nil {
				_ = listener.Wait()
			}

			stop()
			listener.Disconnect()
		}

		select {
		case <-ctx.Done():
			return

		case <-time.After(eventRetryDelay):
		}
	}
}
