package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

func TestRedfishServer_unreachable(t *testing.T) {
	t.Parallel()

	poweredOn := time.Date(2026, 11, 9, 14, 4, 36, 0, time.UTC)

	tests := []struct {
		name string
		arm  func(window *unreachableWindow)
		at   time.Duration

		wantAnswered bool
	}{
		{name: "before the window", arm: (*unreachableWindow).keysDeleted, at: 89 * time.Second, wantAnswered: true},
		{name: "in the window", arm: (*unreachableWindow).keysDeleted, at: 90 * time.Second, wantAnswered: false},
		{name: "at the end of the window", arm: (*unreachableWindow).keysDeleted, at: 110 * time.Second, wantAnswered: true},
		{name: "no window without a key reset", arm: func(*unreachableWindow) {}, at: 100 * time.Second, wantAnswered: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			p := profile.Default()
			p.SecureBoot.ResetKeys.Unreachable = profile.Unreachable{After: 90 * time.Second, For: 20 * time.Second}

			server := NewRedfishServer("test-instance", instanceStateClient{state: &incusapi.InstanceState{}}, WithProfile(p), WithClock(func() time.Time { return poweredOn.Add(tc.at) }))
			tc.arm(server.unreachableWindow)
			server.openUnreachable(poweredOn)

			answered := false
			handler := server.unreachable(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { answered = true }))

			// The client gives up after a short time, like Operations Center after its timeout.
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()

			// Run test
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/redfish/v1/", nil))

			// Assert
			require.Equal(t, tc.wantAnswered, answered, "a request in the window gets no answer")
		})
	}
}

func TestUnreachableWindow_poweredOn(t *testing.T) {
	t.Parallel()

	// Setup
	poweredOn := time.Date(2026, 11, 9, 14, 4, 36, 0, time.UTC)
	window := &unreachableWindow{}
	window.keysDeleted()

	// Run test
	window.poweredOn(poweredOn, time.Minute, time.Minute)
	window.poweredOn(poweredOn.Add(time.Hour), time.Minute, time.Minute)

	// Assert
	require.True(t, window.contains(poweredOn.Add(90*time.Second)), "the first power on after the key reset opens the window")
	require.False(t, window.contains(poweredOn.Add(time.Hour+90*time.Second)), "a later power on opens no window")
}
