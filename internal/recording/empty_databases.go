package recording

import (
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// databaseCollectionPattern matches the certificate and signature collections of a key database.
var databaseCollectionPattern = regexp.MustCompile(`/securebootdatabases/[^/]+/(certificates|signatures)$`)

func isDatabaseWrite(e Exchange) bool {
	return (e.Method == http.MethodPost || e.Method == http.MethodDelete) && e.Succeeded() && strings.Contains(e.Path(), "/securebootdatabases/")
}

func isPowerOn(e Exchange) bool {
	if !isResetPost(e) || !e.Succeeded() {
		return false
	}

	switch resetType(e) {
	case "On", "ForceOn", "ForceRestart", "GracefulRestart":
		return true
	}

	return false
}

// memberCount returns the number of members of a collection read.
func memberCount(e Exchange) (int, bool) {
	var collection struct {
		Count *int `json:"Members@odata.count"`
	}

	err := e.ResponseJSON(&collection)
	if err != nil || collection.Count == nil {
		return 0, false
	}

	return *collection.Count, true
}

// emptyAfterPowerOn draws how often and how long the key databases read empty after the power ons that follow a write.
// A collection reads empty, if the last read before the power on had members.
func (d *Draft) emptyAfterPowerOn(rec *Recording) {
	keys := []string{"secure_boot.empty_after_power_on.times", "secure_boot.empty_after_power_on.for"}

	write, ok := lastOf(rec.where(isDatabaseWrite))
	if !ok {
		d.explain("Operations Center wrote no key database", keys...)

		return
	}

	powerOns := rec.where(func(e Exchange) bool { return e.Sequence > write.Sequence && isPowerOn(e) })

	times := 0

	var lower time.Duration

	var evidence []Exchange

	for i, powerOn := range powerOns {
		// The boot ends with the next reset the BMC accepted.
		end := int64(math.MaxInt64)

		next, ok := rec.firstAfter(powerOn, func(e Exchange) bool { return isResetPost(e) && e.Succeeded() })
		if ok {
			end = next.Sequence
		}

		reads := rec.where(func(e Exchange) bool {
			return e.Sequence > powerOn.Sequence && e.Sequence < end && isGET(databaseCollectionPattern.MatchString)(e)
		})

		for _, read := range reads {
			count, ok := memberCount(read)
			if !ok || count > 0 {
				continue
			}

			before, ok := rec.lastBefore(powerOn, func(e Exchange) bool {
				return isGET(databaseCollectionPattern.MatchString)(e) && e.Path() == read.Path()
			})
			if !ok {
				continue
			}

			countBefore, ok := memberCount(before)
			if !ok || countBefore == 0 {
				continue
			}

			times = i + 1
			lower = max(lower, read.Timestamp.Sub(powerOn.Timestamp))
			evidence = append(evidence, powerOn, read)
		}
	}

	if times == 0 {
		v := observed("0", write).withNote("no key database read empty after a power on")
		d.set(keys[0], v)
		d.explain("no key database read empty after a power on", keys[1])

		return
	}

	d.set(keys[0], observed(strconv.Itoa(times), evidence...).withNote("counted from the last write, seq %d", write.Sequence))
	d.set(keys[1], inferred(lower.Truncate(time.Second).String(), evidence...).withNote("the last empty read came %s after the power on, the end is not recorded", lower.Truncate(time.Second)))
}

func lastOf(exchanges []Exchange) (Exchange, bool) {
	if len(exchanges) == 0 {
		return Exchange{}, false
	}

	return exchanges[len(exchanges)-1], true
}
