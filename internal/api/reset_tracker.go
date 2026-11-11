package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
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

// powerEvent describes a power on of the tracked instance.
type powerEvent struct {
	// Restart is set for an in place restart, unset for a start from stopped.
	Restart bool
	At      time.Time
}

// powerHook runs after the tracked instance started or restarted.
type powerHook func(ctx context.Context, event powerEvent) error

// resetTracker records instance starts and restarts, as Incus resets a guest in place when no full restart is needed, leaving StartedAt unchanged.
type resetTracker struct {
	instanceName string

	// hooks are only registered before the watcher starts, so they need no locking.
	hooks []powerHook

	mu            sync.Mutex
	lastRestartAt time.Time
	lastPowerOnAt time.Time
}

// last returns the time of the last restart observed.
func (t *resetTracker) last() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.lastRestartAt
}

// lastPowerOn returns when the instance was last started or restarted.
func (t *resetTracker) lastPowerOn() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.lastPowerOnAt
}

// onPowerOn registers a hook, hooks run in registration order.
func (t *resetTracker) onPowerOn(hook powerHook) {
	t.hooks = append(t.hooks, hook)
}

// handle records the event and runs the power on hooks, if it is a start or restart of the tracked instance.
func (t *resetTracker) handle(ctx context.Context, event incusapi.Event) {
	if event.Type != incusapi.EventTypeLifecycle {
		return
	}

	lifecycle := incusapi.EventLifecycle{}

	err := json.Unmarshal(event.Metadata, &lifecycle)
	if err != nil {
		return
	}

	if lifecycle.Action != incusapi.EventLifecycleInstanceRestarted && lifecycle.Action != incusapi.EventLifecycleInstanceStarted {
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

	restart := lifecycle.Action == incusapi.EventLifecycleInstanceRestarted

	t.mu.Lock()

	if restart && event.Timestamp.After(t.lastRestartAt) {
		t.lastRestartAt = event.Timestamp
	}

	if event.Timestamp.After(t.lastPowerOnAt) {
		t.lastPowerOnAt = event.Timestamp
	}

	t.mu.Unlock()

	// Hooks run outside the lock, they may talk to Incus.
	for _, hook := range t.hooks {
		err := hook(ctx, powerEvent{Restart: restart, At: event.Timestamp})
		if err != nil {
			log.Printf("power on hook of instance %q failed: %v", t.instanceName, err)
		}
	}
}

// watch feeds the lifecycle events of the source to the tracker, until the context is done.
func (t *resetTracker) watch(ctx context.Context, source EventSource) {
	for {
		err := t.listen(ctx, source)
		if ctx.Err() != nil {
			return
		}

		// The power on hooks miss the events until the reconnect.
		log.Printf("event stream of instance %q failed, reconnect in %s: %v", t.instanceName, eventRetryDelay, err)

		select {
		case <-ctx.Done():
			return

		case <-time.After(eventRetryDelay):
		}
	}
}

// listen feeds the events of one connection to the tracker. It returns, once the connection ends.
func (t *resetTracker) listen(ctx context.Context, source EventSource) error {
	listener, err := source.GetEventsByType([]string{incusapi.EventTypeLifecycle})
	if err != nil {
		return err
	}

	defer listener.Disconnect()

	stop := context.AfterFunc(ctx, listener.Disconnect)
	defer stop()

	_, err = listener.AddHandler([]string{incusapi.EventTypeLifecycle}, func(event incusapi.Event) {
		t.handle(ctx, event)
	})
	if err != nil {
		return err
	}

	err = listener.Wait()
	if err == nil {
		err = errors.New("connection closed")
	}

	return err
}
