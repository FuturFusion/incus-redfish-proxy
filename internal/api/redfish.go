package api

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	incusclient "github.com/lxc/incus/v7/client"
	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/uefi"
	incusutil "github.com/lxc/incus/v7/shared/util"
)

type IncusClient interface {
	GetServer() (server *incusapi.Server, ETag string, err error)
	HasExtension(extension string) (exists bool)

	GetInstance(name string) (*incusapi.Instance, string, error)
	UpdateInstance(name string, instance incusapi.InstancePut, ETag string) (op incusclient.Operation, err error)
	UpdateInstanceState(name string, state incusapi.InstanceStatePut, ETag string) (op incusclient.Operation, err error)

	GetInstanceNVRAMGUID(name string, guid string) (vars map[string]*incusapi.InstanceNVRAMVariable, err error)
	GetInstanceNVRAMGUIDVar(name string, guid string, varName string) (resp *incusapi.InstanceNVRAMVariable, ETag string, err error)
	UpdateInstanceNVRAMGUIDVar(name string, guid string, varName string, data incusapi.InstanceNVRAMVariablePut, ETag string) error
	DeleteInstanceNVRAMGUIDVar(name string, guid string, varName string) error

	CreateStoragePoolVolumeFromISO(pool string, args incusclient.StorageVolumeBackupArgs) (op incusclient.Operation, err error)
	DeleteStoragePoolVolume(pool string, volType string, name string) (err error)
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

func validateManagerID(w http.ResponseWriter, managerID string) bool {
	if managerID != managerName {
		responseErr(w, http.StatusNotFound)
		return false
	}

	return true
}

func validateVirtualMediaID(w http.ResponseWriter, managerID string, virtualMediaID string) bool {
	if !validateManagerID(w, managerID) {
		return false
	}

	if virtualMediaID != virtualMediaName {
		responseErr(w, http.StatusNotFound)
		return false
	}

	return true
}

func (s redfishServer) validateComputerSystemID(w http.ResponseWriter, computerSystemID string) bool {
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return false
	}

	return true
}

func (s redfishServer) validateDatabaseID(w http.ResponseWriter, computerSystemID string, databaseID string) (secureBootDatabase, bool) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return secureBootDatabase{}, false
	}

	db, ok := lookupSecureBootDatabase(databaseID)
	if !ok {
		responseErr(w, http.StatusNotFound)
		return secureBootDatabase{}, false
	}

	return db, true
}

func (s redfishServer) hasNVRAMSupport(w http.ResponseWriter) bool {
	if !s.client.HasExtension(nvramExtension) {
		responseErrWithMessage(w, http.StatusNotImplemented, fmt.Sprintf("the Incus server is missing the %q API extension", nvramExtension))
		return false
	}

	return true
}

func (s redfishServer) GetRedfishV1(w http.ResponseWriter, r *http.Request) {
	response(w, ServiceRootV1220ServiceRoot{
		OdataID:   ref("/redfish/v1/"),
		OdataType: ref("#ServiceRoot.v1_22_0.ServiceRoot"),
		// EventService: &OdataV4IdRef{
		// 	OdataID: ref("/redfish/v1/EventService"),
		// },
		ID: "RootService",
		// Links: ServiceRootV1220Links{
		// 	Sessions: OdataV4IdRef{
		// 		OdataID: ref("/redfish/v1/SessionService/Sessions"),
		// 	},
		// },
		Managers: &OdataV4IdRef{
			OdataID: ref("/redfish/v1/Managers"),
		},
		Name:           "Root Service",
		RedfishVersion: ref("1.21.0"),
		// SessionService: &OdataV4IdRef{
		// 	OdataID: ref("/redfish/v1/SessionService"),
		// },
		Systems: &OdataV4IdRef{
			OdataID: ref("/redfish/v1/Systems"),
		},
		// Tasks: &OdataV4IdRef{
		// 	OdataID: ref("/redfish/v1/TaskService"),
		// },
	})
}

const managerName = "bmc1"

func (s redfishServer) GetRedfishV1Managers(w http.ResponseWriter, r *http.Request) {
	response(w, ManagerCollectionManagerCollection{
		OdataID:   ref("/redfish/v1/Managers"),
		OdataType: ref("#ManagerCollection.ManagerCollection"),
		Members: &[]OdataV4IdRef{
			{
				OdataID: ref(fmt.Sprintf("/redfish/v1/Managers/%s", managerName)),
			},
		},
		MembersOdataCount: ref(OdataV4Count(1)),
		Name:              "Manager Collection",
	})
}

func (s redfishServer) GetRedfishV1ManagersManagerID(w http.ResponseWriter, r *http.Request, managerID string) {
	if !validateManagerID(w, managerID) {
		return
	}

	response(w, ManagerV1250Manager{
		OdataID:   ref(fmt.Sprintf("/redfish/v1/Managers/%s", managerName)),
		OdataType: ref("#Manager.v1_25_0.Manager"),
		VirtualMedia: &OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia", managerName)),
		},
		Name: managerName,
	})
}

