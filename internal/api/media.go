package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"

	"github.com/FuturFusion/incus-redfish-proxy/internal/media"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

const bootMediaDeliveryKey = redfishConfigPrefix + "delivery"

const bootMediaTypeKey = redfishConfigPrefix + "media_type"

const downloadedMediaName = "boot-media.img"

func mediaFileName(mediaType profile.MediaType) string {
	// Incus attaches a file that ends in .iso as an optical drive, any other file as a disk.
	if mediaType.IsOptical() {
		return "boot-media.iso"
	}

	return downloadedMediaName
}

// MediaMounter exposes an image as a local file, which Incus attaches as a disk.
type MediaMounter interface {
	// Mount exposes the image as a file with the given name. The file outlives ctx, Close of the result removes it.
	Mount(ctx context.Context, image string, name string, opts media.Options) (MountedMedia, error)

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

type mountedMedia struct {
	mu      sync.Mutex
	current MountedMedia
}

func (m *mountedMedia) set(mounted MountedMedia) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.current = mounted
}

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

const restoreProbeTimeout = 30 * time.Second

func (s redfishServer) mediaOptions(delivery profile.Delivery) media.Options {
	return media.Options{
		ReadAhead: int64(s.profile.VirtualMedia.Stream.ReadAhead),
		CacheSize: int64(s.profile.VirtualMedia.Stream.Cache),
		Prefetch:  delivery == profile.DeliveryPrefetch,
	}
}

func (s redfishServer) mountMedia(ctx context.Context, image string, mediaType profile.MediaType) (MountedMedia, error) {
	// The full fetch delivers the image without a mount, so there is no media to return.
	delivery := s.profile.VirtualMedia.Delivery
	if delivery == profile.DeliveryFullFetch {
		return nil, nil
	}

	mounted, err := s.mounter.Mount(ctx, image, mediaFileName(mediaType), s.mediaOptions(delivery))
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

func (s redfishServer) attachMountedMedia(instance *incusapi.Instance, etag string, image string, mediaType profile.MediaType, delivery profile.Delivery, mounted MountedMedia) error {
	device := map[string]string{
		"type":               "disk",
		"source":             mounted.Path(),
		"readonly":           "true",
		bootMediaImageKey:    image,
		bootMediaDeliveryKey: string(delivery),
	}

	if !mediaType.IsOptical() {
		device[bootMediaTypeKey] = string(mediaType)
	}

	instance.Devices[bootMediaDeviceName] = device

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

func (s redfishServer) restoreMedia() {
	// Mount the image again, so the instance finds it on its next start.
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

	opts := s.mediaOptions(delivery)
	opts.ProbeTimeout = restoreProbeTimeout

	mounted, err := s.mounter.Mount(s.ctx, device[bootMediaImageKey], mediaFileName(deviceMediaType(device)), opts)
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

type fuseMounter struct {
	dir    string
	client *http.Client
}

func (m fuseMounter) Mount(ctx context.Context, image string, name string, opts media.Options) (MountedMedia, error) {
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
	mount, err := media.NewMount(m.mountPoint(), name, fetcher, media.MountOptions{AllowOther: true})
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

func deviceMediaType(device map[string]string) profile.MediaType {
	mediaType := profile.MediaType(device[bootMediaTypeKey])
	if mediaType == "" {
		return profile.MediaTypeCD
	}

	return mediaType
}

type downloadedMedia struct {
	path string
}

func (m downloadedMedia) Path() string {
	return m.path
}

func (m downloadedMedia) Close() error {
	err := os.Remove(m.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	return err
}

func (s redfishServer) downloadMedia(ctx context.Context, image string) (MountedMedia, error) {
	// Incus imports an image only as an optical drive, so a USB stick image is attached as a file instead.
	if s.mediaDir == "" {
		return nil, errors.New("no media directory is configured, a USB stick image needs it")
	}

	err := os.MkdirAll(s.mediaDir, 0o755)
	if err != nil {
		return nil, err
	}

	body, err := s.fetchImage(ctx, image)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = body.Close()
	}()

	// The name matches the stale prefetch files, so the cleanup at the start removes a partial download.
	file, err := os.CreateTemp(s.mediaDir, "media-download-*.img")
	if err != nil {
		return nil, err
	}

	_, err = io.Copy(file, body)
	err = errors.Join(err, file.Close())

	// QEMU opens the file as an unprivileged user.
	if err == nil {
		err = os.Chmod(file.Name(), 0o644)
	}

	path := filepath.Join(s.mediaDir, downloadedMediaName)
	if err == nil {
		err = os.Rename(file.Name(), path)
	}

	if err != nil {
		return nil, errors.Join(fmt.Errorf("download image %q: %w", image, err), os.Remove(file.Name()))
	}

	return downloadedMedia{path: path}, nil
}

func (s redfishServer) fetchImage(ctx context.Context, image string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, image, nil)
	if err != nil {
		return nil, statusErrorf(http.StatusBadRequest, "%s", err.Error())
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, statusErrorf(http.StatusBadRequest, "%s", err.Error())
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_ = resp.Body.Close()

		return nil, statusErrorf(http.StatusBadRequest, "fetch image %q: unexpected status %s", image, resp.Status)
	}

	return resp.Body, nil
}
