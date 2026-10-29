package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	incusapi "github.com/lxc/incus/v7/shared/api"

	"github.com/FuturFusion/incus-redfish-proxy/internal/media"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// bootMediaDeliveryKey tells on the device, how the image reaches the instance.
const bootMediaDeliveryKey = redfishConfigPrefix + "delivery"

// MediaMounter exposes an image as a local file, which Incus attaches as a disk.
type MediaMounter interface {
	// Mount exposes the image. The file outlives ctx, Close of the result removes it.
	Mount(ctx context.Context, image string, opts media.Options) (MountedMedia, error)

	// Cleanup removes what a previous process left behind.
	Cleanup() error
}

// MountedMedia is an image exposed as a local file.
type MountedMedia interface {
	Path() string

	// Close removes the file and stops reading the image.
	Close() error
}

// WithMediaDir sets the directory for the media of the instance, it must be visible to incusd.
func WithMediaDir(dir string) Option {
	return func(s *redfishServer) {
		s.mediaDir = dir
	}
}

// WithMediaMounter replaces the FUSE mounter, which streams images below the media directory.
func WithMediaMounter(mounter MediaMounter) Option {
	return func(s *redfishServer) {
		s.mounter = mounter
	}
}

// mountedMedia holds the media mounted for the instance.
type mountedMedia struct {
	mu      sync.Mutex
	current MountedMedia
}

func (m *mountedMedia) set(mounted MountedMedia) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.current = mounted
}

// close removes the mounted media, if there is any.
func (m *mountedMedia) close() error {
	m.mu.Lock()
	current := m.current
	m.current = nil
	m.mu.Unlock()

	if current == nil {
		return nil
	}

	return current.Close()
}

func (s redfishServer) mediaOptions(delivery profile.Delivery) media.Options {
	return media.Options{
		ReadAhead: int64(s.profile.VirtualMedia.Stream.ReadAhead),
		CacheSize: int64(s.profile.VirtualMedia.Stream.Cache),
		Prefetch:  delivery == profile.DeliveryPrefetch,
	}
}

// mountMedia mounts the image for the stream and prefetch delivery.
// It returns no media, if the full fetch delivers the image instead.
func (s redfishServer) mountMedia(ctx context.Context, image string) (MountedMedia, error) {
	delivery := s.profile.VirtualMedia.Delivery
	if delivery == profile.DeliveryFullFetch {
		return nil, nil
	}

	mounted, err := s.mounter.Mount(ctx, image, s.mediaOptions(delivery))
	if err == nil {
		return mounted, nil
	}

	// Other errors would hide a broken setup behind the fallback.
	canFallBack := errors.Is(err, media.ErrRangeNotSupported) || errors.Is(err, media.ErrNotSupported)
	if canFallBack && s.profile.VirtualMedia.Stream.Fallback == profile.StreamFallbackFullFetch {
		log.Printf("stream image %q failed, fetching it in full: %v", image, err)
		return nil, nil
	}

	if errors.Is(err, media.ErrImage) {
		return nil, &statusError{status: http.StatusBadRequest, err: fmt.Errorf("stream image %q: %w", image, err)}
	}

	return nil, fmt.Errorf("stream image %q: %w", image, err)
}

// attachMountedMedia adds the mounted image as the boot media device.
func (s redfishServer) attachMountedMedia(instance *incusapi.Instance, etag string, image string, mounted MountedMedia) error {
	instance.Devices[bootMediaDeviceName] = map[string]string{
		"type":               "disk",
		"source":             mounted.Path(),
		"readonly":           "true",
		bootMediaImageKey:    image,
		bootMediaDeliveryKey: string(s.profile.VirtualMedia.Delivery),
	}

	op, err := s.client.UpdateInstance(s.instanceName, instance.Writable(), etag)
	if err == nil {
		err = op.Wait()
	}

	if err != nil {
		return combineRollbackError(err, mounted.Close())
	}

	s.mounted.set(mounted)

	return nil
}

// restoreMedia removes stale mounts and mounts the image of a streamed device again, so the instance finds the image on its next start.
func (s redfishServer) restoreMedia() {
	err := s.mounter.Cleanup()
	if err != nil {
		log.Printf("clean up the media of instance %q: %v", s.instanceName, err)
	}

	instance, _, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		log.Printf("restore the media of instance %q: %v", s.instanceName, err)
		return
	}

	device := instance.Devices[bootMediaDeviceName]

	delivery := profile.Delivery(device[bootMediaDeliveryKey])
	if delivery != profile.DeliveryStream && delivery != profile.DeliveryPrefetch {
		return
	}

	mounted, err := s.mounter.Mount(s.ctx, device[bootMediaImageKey], s.mediaOptions(delivery))
	if err != nil {
		log.Printf("restore the media of instance %q, eject it to clean up: %v", s.instanceName, err)
		return
	}

	s.mounted.set(mounted)

	// QEMU keeps the file of the old mount open, a new mount does not reach it.
	if instance.Status != "Stopped" {
		log.Printf("the media of instance %q fails to read until it is ejected and inserted again, the proxy restarted while the instance ran", s.instanceName)
	}
}

// fuseMounter streams images through a FUSE file below dir.
type fuseMounter struct {
	dir    string
	client *http.Client
}

func (m fuseMounter) Mount(ctx context.Context, image string, opts media.Options) (MountedMedia, error) {
	if m.dir == "" {
		return nil, errors.New("no media directory is configured")
	}

	err := os.MkdirAll(m.dir, 0o755)
	if err != nil {
		return nil, err
	}

	opts.TempDir = m.dir

	fetcher, err := media.Open(ctx, m.client, image, opts)
	if err != nil {
		return nil, err
	}

	// QEMU opens the file as an unprivileged user, even if the proxy runs as root.
	mount, err := media.NewMount(m.mountPoint(), "boot-media.iso", fetcher, media.MountOptions{AllowOther: true})
	if err != nil {
		return nil, errors.Join(err, fetcher.Close())
	}

	return fuseMedia{mount: mount, fetcher: fetcher}, nil
}

func (m fuseMounter) Cleanup() error {
	if m.dir == "" {
		return nil
	}

	err := media.Cleanup(m.mountPoint())
	if err != nil {
		return err
	}

	// A prefetch file outlives its process, if the process did not end orderly.
	files, err := filepath.Glob(filepath.Join(m.dir, "media-*.img"))
	if err != nil {
		return err
	}

	for _, file := range files {
		err = errors.Join(err, os.Remove(file))
	}

	return err
}

func (m fuseMounter) mountPoint() string {
	return filepath.Join(m.dir, "mnt")
}

type fuseMedia struct {
	mount   *media.Mount
	fetcher *media.Fetcher
}

func (m fuseMedia) Path() string {
	return m.mount.Path()
}

func (m fuseMedia) Close() error {
	return errors.Join(m.mount.Unmount(), m.fetcher.Close())
}
