package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// extractODataIDs recursively walks v (the result of decoding a Redfish JSON
// response) and collects every "@odata.id" string value found anywhere in the
// structure. Because Links, Actions, Oem, Attributes and collection Members
// are all just nested objects/arrays, this single rule transparently follows
// all of them.
func extractODataIDs(v any) []string {
	var ids []string

	switch val := v.(type) {
	case map[string]any:
		for key, child := range val {
			if key == "@odata.id" {
				id, ok := child.(string)
				if ok {
					ids = append(ids, stripFragment(id))
				}

				continue
			}

			ids = append(ids, extractODataIDs(child)...)
		}

	case []any:
		for _, child := range val {
			ids = append(ids, extractODataIDs(child)...)
		}
	}

	return ids
}

// stripFragment removes a trailing URL fragment, e.g.
// "/redfish/v1/X#/Foo" -> "/redfish/v1/X".
func stripFragment(raw string) string {
	path, _, _ := strings.Cut(raw, "#")

	return path
}

// parseLinkHeader parses the values of one or more HTTP "Link" response
// headers (as returned by http.Header.Values) and returns the contained URIs.
// Each header value may contain several comma-separated "<uri>; param=..."
// entries.
func parseLinkHeader(values []string) []string {
	var uris []string

	for _, value := range values {
		for entry := range strings.SplitSeq(value, ",") {
			entry = strings.TrimSpace(entry)

			uri, ok := strings.CutPrefix(entry, "<")
			if !ok {
				continue
			}

			uri, _, ok = strings.Cut(uri, ">")
			if !ok {
				continue
			}

			uris = append(uris, uri)
		}
	}

	return uris
}

// sameEndpoint reports whether raw refers to a resource on base. Relative
// paths are always considered part of the endpoint. Absolute URLs are part
// of the endpoint only if their host matches base.Host; anything else is an
// external link and is not followed.
func sameEndpoint(base *url.URL, raw string) (path string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}

	if !u.IsAbs() {
		return u.Path, true
	}

	if u.Host != base.Host {
		return "", false
	}

	return u.Path, true
}

// resourceDir maps a Redfish resource path to the directory it should be
// stored in.
//
// path originates from untrusted remote content (@odata.id values and Link
// headers returned by the scraped endpoint), so it is validated to ensure
// the resulting directory cannot escape outDir, e.g. via a "../" segment.
func resourceDir(outDir, path string) (string, error) {
	segments := strings.Split(strings.Trim(path, "/"), "/")

	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, `/\`+"\x00") {
			return "", fmt.Errorf("unsafe resource path %q", path)
		}
	}

	dir := filepath.Join(append([]string{outDir}, segments...)...)

	cleanOutDir := filepath.Clean(outDir)
	if dir != cleanOutDir && !strings.HasPrefix(dir, cleanOutDir+string(filepath.Separator)) {
		return "", fmt.Errorf("resource path %q escapes output directory", path)
	}

	return dir, nil
}
