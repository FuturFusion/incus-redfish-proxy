package redfishproxy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/redfishproxy"
)

func TestNewHandler_RequiresInstanceName(t *testing.T) {
	h, err := redfishproxy.NewHandler(redfishproxy.Config{})

	require.Error(t, err)
	require.Nil(t, h)
}

func TestNewHandler_UnknownProfile(t *testing.T) {
	t.Parallel()

	h, err := redfishproxy.NewHandler(redfishproxy.Config{InstanceName: "vm1", Profile: "does-not-exist"})

	require.ErrorContains(t, err, "load profile", "an unknown profile fails")
	require.Nil(t, h, "no handler on error")
}

func TestProfiles(t *testing.T) {
	t.Parallel()

	require.Contains(t, redfishproxy.Profiles(), "generic", "the generic profile is embedded")
}
