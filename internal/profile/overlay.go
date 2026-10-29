package profile

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// overlayPlaceholders are the placeholders an overlay path may contain.
var overlayPlaceholders = []string{"{system}", "{manager}", "{media}"}

var placeholderPattern = regexp.MustCompile(`\{[^}]*\}`)

// Overlay maps a path to a JSON merge patch (RFC 7386). The proxy applies the patch to the GET response of that path.
// A path may contain the placeholders {system}, {manager} and {media}.
type Overlay map[string]map[string]any

func (o Overlay) validate() error {
	// The proxy ignores a trailing slash, so /X and /X/ name the same resource.
	trimmed := make(map[string]string, len(o))

	for _, path := range slices.Sorted(maps.Keys(o)) {
		other, ok := trimmed[strings.TrimSuffix(path, "/")]
		if ok {
			return fmt.Errorf("overlay paths %q and %q name the same resource", other, path)
		}

		trimmed[strings.TrimSuffix(path, "/")] = path
	}

	for path, patch := range o {
		if path != "/redfish/v1" && !strings.HasPrefix(path, "/redfish/v1/") {
			return fmt.Errorf("overlay path %q must start with /redfish/v1", path)
		}

		for _, placeholder := range placeholderPattern.FindAllString(path, -1) {
			if !slices.Contains(overlayPlaceholders, placeholder) {
				return fmt.Errorf("overlay path %q has the unknown placeholder %s, known are %s", path, placeholder, strings.Join(overlayPlaceholders, ", "))
			}
		}

		// The identity of a resource must not change behind the back of the handlers.
		_, ok := patch["Id"]
		if ok {
			return fmt.Errorf("overlay of %q must not change Id", path)
		}

		if touchesOdataID(patch) {
			return fmt.Errorf("overlay of %q must not change @odata.id", path)
		}

		_, err := json.Marshal(patch)
		if err != nil {
			return fmt.Errorf("overlay of %q is not JSON: %w", path, err)
		}
	}

	return nil
}

func touchesOdataID(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		_, ok := v["@odata.id"]
		if ok {
			return true
		}

		for _, member := range v {
			if touchesOdataID(member) {
				return true
			}
		}

	case []any:
		return slices.ContainsFunc(v, touchesOdataID)
	}

	return false
}

// normalize converts maps with keys of any type, so the patches only hold JSON compatible types.
func (o Overlay) normalize() error {
	for path, patch := range o {
		// jsonValue converts a map with string keys in place.
		_, err := jsonValue(patch)
		if err != nil {
			return fmt.Errorf("overlay of %q: %w", path, err)
		}
	}

	return nil
}

func jsonValue(value any) (any, error) {
	switch v := value.(type) {
	case map[any]any:
		converted := make(map[string]any, len(v))
		for key, member := range v {
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("key %v is not a string", key)
			}

			converted[name] = member
		}

		return jsonValue(converted)

	case map[string]any:
		for key, member := range v {
			converted, err := jsonValue(member)
			if err != nil {
				return nil, err
			}

			v[key] = converted
		}

		return v, nil

	case []any:
		for i, member := range v {
			converted, err := jsonValue(member)
			if err != nil {
				return nil, err
			}

			v[i] = converted
		}

		return v, nil

	default:
		return v, nil
	}
}
