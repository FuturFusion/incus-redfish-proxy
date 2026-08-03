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

func TestRedfishServer_GetRedfishV1SystemsComputerSystemIDProcessors(t *testing.T) {
	tests := []struct {
		name                 string
		clientGetInstance    *incusapi.Instance
		clientGetInstanceErr error

		assertErr require.ErrorAssertionFunc
		wantCount int
	}{
		{
			name:              "success - defaults to a single processor",
			clientGetInstance: &incusapi.Instance{},

			assertErr: require.NoError,
			wantCount: 1,
		},
		{
			name: "success - configured cpu count",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Config: map[string]string{
						"limits.cpu": "4",
					},
				},
			},

			assertErr: require.NoError,
			wantCount: 4,
		},
		{
			name: "success - invalid cpu count falls back to a single processor",
			clientGetInstance: &incusapi.Instance{
				InstancePut: incusapi.InstancePut{
					Config: map[string]string{
						"limits.cpu": "not-a-number",
					},
				},
			},

			assertErr: require.NoError,
			wantCount: 1,
		},
		{
			name:                 "error - client.GetInstance",
			clientGetInstanceErr: boom.Error,

			assertErr: boom.ErrorContains,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The first call to fetch the ComputerSystem must always succeed.
			callCount := 0

			incusClient := &mock.IncusClientMock{
				GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
					callCount++
					if callCount == 1 {
						return &incusapi.Instance{}, "", nil
					}

					return tc.clientGetInstance, "", tc.clientGetInstanceErr
				},
			}

			client := setup(t, incusClient)

			systems, err := client.Service.Systems()
			require.NoError(t, err)
			require.Len(t, systems, 1)

			processors, err := systems[0].Processors()
			tc.assertErr(t, err)
			require.Len(t, processors, tc.wantCount)
		})
	}
}

func TestRedfishServer_GetRedfishV1SystemsComputerSystemIDProcessorsProcessorID(t *testing.T) {
	tests := []struct {
		name         string
		architecture string

		wantArchitecture   schemas.ProcessorArchitecture
		wantInstructionSet schemas.InstructionSet
	}{
		{
			name:               "x86_64",
			architecture:       "x86_64",
			wantArchitecture:   schemas.X86ProcessorArchitecture,
			wantInstructionSet: schemas.X8664InstructionSet,
		},
		{
			name:               "aarch64",
			architecture:       "aarch64",
			wantArchitecture:   schemas.ARMProcessorArchitecture,
			wantInstructionSet: schemas.ARMA64InstructionSet,
		},
		{
			name:         "unknown architecture",
			architecture: "riscv64",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			incusClient := &mock.IncusClientMock{
				GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
					return &incusapi.Instance{
						InstancePut: incusapi.InstancePut{
							Architecture: tc.architecture,
						},
					}, "", nil
				},
			}

			client := setup(t, incusClient)

			systems, err := client.Service.Systems()
			require.NoError(t, err)
			require.Len(t, systems, 1)

			processors, err := systems[0].Processors()
			require.NoError(t, err)
			require.Len(t, processors, 1)

			processor := processors[0]
			require.Equal(t, "0", processor.ID)
			require.Equal(t, tc.wantArchitecture, processor.ProcessorArchitecture)
			require.Equal(t, tc.wantInstructionSet, processor.InstructionSet)
		})
	}
}

func TestRedfishServer_GetRedfishV1SystemsComputerSystemIDProcessorsProcessorID_Errors(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		wantErrMsg string
	}{
		{
			name:       "error - non-numeric processor id",
			url:        "/redfish/v1/Systems/test-instance/Processors/not-a-number",
			wantErrMsg: "invalid syntax",
		},
		{
			name:       "error - processor id out of range",
			url:        "/redfish/v1/Systems/test-instance/Processors/1",
			wantErrMsg: "Not Found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			incusClient := &mock.IncusClientMock{
				GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
					return &incusapi.Instance{}, "", nil
				},
			}

			client := setup(t, incusClient)

			resp, err := client.RunRawRequestWithHeaders(http.MethodGet, tc.url, nil, "", nil)
			require.ErrorContains(t, err, tc.wantErrMsg)

			if resp != nil {
				resp.Body.Close()
			}
		})
	}
}

func TestRedfishServer_GetRedfishV1SystemsComputerSystemIDSecureBoot(t *testing.T) {
	incusClient := &mock.IncusClientMock{
		GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
			return &incusapi.Instance{}, "", nil
		},
	}

	client := setup(t, incusClient)

	systems, err := client.Service.Systems()
	require.NoError(t, err)
	require.Len(t, systems, 1)

	secureBoot, err := systems[0].SecureBoot()
	require.NoError(t, err)
	require.NotNil(t, secureBoot)

	require.False(t, secureBoot.SecureBootEnable)
	require.Equal(t, schemas.DisabledSecureBootCurrentBootType, secureBoot.SecureBootCurrentBoot)
	require.Equal(t, schemas.UserModeSecureBootModeType, secureBoot.SecureBootMode)
}

