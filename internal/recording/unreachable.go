package recording

import (
	"math"
	"slices"
	"strings"
	"time"
)

func isHung(e Exchange) bool {
	return e.Status == 0 && (strings.Contains(e.TransportError, "timeout") || strings.Contains(e.TransportError, "deadline exceeded"))
}

func (d *Draft) unreachable(rec *Recording) {
	// The window starts after the last answered request. It lasts from the first request without an answer to the next request after the last one.
	keys := []string{"secure_boot.reset_keys.unreachable.after", "secure_boot.reset_keys.unreachable.for"}

	post, powerOn, end, reason := bootAfterResetKeys(rec)
	if reason != "" {
		d.explain(reason, keys...)

		return
	}

	hung := rec.where(func(e Exchange) bool { return e.Sequence > powerOn.Sequence && e.Sequence < end && isHung(e) })
	if len(hung) == 0 {
		d.set(keys[1], observed("0s", post, powerOn).withNote("the BMC answered every request after the power on"))
		d.explain("the BMC answered every request after the power on", keys[0])

		return
	}

	first, last := hung[0], hung[len(hung)-1]

	answered, ok := rec.lastBefore(first, func(e Exchange) bool { return e.Status != 0 })
	if !ok || answered.Sequence < powerOn.Sequence {
		answered = powerOn
	}

	after := answered.Timestamp.Sub(powerOn.Timestamp).Truncate(time.Second)
	d.set(keys[0], inferred(after.String(), post, powerOn, answered, first).withNote("the last answer came %s after the power on", after))

	// The timestamp is the start of a request. The client sends the next request only after it gave up on the hung one.
	next, ok := rec.firstAfter(last, func(Exchange) bool { return true })
	if !ok {
		// A window of 0s turns the simulation off, but the BMC hung for at least one request.
		length := max(last.Timestamp.Sub(first.Timestamp).Truncate(time.Second), time.Second)
		d.set(keys[1], inferred(length.String(), first, last).withNote("%d requests got no answer, the end is not recorded", len(hung)))

		return
	}

	length := next.Timestamp.Sub(first.Timestamp).Truncate(time.Second)
	d.set(keys[1], inferred(length.String(), first, last, next).withNote("%d requests got no answer until the next request", len(hung)))
}

func bootAfterResetKeys(rec *Recording) (Exchange, Exchange, int64, string) {
	// The reason tells why the recording has no such boot. It is empty, if it has one.
	post, ok := rec.first(isResetKeysPost)
	if !ok {
		return Exchange{}, Exchange{}, 0, "Operations Center sent no ResetKeys"
	}

	powerOn, ok := rec.firstAfter(post, isPowerOn)
	if !ok {
		return Exchange{}, Exchange{}, 0, "no power on followed the ResetKeys"
	}

	end := int64(math.MaxInt64)

	next, ok := rec.firstAfter(powerOn, func(e Exchange) bool { return isResetPost(e) && e.Succeeded() })
	if ok {
		end = next.Sequence
	}

	return post, powerOn, end, ""
}

func (d *Draft) visibleAfter(rec *Recording) {
	// The time lies between the last read in the old mode and the first read in setup mode.
	const key = "secure_boot.reset_keys.visible_after"

	post, powerOn, end, reason := bootAfterResetKeys(rec)
	if reason != "" {
		d.explain(reason, key)

		return
	}

	reads := rec.where(func(e Exchange) bool {
		_, _, valid := secureBootEnable(e)

		return e.Sequence > powerOn.Sequence && e.Sequence < end && isGET(isSecureBoot)(e) && valid
	})

	first := slices.IndexFunc(reads, func(e Exchange) bool {
		_, mode, _ := secureBootEnable(e)

		return mode == "SetupMode"
	})

	switch {
	case first < 0:
		d.explain("no read of SecureBoot after the power on shows setup mode", key)

	case first == 0:
		d.set(key, observed("0s", post, powerOn, reads[0]).withNote("the first read after the power on shows setup mode"))

	default:
		last, shown := reads[first-1], reads[first]
		lower := last.Timestamp.Sub(powerOn.Timestamp).Truncate(time.Second)
		upper := shown.Timestamp.Sub(powerOn.Timestamp).Truncate(time.Second)

		d.set(key, inferred(lower.String(), post, powerOn, last, shown).withNote("setup mode showed %s to %s after the power on, bounded by the reads", lower, upper))
	}
}
