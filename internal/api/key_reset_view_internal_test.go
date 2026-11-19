package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKeyResetView_hidden(t *testing.T) {
	t.Parallel()

	poweredOn := time.Date(2026, 11, 9, 6, 36, 26, 0, time.UTC)
	before := secureBootState{enabled: true, currentlyEnabled: true, mode: UserMode}

	tests := []struct {
		name    string
		prepare func(v *keyResetView)
		at      time.Duration
		running bool

		wantHidden bool
	}{
		{name: "nothing hidden", prepare: func(*keyResetView) {}, at: time.Minute, running: true, wantHidden: false},
		{name: "keys deleted, before the power on", prepare: func(v *keyResetView) { v.hide(before) }, at: time.Hour, running: false, wantHidden: true},
		{name: "running, in the time", prepare: hideAndPowerOn(before, poweredOn), at: 119 * time.Second, running: true, wantHidden: true},
		{name: "running, after the time", prepare: hideAndPowerOn(before, poweredOn), at: 2 * time.Minute, running: true, wantHidden: false},
		{name: "off, in the time", prepare: hideAndPowerOn(before, poweredOn), at: time.Minute, running: false, wantHidden: false},
		{name: "shown", prepare: func(v *keyResetView) { hideAndPowerOn(before, poweredOn)(v); v.show() }, at: time.Minute, running: true, wantHidden: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			view := &keyResetView{}
			tc.prepare(view)

			// Run test
			_, hidden := view.hidden(poweredOn.Add(tc.at), tc.running)

			// Assert
			require.Equal(t, tc.wantHidden, hidden, "SecureBoot shows the state from before the key reset")
		})
	}
}

func TestKeyResetView_hiddenEnds(t *testing.T) {
	t.Parallel()

	// Setup
	poweredOn := time.Date(2026, 11, 9, 6, 36, 26, 0, time.UTC)
	view := &keyResetView{}
	hideAndPowerOn(secureBootState{mode: UserMode}, poweredOn)(view)

	// Run test
	_, hiddenWhileOff := view.hidden(poweredOn.Add(time.Minute), false)
	_, hiddenAfterPowerOn := view.hidden(poweredOn.Add(61*time.Second), true)

	// Assert
	require.False(t, hiddenWhileOff, "a power off shows the key reset")
	require.False(t, hiddenAfterPowerOn, "the next power on does not hide it again")
}

func hideAndPowerOn(before secureBootState, poweredOn time.Time) func(v *keyResetView) {
	return func(v *keyResetView) {
		v.hide(before)
		v.poweredOn(poweredOn, 2*time.Minute)
	}
}
