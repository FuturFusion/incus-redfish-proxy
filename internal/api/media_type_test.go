package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stmcginnis/gofish/schemas"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

const mediaActions = "/redfish/v1/Managers/bmc1/VirtualMedia/CD/Actions"

func TestRedfishServer_VirtualMediaTypes(t *testing.T) {
	t.Parallel()

	usbStick := []profile.MediaType{profile.MediaTypeCD, profile.MediaTypeDVD, profile.MediaTypeUSBStick}

	tests := []struct {
		name       string
		mediaTypes []profile.MediaType
		delivery   profile.Delivery
		path       string
		body       func(image string) map[string]any

		assertErr   require.ErrorAssertionFunc
		wantDevice  func(image string, mediaDir string) map[string]string
		wantVolumes int
		wantNames   []string
	}{
		{
			name:       "success - USB stick is downloaded",
			mediaTypes: usbStick,
			delivery:   profile.DeliveryFullFetch,
			path:       "/image.raw",
			body:       func(image string) map[string]any { return map[string]any{"Image": image, "MediaType": "USBStick"} },

			assertErr: require.NoError,
			wantDevice: func(image string, mediaDir string) map[string]string {
				return map[string]string{
					"type": "disk", "source": filepath.Join(mediaDir, "boot-media.img"), "readonly": "true",
					"user.redfish.image": image, "user.redfish.delivery": "full-fetch", "user.redfish.media_type": "USBStick",
				}
			},
		},
		{
			name:       "success - the extension of a raw image selects the USB stick",
			mediaTypes: usbStick,
			delivery:   profile.DeliveryFullFetch,
			path:       "/image.img",
			body:       func(image string) map[string]any { return map[string]any{"Image": image} },

			assertErr: require.NoError,
			wantDevice: func(image string, mediaDir string) map[string]string {
				return map[string]string{
					"type": "disk", "source": filepath.Join(mediaDir, "boot-media.img"), "readonly": "true",
					"user.redfish.image": image, "user.redfish.delivery": "full-fetch", "user.redfish.media_type": "USBStick",
				}
			},
		},
		{
			name:       "success - ISO image stays a volume",
			mediaTypes: usbStick,
			delivery:   profile.DeliveryFullFetch,
			path:       "/image.iso",
			body:       func(image string) map[string]any { return map[string]any{"Image": image} },

			assertErr: require.NoError,
			wantDevice: func(image string, _ string) map[string]string {
				return map[string]string{"type": "disk", "pool": "default", "source": "test-instance-boot-media.iso", "user.redfish.image": image}
			},
			wantVolumes: 1,
		},
		{
			name:       "success - streamed USB stick is a disk file",
			mediaTypes: usbStick,
			delivery:   profile.DeliveryStream,
			path:       "/image.raw",
			body:       func(image string) map[string]any { return map[string]any{"Image": image, "MediaType": "USBStick"} },

			assertErr: require.NoError,
			wantDevice: func(image string, _ string) map[string]string {
				return map[string]string{
					"type": "disk", "source": fakeMediaPath, "readonly": "true",
					"user.redfish.image": image, "user.redfish.delivery": "stream", "user.redfish.media_type": "USBStick",
				}
			},
			wantNames: []string{"boot-media.img"},
		},
		{
			name:       "success - streamed CD keeps the ISO name",
			mediaTypes: usbStick,
			delivery:   profile.DeliveryStream,
			path:       "/image.iso",
			body:       func(image string) map[string]any { return map[string]any{"Image": image, "MediaType": "CD"} },

			assertErr: require.NoError,
			wantDevice: func(image string, _ string) map[string]string {
				return map[string]string{"type": "disk", "source": fakeMediaPath, "readonly": "true", "user.redfish.image": image, "user.redfish.delivery": "stream"}
			},
			wantNames: []string{"boot-media.iso"},
		},
		{
			name:       "error - media type not offered",
			mediaTypes: []profile.MediaType{profile.MediaTypeCD, profile.MediaTypeDVD},
			delivery:   profile.DeliveryFullFetch,
			path:       "/image.raw",
			body:       func(image string) map[string]any { return map[string]any{"Image": image, "MediaType": "USBStick"} },

			assertErr: func(tt require.TestingT, err error, a ...any) {
				requireStatusError(tt, err, http.StatusBadRequest, a...)
			},
			wantDevice: func(string, string) map[string]string { return nil },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("image"))
			}))
			t.Cleanup(images.Close)

			p := profile.Default()
			p.VirtualMedia.MediaTypes = tc.mediaTypes
			p.VirtualMedia.Delivery = tc.delivery

			mediaDir := t.TempDir()
			mounter := &fakeMounter{}
			incusClient := fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}})
			client := setup(t, incusClient, api.WithProfile(p), api.WithMediaMounter(mounter), api.WithMediaDir(mediaDir))

			image := images.URL + tc.path

			// Run test
			resp, err := client.Post(mediaActions+"/VirtualMedia.InsertMedia", tc.body(image))
			closeBody(resp)

			// Assert
			tc.assertErr(t, err, "insert the media")

			instance, _, err := incusClient.GetInstance("test-instance")
			require.NoError(t, err, "read the instance")

			require.Equal(t, tc.wantDevice(image, mediaDir), instance.Devices["boot-media"], "the device matches the media type")
			require.Len(t, incusClient.CreateStoragePoolVolumeFromISOCalls(), tc.wantVolumes, "only an optical image becomes a volume")
			require.Equal(t, tc.wantNames, mounter.names, "the name of the streamed file tells Incus the kind of drive")
		})
	}
}

