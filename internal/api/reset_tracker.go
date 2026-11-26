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

type powerEvent struct {
	// Restart is set for an in place restart, unset for a start from stopped.
	Restart bool
	At      time.Time
}

type powerHook func(ctx context.Context, event powerEvent) error

type resetTracker struct {
	// Incus resets a guest in place when no full restart is needed. This leaves StartedAt unchanged.
	instanceName string

	// hooks are only registered before the watcher starts, so they need no locking.
	hooks []powerHook

	// observers learn about each new power on, also about the ones the proxy issued. They run under the lock.
	observers []func(at time.Time)

	// lifecycleObservers see all lifecycle events, also of other instances. Registered before the watcher starts, they run outside the lock.
	lifecycleObservers []func(at time.Time, lifecycle incusapi.EventLifecycle)

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

func (t *resetTracker) lastPowerOn() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.lastPowerOnAt
}

func (t *resetTracker) poweredOn(at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.advance(at)
}

func (t *resetTracker) advance(at time.Time) {
	if !at.After(t.lastPowerOnAt) {
		return
	}

	t.lastPowerOnAt = at
	for _, observe := range t.observers {
		observe(at)
	}
}

func (t *resetTracker) onPowerOn(hook powerHook) {
	// The hooks of one event run in registration order, but the events of Incus can overlap.
	t.hooks = append(t.hooks, hook)
}

func (t *resetTracker) handle(ctx context.Context, event incusapi.Event) {
	if event.Type != incusapi.EventTypeLifecycle {
		return
	}

	lifecycle := incusapi.EventLifecycle{}

	err := json.Unmarshal(event.Metadata, &lifecycle)
	if err != nil {
		log.Printf("lifecycle event of instance %q has bad metadata, skip it: %v", t.instanceName, err)
		return
	}

	for _, observe := range t.lifecycleObservers {
		observe(event.Timestamp, lifecycle)
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

	t.advance(event.Timestamp)

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

		// The power on hooks and the event log miss the events until the reconnect.
		log.Printf("event stream of instance %q failed, reconnect in %s: %v", t.instanceName, eventRetryDelay, err)

		select {
		case <-ctx.Done():
			return

		case <-time.After(eventRetryDelay):
		}
	}
}

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
