package api_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Managers/bmc1/VirtualMedia/invalid",
		},
		{
			method: http.MethodPatch,
			url:    "/redfish/v1/Managers/bmc1/VirtualMedia/invalid",
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
		{
			method: http.MethodPatch,
			url:    "/redfish/v1/Managers/bmc1/VirtualMedia/CD",
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

func TestRedfishServer_GetRedfishV1ManagersManagerIDVirtualMedia(t *testing.T) {
	incusClient := &mock.IncusClientMock{
		GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
			return &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{},
				},
			}, "", nil
		},
	}

	client := setup(t, incusClient)

	managers, err := client.Service.Managers()
	require.NoError(t, err)
	require.Len(t, managers, 1)

	vms, err := managers[0].VirtualMedia()
	require.NoError(t, err)
	require.Len(t, vms, 1)
	require.Equal(t, "CD", vms[0].Name)
}

func TestRedfishServer_GetRedfishV1ManagersManagerIDVirtualMediaVirtualMediaID(t *testing.T) {
	tests := []struct {
		name                 string
		clientGetInstance    *incusapi.Instance
		clientGetInstanceErr error

		assertErr require.ErrorAssertionFunc
		assert    func(t *testing.T, vms []*schemas.VirtualMedia)
	}{
		{
			name: "success - not inserted",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{},
				},
			},

			assertErr: require.NoError,
			assert: func(t *testing.T, vms []*schemas.VirtualMedia) {
				t.Helper()

				require.Len(t, vms, 1)
				vm := vms[0]

				require.NotNil(t, vm.Inserted)
				require.False(t, *vm.Inserted)
				require.Equal(t, "test-instance-boot-media.iso", vm.Image)
				require.Equal(t, schemas.URIConnectedVia, vm.ConnectedVia)
				require.NotNil(t, vm.WriteProtected)
				require.True(t, *vm.WriteProtected)
			},
		},
		{
			name: "success - inserted",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{
						"boot-media": map[string]string{
							"boot.priority": "10",
							"pool":          "default",
							"source":        "test-instance-boot-media.iso",
							"type":          "disk",
						},
					},
				},
			},

			assertErr: require.NoError,
			assert: func(t *testing.T, vms []*schemas.VirtualMedia) {
				t.Helper()

				require.Len(t, vms, 1)
				vm := vms[0]

				require.NotNil(t, vm.Inserted)
				require.True(t, *vm.Inserted)
			},
		},
		{
			name:                 "error - client.GetInstance",
			clientGetInstanceErr: boom.Error,

			assertErr: boom.ErrorContains,
			assert: func(t *testing.T, vms []*schemas.VirtualMedia) {
				t.Helper()

				require.Nil(t, vms)
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

			managers, err := client.Service.Managers()
			require.NoError(t, err)
			require.Len(t, managers, 1)

			vms, err := managers[0].VirtualMedia()
			tc.assertErr(t, err)
			tc.assert(t, vms)
		})
	}
}