func TestRedfishServer_SecureBootDatabases(t *testing.T) {
	incusClient := &mock.IncusClientMock{
		GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
			return &incusapi.Instance{}, "", nil
		},
	}

	client := setup(t, incusClient)

	systems, err := client.Service.Systems()
	require.NoError(t, err)
	require.Len(t, systems, 1)

	secureBoot, err := systems[0].SecureBoot()
	require.NoError(t, err)

	databases, err := secureBoot.SecureBootDatabases()
	require.NoError(t, err)
	require.Len(t, databases, 3)

	ids := make([]string, 0, len(databases))
	for _, db := range databases {
		ids = append(ids, db.ID)
	}

	require.ElementsMatch(t, []string{"DB", "DBX", "KEK"}, ids)
}

func TestRedfishServer_SecureBootDatabaseByID(t *testing.T) {
	for _, databaseID := range []string{"DB", "DBX", "KEK"} {
		t.Run(databaseID, func(t *testing.T) {
			incusClient := &mock.IncusClientMock{
				GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
					return &incusapi.Instance{}, "", nil
				},
			}

			client := setup(t, incusClient)

			systems, err := client.Service.Systems()
			require.NoError(t, err)
			require.Len(t, systems, 1)

			secureBoot, err := systems[0].SecureBoot()
			require.NoError(t, err)

			databases, err := secureBoot.SecureBootDatabases()
			require.NoError(t, err)

			var found *schemas.SecureBootDatabase
			for _, db := range databases {
				if db.ID == databaseID {
					found = db
				}
			}

			require.NotNil(t, found)
			require.Equal(t, fmt.Sprintf("%s - database", databaseID), found.Name)
		})
	}
}

func TestRedfishServer_SecureBootCertificates(t *testing.T) {
	incusClient := &mock.IncusClientMock{
		GetInstanceFunc: func(name string) (*incusapi.Instance, string, error) {
			return &incusapi.Instance{}, "", nil
		},
	}

	client := setup(t, incusClient)

	systems, err := client.Service.Systems()
	require.NoError(t, err)
	require.Len(t, systems, 1)

	secureBoot, err := systems[0].SecureBoot()
	require.NoError(t, err)

	databases, err := secureBoot.SecureBootDatabases()
	require.NoError(t, err)
	require.NotEmpty(t, databases)

	certificates, err := databases[0].Certificates()
	require.NoError(t, err)
	require.Len(t, certificates, 1)

	cert := certificates[0]
	require.Equal(t, "1", cert.ID)
	require.Equal(t, schemas.PEMCertificateType, cert.CertificateType)
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
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/invalid/Processors",
		},
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/invalid/Processors/0",
		},
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/invalid/SecureBoot",
		},
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/invalid/SecureBoot/SecureBootDatabases",
		},
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/invalid/SecureBoot/SecureBootDatabases/DB",
		},
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/test-instance/SecureBoot/SecureBootDatabases/INVALID",
		},
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/invalid/SecureBoot/SecureBootDatabases/DB/Certificates",
		},
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/test-instance/SecureBoot/SecureBootDatabases/INVALID/Certificates",
		},
		{
			method: http.MethodPost,
			url:    "/redfish/v1/Systems/invalid/SecureBoot/SecureBootDatabases/DB/Certificates",
		},
		{
			method: http.MethodPost,
			url:    "/redfish/v1/Systems/test-instance/SecureBoot/SecureBootDatabases/INVALID/Certificates",
		},
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/invalid/SecureBoot/SecureBootDatabases/DB/Certificates/1",
		},
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/test-instance/SecureBoot/SecureBootDatabases/INVALID/Certificates/1",
		},
		{
			method: http.MethodGet,
			url:    "/redfish/v1/Systems/test-instance/SecureBoot/SecureBootDatabases/DB/Certificates/2",
		},
		{
			method: http.MethodDelete,
			url:    "/redfish/v1/Systems/invalid/SecureBoot/SecureBootDatabases/DB/Certificates/1",
		},
		{
			method: http.MethodDelete,
			url:    "/redfish/v1/Systems/test-instance/SecureBoot/SecureBootDatabases/INVALID/Certificates/1",
		},
		{
			method: http.MethodDelete,
			url:    "/redfish/v1/Systems/test-instance/SecureBoot/SecureBootDatabases/DB/Certificates/2",
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
