//go:build e2e

package e2e_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// alpineISO is the pinned boot image of the stream tests.
const (
	alpineURL    = "https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/x86_64/alpine-virt-3.24.3-x86_64.iso"
	alpineSHA256 = "cdc1bc0efc64d8d611bf3733f54868172a545888dfd726f518bf279bcb0ff1af"
)

// Extents of the files GRUB and Linux read from the pinned image, from xorriso -find / -exec report_lba, in bytes.
// GRUB loads the kernel and the initramfs, the booted Linux mounts the modloop from the CD.
var (
	alpineKernel    = byteRange{start: 27095 * 2048, end: 27095*2048 + 12641280}
	alpineInitramfs = byteRange{start: 7714 * 2048, end: 7714*2048 + 9843765}
	alpineModloop   = byteRange{start: 12521 * 2048, end: 12521*2048 + 22982656}
)

// streamReadAhead is the size of every range request of the stream-media profile.
const streamReadAhead = 4 << 20

var alpine = struct {
	once  sync.Once
	image []byte
	err   error
}{}

// alpineImage downloads the pinned image once into the cache and checks its hash.
func alpineImage(t *testing.T) []byte {
	t.Helper()

	alpine.once.Do(func() {
		path := filepath.Join(env.cacheDir, filepath.Base(alpineURL))

		alpine.image, alpine.err = os.ReadFile(path)
		if alpine.err != nil || checksum(alpine.image) != alpineSHA256 {
			alpine.image, alpine.err = download(alpineURL)
			if alpine.err == nil {
				alpine.err = os.WriteFile(path, alpine.image, 0o644)
			}
		}

		if alpine.err == nil && checksum(alpine.image) != alpineSHA256 {
			alpine.err = fmt.Errorf("%s has the SHA256 %s, expected %s", alpineURL, checksum(alpine.image), alpineSHA256)
		}
	})

	require.NoError(t, alpine.err, "the Alpine image")

	return alpine.image
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

func download(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}

	return io.ReadAll(resp.Body)
}

// bootFromMedia boots the VM from the inserted media.
func (p *proxy) bootFromMedia(t *testing.T) {
	t.Helper()

	resp := p.setBootOverride(t, "Continuous")
	require.Equal(t, http.StatusNoContent, resp.status, "boot from the media: %s", resp.body)

	resp = p.reset(t, "On")
	require.Equal(t, http.StatusNoContent, resp.status, "power on: %s", resp.body)
}

func TestStream_Boot(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "stream-media")
	images := newImageServer(t, "alpine.iso", alpineImage(t))
	mountPath := mountPoint(vm)

	// Run test: insert mounts the image instead of importing it.
	resp := p.insert(t, images.url)

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "insert: %s", resp.body)
	require.Equal(t, map[string]string{
		"type":                  "disk",
		"source":                filepath.Join(mountPath, "boot-media.iso"),
		"readonly":              "true",
		"user.redfish.image":    images.url,
		"user.redfish.delivery": "stream",
	}, bootMediaDevice(t, vm), "the device attaches the mounted file")
	require.False(t, bootMediaVolume(t, vm), "a streamed image is not imported")
	require.Equal(t, 1, mounts(t, mountPath), "the image is mounted")

	// Run test: boot from the streamed image.
	p.bootFromMedia(t)

	// Assert
	eventually(t, 3*time.Minute, func() bool {
		ranges := images.ranges(t, 0)

		return covers(ranges, alpineKernel.start, alpineKernel.end) && covers(ranges, alpineInitramfs.start, alpineInitramfs.end)
	}, "GRUB loads the kernel and the initramfs through the mount")

	// The chunk at the end of the initramfs reaches into the modloop, so only reads after that chunk count.
	modloopStart := (alpineInitramfs.end/streamReadAhead + 1) * streamReadAhead
	eventually(t, 3*time.Minute, func() bool {
		return overlaps(images.ranges(t, 0), modloopStart, alpineModloop.end)
	}, "the booted Linux reads the modloop from the CD")

	for _, r := range images.ranges(t, 0) {
		require.LessOrEqual(t, r.end-r.start+1, int64(streamReadAhead), "every range request is at most read_ahead: %v", r)
	}

	for _, request := range images.recorded() {
		require.False(t, request.Method == http.MethodGet && request.Range == "", "the image is never downloaded in full: %v", request)
	}

	// Run test: a new proxy mounts the image again, the next boot reads through the new mount.
	before := len(images.recorded())
	p.restart(t)

	// Assert
	require.Equal(t, 1, mounts(t, mountPath), "the new proxy mounts the image again")

	require.Equal(t, http.StatusNoContent, p.reset(t, "ForceRestart").status, "restart")
	eventually(t, 3*time.Minute, func() bool {
		ranges := images.ranges(t, before)

		return covers(ranges, alpineInitramfs.start, alpineInitramfs.end)
	}, "the VM boots again through the new mount")

	// Run test: eject a running VM.
	resp = p.eject(t)

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "eject: %s", resp.body)
	require.Nil(t, bootMediaDevice(t, vm), "eject removes the device")
	eventually(t, 30*time.Second, func() bool { return mounts(t, mountPath) == 0 }, "eject unmounts the image")
	require.Equal(t, "Running", instanceStatus(t, vm), "the VM keeps running")
}

func TestStream_Prefetch(t *testing.T) {
	t.Parallel()

	// Setup
	vm := newVM(t)
	p := startProxy(t, vm, "testdata/profiles/e2e-prefetch.yaml")
	image := alpineImage(t)
	images := newImageServer(t, "alpine.iso", image)
	prefetchFiles := filepath.Join(filepath.Dir(mountPoint(vm)), "media-*.img")

	// Run test
	resp := p.insert(t, images.url)
	require.Equal(t, http.StatusNoContent, resp.status, "insert: %s", resp.body)

	p.bootFromMedia(t)

	// Assert
	require.Equal(t, "prefetch", bootMediaDevice(t, vm)["user.redfish.delivery"], "the device is prefetched")

	eventually(t, 3*time.Minute, func() bool {
		return covers(images.ranges(t, 0), 0, int64(len(image)))
	}, "the whole image is downloaded")

	files, err := filepath.Glob(prefetchFiles)
	require.NoError(t, err, "find the prefetch file")
	require.Len(t, files, 1, "the image is prefetched into one file")
	require.Equal(t, "Running", instanceStatus(t, vm), "the VM runs")

	// Run test
	resp = p.eject(t)

	// Assert
	require.Equal(t, http.StatusNoContent, resp.status, "eject: %s", resp.body)
	eventually(t, 30*time.Second, func() bool {
		files, err := filepath.Glob(prefetchFiles)

		return err == nil && len(files) == 0
	}, "eject removes the prefetch file")
}

func TestStream_Fallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile string

		wantStatus int
		wantPool   string
	}{
		{
			name:       "fallback error refuses the insert",
			profile:    "stream-media",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "fallback full-fetch imports the image",
			profile:    "testdata/profiles/e2e-stream-fallback.yaml",
			wantStatus: http.StatusNoContent,
			wantPool:   "default",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			vm := newVM(t)
			p := startProxy(t, vm, tc.profile)
			images := newPlainImageServer(t, "install.iso", randomImage(t))

			// Run test
			resp := p.insert(t, images.url)

			// Assert
			require.Equal(t, tc.wantStatus, resp.status, "insert: %s", resp.body)
			require.Equal(t, tc.wantPool, bootMediaDevice(t, vm)["pool"], "pool of the device")
			require.Equal(t, 0, mounts(t, mountPoint(vm)), "nothing stays mounted")
		})
	}
}