func TestRedfishServer_PatchRedfishV1ManagersManagerIDVirtualMediaVirtualMediaID(t *testing.T) {
	tests := []struct {
		name string

		clientGetInstance    *incusapi.Instance
		clientGetInstanceErr error

		clientUpdateInstance    incusclient.Operation
		clientUpdateInstanceErr error

		clientCreateStoragePoolVolumeFromISO    incusclient.Operation
		clientCreateStoragePoolVolumeFromISOErr error

		clientDeleteStoragePoolVolumeErr error

		// Placeholder {{IMAGE_URL}} is substituted before sending.
		body string

		assertErr require.ErrorAssertionFunc
		assert    func(t *testing.T, incusClient *mock.IncusClientMock)
	}{
		{
			name: "eject - not inserted - no-op",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{},
				},
			},
			body: `{"Inserted":false}`,

			assertErr: require.NoError,
			assert: func(t *testing.T, incusClient *mock.IncusClientMock) {
				t.Helper()

				require.Empty(t, incusClient.UpdateInstanceCalls())
				require.Empty(t, incusClient.DeleteStoragePoolVolumeCalls())
			},
		},
		{
			name: "eject - inserted - success",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{
						"boot-media": map[string]string{
							"boot.priority": "10",
							"pool":          "default",
							"source":        "test-instance-boot-media.iso",
							"type":          "disk",
						},
					},
				},
			},
			clientUpdateInstance: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},
			body: `{"Inserted":false}`,

			assertErr: require.NoError,
			assert: func(t *testing.T, incusClient *mock.IncusClientMock) {
				t.Helper()

				updateCalls := incusClient.UpdateInstanceCalls()
				require.Len(t, updateCalls, 1)
				_, stillPresent := updateCalls[0].Instance.Devices["boot-media"]
				require.False(t, stillPresent)

				deleteCalls := incusClient.DeleteStoragePoolVolumeCalls()
				require.Len(t, deleteCalls, 1)
				require.Equal(t, "default", deleteCalls[0].Pool)
				require.Equal(t, "custom", deleteCalls[0].VolType)
				require.Equal(t, "test-instance-boot-media.iso", deleteCalls[0].Name)
			},
		},
		{
			name: "eject - client.UpdateInstance error",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{
						"boot-media": map[string]string{"type": "disk"},
					},
				},
			},
			clientUpdateInstanceErr: boom.Error,
			body:                    `{"Inserted":false}`,

			assertErr: boom.ErrorContains,
		},
		{
			name: "eject - client.UpdateInstance Operation.Wait error",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{
						"boot-media": map[string]string{"type": "disk"},
					},
				},
			},
			clientUpdateInstance: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return boom.Error
				},
			},
			body: `{"Inserted":false}`,

			assertErr: boom.ErrorContains,
		},
		{
			name: "eject - client.DeleteStoragePoolVolume error",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{
						"boot-media": map[string]string{"type": "disk"},
					},
				},
			},
			clientUpdateInstance: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},
			clientDeleteStoragePoolVolumeErr: boom.Error,
			body:                             `{"Inserted":false}`,

			assertErr: boom.ErrorContains,
		},
		{
			name: "insert - success",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{},
				},
			},
			clientCreateStoragePoolVolumeFromISO: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},
			clientUpdateInstance: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},
			body: `{"Inserted":true,"Image":"{{IMAGE_URL}}"}`,

			assertErr: require.NoError,
			assert: func(t *testing.T, incusClient *mock.IncusClientMock) {
				t.Helper()

				createCalls := incusClient.CreateStoragePoolVolumeFromISOCalls()
				require.Len(t, createCalls, 1)
				require.Equal(t, "test-instance-boot-media.iso", createCalls[0].Args.Name)

				updateCalls := incusClient.UpdateInstanceCalls()
				require.Len(t, updateCalls, 1)
				require.Equal(t, map[string]string{
					"boot.priority": "10",
					"pool":          "default",
					"source":        "test-instance-boot-media.iso",
					"type":          "disk",
				}, updateCalls[0].Instance.Devices["boot-media"])
			},
		},
		{
			name: "insert - bad image URL",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{},
				},
			},
			body: `{"Inserted":true,"Image":"not-a-url"}`,

			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "unsupported protocol scheme")
			},
			assert: func(t *testing.T, incusClient *mock.IncusClientMock) {
				t.Helper()

				require.Empty(t, incusClient.CreateStoragePoolVolumeFromISOCalls())
				require.Empty(t, incusClient.UpdateInstanceCalls())
			},
		},
		{
			name: "insert - client.CreateStoragePoolVolumeFromISO error",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{},
				},
			},
			clientCreateStoragePoolVolumeFromISOErr: boom.Error,
			body:                                    `{"Inserted":true,"Image":"{{IMAGE_URL}}"}`,

			assertErr: boom.ErrorContains,
		},
		{
			name: "insert - client.CreateStoragePoolVolumeFromISO Operation.Wait error",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{},
				},
			},
			clientCreateStoragePoolVolumeFromISO: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return boom.Error
				},
			},
			body: `{"Inserted":true,"Image":"{{IMAGE_URL}}"}`,

			assertErr: boom.ErrorContains,
		},
		{
			name: "insert - client.UpdateInstance error",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{},
				},
			},
			clientCreateStoragePoolVolumeFromISO: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},
			clientUpdateInstanceErr: boom.Error,
			body:                    `{"Inserted":true,"Image":"{{IMAGE_URL}}"}`,

			assertErr: boom.ErrorContains,
		},
		{
			name: "insert - client.UpdateInstance Operation.Wait error",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Devices: incusapi.DevicesMap{},
				},
			},
			clientCreateStoragePoolVolumeFromISO: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return nil
				},
			},
			clientUpdateInstance: &mock.IncusOperationMock{
				WaitFunc: func() error {
					return boom.Error
				},
			},
			body: `{"Inserted":true,"Image":"{{IMAGE_URL}}"}`,

			assertErr: boom.ErrorContains,
		},
		{
			name:                 "error - client.GetInstance",
			clientGetInstanceErr: boom.Error,
			body:                 `{"Inserted":false}`,

			assertErr: boom.ErrorContains,
			assert: func(t *testing.T, incusClient *mock.IncusClientMock) {
				t.Helper()

				require.Empty(t, incusClient.UpdateInstanceCalls())
				require.Empty(t, incusClient.CreateStoragePoolVolumeFromISOCalls())
				require.Empty(t, incusClient.DeleteStoragePoolVolumeCalls())
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("fake-iso-data"))
			}))
			t.Cleanup(imageServer.Close)

			incusClient := &mock.IncusClientMock{
				GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
					return tc.clientGetInstance, "", tc.clientGetInstanceErr
				},
				UpdateInstanceFunc: func(name string, instance incusapi.InstancePut, ETag string) (incusclient.Operation, error) {
					return tc.clientUpdateInstance, tc.clientUpdateInstanceErr
				},
				CreateStoragePoolVolumeFromISOFunc: func(pool string, args incusclient.StorageVolumeBackupArgs) (incusclient.Operation, error) {
					return tc.clientCreateStoragePoolVolumeFromISO, tc.clientCreateStoragePoolVolumeFromISOErr
				},
				DeleteStoragePoolVolumeFunc: func(pool string, volType string, name string) error {
					return tc.clientDeleteStoragePoolVolumeErr
				},
			}

			client := setup(t, incusClient)

			body := strings.ReplaceAll(tc.body, "{{IMAGE_URL}}", imageServer.URL)

			resp, err := client.RunRawRequestWithHeaders(
				http.MethodPatch,
				"/redfish/v1/Managers/bmc1/VirtualMedia/CD",
				bytes.NewReader([]byte(body)),
				"",
				nil,
			)
			tc.assertErr(t, err)

			if resp != nil {
				resp.Body.Close()
			}

			if tc.assert != nil {
				tc.assert(t, incusClient)
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
