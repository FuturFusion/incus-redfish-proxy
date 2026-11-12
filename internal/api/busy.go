package api

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// busyCounter counts the refused reads of each resource since the last power on.
type busyCounter struct {
	mu        sync.Mutex
	poweredOn time.Time
	refused   map[profile.BusyResource]int
}

// refuse reports whether the read of the resource is refused, and counts it.
// A new power on starts the count again.
func (c *busyCounter) refuse(poweredOn time.Time, resource profile.BusyResource, failures int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !poweredOn.Equal(c.poweredOn) || c.refused == nil {
		c.poweredOn = poweredOn
		c.refused = map[profile.BusyResource]int{}
	}

	if c.refused[resource] >= failures {
		return false
	}

	c.refused[resource]++

	return true
}

// busy refuses the first reads of some resources after every power on, like a baseboard management controller (BMC) that can not read the data of the host yet.
func (s redfishServer) busy(next http.Handler) http.Handler {
	reads := s.profile.Power.BusyReads
	if !reads.Enabled() {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resource, ok := s.busyResource(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		poweredOn, running, err := s.poweredOnAt()
		if err != nil {
			respondStatusError(w, err)
			return
		}

		if !running || !s.busyReads.refuse(poweredOn, resource, reads.Failures) {
			next.ServeHTTP(w, r)
			return
		}

		message := "the data of the system is not available yet"
		responseRedfishError(w, reads.Error.Status, message, redfishMessage{MessageID: reads.Error.MessageID, Message: message})
	})
}

// busyResource returns the resource of a GET request, if the profile refuses its reads.
func (s redfishServer) busyResource(r *http.Request) (profile.BusyResource, bool) {
	if r.Method != http.MethodGet {
		return "", false
	}

	system := "/redfish/v1/Systems/" + s.instanceName
	paths := map[string]profile.BusyResource{
		system:                 profile.BusyResourceSystem,
		system + "/Bios":       profile.BusyResourceBios,
		system + "/SecureBoot": profile.BusyResourceSecureBoot,
	}

	resource, ok := paths[strings.TrimSuffix(r.URL.Path, "/")]
	if !ok {
		return "", false
	}

	return resource, slices.Contains(s.profile.Power.BusyReads.Resources, resource)
}
