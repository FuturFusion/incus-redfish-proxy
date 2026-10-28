package api

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"strconv"
	"strings"
)

// overlay applies the JSON merge patches of the profile to the GET responses of their paths.
func (s redfishServer) overlay(next http.Handler) http.Handler {
	if len(s.profile.Overlay) == 0 {
		return next
	}

	placeholders := strings.NewReplacer("{system}", s.instanceName, "{manager}", managerName, "{media}", virtualMediaName)

	patches := make(map[string]map[string]any, len(s.profile.Overlay))
	for path, patch := range s.profile.Overlay {
		patches[strings.TrimSuffix(placeholders.Replace(path), "/")] = patch
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		patch, ok := patches[strings.TrimSuffix(r.URL.Path, "/")]
		if r.Method != http.MethodGet || !ok {
			next.ServeHTTP(w, r)
			return
		}

		buffer := &responseBuffer{header: http.Header{}}
		next.ServeHTTP(buffer, r)

		status := buffer.statusCode()
		body := buffer.body.Bytes()

		if status == http.StatusOK {
			body = applyOverlay(body, patch)
		}

		maps.Copy(w.Header(), buffer.header)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

// applyOverlay merges the patch into a JSON object body. Any other body stays as it is.
func applyOverlay(body []byte, patch map[string]any) []byte {
	dec := json.NewDecoder(bytes.NewReader(body))
	// Numbers stay as they are, a float64 would round large integers.
	dec.UseNumber()

	document := map[string]any{}

	err := dec.Decode(&document)
	if err != nil {
		return body
	}

	patched := &bytes.Buffer{}
	enc := json.NewEncoder(patched)
	enc.SetIndent("", "  ")

	err = enc.Encode(mergePatch(document, patch))
	if err != nil {
		return body
	}

	return patched.Bytes()
}

// mergePatch applies a JSON merge patch as RFC 7386 defines it: objects merge recursively, null deletes, anything else replaces.
func mergePatch(target any, patch any) any {
	patchObject, ok := patch.(map[string]any)
	if !ok {
		return patch
	}

	targetObject, ok := target.(map[string]any)
	if !ok {
		targetObject = map[string]any{}
	}

	for name, value := range patchObject {
		if value == nil {
			delete(targetObject, name)
			continue
		}

		targetObject[name] = mergePatch(targetObject[name], value)
	}

	return targetObject
}

// responseBuffer keeps a response, so the overlay can change it before the proxy sends it.
type responseBuffer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *responseBuffer) Header() http.Header {
	return b.header
}

func (b *responseBuffer) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *responseBuffer) Write(p []byte) (int, error) {
	return b.body.Write(p)
}

func (b *responseBuffer) statusCode() int {
	if b.status == 0 {
		return http.StatusOK
	}

	return b.status
}
