// Package redfishproxy allows embedding the Incus Redfish proxy as a library,
// for example to spin up an in-process Redfish API in tests.
package redfishproxy

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	config "github.com/lxc/incus/v7/shared/cliconfig"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// Config holds the settings required to build a Redfish proxy handler for a
// single Incus instance.
type Config struct {
	// InstanceName is the name of the Incus instance to expose over Redfish.
	InstanceName string

	// Remote is the Incus remote to connect to. If empty, the default
	// remote from the local Incus client configuration is used.
	Remote string

	// Project is the Incus project the instance belongs to. If empty,
	// "default" is used.
	Project string

	// HTTPClient downloads virtual media images, defaults to http.DefaultClient.
	HTTPClient *http.Client

	// Context aborts the proxy once done. It stops the Incus event watcher and the background work.
	// It also unmounts the streamed media. The device stays, so the next start mounts the media again.
	// For an orderly stop, call Handler.Close instead. A Handler.Close after the abort still removes the streamed media device.
	Context context.Context

	// CloseTimeout is the time Handler.Close waits for the background work, for example a reset. Then Handler.Close cancels it.
	// A value of zero or less means api.DefaultCloseTimeout (30 seconds).
	CloseTimeout time.Duration

	// Profile is the name of an embedded profile or the path of a profile file. If empty, the proxy uses "generic".
	Profile string

	// TimingScale replaces timing.scale of the profile, if not zero.
	TimingScale float64

	// MediaDir holds the streamed media, if empty the proxy uses DefaultMediaDir. The proxy must run on the Incus host, as incusd reads the media from there.
	MediaDir string
}

// DefaultMediaDir is the directory for streamed media, if Config.MediaDir is empty.
const DefaultMediaDir = "/var/lib/incus-redfish-proxy/media"

// Profiles returns the names of the embedded profiles.
func Profiles() []string {
	return profile.List()
}

// Handler serves the Redfish API of one Incus instance.
type Handler struct {
	service *api.Service
}

// ServeHTTP serves a Redfish request.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.service.ServeHTTP(w, r)
}

// ReleaseHeld ends the requests that the unreachable window of the profile holds.
// Register it with http.Server.RegisterOnShutdown, so the shutdown does not wait for them.
func (h *Handler) ReleaseHeld() {
	h.service.ReleaseHeld()
}

// Close stops the proxy in order. Call it after the HTTP server stopped serving requests.
// It waits for the background work, cancels it after the close timeout and waits until it stops.
// Then it ejects the streamed media and stops the Incus event watcher. Other media stays inserted.
func (h *Handler) Close() error {
	return h.service.Close()
}

// NewHandler builds a ready to use Handler serving the Redfish API for
// the instance described by cfg, using the local Incus client configuration
// to connect to the remote. It starts an Incus event watcher.
// Call Handler.Close or end Config.Context to stop it.
func NewHandler(cfg Config) (*Handler, error) {
	if cfg.InstanceName == "" {
		return nil, fmt.Errorf("instance name must not be empty")
	}

	p, err := profile.Load(cfg.Profile)
	if err != nil {
		return nil, fmt.Errorf("load profile: %w", err)
	}

	if cfg.TimingScale != 0 {
		p.Timing.Scale = cfg.TimingScale

		err = p.Validate()
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", p.Name, err)
		}
	}

	for _, warning := range p.Warnings() {
		log.Printf("profile %q: %s", p.Name, warning)
	}

	incusCfg, err := config.LoadConfig("")
	if err != nil {
		return nil, fmt.Errorf("load incus config: %w", err)
	}

	remote := cfg.Remote
	if remote == "" {
		remote = incusCfg.DefaultRemote
	}

	client, err := incusCfg.GetInstanceServer(remote)
	if err != nil {
		return nil, fmt.Errorf("get instance server for remote %q: %w", remote, err)
	}

	project := cfg.Project
	if project == "" {
		project = "default"
	}

	client = client.UseProject(project)

	// The event source needs its own client, as the Incus client shares one event connection, including its type filter, per client.
	events := client.UseProject(project)

	mediaDir := cfg.MediaDir
	if mediaDir == "" {
		mediaDir = DefaultMediaDir
	}

	// The project is part of the name, so equally named instances of different projects do not share a directory.
	mediaDir = filepath.Join(mediaDir, filepath.Base(project)+"_"+filepath.Base(cfg.InstanceName))

	service := api.NewHandler(
		cfg.InstanceName,
		client,
		api.WithHTTPClient(cfg.HTTPClient),
		api.WithContext(cfg.Context),
		api.WithCloseTimeout(cfg.CloseTimeout),
		api.WithEventSource(events),
		api.WithProfile(p),
		api.WithMediaDir(mediaDir),
	)

	return &Handler{service: service}, nil
}
