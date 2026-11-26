package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// DefaultCloseTimeout is the time Service.Close waits for the background work, if WithCloseTimeout is not set.
const DefaultCloseTimeout = 30 * time.Second

// WithCloseTimeout sets the time Service.Close waits for the background work. Then Service.Close cancels it.
// A value of zero or less keeps DefaultCloseTimeout.
func WithCloseTimeout(d time.Duration) Option {
	return func(s *redfishServer) {
		if d > 0 {
			s.shutdown.timeout = d
		}
	}
}

var errShuttingDown = errors.New("the proxy shuts down")

type shutdown struct {
	mu      sync.Mutex
	closing bool
	work    sync.WaitGroup
	watcher sync.WaitGroup
	cancel  context.CancelFunc
	timeout time.Duration

	hold    context.Context
	release context.CancelFunc

	stopAbort func()

	once sync.Once
	err  error
}

func newShutdown() *shutdown {
	return &shutdown{timeout: DefaultCloseTimeout, stopAbort: func() {}}
}

func (s redfishServer) goBackground(fn func()) bool {
	s.shutdown.mu.Lock()
	defer s.shutdown.mu.Unlock()

	// Close waits only for admitted work. On false, the caller must undo what it prepared for fn.
	if s.shutdown.closing {
		return false
	}

	s.shutdown.work.Go(fn)

	return true
}

func (s redfishServer) respondShuttingDown(w http.ResponseWriter, work string) {
	log.Printf("refuse the %s of instance %q: %v", work, s.instanceName, errShuttingDown)
	responseErrWithMessage(w, http.StatusServiceUnavailable, errShuttingDown.Error())
}

func (s redfishServer) close() error {
	s.shutdown.once.Do(func() {
		s.shutdown.mu.Lock()
		s.shutdown.closing = true
		s.shutdown.mu.Unlock()

		if !waitTimeout(&s.shutdown.work, s.shutdown.timeout) {
			log.Printf("cancel the background work of instance %q, it still runs after %v", s.instanceName, s.shutdown.timeout)
			s.shutdown.cancel()
			s.shutdown.work.Wait()
		}

		// An abort that already started unmounts the media. Wait for it, so the eject does not race it.
		s.shutdown.stopAbort()

		s.shutdown.err = s.ejectStreamedMedia()

		s.shutdown.cancel()
		s.shutdown.watcher.Wait()
	})

	return s.shutdown.err
}

func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-done:
		return true

	case <-timer.C:
		return false
	}
}

func (s redfishServer) ejectStreamedMedia() error {
	// Only a mount dies with the process. A volume or a downloaded file still works after a restart.
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()

	return errors.Join(
		wrapStepf(s.removeStreamedDevice(), "remove the streamed media device of instance %q", s.instanceName),
		wrapStepf(s.mounted.release(), "unmount the media of instance %q", s.instanceName),
		wrapStepf(s.mounter.Cleanup(), "clean up the media directory of instance %q", s.instanceName),
	)
}

func wrapStepf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf(format+": %w", append(args, err)...)
}

func (s redfishServer) removeStreamedDevice() error {
	instance, etag, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		return err
	}

	delivery := profile.Delivery(instance.Devices[bootMediaDeviceName][bootMediaDeliveryKey])
	if delivery != profile.DeliveryStream && delivery != profile.DeliveryPrefetch {
		return nil
	}

	delete(instance.Devices, bootMediaDeviceName)

	op, err := s.client.UpdateInstance(s.instanceName, instance.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Service serves the Redfish API. Close ejects the streamed media.
type Service struct {
	http.Handler

	server *redfishServer
}

// ReleaseHeld ends the requests that the unreachable window holds.
// Register it with http.Server.RegisterOnShutdown, so the shutdown does not wait for them.
func (h *Service) ReleaseHeld() {
	h.server.shutdown.release()
}

// Close stops the server in order. Call it after the HTTP server stopped serving requests.
// It waits for the background work, cancels it after the close timeout and waits until it stops. Then it ejects the streamed media and stops the event watcher.
func (h *Service) Close() error {
	return h.server.close()
}
