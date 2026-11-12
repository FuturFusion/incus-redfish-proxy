package profile

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
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
	// InsertResponseOK inserts before it answers 200 with a top level @Message.ExtendedInfo.
	InsertResponseOK InsertResponse = AnswerOK
	// InsertResponseOKError inserts before it answers 200 with the messages inside an error object.
	InsertResponseOKError InsertResponse = AnswerOKError
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

	// InsertResponse applies to the insert and eject actions. The answer to a PATCH is Responses.Patch.
	InsertResponse InsertResponse `yaml:"insert_response"`

	// BootOverrideOnce also offers BootSourceOverrideEnabled Once. Without it, the proxy offers Disabled and Continuous, as before profiles existed.
	BootOverrideOnce bool `yaml:"boot_override_once"`

	Delivery Delivery `yaml:"delivery"`
	Stream   Stream   `yaml:"stream"`

	// RefusedParameters are InsertMedia parameters the BMC does not know. A client must send the insert again without them.
	RefusedParameters RefusedParameters `yaml:"refused_parameters"`

	// Locations are the collections that show the virtual media device. All of them show the same device.
	Locations []MediaLocation `yaml:"locations"`

	// ID is the member ID of the virtual media device, like CD on the proxy or 1 on iDRAC.
	ID string `yaml:"id"`
}

// MediaLocation is a collection that shows the virtual media device.
type MediaLocation string

const (
	// MediaLocationManagers shows the device below /redfish/v1/Managers/{manager}/VirtualMedia.
	MediaLocationManagers MediaLocation = "managers"

	// MediaLocationSystems shows the device below /redfish/v1/Systems/{system}/VirtualMedia.
	MediaLocationSystems MediaLocation = "systems"
)

// mediaIDPattern keeps the member ID usable in a URI.
var mediaIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// HasLocation reports whether the collection shows the virtual media device.
func (v VirtualMedia) HasLocation(location MediaLocation) bool {
	return slices.Contains(v.Locations, location)
}

// RefusedParameters refuse an InsertMedia request that holds one of the names.
type RefusedParameters struct {
	Names []string   `yaml:"names"`
	Error ErrorShape `yaml:"error"`
}

// insertMediaParameters are the optional parameters of InsertMedia in the Redfish schema. Image is required, so a profile can not refuse it.
var insertMediaParameters = []string{"Inserted", "MediaType", "Password", "TransferMethod", "TransferProtocolType", "UserName", "WriteProtected"}

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
	case InsertResponseNoContent, InsertResponseTask, InsertResponseOK, InsertResponseOKError:

	default:
		return fmt.Errorf("unknown virtual_media.insert_response %q", v.InsertResponse)
	}

	switch v.Delivery {
	case DeliveryFullFetch, DeliveryStream, DeliveryPrefetch:

	default:
		return fmt.Errorf("unknown virtual_media.delivery %q", v.Delivery)
	}

	if len(v.Locations) == 0 {
		return errors.New("virtual_media.locations must not be empty")
	}

	for _, location := range v.Locations {
		switch location {
		case MediaLocationManagers, MediaLocationSystems:

		default:
			return fmt.Errorf("unknown virtual_media.locations entry %q", location)
		}
	}

	if !mediaIDPattern.MatchString(v.ID) {
		return fmt.Errorf("virtual_media.id %q must start with a letter or digit and contain only letters, digits, dots, dashes and underscores", v.ID)
	}

	for _, name := range v.RefusedParameters.Names {
		if !slices.Contains(insertMediaParameters, name) {
			return fmt.Errorf("virtual_media.refused_parameters.names entry %q is not one of %s", name, strings.Join(insertMediaParameters, ", "))
		}
	}

	if len(v.RefusedParameters.Names) > 0 {
		err := v.RefusedParameters.Error.Validate()
		if err != nil {
			return fmt.Errorf("virtual_media.refused_parameters.error: %w", err)
		}
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
