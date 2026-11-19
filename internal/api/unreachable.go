package api

import (
	"net/http"
	"sync"
	"time"
)

type unreachableWindow struct {
	// Like Lenovo XCC after the power on that follows the deletion of the keys.
	mu sync.Mutex

	// armed is set by the deletion of the keys. The next power on opens the window.
	armed bool

	from  time.Time
	until time.Time
}

func (u *unreachableWindow) keysDeleted() {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.armed = true
}

func (u *unreachableWindow) poweredOn(at time.Time, after time.Duration, length time.Duration) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if !u.armed {
		return
	}

	u.armed = false
	u.from = at.Add(after)
	u.until = u.from.Add(length)
}

func (u *unreachableWindow) contains(now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	return !now.Before(u.from) && now.Before(u.until)
}

func (s redfishServer) openUnreachable(at time.Time) {
	unreachable := s.profile.SecureBoot.ResetKeys.Unreachable
	if unreachable.For <= 0 {
		return
	}

	s.unreachableWindow.poweredOn(at, s.profile.Scaled(unreachable.After), s.profile.Scaled(unreachable.For))
}

func (s redfishServer) unreachable(next http.Handler) http.Handler {
	// Hold every request in the window until the client gives up. Never answer it.
	if s.profile.SecureBoot.ResetKeys.Unreachable.For <= 0 {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.unreachableWindow.contains(s.now()) {
			next.ServeHTTP(w, r)
			return
		}

		select {
		case <-r.Context().Done():
		case <-s.ctx.Done():
		}
	})
}
