package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteResource_ReadResource_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Link", "</redfish/v1/Systems/1/NetworkInterfaces>; path=/NetworkInterfaces")

	body := map[string]any{
		"@odata.id": "/redfish/v1/Systems/1",
		"Name":      "System 1",
	}

	err := writeResource(dir, body, header)
	require.NoError(t, err)

	gotBody, gotHeader, ok, err := readResource(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, body, gotBody)
	require.Equal(t, header.Get("Content-Type"), gotHeader.Get("Content-Type"))
	require.Equal(t, header.Get("Link"), gotHeader.Get("Link"))
}

func TestReadResource_NotYetScraped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "redfish", "v1")

	body, header, ok, err := readResource(dir)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, body)
	require.Nil(t, header)
}

func TestWriteNotFoundMarker_ReadNotFoundMarker_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	notFound, err := readNotFoundMarker(dir)
	require.NoError(t, err)
	require.False(t, notFound)

	err = writeNotFoundMarker(dir)
	require.NoError(t, err)

	notFound, err = readNotFoundMarker(dir)
	require.NoError(t, err)
	require.True(t, notFound)
}

func TestReadResource_CorruptIndexJSON(t *testing.T) {
	dir := t.TempDir()

	err := writeResource(dir, map[string]any{"@odata.id": "/redfish/v1/"}, http.Header{})
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, "index.json"), []byte("not valid json"), 0o600)
	require.NoError(t, err)

	_, _, ok, err := readResource(dir)
	require.Error(t, err)
	require.False(t, ok)
}
