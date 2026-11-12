//go:build linux

package media_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/media"
)

func TestMount(t *testing.T) {
	t.Parallel()

	_, err := os.Stat("/dev/fuse")
	if err != nil {
		t.Skip("no /dev/fuse")
	}

	data := image(3_000_000)
	fetcher := open(t, serve(t, data, nil), media.Options{ReadAhead: 1 << 20})

	dir := filepath.Join(t.TempDir(), "mnt")

	mount, err := media.NewMount(dir, "boot-media.iso", fetcher, media.MountOptions{})
	if err != nil {
		t.Skipf("mount refused: %v", err)
	}

	got, err := os.ReadFile(mount.Path())
	require.NoError(t, err, "read the file through the mount")
	require.Equal(t, data, got, "the mount serves the image")

	_, err = os.OpenFile(mount.Path(), os.O_RDWR, 0)
	require.Error(t, err, "the file is read only")

	_, err = os.OpenFile(mount.Path(), os.O_RDONLY|syscall.O_DIRECT, 0)
	require.ErrorIs(t, err, syscall.EINVAL, "O_DIRECT is refused, so Incus uses the page cache")

	file, err := os.Open(mount.Path())
	require.NoError(t, err, "open the file")

	require.NoError(t, mount.Unmount(), "a busy mount is detached")
	require.NoError(t, file.Close(), "close the file")

	require.NoError(t, media.Cleanup(dir), "Cleanup ignores a directory that is no mount point")

	_, err = media.NewMount(dir, "boot-media.iso", fetcher, media.MountOptions{})
	require.NoError(t, err, "mount again")

	require.NoError(t, media.Cleanup(dir), "Cleanup detaches a mount left behind")

	_, err = os.Stat(mount.Path())
	require.ErrorIs(t, err, os.ErrNotExist, "the file is gone after Cleanup")
}
