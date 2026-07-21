package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// writeResource writes the index.json (resource body) and headers.json
// (response headers) files for a scraped resource into dir.
func writeResource(dir string, body any, header http.Header) error {
	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return fmt.Errorf("failed to create resource directory %q: %w", dir, err)
	}

	indexJSON, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode resource body: %w", err)
	}

	err = os.WriteFile(filepath.Join(dir, "index.json"), indexJSON, 0o600)
	if err != nil {
		return fmt.Errorf("failed to write index.json: %w", err)
	}

	headers := map[string]string{}
	for key := range header {
		headers[key] = header.Get(key)
	}

	headersJSON, err := json.MarshalIndent(map[string]map[string]string{"GET": headers}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode response headers: %w", err)
	}

	err = os.WriteFile(filepath.Join(dir, "headers.json"), headersJSON, 0o600)
	if err != nil {
		return fmt.Errorf("failed to write headers.json: %w", err)
	}

	return nil
}

// notFoundMarkerName is the file written by writeNotFoundMarker to record
// that a resource returned HTTP 404, treating it as a permanent error so
// future scrapes can skip refetching it.
const notFoundMarkerName = "index.404.json"

// writeNotFoundMarker records that the resource stored in dir returned HTTP
// 404, so that a future scrape can skip refetching it. See readNotFoundMarker.
func writeNotFoundMarker(dir string) error {
	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return fmt.Errorf("failed to create resource directory %q: %w", dir, err)
	}

	err = os.WriteFile(filepath.Join(dir, notFoundMarkerName), []byte("{}\n"), 0o600)
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", notFoundMarkerName, err)
	}

	return nil
}

// readNotFoundMarker reports whether dir holds a marker written by
// writeNotFoundMarker, i.e. whether the resource is known to have
// permanently returned HTTP 404 on a previous scrape.
func readNotFoundMarker(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, notFoundMarkerName))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to stat %s: %w", notFoundMarkerName, err)
	}

	return true, nil
}

// readResource reads back a resource previously written by writeResource.
// ok is false if dir does not contain a scraped resource yet (no
// index.json), which is not an error: it just means the resource still
// needs to be fetched. header is reconstructed on a best-effort basis from
// headers.json and is empty if that file is missing or unreadable.
func readResource(dir string) (body any, header http.Header, ok bool, err error) {
	indexJSON, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, false, nil
	} else if err != nil {
		return nil, nil, false, fmt.Errorf("failed to read index.json: %w", err)
	}

	err = json.Unmarshal(indexJSON, &body)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to decode index.json: %w", err)
	}

	header = http.Header{}

	headersJSON, err := os.ReadFile(filepath.Join(dir, "headers.json"))
	if err == nil {
		var stored map[string]map[string]string

		if json.Unmarshal(headersJSON, &stored) == nil {
			for key, value := range stored["GET"] {
				header.Set(key, value)
			}
		}
	}

	return body, header, true, nil
}
