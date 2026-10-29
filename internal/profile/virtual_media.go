package profile

import (
	"errors"
	"fmt"
	"regexp"
)

// MediaInsert is how virtual media is inserted.
type MediaInsert string

const (
	// MediaInsertStandard offers the #VirtualMedia.InsertMedia action.
	MediaInsertStandard MediaInsert = "standard"
	// MediaInsertOEM offers an OEM action instead of the standard one.
	MediaInsertOEM MediaInsert = "oem"
	// MediaInsertPatch offers no insert action, only a PATCH of the resource.
	MediaInsertPatch MediaInsert = "patch"
)

// InsertResponse is the answer to an insert action.
type InsertResponse string

const (
	// InsertResponseNoContent inserts before it answers 204.
	InsertResponseNoContent InsertResponse = "204"
	// InsertResponseTask answers 202 with a task monitor and inserts in the background.
	InsertResponseTask InsertResponse = "202-task"
)

// Delivery is how inserted media reaches the instance.
type Delivery string

const (
	// DeliveryFullFetch downloads the image into a storage volume before the insert completes.
	DeliveryFullFetch Delivery = "full-fetch"
	// DeliveryStream exposes the image as a file, whose reads become range requests.
	DeliveryStream Delivery = "stream"
	// DeliveryPrefetch exposes the image as a file and downloads the image in order while the instance reads it.
	DeliveryPrefetch Delivery = "prefetch"
)

// StreamFallback is what happens, if the image server does not support range requests or FUSE is missing.
type StreamFallback string

const (
	// StreamFallbackFullFetch downloads the image into a storage volume instead.
	StreamFallbackFullFetch StreamFallback = "full-fetch"
	// StreamFallbackError refuses the insert.
	StreamFallbackError StreamFallback = "error"
)

// oemNamePattern keeps the OEM names usable in an action name and a URI.
var oemNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// IsOEMName reports whether the name is usable as virtual_media.oem_vendor or virtual_media.oem_action.
func IsOEMName(name string) bool {
	return oemNamePattern.MatchString(name)
}

// VirtualMedia describes how virtual media is inserted.
type VirtualMedia struct {
	Insert MediaInsert `yaml:"insert"`

	// OEMVendor is the key below Actions.Oem. It must not name a real vendor.
	OEMVendor string `yaml:"oem_vendor"`
	OEMAction string `yaml:"oem_action"`

	// InsertResponse applies to the insert actions. A PATCH always answers 204.
	InsertResponse InsertResponse `yaml:"insert_response"`

	// BootOverrideOnce also offers BootSourceOverrideEnabled Once. Without it, the proxy offers Disabled and Continuous, as before profiles existed.
	BootOverrideOnce bool `yaml:"boot_override_once"`

	Delivery Delivery `yaml:"delivery"`
	Stream   Stream   `yaml:"stream"`
}

// minReadAhead is the smallest size of a range request.
const minReadAhead = 4 << 10

// Stream configures the stream and prefetch delivery.
type Stream struct {
	// ReadAhead is the size of every range request.
	ReadAhead Size `yaml:"read_ahead"`
	// Cache bounds the bytes kept in memory.
	Cache    Size           `yaml:"cache"`
	Fallback StreamFallback `yaml:"fallback"`
}

func (v VirtualMedia) validate() error {
	switch v.Insert {
	case MediaInsertStandard, MediaInsertPatch:

	case MediaInsertOEM:
		if !oemNamePattern.MatchString(v.OEMVendor) {
			return fmt.Errorf("virtual_media.oem_vendor %q must start with a letter and contain only letters and digits", v.OEMVendor)
		}

		if !oemNamePattern.MatchString(v.OEMAction) {
			return fmt.Errorf("virtual_media.oem_action %q must start with a letter and contain only letters and digits", v.OEMAction)
		}

	default:
		return fmt.Errorf("unknown virtual_media.insert %q", v.Insert)
	}

	switch v.InsertResponse {
	case InsertResponseNoContent, InsertResponseTask:

	default:
		return fmt.Errorf("unknown virtual_media.insert_response %q", v.InsertResponse)
	}

	switch v.Delivery {
	case DeliveryFullFetch, DeliveryStream, DeliveryPrefetch:

	default:
		return fmt.Errorf("unknown virtual_media.delivery %q", v.Delivery)
	}

	return v.Stream.validate()
}

func (s Stream) validate() error {
	if s.ReadAhead <= 0 || s.Cache <= 0 {
		return errors.New("virtual_media.stream.read_ahead and cache must be positive")
	}

	// Smaller range requests would cost one HTTP request for every few sectors.
	if s.ReadAhead < minReadAhead {
		return fmt.Errorf("virtual_media.stream.read_ahead %d must be at least %d", s.ReadAhead, minReadAhead)
	}

	if s.ReadAhead > s.Cache {
		return fmt.Errorf("virtual_media.stream.read_ahead %d must not exceed the cache %d", s.ReadAhead, s.Cache)
	}

	switch s.Fallback {
	case StreamFallbackFullFetch, StreamFallbackError:

	default:
		return fmt.Errorf("unknown virtual_media.stream.fallback %q", s.Fallback)
	}

	return nil
}
