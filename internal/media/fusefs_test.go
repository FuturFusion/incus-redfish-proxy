//go:build linux

package media_test

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
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

	release := holdOpen(t, mount.Path())

	require.NoError(t, mount.Unmount(), "a busy mount is detached")
	require.NoError(t, release(), "close the file")

	require.NoError(t, media.Cleanup(dir), "Cleanup ignores a directory that is no mount point")

	_, err = media.NewMount(dir, "boot-media.iso", fetcher, media.MountOptions{})
	require.NoError(t, err, "mount again")

	require.NoError(t, media.Cleanup(dir), "Cleanup detaches a mount left behind")

	_, err = os.Stat(mount.Path())
	require.ErrorIs(t, err, os.ErrNotExist, "the file is gone after Cleanup")
}

// holdOpen keeps the file open in a child process. The test process must not hold a file of its own mount
// while it forks: exec flushes the file, and the FUSE server cannot answer while the runtime waits for the fork.
func holdOpen(t *testing.T, path string) (release func() error) {
	t.Helper()

	cmd := exec.Command("sh", "-c", `exec 3<"$1" && echo open && cat >/dev/null`, "sh", path)

	stdin, err := cmd.StdinPipe()
	require.NoError(t, err, "connect the input of the child process")

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err, "connect the output of the child process")

	require.NoError(t, cmd.Start(), "start the child process")

	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err, "wait until the child process opened the file")
	require.Equal(t, "open\n", line, "the child process opened the file")

	return func() error {
		err := stdin.Close()
		return errors.Join(err, cmd.Wait())
	}
}
