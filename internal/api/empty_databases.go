package api

import (
	"slices"
	"sync"
	"time"
)

// databaseRefresh tracks the power ons after the last write to a key database.
type databaseRefresh struct {
	mu        sync.Mutex
	writtenAt time.Time

	// powerOns are the distinct power ons after the write, in order.
	powerOns []time.Time
}

// written records a write to a key database. The count of the power ons starts again.
func (r *databaseRefresh) written(at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.writtenAt = at
	r.powerOns = nil
}

// powerOnIndex returns the number of the power on since the last write, starting at 0. It is -1 for no write before the power on.
func (r *databaseRefresh) powerOnIndex(poweredOn time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.writtenAt.IsZero() || !poweredOn.After(r.writtenAt) {
		return -1
	}

	index := slices.IndexFunc(r.powerOns, poweredOn.Equal)
	if index < 0 {
		r.powerOns = append(r.powerOns, poweredOn)
		index = len(r.powerOns) - 1
	}

	return index
}

// databasesEmpty reports whether the key databases read empty now, like iDRAC while it collects the data of the host after a power on.
func (s redfishServer) databasesEmpty() (bool, error) {
	empty := s.profile.SecureBoot.EmptyAfterPowerOn
	if !empty.Enabled() {
		return false, nil
	}

	poweredOn, running, err := s.poweredOnAt()
	if err != nil || !running {
		return false, err
	}

	index := s.databaseRefresh.powerOnIndex(poweredOn)

	return index >= 0 && index < empty.Times && s.now().Sub(poweredOn) < s.profile.Scaled(empty.For), nil
}
