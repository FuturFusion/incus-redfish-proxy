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