func TestRedfishServer_VirtualMediaDownloadedUSBStick(t *testing.T) {
	t.Parallel()

	// Setup
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("raw image"))
	}))
	t.Cleanup(images.Close)

	p := profile.Default()
	p.VirtualMedia.MediaTypes = []profile.MediaType{profile.MediaTypeCD, profile.MediaTypeUSBStick}

	mediaDir := t.TempDir()
	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}}), api.WithProfile(p), api.WithMediaDir(mediaDir))

	downloaded := filepath.Join(mediaDir, "boot-media.img")

	// Run test
	resp, err := client.Post(mediaActions+"/VirtualMedia.InsertMedia", map[string]any{"Image": images.URL + "/image.raw"})
	require.NoError(t, err, "insert the media")
	resp.Body.Close()

	content, readErr := os.ReadFile(downloaded)
	info, statErr := os.Stat(downloaded)

	resp, err = client.Post(mediaActions+"/VirtualMedia.EjectMedia", map[string]any{})
	require.NoError(t, err, "eject the media")
	resp.Body.Close()

	// Assert
	require.NoError(t, readErr, "the insert downloads the image")
	require.Equal(t, "raw image", string(content), "the file holds the image")
	require.NoError(t, statErr, "the downloaded file exists")
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "QEMU reads the file as an unprivileged user")
	require.NoFileExists(t, downloaded, "the eject removes the file")
}

func TestRedfishServer_VirtualMediaUSBStickWithoutMediaDir(t *testing.T) {
	t.Parallel()

	// Setup
	p := profile.Default()
	p.VirtualMedia.MediaTypes = []profile.MediaType{profile.MediaTypeUSBStick}

	client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}}), api.WithProfile(p))

	// Run test
	resp, err := client.Post(mediaActions+"/VirtualMedia.InsertMedia", map[string]any{"Image": "http://localhost/image.raw"})
	closeBody(resp)

	// Assert
	requireStatusError(t, err, http.StatusInternalServerError, "a USB stick image needs the media directory")
}

