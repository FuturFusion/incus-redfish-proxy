// Package redfishproxy allows embedding the Incus Redfish proxy as a library,
// for example to spin up an in-process Redfish API in tests.
package redfishproxy

import (
	"fmt"
	"net/http"

	config "github.com/lxc/incus/v6/shared/cliconfig"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
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
}

// NewHandler builds a ready to use http.Handler serving the Redfish API for
// the instance described by cfg, using the local Incus client configuration
// to connect to the remote.
func NewHandler(cfg Config) (http.Handler, error) {
	if cfg.InstanceName == "" {
		return nil, fmt.Errorf("instance name must not be empty")
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

	return api.NewHandler(cfg.InstanceName, client), nil
}
