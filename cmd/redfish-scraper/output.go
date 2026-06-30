package main

import (
	"encoding/json"
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
