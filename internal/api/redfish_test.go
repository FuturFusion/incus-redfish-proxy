package api_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	incusclient "github.com/lxc/incus/v6/client"
	incusapi "github.com/lxc/incus/v6/shared/api"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
	"github.com/FuturFusion/incus-redfish-proxy/internal/api/mock"
	"github.com/FuturFusion/incus-redfish-proxy/internal/util/testing/boom"
	"github.com/FuturFusion/incus-redfish-proxy/internal/util/testing/queue"
)

func TestRedfishServer_GetRedfishV1SystemsComputerSystemID(t *testing.T) {
	tests := []struct {
		name                 string
		clientGetInstance    *incusapi.Instance
		clientGetInstanceErr error

		assertErr require.ErrorAssertionFunc
		assert    func(t *testing.T, cs []*schemas.ComputerSystem)
	}{
		{
			name: "success - running",
			clientGetInstance: &incusapi.Instance{
				Status: "Running",
			},

			assertErr: require.NoError,
			assert: func(t *testing.T, cs []*schemas.ComputerSystem) {
				t.Helper()

				require.Len(t, cs, 1)
				c := cs[0]

				require.Equal(t, schemas.OnPowerState, c.PowerState)
			},
		},
		{
			name: "success - stopped",
			clientGetInstance: &incusapi.Instance{
				Status: "Stopped",
			},

			assertErr: require.NoError,
			assert: func(t *testing.T, cs []*schemas.ComputerSystem) {
				t.Helper()

				require.Len(t, cs, 1)
				c := cs[0]

				require.Equal(t, schemas.OffPowerState, c.PowerState)
			},
		},
		{
			name:                 "error - client.GetInstance",
			clientGetInstanceErr: boom.Error,

			assertErr: boom.ErrorContains,
			assert: func(t *testing.T, cs []*schemas.ComputerSystem) {
				t.Helper()

				require.Nil(t, cs)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			incusClient := &mock.IncusClientMock{
				GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
					return tc.clientGetInstance, "", tc.clientGetInstanceErr
				},
			}

			client := setup(t, incusClient)

			systems, err := client.Service.Systems()
			tc.assertErr(t, err)
			tc.assert(t, systems)
		})
	}
}

func TestRedfishServer_PostRedfishV1SystemsComputerSystemIDActionsComputerSystemReset(t *testing.T) {
	tests := []struct {
		name                         string
		resetType                    schemas.ResetType
		clientGetInstance            *incusapi.Instance
		clientUpdateInstanceState    incusclient.Operation
		clientUpdateInstanceStateErr error

		assertErr require.ErrorAssertionFunc
	}{
		{
			name:      "success - on",
			resetType: schemas.OnResetType,
			clientGetInstance: &incusapi.Instance{
				Status: "Stopped",
			},
			clientUpdateInstanceState: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},

			assertErr: require.NoError,
		},
		{
			name:      "success - force on",
			resetType: schemas.ForceOnResetType,
			clientGetInstance: &incusapi.Instance{
				Status: "Stopped",
			},
			clientUpdateInstanceState: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},

			assertErr: require.NoError,
		},
		{
			name:      "success - shutdown",
			resetType: schemas.ResetType(api.ResourceResetTypeGracefulShutdown),
			clientGetInstance: &incusapi.Instance{
				Status: "Running",
			},
			clientUpdateInstanceState: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},

			assertErr: require.NoError,
		},
		{
			name:      "success - force off",
			resetType: schemas.ForceOffResetType,
			clientGetInstance: &incusapi.Instance{
				Status: "Running",
			},
			clientUpdateInstanceState: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},

			assertErr: require.NoError,
		},

		{
			name:      "error - invalid reset type",
			resetType: schemas.ResetType("invalid"),
			clientGetInstance: &incusapi.Instance{
				Status: "Running",
			},
			clientUpdateInstanceState: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(t, err, "reset type not supported")
			},
		},
		{
			name:      "error - client.UpdateInstanceState",
			resetType: schemas.ForceOffResetType,
			clientGetInstance: &incusapi.Instance{
				Status: "Running",
			},
			clientUpdateInstanceStateErr: boom.Error,

			assertErr: boom.ErrorContains,
		},
		{
			name:      "error - client.UpdateInstanceState - Operation.Wait",
			resetType: schemas.ForceOffResetType,
			clientGetInstance: &incusapi.Instance{
				Status: "Running",
			},
			clientUpdateInstanceState: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return boom.Error
				},
			},
			assertErr: boom.ErrorContains,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			incusClient := &mock.IncusClientMock{
				GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
					return tc.clientGetInstance, "", nil
				},
				UpdateInstanceStateFunc: func(name string, state incusapi.InstanceStatePut, ETag string) (incusclient.Operation, error) {
					return tc.clientUpdateInstanceState, tc.clientUpdateInstanceStateErr
				},
			}

			client := setup(t, incusClient)

			systems, err := client.Service.Systems()
			require.NoError(t, err)
			require.Len(t, systems, 1)

			system := systems[0]

			_, err = system.Reset(tc.resetType)
			tc.assertErr(t, err)
		})
	}
}

