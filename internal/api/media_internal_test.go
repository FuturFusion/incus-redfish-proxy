package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/media"
)

func TestFuseMounter(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("boot media "), 10_000)

	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "boot.iso", time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(image.Close)

	dir := t.TempDir()
	mounter := fuseMounter{dir: dir, client: image.Client()}

	stale := filepath.Join(dir, "media-stale.img")
	require.NoError(t, os.WriteFile(stale, nil, 0o600), "write a stale prefetch file")
	require.NoError(t, mounter.Cleanup(), "clean up the media directory")
	require.NoFileExists(t, stale, "Cleanup removes prefetch files of a previous process")

	mounted := mountOrSkip(t, mounter, image.URL, media.Options{ReadAhead: 4096, CacheSize: 65536, Prefetch: true})

	got, err := os.ReadFile(mounted.Path())
	require.NoError(t, err, "read the mounted image")
	require.Equal(t, data, got, "the mount serves the image data")

	require.NoError(t, mounted.Close(), "close the mount")
	require.NoFileExists(t, mounted.Path(), "Close unmounts")

	files, err := filepath.Glob(filepath.Join(dir, "media-*.img"))
	require.NoError(t, err, "list the prefetch files")
	require.Empty(t, files, "Close removes the prefetch file")
}

func mountOrSkip(t *testing.T, mounter fuseMounter, image string, opts media.Options) MountedMedia {
	t.Helper()

	mounted, err := mounter.Mount(t.Context(), image, opts)
	if err != nil {
		t.Skipf("FUSE mount refused: %v", err)
	}

	return mounted
}
