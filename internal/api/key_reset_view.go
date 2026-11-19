package api

import (
	"sync"
	"time"
)

type secureBootState struct {
	// enabled is SecureBootEnable, the requested setting.
	enabled bool

	// currentlyEnabled is the setting in effect.
	currentlyEnabled bool

	mode SecureBootV121SecureBootModeType
}

type keyResetView struct {
	// Show the state from before a key reset for a while after the power on that deletes the keys.
	mu sync.Mutex

	// before is nil, if SecureBoot shows the current state.
	before *secureBootState

	// until is the end of the time. It is zero until the power on.
	until time.Time
}

func (v *keyResetView) hide(before secureBootState) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.before = &before
	v.until = time.Time{}
}

func (v *keyResetView) poweredOn(at time.Time, d time.Duration) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.before != nil && v.until.IsZero() {
		v.until = at.Add(d)
	}
}

func (v *keyResetView) show() {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.before = nil
}

func (v *keyResetView) hidden(now time.Time, running bool) (secureBootState, bool) {
	// Between the deletion of the keys and the power on, show the old state in any case.
	// After the power on, show it while the system runs, until the time ends.
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.before == nil {
		return secureBootState{}, false
	}

	if v.until.IsZero() || (running && now.Before(v.until)) {
		return *v.before, true
	}

	v.before = nil

	return secureBootState{}, false
}

func (s redfishServer) hideKeyReset() error {
	if s.profile.SecureBoot.ResetKeys.VisibleAfter <= 0 {
		return nil
	}

	instance, _, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		return err
	}

	state, err := s.secureBootState(instance)
	if err != nil {
		return err
	}

	s.keyResetView.hide(state)

	return nil
}