func TestRedfishServer_GetAndPatchBiosSettings(t *testing.T) {
	tests := []struct {
		name                         string
		vTPMValue                    string
		clientGetInstance            []queue.Item[*incusapi.Instance]
		clientUpdateInstanceState    incusclient.Operation
		clientUpdateInstanceStateErr error

		assertErr require.ErrorAssertionFunc
	}{
		{
			name:      "success - on",
			vTPMValue: "On",
			clientGetInstance: []queue.Item[*incusapi.Instance]{
				// Setup Get System
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Setup Get Bios
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Pre Update
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Update
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
			},
			clientUpdateInstanceState: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},

			assertErr: require.NoError,
		},
		{
			name:      "success - off",
			vTPMValue: "Off",
			clientGetInstance: []queue.Item[*incusapi.Instance]{
				// Setup Get System
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
						InstancePut: incusapi.InstancePut{
							Devices: incusapi.DevicesMap{
								"vtpm": map[string]string{
									"type": "tpm",
								},
							},
						},
					},
				},
				// Setup Get Bios
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
						InstancePut: incusapi.InstancePut{
							Devices: incusapi.DevicesMap{
								"vtpm": map[string]string{
									"type": "tpm",
								},
							},
						},
					},
				},
				// Pre Update
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
						InstancePut: incusapi.InstancePut{
							Devices: incusapi.DevicesMap{
								"vtpm": map[string]string{
									"type": "tpm",
								},
							},
						},
					},
				},
				// Update
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
						InstancePut: incusapi.InstancePut{
							Devices: incusapi.DevicesMap{
								"vtpm": map[string]string{
									"type": "tpm",
								},
							},
						},
					},
				},
			},
			clientUpdateInstanceState: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},

			assertErr: require.NoError,
		},

		{
			name:      "error - started instance",
			vTPMValue: "Off",
			clientGetInstance: []queue.Item[*incusapi.Instance]{
				// Setup Get System
				{
					Value: &incusapi.Instance{
						Status: "Started",
					},
				},
				// Setup Get Bios
				{
					Value: &incusapi.Instance{
						Status: "Started",
					},
				},
				// Pre Update
				{
					Value: &incusapi.Instance{
						Status: "Started",
					},
				},
				// Update
				{
					Value: &incusapi.Instance{
						Status: "Started",
					},
				},
			},

			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, `instance \"test-instance\" is not stopped`)
			},
		},
		{
			name:      "error - client.GetInstance",
			vTPMValue: "On",
			clientGetInstance: []queue.Item[*incusapi.Instance]{
				// Setup Get System
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Setup Get Bios
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Pre Update
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Update
				{
					Err: boom.Error,
				},
			},

			assertErr: boom.ErrorContains,
		},
		{
			name:      "error - client.UpdateInstance",
			vTPMValue: "On",
			clientGetInstance: []queue.Item[*incusapi.Instance]{
				// Setup Get System
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Setup Get Bios
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Pre Update
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Update
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
			},
			clientUpdateInstanceStateErr: boom.Error,

			assertErr: boom.ErrorContains,
		},
		{
			name:      "error - client.UpdateInstance",
			vTPMValue: "On",
			clientGetInstance: []queue.Item[*incusapi.Instance]{
				// Setup Get System
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Setup Get Bios
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Pre Update
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
				// Update
				{
					Value: &incusapi.Instance{
						Status: "Stopped",
					},
				},
			},
			clientUpdateInstanceState: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return boom.Error
				},
			},

			assertErr: boom.ErrorContains,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// if tc.name != "error - client.GetInstance" {
			// 	t.SkipNow()
			// }
			// Setup
			incusClient := &mock.IncusClientMock{
				GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
					ret, err := queue.Pop(t, &tc.clientGetInstance)
					return ret, "", err
				},
				UpdateInstanceFunc: func(name string, instance incusapi.InstancePut, ETag string) (incusclient.Operation, error) {
					return tc.clientUpdateInstanceState, tc.clientUpdateInstanceStateErr
				},
			}

			client := setup(t, incusClient)

			systems, err := client.Service.Systems()
			require.NoError(t, err)
			require.Len(t, systems, 1)

			system := systems[0]

			bios, err := system.Bios()
			require.NoError(t, err)

			// Run test
			err = bios.UpdateBiosAttributesApplyAt(schemas.SettingsAttributes{
				"vTPM": tc.vTPMValue,
			}, schemas.OnResetSettingsApplyTime)

			// Assert
			tc.assertErr(t, err)
			require.Empty(t, tc.clientGetInstance)
		})
	}
}

