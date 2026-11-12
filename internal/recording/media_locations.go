package recording

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// managerPathPattern matches a manager resource.
var managerPathPattern = regexp.MustCompile(`^/redfish/v1/managers/[^/]+$`)

// mediaIDPattern finds the location and the member ID of the virtual media device in the original case of the path.
var mediaIDPattern = regexp.MustCompile(`(?i)/redfish/v1/(systems|managers)/[^/]+/virtualmedia/([^/]+)`)

func isManager(p string) bool { return managerPathPattern.MatchString(p) }

// linksVirtualMedia reports whether a GET of the resource links a VirtualMedia collection.
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

// mediaLocations draws the collections that show the virtual media device, and the member ID that Operations Center inserted into.
// The location of the insert comes first, since the proxy names it in its tasks.
func (d *Draft) mediaLocations(rec *Recording, insert Exchange, inserted bool) {
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
