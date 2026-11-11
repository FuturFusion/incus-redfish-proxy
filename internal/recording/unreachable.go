package recording

import (
	"math"
	"strings"
	"time"
)

// isHung reports a request that got no answer until the client gave up.
func isHung(e Exchange) bool {
	return e.Status == 0 && (strings.Contains(e.TransportError, "timeout") || strings.Contains(e.TransportError, "deadline exceeded"))
}

// unreachable draws the time window, in which the BMC answered no request after the power on that followed a ResetKeys.
// The window starts after the last answered request and lasts at least from the first to the last request without an answer.
func (d *Draft) unreachable(rec *Recording) {
	keys := []string{"secure_boot.reset_keys.unreachable.after", "secure_boot.reset_keys.unreachable.for"}

	post, ok := rec.first(isResetKeysPost)
	if !ok {
		d.explain("Operations Center sent no ResetKeys", keys...)

		return
	}

	powerOn, ok := rec.firstAfter(post, isPowerOn)
	if !ok {
		d.explain("no power on followed the ResetKeys", keys...)

		return
	}

	end := int64(math.MaxInt64)

	next, ok := rec.firstAfter(powerOn, func(e Exchange) bool { return isResetPost(e) && e.Succeeded() })
	if ok {
		end = next.Sequence
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
	length := last.Timestamp.Sub(first.Timestamp).Truncate(time.Second)

	d.set(keys[0], inferred(after.String(), post, powerOn, answered, first).withNote("the last answer came %s after the power on", after))
	d.set(keys[1], inferred(length.String(), first, last).withNote("%d requests got no answer, the end is not recorded", len(hung)))
}