func TestRedfishServer_NotFound_Error(t *testing.T) {
	tests := []struct {
		method string
		url    string
	}{
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/invalid",
		},
		{
			method: http.MethodPost,
			url:    "/redfish/v1/Systems/invalid/Actions/ComputerSystem.Reset",
		},
		{
			method: http.MethodPatch,
			url:    "/redfish/v1/Systems/invalid/Bios/Settings",
		},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.url), func(t *testing.T) {
			incusClient := &mock.IncusClientMock{}

			client := setup(t, incusClient)

			resp, err := client.RunRawRequestWithHeaders(tc.method, tc.url, nil, "", nil)
			require.ErrorContains(t, err, "Not Found")

			if resp != nil {
				resp.Body.Close()
			}
		})
	}
}

func TestRedfishServer_InvalidRequest_Error(t *testing.T) {
	tests := []struct {
		method string
		url    string
	}{
		{
			method: http.MethodPost,
			url:    "/redfish/v1/Systems/test-instance/Actions/ComputerSystem.Reset",
		},
		{
			method: http.MethodPatch,
			url:    "/redfish/v1/Systems/test-instance/Bios/Settings",
		},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.url), func(t *testing.T) {
			incusClient := &mock.IncusClientMock{}

			client := setup(t, incusClient)

			resp, err := client.RunRawRequestWithHeaders(
				tc.method,
				tc.url,
				bytes.NewReader([]byte("{")), // invalid JSON
				"",
				nil,
			)
			require.ErrorContains(t, err, "unexpected EOF")

			if resp != nil {
				resp.Body.Close()
			}
		})
	}
}

func setup(t *testing.T, client api.IncusClient) *gofish.APIClient {
	t.Helper()

	server := api.NewRedfishServer("test-instance", client)

	r := http.NewServeMux()

	h := api.HandlerFromMux(server, r)

	httpserver := httptest.NewServer(h)
	t.Cleanup(func() {
		httpserver.Close()
	})

	c, err := gofish.Connect(
		gofish.ClientConfig{
			AutoExpand: false,
			Endpoint:   httpserver.URL,
			DumpWriter: t.Output(),
		},
	)
	require.NoError(t, err)

	return c
}
