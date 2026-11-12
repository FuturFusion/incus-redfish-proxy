package recording

import (
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

func isResetKeysPost(e Exchange) bool {
	return e.Method == http.MethodPost && strings.HasSuffix(e.Path(), "/actions/secureboot.resetkeys") && e.Succeeded()
}

func isSecureBootEnable(e Exchange) bool {
	var request struct {
		SecureBootEnable *bool `json:"SecureBootEnable"`
	}

	return e.Method == http.MethodPatch && isSecureBoot(e.Path()) && e.Succeeded() &&
		e.RequestJSON(&request) == nil && request.SecureBootEnable != nil && *request.SecureBootEnable
}

// secureBootEnable returns SecureBootEnable and SecureBootMode of a GET of SecureBoot.
func secureBootEnable(e Exchange) (bool, string, bool) {
	var secureBoot struct {
		SecureBootEnable *bool  `json:"SecureBootEnable"`
		SecureBootMode   string `json:"SecureBootMode"`
	}

	err := e.ResponseJSON(&secureBoot)
	if err != nil || secureBoot.SecureBootEnable == nil {
		return false, "", false
	}

	return *secureBoot.SecureBootEnable, secureBoot.SecureBootMode, true
}

// resetKeysDisables draws whether the deleted keys switch SecureBootEnable off.
// It compares the last read before the ResetKeys with the first read in setup mode after it.
func (d *Draft) resetKeysDisables(rec *Recording) {
	const key = "secure_boot.reset_keys.disables_secure_boot"

	post, ok := rec.first(isResetKeysPost)
	if !ok {
		d.explain("Operations Center sent no ResetKeys", key)

		return
	}

	before, ok := rec.lastBefore(post, func(e Exchange) bool {
		_, _, valid := secureBootEnable(e)

		return isGET(isSecureBoot)(e) && valid
	})
	if !ok {
		d.explain("SecureBoot was not read before the ResetKeys", key)

		return
	}

	after, ok := rec.firstAfter(post, func(e Exchange) bool {
		_, mode, valid := secureBootEnable(e)

		return isGET(isSecureBoot)(e) && valid && mode == "SetupMode"
	})
	if !ok {
		d.explain("no read of SecureBoot shows setup mode", key)

		return
	}

	enabledBefore, _, _ := secureBootEnable(before)
	enabledAfter, _, _ := secureBootEnable(after)

	d.set(key, observed(yamlBool(enabledBefore && !enabledAfter), before, post, after))
}

// powerCycle is a power off and on, that no reset asked for.
type powerCycle struct {
	trigger profile.PowerCycleTrigger

	// The bounds of the time from the power on to the power off, and of the time the system stayed off.
	offAfter, offFor delayBounds

	evidence []Exchange
}

// powerCycles finds the power cycles of the system, that follow a secure boot change.
// A poll shows the system off without a reset to off before it, and a later poll shows it on without a reset to on.
func powerCycles(rec *Recording) []powerCycle {
	polls := rec.where(func(e Exchange) bool { return isGET(isSystem)(e) && powerState(e) != "" })

	var cycles []powerCycle

	for i := 1; i < len(polls); i++ {
		lastOn, firstOff := polls[i-1], polls[i]
		if powerState(lastOn) == "Off" || powerState(firstOff) != "Off" || resetBetween(rec, lastOn, firstOff) {
			continue
		}

		// The last poll that still shows off, and the first that shows on again.
		j := i
		for j+1 < len(polls) && powerState(polls[j+1]) == "Off" {
			j++
		}

		if j+1 >= len(polls) {
			break
		}

		lastOff, firstOn := polls[j], polls[j+1]
		if resetBetween(rec, lastOff, firstOn) {
			continue
		}

		powerOn, ok := rec.lastBefore(lastOn, func(e Exchange) bool { return isResetPost(e) && e.Succeeded() && targetPowerState(e) == "On" })
		if !ok {
			continue
		}

		trigger, change, ok := cycleTrigger(rec, powerOn)
		if !ok {
			continue
		}

		cycles = append(cycles, powerCycle{
			trigger:  trigger,
			offAfter: delayBounds{lower: lastOn.Timestamp.Sub(powerOn.Timestamp), upper: firstOff.Timestamp.Sub(powerOn.Timestamp)},
			offFor:   delayBounds{lower: lastOff.Timestamp.Sub(firstOff.Timestamp), upper: firstOn.Timestamp.Sub(lastOn.Timestamp)},
			evidence: slices.CompactFunc([]Exchange{change, powerOn, lastOn, firstOff, lastOff, firstOn}, func(a, b Exchange) bool { return a.Sequence == b.Sequence }),
		})

		i = j + 1
	}

	return cycles
}

// resetBetween reports a reset that the BMC accepted between the two exchanges.
func resetBetween(rec *Recording, from Exchange, to Exchange) bool {
	_, ok := rec.firstAfter(from, func(e Exchange) bool { return e.Sequence < to.Sequence && isResetPost(e) && e.Succeeded() })

	return ok
}

// cycleTrigger returns the last secure boot change before the power on.
func cycleTrigger(rec *Recording, powerOn Exchange) (profile.PowerCycleTrigger, Exchange, bool) {
	change, ok := rec.lastBefore(powerOn, func(e Exchange) bool { return isResetKeysPost(e) || isSecureBootEnable(e) })
	if !ok {
		return "", Exchange{}, false
	}

	// A power on between the change and this power on applied the change already.
	_, applied := rec.firstAfter(change, func(e Exchange) bool {
		return e.Sequence < powerOn.Sequence && isResetPost(e) && e.Succeeded() && targetPowerState(e) == "On"
	})
	if applied {
		return "", Exchange{}, false
	}

	if isResetKeysPost(change) {
		return profile.PowerCycleTriggerResetKeys, change, true
	}

	return profile.PowerCycleTriggerEnable, change, true
}

// powerCycle draws the power cycles that follow a secure boot change.
func (d *Draft) powerCycle(rec *Recording) {
	keys := []string{"secure_boot.power_cycle.after", "secure_boot.power_cycle.off_after", "secure_boot.power_cycle.off_for"}

	changes := rec.where(func(e Exchange) bool { return isResetKeysPost(e) || isSecureBootEnable(e) })
	if len(changes) == 0 {
		d.explain("Operations Center changed no secure boot setting", keys...)

		return
	}

	cycles := powerCycles(rec)
	if len(cycles) == 0 {
		d.set("secure_boot.power_cycle.after", observed("[]", changes...).withNote("no power cycle followed a secure boot change"))
		d.explain("no power cycle followed a secure boot change", keys[1:]...)

		return
	}

	var triggers []string

	var evidence []Exchange

	offAfter := delayBounds{upper: time.Duration(math.MaxInt64)}
	offFor := delayBounds{upper: time.Duration(math.MaxInt64)}

	for _, cycle := range cycles {
		if !slices.Contains(triggers, string(cycle.trigger)) {
			triggers = append(triggers, string(cycle.trigger))
		}

		evidence = append(evidence, cycle.evidence...)
		offAfter = delayBounds{lower: max(offAfter.lower, cycle.offAfter.lower), upper: min(offAfter.upper, cycle.offAfter.upper)}
		offFor = delayBounds{lower: max(offFor.lower, cycle.offFor.lower), upper: min(offFor.upper, cycle.offFor.upper)}
	}

	d.set("secure_boot.power_cycle.after", observed(yamlList(triggers), evidence...))
	d.set("secure_boot.power_cycle.off_after", boundedDuration(offAfter, "the power off came %s to %s after the power on", evidence))
	d.set("secure_boot.power_cycle.off_for", boundedDuration(offFor, "the system stayed off for %s to %s", evidence))
}

// boundedDuration takes the lower bound of the polls, unless it exceeds the upper bound.
// The note is a format with the two bounds.
func boundedDuration(b delayBounds, note string, evidence []Exchange) value {
	lower := b.lower.Truncate(time.Second)
	upper := time.Duration(math.Ceil(b.upper.Seconds())) * time.Second

	v := inferred(min(lower, upper).String(), evidence...).withNote(note+", bounded by the polls", lower, upper)
	if lower > upper {
		v = v.addNote("the cycles differ")
	}

	return v
}
