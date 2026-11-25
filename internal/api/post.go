package api

import (
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
)

const rawQEMUConfKey = "raw.qemu.conf"

// OVMF waits for the splash time of the boot menu before it boots. QEMU keeps it in fw_cfg, so every reset waits.
const bootOptsSection = "boot-opts"

var postKeys = []string{"menu", "splash-time"}

// applyPOSTDuration writes the POST duration of the profile into raw.qemu.conf before a start.
func (s redfishServer) applyPOSTDuration() error {
	splash := s.profile.Scaled(s.profile.Power.PostDuration)

	err := s.updateInstance(func(instance *incusapi.Instance, put *incusapi.InstancePut) (bool, error) {
		// Without an own value, the value of the profiles of the instance is in effect.
		current, own := put.Config[rawQEMUConfKey]
		if !own {
			current = instance.ExpandedConfig[rawQEMUConfKey]
		}

		want := withPOSTDuration(current, splash)
		if want == current {
			return false, nil
		}

		// Incus refuses a change of raw.qemu.conf while the instance runs.
		if instance.Status != "Stopped" {
			log.Printf("instance %q is %s, the POST duration of %s applies at its next start", s.instanceName, strings.ToLower(instance.Status), splash)
			return false, nil
		}

		if put.Config == nil {
			put.Config = map[string]string{}
		}

		put.Config[rawQEMUConfKey] = want
		if want == "" {
			delete(put.Config, rawQEMUConfKey)
		}

		return true, nil
	})
	if err != nil {
		return fmt.Errorf("apply the POST duration: %w", err)
	}

	return nil
}

// withPOSTDuration returns conf with the boot menu keys for the splash time. A splash time of 0 removes them.
// It keeps every other section and key, because Incus merges them with the configuration it generates.
func withPOSTDuration(conf string, splash time.Duration) string {
	splash = splash.Round(time.Millisecond)

	lines := strings.Split(conf, "\n")
	kept := make([]string, 0, len(lines))
	removed := false

	// The current boot-opts section: the index of its header in kept, its kept entries and whether it lost an entry.
	inBootOpts := false
	header := -1
	entries := 0
	lost := false

	endSection := func() {
		if header >= 0 && entries == 0 && lost {
			// Incus clears a section without entries. Drop the header, so the generated entries stay.
			kept = slices.Delete(kept, header, header+1)
		}

		header, entries, lost = -1, 0, false
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "[") {
			endSection()

			inBootOpts = isBootOptsHeader(trimmed)
			if inBootOpts {
				header = len(kept)
			}

			kept = append(kept, line)

			continue
		}

		key, _, isEntry := strings.Cut(trimmed, "=")
		if inBootOpts && isEntry && slices.Contains(postKeys, strings.TrimSpace(key)) {
			removed = true
			lost = true

			continue
		}

		if inBootOpts && isEntry {
			entries++
		}

		kept = append(kept, line)
	}

	endSection()

	if !removed && splash <= 0 {
		return conf
	}

	result := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if splash <= 0 {
		return result
	}

	if result != "" {
		result += "\n\n"
	}

	return result + fmt.Sprintf("[%s]\nmenu = \"on\"\nsplash-time = \"%d\"", bootOptsSection, splash.Milliseconds())
}

func isBootOptsHeader(line string) bool {
	end := strings.IndexByte(line, ']')
	if end < 0 || strings.TrimSpace(line[1:end]) != bootOptsSection {
		return false
	}

	// QEMU has one boot-opts section, so only its index 0 counts.
	index := line[end+1:]

	return index == "" || index == "[0]"
}
