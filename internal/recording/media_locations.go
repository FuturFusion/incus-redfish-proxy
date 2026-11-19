package recording

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

var managerPathPattern = regexp.MustCompile(`^/redfish/v1/managers/[^/]+$`)

var mediaIDPattern = regexp.MustCompile(`(?i)/redfish/v1/(systems|managers)/[^/]+/virtualmedia/([^/]+)`)

func isManager(p string) bool { return managerPathPattern.MatchString(p) }

func linksVirtualMedia(rec *Recording, match func(string) bool) (Exchange, bool, bool) {
	var resource struct {
		VirtualMedia *struct {
			OdataID string `json:"@odata.id"`
		} `json:"VirtualMedia"`
	}

	get, ok := rec.first(func(e Exchange) bool { return isGET(match)(e) && e.ResponseJSON(&resource) == nil })
	if !ok {
		return Exchange{}, false, false
	}

	return get, resource.VirtualMedia != nil && resource.VirtualMedia.OdataID != "", true
}

func (d *Draft) mediaLocations(rec *Recording, insert Exchange, inserted bool) {
	// The location of the insert comes first, since the proxy names it in its tasks.
	system, systemLinks, systemRead := linksVirtualMedia(rec, isSystem)
	manager, managerLinks, managerRead := linksVirtualMedia(rec, isManager)

	var locations []string

	var evidence []Exchange

	insertLocation := ""

	if inserted {
		u, err := url.Parse(insert.URL)
		if err == nil {
			match := mediaIDPattern.FindStringSubmatch(u.Path)
			if match != nil {
				insertLocation = strings.ToLower(match[1])
				d.set("virtual_media.id", observed(quote(match[2]), insert))
				evidence = append(evidence, insert)
			}
		}
	}

	for _, location := range []struct {
		name  profile.MediaLocation
		links bool
		get   Exchange
	}{{profile.MediaLocationSystems, systemLinks, system}, {profile.MediaLocationManagers, managerLinks, manager}} {
		if !location.links {
			continue
		}

		evidence = append(evidence, location.get)

		if string(location.name) == insertLocation {
			locations = append([]string{string(location.name)}, locations...)
		} else {
			locations = append(locations, string(location.name))
		}
	}

	if !inserted {
		d.explain("Operations Center inserted no media", "virtual_media.id")
	}

	if !systemRead || !managerRead || len(locations) == 0 {
		d.explain("the system and the manager were not read", "virtual_media.locations")

		return
	}

	d.set("virtual_media.locations", observed(yamlList(locations), evidence...))
}

func (d *Draft) mediaTypes(rec *Recording, insert Exchange, inserted bool) {
	const key = "virtual_media.media_types"

	device := ""

	if inserted {
		match := mediaIDPattern.FindString(insert.Path())
		if match != "" {
			device = strings.ToLower(match)
		}
	}

	var resource struct {
		MediaTypes []string `json:"MediaTypes"`
	}

	get, ok := rec.first(func(e Exchange) bool {
		resource.MediaTypes = nil

		return isGET(virtualMediaPathPattern.MatchString)(e) && (device == "" || e.Path() == device) &&
			e.ResponseJSON(&resource) == nil && len(resource.MediaTypes) > 0
	})
	if !ok {
		d.explain("no read of a virtual media device shows its media types", key)

		return
	}

	var known, dropped []string

	for _, mediaType := range resource.MediaTypes {
		switch profile.MediaType(mediaType) {
		case profile.MediaTypeCD, profile.MediaTypeDVD, profile.MediaTypeUSBStick:
			known = append(known, mediaType)

		default:
			dropped = append(dropped, mediaType)
		}
	}

	if len(known) == 0 {
		d.explain("the device offers no media type that the proxy knows", key)

		return
	}

	v := observed(yamlList(known), get)
	if len(dropped) > 0 {
		v = v.withNote("dropped %s, the proxy knows only CD, DVD and USBStick", strings.Join(dropped, ", "))
	}

	d.set(key, v)
}
