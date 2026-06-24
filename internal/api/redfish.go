package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	incusclient "github.com/lxc/incus/v6/client"
	incusapi "github.com/lxc/incus/v6/shared/api"
)

type IncusClient interface {
	GetInstance(name string) (*incusapi.Instance, string, error)
	UpdateInstance(name string, instance incusapi.InstancePut, ETag string) (op incusclient.Operation, err error)
	UpdateInstanceState(name string, state incusapi.InstanceStatePut, ETag string) (op incusclient.Operation, err error)
}

type IncusOperation = incusclient.Operation

type redfishServer struct {
	instanceName string
	client       IncusClient
}

var _ ServerInterface = (*redfishServer)(nil)

func NewRedfishServer(instanceName string, client IncusClient) *redfishServer {
	return &redfishServer{
		instanceName: instanceName,
		client:       client,
	}
}

func (s redfishServer) GetRedfishV1(w http.ResponseWriter, r *http.Request) {
	response(w, ServiceRootV1210ServiceRoot{
		OdataID:   ref("/redfish/v1/"),
		OdataType: ref("#ServiceRoot.v1_21_0.ServiceRoot"),
		ID:        "RootService",
		// Links: ServiceRootV1210Links{
		// 	Sessions: OdataV4IdRef{
		// 		OdataID: ref("/redfish/v1/SessionService/Sessions"),
		// 	},
		// },
		// Managers: &OdataV4IDRef{
		// 	OdataID: ref("/redfish/v1/Managers"),
		// },
		Name:           "Root Service",
		RedfishVersion: ref("1.21.0"),
		// SessionService: &OdataV4IdRef{
		// 	OdataID: ref("/redfish/v1/SessionService"),
		// },
		Systems: &OdataV4IdRef{
			OdataID: ref("/redfish/v1/Systems"),
		},
	})
}

func (s redfishServer) GetRedfishV1Systems(w http.ResponseWriter, r *http.Request) {
	response(w, ComputerSystemCollectionComputerSystemCollection{
		OdataID:   ref("/redfish/v1/Systems"),
		OdataType: ref("#ComputerSystemCollection.ComputerSystemCollection"),
		Members: &[]OdataV4IdRef{
			{
				OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s", s.instanceName)),
			},
		},
		MembersOdataCount: ref(OdataV4Count(1)),
		Name:              "Computer System Collection",
	})
}

func (s redfishServer) PostRedfishV1Systems(w http.ResponseWriter, r *http.Request) {
	responseNotImplemented(w)
}

func (s redfishServer) DeleteRedfishV1SystemsComputerSystemID(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	responseNotImplemented(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemID(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return
	}

	instance, _, err := s.client.GetInstance(computerSystemID)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	powerState := ComputerSystemV1280ComputerSystem_PowerState{}
	switch instance.Status {
	case "Running":
		_ = powerState.FromResourcePowerState(ResourcePowerStateOn)

	case "Stopped":
		fallthrough

	default:
		_ = powerState.FromResourcePowerState(ResourcePowerStateOff)
	}

	response(w, ComputerSystemV1280ComputerSystem{
		OdataID:   ref(fmt.Sprintf("/redfish/v1/Systems/%s", s.instanceName)),
		OdataType: ref("#ComputerSystem.v1_28_0.ComputerSystem"),
		Actions: &ComputerSystemV1280Actions{
			HashComputerSystemReset: &ComputerSystemV1280Reset{
				Target: ref(fmt.Sprintf("/redfish/v1/Systems/%s/Actions/ComputerSystem.Reset", s.instanceName)),
			},
		},
		Bios: &OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/Bios", s.instanceName)),
		},
		ID:           s.instanceName,
		Manufacturer: ref("linuxcontainers.org"),
		Model:        ref("Incus"),
		Name:         s.instanceName,
		PowerState:   &powerState,
		SerialNumber: ref(s.instanceName),
	})
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemID(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	responseNotImplemented(w)
}

func (s redfishServer) PutRedfishV1SystemsComputerSystemID(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	responseNotImplemented(w)
}

func (s redfishServer) PostRedfishV1SystemsComputerSystemIDActionsComputerSystemReset(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return
	}

	request := ComputerSystemV1280ResetRequestBody{}
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	var action string
	var force bool
	switch *request.ResetType {
	case ResourceResetTypeOn:
		action = "start"

	case ResourceResetTypeForceOn:
		action = "start"
		force = true

	case ResourceResetTypeGracefulShutdown:
		action = "stop"

	case ResourceResetTypeForceOff:
		action = "stop"
		force = true

	default:
		responseErrWithMessage(w, http.StatusBadRequest, "reset type not supported")
		return
	}

	op, err := s.client.UpdateInstanceState(computerSystemID, incusapi.InstanceStatePut{
		Action: action,
		Force:  force,
	}, "")
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	err = op.Wait()
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	responseNoContent(w)
}