func (s redfishServer) PatchRedfishV1ManagersManagerID(w http.ResponseWriter, r *http.Request, managerID string) {
	if !validateManagerID(w, managerID) {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) PutRedfishV1ManagersManagerID(w http.ResponseWriter, r *http.Request, managerID string) {
	if !validateManagerID(w, managerID) {
		return
	}

	responseNotImplemented(w)
}

const virtualMediaName = "CD"

func (s redfishServer) GetRedfishV1ManagersManagerIDVirtualMedia(w http.ResponseWriter, r *http.Request, managerID string) {
	if !validateManagerID(w, managerID) {
		return
	}

	response(w, VirtualMediaCollectionVirtualMediaCollection{
		OdataID:   ref(fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia", managerName)),
		OdataType: ref("#VirtualMediaCollection.VirtualMediaCollection"),
		Members: &[]OdataV4IdRef{
			{
				OdataID: ref(fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia/%s", managerName, virtualMediaName)),
			},
		},
		MembersOdataCount: ref(OdataV4Count(1)),
		Name:              "Virtual Media Collection",
	})
}

func (s redfishServer) GetRedfishV1ManagersManagerIDVirtualMediaVirtualMediaID(w http.ResponseWriter, r *http.Request, managerID string, virtualMediaID string) {
	if !validateVirtualMediaID(w, managerID, virtualMediaID) {
		return
	}

	instance, _, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, inserted := instance.Devices["boot-media"]

	connectedViaURI := VirtualMediaV170VirtualMedia_ConnectedVia{}
	_ = connectedViaURI.FromVirtualMediaV170ConnectedVia(URI)

	base := fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia/%s", managerName, virtualMediaName)

	response(w, virtualMediaGetResponse{
		VirtualMediaV170VirtualMedia: VirtualMediaV170VirtualMedia{
			OdataID:   ref(base),
			OdataType: ref("#VirtualMedia.v1_7_0.VirtualMedia"),
			Name:      ResourceName(virtualMediaName),
			MediaTypes: &[]VirtualMediaV170MediaType{
				CD,
				DVD,
			},
			Image:             ref(fmt.Sprintf("%s-boot-media.iso", s.instanceName)),
			ConnectedVia:      ref(connectedViaURI),
			Inserted:          ref(inserted),
			WriteProtected:    ref(true),
			VerifyCertificate: ref(false),
		},
		Actions: &virtualMediaActions{
			HashVirtualMediaEjectMedia: virtualMediaActionTarget{
				Target: base + "/Actions/VirtualMedia.EjectMedia",
			},
			HashVirtualMediaInsertMedia: virtualMediaActionTarget{
				Target:     base + "/Actions/VirtualMedia.InsertMedia",
				ActionInfo: base + "/InsertMediaActionInfo",
			},
		},
	})
}

// virtualMediaGetResponse overrides the Actions field of the generated
// VirtualMediaV170VirtualMedia type.
type virtualMediaGetResponse struct {
	VirtualMediaV170VirtualMedia
	Actions *virtualMediaActions `json:"Actions,omitempty"`
}

type virtualMediaActionTarget struct {
	Target     string `json:"target"`
	ActionInfo string `json:"@Redfish.ActionInfo,omitempty"`
}

type virtualMediaActions struct {
	HashVirtualMediaEjectMedia  virtualMediaActionTarget `json:"#VirtualMedia.EjectMedia"`
	HashVirtualMediaInsertMedia virtualMediaActionTarget `json:"#VirtualMedia.InsertMedia"`
}

func (s redfishServer) PatchRedfishV1ManagersManagerIDVirtualMediaVirtualMediaID(w http.ResponseWriter, r *http.Request, managerID string, virtualMediaID string) {
	if !validateVirtualMediaID(w, managerID, virtualMediaID) {
		return
	}

	request := VirtualMediaV170VirtualMedia{}
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	if request.Inserted == nil {
		responseErrWithMessage(w, http.StatusBadRequest, "virtual media inserted state is required")
		return
	}

	if *request.Inserted && request.Image == nil {
		responseErrWithMessage(w, http.StatusBadRequest, "virtual media image is required")
		return
	}

	if !*request.Inserted {
		err = s.ejectVirtualMedia()
		if err != nil {
			respondStatusError(w, err)
			return
		}

		responseNoContent(w)
		return
	}

	err = s.insertVirtualMedia(deref(request.Image))
	if err != nil {
		respondStatusError(w, err)
		return
	}

	responseNoContent(w)
}

// statusError pairs an error with the HTTP status code the
// response to the client should use.
type statusError struct {
	status int
	err    error
}

func (e *statusError) Error() string {
	return e.err.Error()
}

func (e *statusError) Unwrap() error {
	return e.err
}

func statusErrorf(status int, format string, args ...any) error {
	return &statusError{status: status, err: fmt.Errorf(format, args...)}
}

// respondStatusError writes the response for a status error:
// the status code it carries, or 500 for any other error.
func respondStatusError(w http.ResponseWriter, err error) {
	var statusErr *statusError
	if errors.As(err, &statusErr) {
		responseErrWithMessage(w, statusErr.status, statusErr.Error())
		return
	}

	responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
}

func (s redfishServer) ejectVirtualMedia() error {
	instance, etag, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		return err
	}

	_, inserted := instance.Devices["boot-media"]
	if !inserted {
		return statusErrorf(http.StatusConflict, "no virtual media is inserted")
	}

	originalDevice := cloneStringMap(instance.Devices["boot-media"])
	delete(instance.Devices, "boot-media")

	op, err := s.client.UpdateInstance(s.instanceName, instance.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	err = s.client.DeleteStoragePoolVolume("default", "custom", fmt.Sprintf("%s-boot-media.iso", s.instanceName))
	if err != nil {
		rollbackErr := s.restoreBootMediaDevice(originalDevice)
		return combineRollbackError(err, rollbackErr)
	}

	return nil
}

func (s redfishServer) insertVirtualMedia(image string) error {
	instance, etag, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		return err
	}

	_, inserted := instance.Devices["boot-media"]
	if inserted {
		return statusErrorf(http.StatusConflict, "virtual media is already inserted")
	}

	resp, err := http.Get(image)
	if err != nil {
		return statusErrorf(http.StatusBadRequest, "%s", err.Error())
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	op, err := s.client.CreateStoragePoolVolumeFromISO("default", incusclient.StorageVolumeBackupArgs{
		Name:       fmt.Sprintf("%s-boot-media.iso", s.instanceName),
		BackupFile: resp.Body,
	})
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		rollbackErr := s.deleteBootMediaVolume()
		return combineRollbackError(err, rollbackErr)
	}

	instance.Devices["boot-media"] = map[string]string{
		"boot.priority": "10",
		"pool":          "default",
		"source":        fmt.Sprintf("%s-boot-media.iso", s.instanceName),
		"type":          "disk",
	}

	op, err = s.client.UpdateInstance(s.instanceName, instance.Writable(), etag)
	if err != nil {
		rollbackErr := s.deleteBootMediaVolume()
		return combineRollbackError(err, rollbackErr)
	}

	err = op.Wait()
	if err != nil {
		rollbackErr := s.deleteBootMediaVolume()
		return combineRollbackError(err, rollbackErr)
	}

	return nil
}

func (s redfishServer) PutRedfishV1ManagersManagerIDVirtualMediaVirtualMediaID(w http.ResponseWriter, r *http.Request, managerID string, virtualMediaID string) {
	if !validateVirtualMediaID(w, managerID, virtualMediaID) {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) PostRedfishV1ManagersManagerIDVirtualMediaVirtualMediaIDActionsVirtualMediaEjectMedia(w http.ResponseWriter, r *http.Request, managerID string, virtualMediaID string) {
	if !validateVirtualMediaID(w, managerID, virtualMediaID) {
		return
	}

	err := s.ejectVirtualMedia()
	if err != nil {
		respondStatusError(w, err)
		return
	}

	responseNoContent(w)
}

func (s redfishServer) PostRedfishV1ManagersManagerIDVirtualMediaVirtualMediaIDActionsVirtualMediaInsertMedia(w http.ResponseWriter, r *http.Request, managerID string, virtualMediaID string) {
	if !validateVirtualMediaID(w, managerID, virtualMediaID) {
		return
	}

	request := VirtualMediaV170InsertMediaRequestBody{}
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	if request.Image == "" {
		responseErrWithMessage(w, http.StatusBadRequest, "virtual media image is required")
		return
	}

	if request.TransferProtocolType != nil && *request.TransferProtocolType != HTTP && *request.TransferProtocolType != HTTPS {
		responseErrWithMessage(w, http.StatusBadRequest, fmt.Sprintf("transfer protocol %s is not supported, only HTTP and HTTPS are supported", *request.TransferProtocolType))
		return
	}

	err = s.insertVirtualMedia(request.Image)
	if err != nil {
		respondStatusError(w, err)
		return
	}

	responseNoContent(w)
}

// actionInfo and actionInfoParameter mirror the Redfish ActionInfo schema
// (http://redfish.dmtf.org/schemas/v1/ActionInfo.v1_5_0.json). They are
// hand-written rather than generated because ActionInfo resources have
// service-defined URIs that the DMTF Redfish OpenAPI schema does not
// enumerate, so oapi-codegen never sees a path to generate types or a route
// for.
type actionInfo struct {
	OdataID    string                `json:"@odata.id"`
	OdataType  string                `json:"@odata.type"`
	ID         string                `json:"Id"`
	Name       string                `json:"Name"`
	Parameters []actionInfoParameter `json:"Parameters"`
}

type actionInfoParameter struct {
	Name            string   `json:"Name"`
	Required        bool     `json:"Required"`
	DataType        string   `json:"DataType,omitempty"`
	AllowableValues []string `json:"AllowableValues,omitempty"`
}

// GetRedfishV1ManagersManagerIDVirtualMediaVirtualMediaIDInsertMediaActionInfo
// serves the ActionInfo resource referenced by the InsertMedia action's
// "@Redfish.ActionInfo" annotation. It is not part of the generated
// ServerInterface, NewHandler registers it directly.
func (s redfishServer) GetRedfishV1ManagersManagerIDVirtualMediaVirtualMediaIDInsertMediaActionInfo(w http.ResponseWriter, r *http.Request, managerID string, virtualMediaID string) {
	if !validateVirtualMediaID(w, managerID, virtualMediaID) {
		return
	}

	response(w, actionInfo{
		OdataID:   fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia/%s/InsertMediaActionInfo", managerName, virtualMediaName),
		OdataType: "#ActionInfo.v1_5_0.ActionInfo",
		ID:        "InsertMediaActionInfo",
		Name:      "Insert Media Action Info",
		Parameters: []actionInfoParameter{
			{Name: "Image", Required: true, DataType: "String"},
			{Name: "TransferProtocolType", Required: false, DataType: "String", AllowableValues: []string{string(HTTP), string(HTTPS)}},
			{Name: "Inserted", Required: false, DataType: "Boolean"},
			{Name: "WriteProtected", Required: false, DataType: "Boolean"},
		},
	})
}

// instanceConfig returns the effective configuration of the instance,
// preferring the expanded configuration so that keys coming from a profile are
// taken into account.
func instanceConfig(instance *incusapi.Instance) map[string]string {
	if len(instance.ExpandedConfig) > 0 {
		return instance.ExpandedConfig
	}

	return instance.Config
}

// instanceDevices returns the effective devices of the instance, preferring
// the expanded devices so that devices coming from a profile are taken into
// account.
func instanceDevices(instance *incusapi.Instance) incusapi.DevicesMap {
	if len(instance.ExpandedDevices) > 0 {
		return instance.ExpandedDevices
	}

	return instance.Devices
}

// instanceCPUCount returns the number of vCPUs configured for the instance,
// defaulting to one if limits.cpu is absent or is not a plain count.
func instanceCPUCount(instance *incusapi.Instance) int64 {
	cfgLimitCPU, ok := instanceConfig(instance)["limits.cpu"]
	if !ok {
		return 1
	}

	cpuNo, err := strconv.ParseInt(cfgLimitCPU, 10, 64)
	if err != nil {
		return 1
	}

	return cpuNo
}

// instanceHasTPM reports whether the instance has a TPM device attached.
func instanceHasTPM(instance *incusapi.Instance) bool {
	for _, deviceConfig := range instanceDevices(instance) {
		if deviceConfig["type"] == "tpm" {
			return true
		}
	}

	return false
}

func cloneStringMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}

	return clone
}

func (s redfishServer) deleteBootMediaVolume() error {
	return s.client.DeleteStoragePoolVolume("default", "custom", fmt.Sprintf("%s-boot-media.iso", s.instanceName))
}

func (s redfishServer) restoreBootMediaDevice(device map[string]string) error {
	instance, etag, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		return err
	}

	if instance.Devices == nil {
		instance.Devices = incusapi.DevicesMap{}
	}

	instance.Devices["boot-media"] = cloneStringMap(device)

	op, err := s.client.UpdateInstance(s.instanceName, instance.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// combineRollbackError joins operationErr with rollbackErr, if the rollback
// itself also failed, so the caller's error response reports both.
func combineRollbackError(operationErr error, rollbackErr error) error {
	if rollbackErr != nil {
		return errors.Join(operationErr, fmt.Errorf("rollback failed: %w", rollbackErr))
	}

	return operationErr
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
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemID(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	instance, _, err := s.client.GetInstance(computerSystemID)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	powerState := ComputerSystemV1290ComputerSystem_PowerState{}
	switch instance.Status {
	case "Running":
		_ = powerState.FromResourcePowerState(On)

	case "Stopped":
		fallthrough

	default:
		_ = powerState.FromResourcePowerState(Off)
	}

	server, _, err := s.client.GetServer()
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	cpuNo := instanceCPUCount(instance)

	var trustedModules *[]ComputerSystemV1290TrustedModules

	if instanceHasTPM(instance) {
		interfaceType := ComputerSystemV1290TrustedModules_InterfaceType{}
		_ = interfaceType.FromComputerSystemV1290InterfaceType(TPM20)

		state := ResourceStatus_State{}
		_ = state.FromResourceState(ResourceStateEnabled)

		trustedModules = &[]ComputerSystemV1290TrustedModules{
			{
				InterfaceType: &interfaceType,
				Status:        &ResourceStatus{State: &state},
			},
		}
	}

	response(w, ComputerSystemV1290ComputerSystem{
		OdataID:   ref(fmt.Sprintf("/redfish/v1/Systems/%s", s.instanceName)),
		OdataType: ref("#ComputerSystem.v1_29_0.ComputerSystem"),
		Actions: &ComputerSystemV1290Actions{
			HashComputerSystemReset: &ComputerSystemV1290Reset{
				Target: ref(fmt.Sprintf("/redfish/v1/Systems/%s/Actions/ComputerSystem.Reset", s.instanceName)),
			},
		},
		Bios: &OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/Bios", s.instanceName)),
		},
		// The Incus version is what determines the firmware the instance boots
		// with, so it stands in for the BIOS version.
		BiosVersion: ref(server.Environment.ServerVersion),
		Processors: &OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/Processors", s.instanceName)),
		},

		ID:           s.instanceName,
		Manufacturer: ref("linuxcontainers.org"),
		// TODO: add memory summary
		// MemorySummary: &ComputerSystemV1290MemorySummary{},
		Model:      ref("Incus"),
		Name:       s.instanceName,
		PowerState: &powerState,
		// Every vCPU is exposed as its own processor, so the number of vCPUs is
		// both the socket and the logical processor count.
		ProcessorSummary: &ComputerSystemV1290ProcessorSummary{
			Count:                 ref(cpuNo),
			CoreCount:             ref(cpuNo),
			LogicalProcessorCount: ref(cpuNo),
		},
		SecureBoot: &OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot", s.instanceName)),
		},
		SerialNumber:   ref(s.instanceName),
		TrustedModules: trustedModules,
		// TODO: add virtual media for system
		// VirtualMedia: &OdataV4IdRef{
		// 	OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/VirtualMedia", s.instanceName)),
		// },
	})
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemID(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) PutRedfishV1SystemsComputerSystemID(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) PostRedfishV1SystemsComputerSystemIDActionsComputerSystemReset(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	request := ComputerSystemV1290ResetRequestBody{}
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	if request.ResetType == nil {
		responseErrWithMessage(w, http.StatusBadRequest, "reset type is required")
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

	case ResourceResetTypeGracefulRestart:
		action = "restart"

	case ResourceResetTypeForceRestart:
		action = "restart"
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

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDBios(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	instance, _, err := s.client.GetInstance(computerSystemID)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	biosAttributes := BiosV130Attributes{}

	for configKey, configValue := range instance.Config {
		biosAttributes[fmt.Sprintf("incus.config.%s", configKey)] = configValue
	}

	for deviceName, deviceConfig := range instance.Devices {
		deviceConfigJSON, _ := json.Marshal(deviceConfig)
		biosAttributes[fmt.Sprintf("incus.devices.%s", deviceName)] = string(deviceConfigJSON)
	}

	response(w, BiosV130Bios{
		RedfishSettings: ref(SettingsV150Settings{
			SettingsObject: &OdataV4IdRef{
				OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/Bios/Settings", s.instanceName)),
			},
		}),
		OdataID:    ref(fmt.Sprintf("/redfish/v1/Systems/%s/Bios", s.instanceName)),
		OdataType:  ref("#Bios.v1_3_0.Bios"),
		Attributes: &biosAttributes,
		ID:         "Bios",
		Name:       "BIOS Configuration Current Settings",
	})
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemIDBios(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	request := BiosV130Bios{}
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	if request.Attributes == nil || len(*request.Attributes) == 0 {
		responseNoContent(w)
		return
	}

	instance, etag, err := s.client.GetInstance(computerSystemID)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	if instance.Status != "Stopped" {
		responseErrWithMessage(w, http.StatusPreconditionFailed, fmt.Sprintf("instance %q is not stopped", computerSystemID))
		return
	}

	if instance.Config == nil {
		instance.Config = map[string]string{}
	}

	if instance.Devices == nil {
		instance.Devices = incusapi.DevicesMap{}
	}

	for name, value := range *request.Attributes {
		valueStr, ok := value.(string)
		if !ok {
			responseErrWithMessage(w, http.StatusBadRequest, fmt.Sprintf("bios attribute %q has invalid type %T, string expected", name, value))
			return
		}

		switch {
		case strings.HasPrefix(name, "incus.config."):
			configKey := strings.TrimPrefix(name, "incus.config.")
			if configKey == "" {
				responseErrWithMessage(w, http.StatusBadRequest, fmt.Sprintf("bios attribute %q has an empty config key", name))
				return
			}

			if valueStr == "" {
				delete(instance.Config, configKey)
				continue
			}

			instance.Config[configKey] = valueStr

		case strings.HasPrefix(name, "incus.devices."):
			deviceName := strings.TrimPrefix(name, "incus.devices.")
			if deviceName == "" {
				responseErrWithMessage(w, http.StatusBadRequest, fmt.Sprintf("bios attribute %q has an empty device name", name))
				return
			}

			if valueStr == "" {
				delete(instance.Devices, deviceName)
				continue
			}

			var deviceConfig map[string]string

			err = json.Unmarshal([]byte(valueStr), &deviceConfig)
			if err != nil {
				responseErrWithMessage(w, http.StatusBadRequest, fmt.Sprintf("invalid value for device %q, JSON string expected: %v", deviceName, err))
				return
			}

			instance.Devices[deviceName] = deviceConfig

		default:
			responseErrWithMessage(w, http.StatusBadRequest, fmt.Sprintf("bios attribute %q is not supported", name))
			return
		}
	}

	op, err := s.client.UpdateInstance(computerSystemID, instance.Writable(), etag)
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

func (s redfishServer) PutRedfishV1SystemsComputerSystemIDBios(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDBiosSettings(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	s.GetRedfishV1SystemsComputerSystemIDBios(w, r, computerSystemID)
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemIDBiosSettings(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	s.PatchRedfishV1SystemsComputerSystemIDBios(w, r, computerSystemID)
}

func (s redfishServer) PutRedfishV1SystemsComputerSystemIDBiosSettings(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDProcessors(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	instance, _, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	cpuNo := instanceCPUCount(instance)

	cpuMembers := make([]OdataV4IdRef, 0, cpuNo)
	for i := range cpuNo {
		cpuMembers = append(cpuMembers, OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/Processors/%d", s.instanceName, i)),
		})
	}

	response(w, ProcessorCollectionProcessorCollection{
		OdataID:           ref(fmt.Sprintf("/redfish/v1/Systems/%s/Processors", s.instanceName)),
		OdataType:         ref("#ProcessorCollection.ProcessorCollection"),
		Members:           &cpuMembers,
		MembersOdataCount: ref(cpuNo),
		Name:              "Processors",
	})
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDProcessorsProcessorID(w http.ResponseWriter, r *http.Request, computerSystemID string, processorIDStr string) {
	instance, ok := s.validateProcessorID(w, computerSystemID, processorIDStr)
	if !ok {
		return
	}

	processorArchitecture := &ProcessorV1240Processor_ProcessorArchitecture{}
	instructionSet := &ProcessorV1240Processor_InstructionSet{}
	switch instance.Architecture {
	case "x86_64":
		_ = processorArchitecture.FromProcessorV1240ProcessorArchitecture(ProcessorV1240ProcessorArchitectureX86)
		_ = instructionSet.FromProcessorV1240InstructionSet(ProcessorV1240InstructionSetX8664)

	case "aarch64":
		_ = processorArchitecture.FromProcessorV1240ProcessorArchitecture(ProcessorV1240ProcessorArchitectureARM)
		_ = instructionSet.FromProcessorV1240InstructionSet(ProcessorV1240InstructionSetARMA64)
	}

	processorType := ProcessorV1240Processor_ProcessorType{}
	_ = processorType.FromProcessorV1240ProcessorType(ProcessorV1240ProcessorTypeCPU)

	response(w, ProcessorV1240Processor{
		OdataID:   ref(fmt.Sprintf("/redfish/v1/Systems/%s/Processors/%s", s.instanceName, processorIDStr)),
		OdataType: ref("#Processor.v1_24_0.Processor"),

		ID:   processorIDStr,
		Name: "Processor",
		// Incus does not report the vCPU vendor.
		Manufacturer:          ref("qemu"),
		Model:                 ref("qemu64"),
		ProcessorType:         &processorType,
		ProcessorArchitecture: processorArchitecture,
		InstructionSet:        instructionSet,
	})
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemIDProcessorsProcessorID(w http.ResponseWriter, r *http.Request, computerSystemID string, processorID string) {
	_, ok := s.validateProcessorID(w, computerSystemID, processorID)
	if !ok {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) PutRedfishV1SystemsComputerSystemIDProcessorsProcessorID(w http.ResponseWriter, r *http.Request, computerSystemID string, processorID string) {
	_, ok := s.validateProcessorID(w, computerSystemID, processorID)
	if !ok {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) validateProcessorID(w http.ResponseWriter, computerSystemID string, processorIDStr string) (*incusapi.Instance, bool) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return nil, false
	}

	processorID, err := strconv.ParseInt(processorIDStr, 10, 64)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return nil, false
	}

	instance, _, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}

	cpuNo := instanceCPUCount(instance)

	if processorID < 0 || processorID >= cpuNo {
		responseErr(w, http.StatusNotFound)
		return nil, false
	}

	return instance, true
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBoot(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	instance, _, err := s.client.GetInstance(computerSystemID)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	enabled := incusutil.IsTrueOrEmpty(instance.ExpandedConfig["security.secureboot"])

	mode := SetupMode

	if s.client.HasExtension(nvramExtension) {
		enrolled, err := s.isPlatformKeyEnrolled()
		if err != nil {
			respondNVRAMError(w, err)
			return
		}

		if enrolled {
			mode = UserMode
		}
	}

	currentBootType := SecureBootV121SecureBootCurrentBootTypeDisabled
	if enabled && instance.Status == "Running" {
		currentBootType = SecureBootV121SecureBootCurrentBootTypeEnabled
	}

	secureBootCurrentBootType := SecureBootV121SecureBoot_SecureBootCurrentBoot{}
	_ = secureBootCurrentBootType.FromSecureBootV121SecureBootCurrentBootType(currentBootType)

	secureBootMode := SecureBootV121SecureBoot_SecureBootMode{}
	_ = secureBootMode.FromSecureBootV121SecureBootModeType(mode)

	response(w, SecureBootV121SecureBoot{
		OdataID:               ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot", s.instanceName)),
		OdataType:             ref("#SecureBoot.v1_2_1.SecureBoot"),
		ID:                    "SecureBoot",
		Name:                  "UEFI Secure Boot",
		SecureBootCurrentBoot: &secureBootCurrentBootType,
		SecureBootDatabases: &OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases", s.instanceName)),
		},
		SecureBootEnable: ref(enabled),
		SecureBootMode:   &secureBootMode,
	})
}

func (s redfishServer) isPlatformKeyEnrolled() (bool, error) {
	vars, err := s.client.GetInstanceNVRAMGUID(s.instanceName, uefi.EfiGlobalVariableGuid)
	if incusapi.StatusErrorCheck(err, http.StatusNotFound) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	platformKey, ok := vars["PK"]
	if !ok || platformKey == nil {
		return false, nil
	}

	return len(platformKey.Binary) > 0, nil
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemIDSecureBoot(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	request := SecureBootV121SecureBoot{}

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	// The remaining properties of the resource are read only.
	if request.SecureBootEnable == nil {
		responseNoContent(w)
		return
	}

	instance, etag, err := s.client.GetInstance(computerSystemID)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	if instance.Status != "Stopped" {
		responseErrWithMessage(w, http.StatusPreconditionFailed, fmt.Sprintf("instance %q is not stopped", computerSystemID))
		return
	}

	if instance.Config == nil {
		instance.Config = map[string]string{}
	}

	instance.Config["security.secureboot"] = strconv.FormatBool(*request.SecureBootEnable)

	op, err := s.client.UpdateInstance(computerSystemID, instance.Writable(), etag)
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

func (s redfishServer) PutRedfishV1SystemsComputerSystemIDSecureBoot(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabases(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if !s.validateComputerSystemID(w, computerSystemID) {
		return
	}

	members := make([]OdataV4IdRef, 0, len(secureBootDatabases))
	for _, db := range secureBootDatabases {
		members = append(members, OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s", s.instanceName, db.id)),
		})
	}

	response(w, SecureBootDatabaseCollectionSecureBootDatabaseCollection{
		OdataID:           ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases", s.instanceName)),
		OdataType:         ref("#SecureBootDatabaseCollection.SecureBootDatabaseCollection"),
		Members:           &members,
		MembersOdataCount: ref(OdataV4Count(len(members))),
		Name:              "UEFI SecureBoot Database Collection",
	})
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string) {
	db, ok := s.validateDatabaseID(w, computerSystemID, databaseID)
	if !ok {
		return
	}

	response(w, SecureBootDatabaseV103SecureBootDatabase{
		OdataID:   ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s", s.instanceName, db.id)),
		OdataType: ref("#SecureBootDatabase.v1_0_3.SecureBootDatabase"),
		Certificates: &OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Certificates", s.instanceName, db.id)),
		},
		Signatures: &OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Signatures", s.instanceName, db.id)),
		},
		DatabaseID: ref(db.id),
		ID:         db.id,
		Name:       fmt.Sprintf("%s - database", db.id),
	})
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificates(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string) {
	db, ok := s.validateDatabaseID(w, computerSystemID, databaseID)
	if !ok {
		return
	}

	if !s.hasNVRAMSupport(w) {
		return
	}

	lists, _, _, err := s.getSignatureDatabase(db)
	if err != nil {
		respondNVRAMError(w, err)
		return
	}

	certificates := collectSignatures(lists, true)

	members := make([]OdataV4IdRef, 0, len(certificates))
	for _, certificate := range certificates {
		members = append(members, OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Certificates/%s", s.instanceName, db.id, certificate.id)),
		})
	}

	response(w, CertificateCollectionCertificateCollection{
		OdataID:           ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Certificates", s.instanceName, db.id)),
		OdataType:         ref("#CertificateCollection.CertificateCollection"),
		Members:           &members,
		MembersOdataCount: ref(OdataV4Count(len(members))),
		Name:              "Certificate Collection",
	})
}

func (s redfishServer) PostRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificates(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string) {
	db, ok := s.validateDatabaseID(w, computerSystemID, databaseID)
	if !ok {
		return
	}

	if !s.hasNVRAMSupport(w) {
		return
	}

	request := CertificateV1110Certificate{}

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	if request.CertificateType != nil {
		certificateType, err := request.CertificateType.AsCertificateCertificateType()
		if err != nil || certificateType != PEM {
			responseErrWithMessage(w, http.StatusBadRequest, "only PEM encoded certificates are supported")
			return
		}
	}

	if request.CertificateString == nil || *request.CertificateString == "" {
		responseErrWithMessage(w, http.StatusBadRequest, "CertificateString is required")
		return
	}

	der, err := pemToDER(*request.CertificateString)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	owner := deref(request.UefiSignatureOwner)
	if owner == "" {
		owner = uuid.Nil.String()
	}

	owner, err = uefi.ParseGUID(owner)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, fmt.Sprintf("invalid UefiSignatureOwner: %v", err))
		return
	}

	certificateID, err := s.addSignature(db, signatureList{
		Type:    "x509",
		Entries: []signatureEntry{{Owner: owner, Data: der}},
	}, true)
	if err != nil {
		respondNVRAMError(w, err)
		return
	}

	location := fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Certificates/%s", s.instanceName, db.id, certificateID)

	responseCreated(w, location, s.certificateResource(db, signatureRef{id: certificateID, entry: signatureEntry{Owner: owner, Data: der}}))
}

func (s redfishServer) DeleteRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificatesCertificateID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string, certificateID string) {
	db, ok := s.validateDatabaseID(w, computerSystemID, databaseID)
	if !ok {
		return
	}

	if !s.hasNVRAMSupport(w) {
		return
	}

	err := s.deleteSignature(db, certificateID, true)
	if err != nil {
		respondNVRAMError(w, err)
		return
	}

	responseNoContent(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificatesCertificateID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string, certificateID string) {
	db, ok := s.validateDatabaseID(w, computerSystemID, databaseID)
	if !ok {
		return
	}

	if !s.hasNVRAMSupport(w) {
		return
	}

	lists, _, _, err := s.getSignatureDatabase(db)
	if err != nil {
		respondNVRAMError(w, err)
		return
	}

	certificate, ok := lookupSignature(collectSignatures(lists, true), certificateID)
	if !ok {
		responseErr(w, http.StatusNotFound)
		return
	}

	response(w, s.certificateResource(db, certificate))
}

// certificateResource builds the Redfish representation of an X.509 signature entry.
func (s redfishServer) certificateResource(db secureBootDatabase, certificate signatureRef) CertificateV1110Certificate {
	certificateType := CertificateV1110Certificate_CertificateType{}
	_ = certificateType.FromCertificateCertificateType(PEM)

	certificateUsageType := CertificateV1110Certificate_CertificateUsageTypes_Item{}
	_ = certificateUsageType.FromCertificateCertificateUsageType(CertificateCertificateUsageTypeBIOS)

	resource := CertificateV1110Certificate{
		OdataID:                  ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Certificates/%s", s.instanceName, db.id, certificate.id)),
		OdataType:                ref("#Certificate.v1_11_0.Certificate"),
		CertificateString:        ref(derToPEM(certificate.entry.Data)),
		CertificateType:          ref(certificateType),
		CertificateUsageTypes:    &[]CertificateV1110Certificate_CertificateUsageTypes_Item{certificateUsageType},
		Fingerprint:              ref(fingerprint(certificate.entry.Data)),
		FingerprintHashAlgorithm: ref("SHA256"),
		UefiSignatureOwner:       ref(certificate.entry.Owner),
		ID:                       certificate.id,
		Name:                     "SecureBoot Certificate",
	}

	// A signature database may contain entries we cannot parse, report those as is.
	parsed, err := x509.ParseCertificate(certificate.entry.Data)
	if err != nil {
		return resource
	}

	resource.ValidNotBefore = ref(parsed.NotBefore)
	resource.ValidNotAfter = ref(parsed.NotAfter)
	resource.SerialNumber = ref(parsed.SerialNumber.String())
	resource.SignatureAlgorithm = ref(parsed.SignatureAlgorithm.String())
	resource.Subject = certificateIdentifier(parsed.Subject)
	resource.Issuer = certificateIdentifier(parsed.Issuer)

	return resource
}

// certificateIdentifier converts an X.509 name into its Redfish representation.
func certificateIdentifier(name pkix.Name) *CertificateV1110Identifier {
	identifier := CertificateV1110Identifier{
		DisplayString: ref(name.String()),
	}

	if name.CommonName != "" {
		identifier.CommonName = ref(name.CommonName)
	}

	if len(name.Organization) > 0 {
		identifier.Organization = ref(name.Organization[0])
	}

	if len(name.OrganizationalUnit) > 0 {
		identifier.OrganizationalUnit = ref(name.OrganizationalUnit[0])
	}

	if len(name.Locality) > 0 {
		identifier.City = ref(name.Locality[0])
	}

	if len(name.Province) > 0 {
		identifier.State = ref(name.Province[0])
	}

	if len(name.Country) > 0 {
		identifier.Country = ref(name.Country[0])
	}

	return &identifier
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificatesCertificateID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string, certificateID string) {
	_, ok := s.validateDatabaseID(w, computerSystemID, databaseID)
	if !ok {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) PutRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificatesCertificateID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string, certificateID string) {
	_, ok := s.validateDatabaseID(w, computerSystemID, databaseID)
	if !ok {
		return
	}

	responseNotImplemented(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDSignatures(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string) {
	db, ok := s.validateDatabaseID(w, computerSystemID, databaseID)
	if !ok {
		return
	}

	if !s.hasNVRAMSupport(w) {
		return
	}

	lists, _, _, err := s.getSignatureDatabase(db)
	if err != nil {
		respondNVRAMError(w, err)
		return
	}

	signatures := collectSignatures(lists, false)

	members := make([]OdataV4IdRef, 0, len(signatures))
	for _, signature := range signatures {
		members = append(members, OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Signatures/%s", s.instanceName, db.id, signature.id)),
		})
	}

	response(w, SignatureCollectionSignatureCollection{
		OdataID:           ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Signatures", s.instanceName, db.id)),
		OdataType:         ref("#SignatureCollection.SignatureCollection"),
		Members:           &members,
		MembersOdataCount: ref(OdataV4Count(len(members))),
		Name:              "Signature Collection",
	})
}

// The DMTF schema marks the signature collection as not insertable. We accept the
// insertion anyway, as it is the only way to add a hash to a signature database.
func (s redfishServer) PostRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDSignatures(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string) {
	db, ok := s.validateDatabaseID(w, computerSystemID, databaseID)
	if !ok {
		return
	}

	if !s.hasNVRAMSupport(w) {
		return
	}

	request := SignatureV103Signature{}

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	if request.SignatureTypeRegistry != nil {
		registry, err := request.SignatureTypeRegistry.AsSignatureSignatureTypeRegistry()
		if err != nil || registry != SignatureSignatureTypeRegistryUEFI {
			responseErrWithMessage(w, http.StatusBadRequest, "only the UEFI signature type registry is supported")
			return
		}
	}

	signatureType, ok := signatureTypeFromName(deref(request.SignatureType))
	if !ok {
		responseErrWithMessage(w, http.StatusBadRequest, fmt.Sprintf("unsupported signature type %q", deref(request.SignatureType)))
		return
	}

	data, err := hex.DecodeString(strings.TrimPrefix(deref(request.SignatureString), "0x"))
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, fmt.Sprintf("SignatureString must be hex encoded: %v", err))
		return
	}

	if len(data) == 0 {
		responseErrWithMessage(w, http.StatusBadRequest, "SignatureString is required")
		return
	}

	owner := deref(request.UefiSignatureOwner)
	if owner == "" {
		owner = uuid.Nil.String()
	}

	owner, err = uefi.ParseGUID(owner)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, fmt.Sprintf("invalid UefiSignatureOwner: %v", err))
		return
	}

	signatureID, err := s.addSignature(db, signatureList{
		Type:    signatureType,
		Entries: []signatureEntry{{Owner: owner, Data: data}},
	}, false)
	if err != nil {
		respondNVRAMError(w, err)
		return
	}

	location := fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Signatures/%s", s.instanceName, db.id, signatureID)

	responseCreated(w, location, s.signatureResource(db, signatureRef{
		id:        signatureID,
		entry:     signatureEntry{Owner: owner, Data: data},
		entryType: signatureType,
	}))
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDSignaturesSignatureID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string, signatureID string) {
	db, ok := s.validateDatabaseID(w, computerSystemID, databaseID)
	if !ok {
		return
	}

	if !s.hasNVRAMSupport(w) {
		return
	}

	lists, _, _, err := s.getSignatureDatabase(db)
	if err != nil {
		respondNVRAMError(w, err)
		return
	}

	signature, ok := lookupSignature(collectSignatures(lists, false), signatureID)
	if !ok {
		responseErr(w, http.StatusNotFound)
		return
	}

	response(w, s.signatureResource(db, signature))
}

func (s redfishServer) DeleteRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDSignaturesSignatureID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string, signatureID string) {
	db, ok := s.validateDatabaseID(w, computerSystemID, databaseID)
	if !ok {
		return
	}

	if !s.hasNVRAMSupport(w) {
		return
	}

	err := s.deleteSignature(db, signatureID, false)
	if err != nil {
		respondNVRAMError(w, err)
		return
	}

	responseNoContent(w)
}

// signatureResource builds the Redfish representation of a non certificate signature entry.
func (s redfishServer) signatureResource(db secureBootDatabase, signature signatureRef) SignatureV103Signature {
	registry := SignatureV103Signature_SignatureTypeRegistry{}
	_ = registry.FromSignatureSignatureTypeRegistry(SignatureSignatureTypeRegistryUEFI)

	return SignatureV103Signature{
		OdataID:               ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Signatures/%s", s.instanceName, db.id, signature.id)),
		OdataType:             ref("#Signature.v1_0_3.Signature"),
		SignatureString:       ref(strings.ToUpper(hex.EncodeToString(signature.entry.Data))),
		SignatureType:         ref(signatureTypeName(signature.entryType)),
		SignatureTypeRegistry: &registry,
		UefiSignatureOwner:    ref(signature.entry.Owner),
		ID:                    signature.id,
		Name:                  "SecureBoot Signature",
	}
}

// addSignature appends an entry to a signature database and returns its Redfish ID.
// The entry always gets its own signature list, because the Incus encoder requires all
// entries of a list to have the same size.
func (s redfishServer) addSignature(db secureBootDatabase, list signatureList, certificate bool) (string, error) {
	lists, variable, etag, err := s.getSignatureDatabaseForUpdate(db)
	if err != nil {
		return "", err
	}

	for _, existing := range collectSignatures(lists, certificate) {
		if bytes.Equal(existing.entry.Data, list.Entries[0].Data) {
			return "", statusErrorf(http.StatusConflict, "the signature database %q already contains this entry", db.id)
		}
	}

	lists = append(lists, list)

	err = s.putSignatureDatabase(db, lists, variable, etag)
	if err != nil {
		return "", err
	}

	added := collectSignatures(lists, certificate)

	return added[len(added)-1].id, nil
}

// deleteSignature removes an entry from a signature database. Removing the last entry of
// the platform key returns the firmware to setup mode, which is the expected UEFI behaviour.
func (s redfishServer) deleteSignature(db secureBootDatabase, id string, certificate bool) error {
	lists, variable, etag, err := s.getSignatureDatabaseForUpdate(db)
	if err != nil {
		return err
	}

	entry, ok := lookupSignature(collectSignatures(lists, certificate), id)
	if !ok {
		return statusErrorf(http.StatusNotFound, "%s", http.StatusText(http.StatusNotFound))
	}

	return s.putSignatureDatabase(db, removeSignature(lists, entry), variable, etag)
}

// getSignatureDatabaseForUpdate fetches a signature database for modification. Incus only
// allows the UEFI variables of a stopped instance to be changed.
func (s redfishServer) getSignatureDatabaseForUpdate(db secureBootDatabase) ([]signatureList, *incusapi.InstanceNVRAMVariable, string, error) {
	instance, _, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		return nil, nil, "", err
	}

	if instance.Status != "Stopped" {
		return nil, nil, "", statusErrorf(http.StatusPreconditionFailed, "instance %q is not stopped", s.instanceName)
	}

	return s.getSignatureDatabase(db)
}
