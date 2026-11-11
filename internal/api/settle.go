package api

import (
	"net/http"
	"strings"
	"time"
)

// settle refuses writes for a while after a power on, like a baseboard management controller (BMC) whose host still runs its power on self test.
func (s redfishServer) settle(next http.Handler) http.Handler {
	window := s.profile.Scaled(s.profile.Power.NotSettledWindow)
	if window <= 0 {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := r.Method != http.MethodGet && r.Method != http.MethodHead
		if !write || !strings.HasPrefix(r.URL.Path, "/redfish/v1/Systems/") && !strings.HasPrefix(r.URL.Path, "/redfish/v1/Managers/") {
			next.ServeHTTP(w, r)
			return
		}

		remaining, err := s.settleRemaining(window)
		if err != nil {
			respondStatusError(w, err)
			return
		}

		if remaining <= 0 {
			next.ServeHTTP(w, r)
			return
		}

		shape := s.profile.Power.NotSettledError
		message := "the system is not ready yet after the power on"

		w.Header().Set("Retry-After", retryAfterSeconds(remaining))
		responseRedfishError(w, shape.Status, message, redfishMessage{MessageID: shape.MessageID, Message: message})
	})
}

// settleRemaining returns how long the running instance still refuses writes, not more than the window.
func (s redfishServer) settleRemaining(window time.Duration) (time.Duration, error) {
	state, _, err := s.client.GetInstanceState(s.instanceName)
	if err != nil {
		return 0, err
	}

	if state.Status != "Running" {
		return 0, nil
	}

	// The tracker knows about in place restarts, which leave StartedAt unchanged.
	poweredOn := state.StartedAt
	lastPowerOn := s.resets.lastPowerOn()
	if lastPowerOn.After(poweredOn) {
		poweredOn = lastPowerOn
	}

	return min(window-s.now().Sub(poweredOn), window), nil
}
