package api

import (
	"slices"
	"sync"
	"time"
)

type databaseRefresh struct {
	mu        sync.Mutex
	writtenAt time.Time

	// powerOns are the distinct power ons after the write, in order.
	powerOns []time.Time
}

func (r *databaseRefresh) written(at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.writtenAt = at
	r.powerOns = nil
}

func (r *databaseRefresh) poweredOn(at time.Time) {
	// Record every power on, so it counts also without a read during it.
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.writtenAt.IsZero() || !at.After(r.writtenAt) || slices.ContainsFunc(r.powerOns, at.Equal) {
		return
	}

	r.powerOns = append(r.powerOns, at)
}

func (r *databaseRefresh) powerOnIndex(poweredOn time.Time) int {
	// The index is -1 for no write before the power on.
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

func (s redfishServer) databasesEmpty() (bool, error) {
	// Like iDRAC while it collects the data of the host after a power on.
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