func TestRedfishServer_RestoreDownloadedMedia(t *testing.T) {
	t.Parallel()

	// Setup
	p := profile.Default()
	p.VirtualMedia.MediaTypes = []profile.MediaType{profile.MediaTypeUSBStick}

	mediaDir := t.TempDir()
	downloaded := filepath.Join(mediaDir, "boot-media.img")
	require.NoError(t, os.WriteFile(downloaded, []byte("raw image"), 0o644), "write the file of an earlier download")

	// Any user of Incus can change the source of the device, so the eject must not delete it.
	foreign := filepath.Join(t.TempDir(), "foreign.img")
	require.NoError(t, os.WriteFile(foreign, []byte("other data"), 0o644), "write a file outside of the media directory")

	incusClient := fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{
		"boot-media": {"type": "disk", "source": foreign, "user.redfish.image": "http://images/image.raw", "user.redfish.delivery": "full-fetch", "user.redfish.media_type": "USBStick"},
	}}})

	client := setup(t, incusClient, api.WithProfile(p), api.WithMediaMounter(&fakeMounter{}), api.WithMediaDir(mediaDir))

	// Run test
	resp, err := client.Post(mediaActions+"/VirtualMedia.EjectMedia", map[string]any{})
	require.NoError(t, err, "eject the media")
	resp.Body.Close()

	// Assert
	require.NoFileExists(t, downloaded, "the eject after a restart of the proxy removes the downloaded file")
	require.FileExists(t, foreign, "the eject never deletes the source named by the device")
	require.Empty(t, incusClient.DeleteStoragePoolVolumeCalls(), "a downloaded image has no volume")
}

func TestRedfishServer_BootOverrideUSBStick(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string

		wantStatus int
		wantTarget schemas.BootSource
	}{
		{name: "success - boot from the USB stick", target: "Usb", wantStatus: http.StatusNoContent, wantTarget: schemas.UsbBootSource},
		{name: "error - CD target for a USB stick", target: "Cd", wantStatus: http.StatusBadRequest, wantTarget: schemas.NoneBootSource},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("image"))
			}))
			t.Cleanup(images.Close)

			p := profile.Default()
			p.VirtualMedia.MediaTypes = []profile.MediaType{profile.MediaTypeCD, profile.MediaTypeDVD, profile.MediaTypeUSBStick}
			p.VirtualMedia.BootOverrideOnce = true

			client := setup(t, fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}}), api.WithProfile(p), api.WithMediaDir(t.TempDir()))

			resp, err := client.Post(mediaActions+"/VirtualMedia.InsertMedia", map[string]any{"Image": images.URL + "/image.raw", "MediaType": "USBStick"})
			require.NoError(t, err, "insert the media")
			resp.Body.Close()

			// Run test
			resp, err = client.Patch(systemPath, map[string]any{"Boot": map[string]any{"BootSourceOverrideEnabled": "Once", "BootSourceOverrideTarget": tc.target}})
			answer := answerOf(t, resp, err)

			system := getSystem(t, client)

			// Assert
			require.Equal(t, tc.wantStatus, answer.status, "the target must boot from the inserted media")
			require.Contains(t, system.Boot.AllowableBootSourceOverrideTargetValues, schemas.UsbBootSource, "a device with a USB stick offers the Usb target")
			require.Equal(t, tc.wantTarget, system.Boot.BootSourceOverrideTarget, "the target of the override")
		})
	}
}

func TestRedfishServer_VirtualMediaPatchInsertUSBStick(t *testing.T) {
	t.Parallel()

	// Setup
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("image"))
	}))
	t.Cleanup(images.Close)

	p := profile.Default()
	p.VirtualMedia.Insert = profile.MediaInsertPatch
	p.VirtualMedia.MediaTypes = []profile.MediaType{profile.MediaTypeCD, profile.MediaTypeDVD, profile.MediaTypeUSBStick}

	mediaDir := t.TempDir()
	incusClient := fakeIncus(incusapi.Instance{Status: "Stopped", InstancePut: incusapi.InstancePut{Devices: incusapi.DevicesMap{}}})
	client := setup(t, incusClient, api.WithProfile(p), api.WithMediaDir(mediaDir))

	// Run test
	resp, err := client.Patch("/redfish/v1/Managers/bmc1/VirtualMedia/CD", map[string]any{"Image": images.URL + "/image", "Inserted": true, "MediaType": "USBStick", "WriteProtected": true})
	require.NoError(t, err, "insert the media with a PATCH")
	resp.Body.Close()

	// Assert
	instance, _, err := incusClient.GetInstance("test-instance")
	require.NoError(t, err, "read the instance")

	require.Equal(t, "USBStick", instance.Devices["boot-media"]["user.redfish.media_type"], "the PATCH asks for the USB stick, like Operations Center on XCC")
	require.FileExists(t, filepath.Join(mediaDir, "boot-media.img"), "the image is downloaded")
}
