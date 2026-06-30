package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	incusclient "github.com/lxc/incus/v6/client"
	incusapi "github.com/lxc/incus/v6/shared/api"
)

type IncusClient interface {
	GetInstance(name string) (*incusapi.Instance, string, error)
	UpdateInstance(name string, instance incusapi.InstancePut, ETag string) (op incusclient.Operation, err error)
	UpdateInstanceState(name string, state incusapi.InstanceStatePut, ETag string) (op incusclient.Operation, err error)

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

func (s redfishServer) GetRedfishV1(w http.ResponseWriter, r *http.Request) {
	response(w, ServiceRootV1210ServiceRoot{
		OdataID:   ref("/redfish/v1/"),
		OdataType: ref("#ServiceRoot.v1_21_0.ServiceRoot"),
		// EventService: &OdataV4IdRef{
		// 	OdataID: ref("/redfish/v1/EventService"),
		// },
		ID: "RootService",
		// Links: ServiceRootV1210Links{
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
	if managerID != managerName {
		responseErr(w, http.StatusNotFound)
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
	responseNotImplemented(w)
}

func (s redfishServer) PutRedfishV1ManagersManagerID(w http.ResponseWriter, r *http.Request, managerID string) {
	responseNotImplemented(w)
}

const virtualMediaName = "CD"

func (s redfishServer) GetRedfishV1ManagersManagerIDVirtualMedia(w http.ResponseWriter, r *http.Request, managerID string) {
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
	if virtualMediaID != virtualMediaName {
		responseErr(w, http.StatusNotFound)
		return
	}

	instance, _, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, inserted := instance.Devices["boot-media"]

	connectedViaURI := VirtualMediaV165VirtualMedia_ConnectedVia{}
	_ = connectedViaURI.FromVirtualMediaV165ConnectedVia(URI)

	response(w, VirtualMediaV165VirtualMedia{
		OdataID:   ref(fmt.Sprintf("/redfish/v1/Managers/%s/VirtualMedia/%s", managerName, virtualMediaName)),
		OdataType: ref("#VirtualMedia.v1_6_5.VirtualMedia"),
		Name:      ResourceName(virtualMediaName),
		MediaTypes: &[]VirtualMediaV165MediaType{
			CD,
			DVD,
		},
		Image:             ref(fmt.Sprintf("%s-boot-media.iso", s.instanceName)),
		ConnectedVia:      ref(connectedViaURI),
		Inserted:          ref(inserted),
		WriteProtected:    ref(true),
		VerifyCertificate: ref(false),
	})
}

func (s redfishServer) PatchRedfishV1ManagersManagerIDVirtualMediaVirtualMediaID(w http.ResponseWriter, r *http.Request, managerID string, virtualMediaID string) {
	if virtualMediaID != virtualMediaName {
		responseErr(w, http.StatusNotFound)
		return
	}

	request := VirtualMediaV165VirtualMedia{}
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	instance, etag, err := s.client.GetInstance(s.instanceName)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, inserted := instance.Devices["boot-media"]

	// eject
	if !deref(request.Inserted) {
		if !inserted {
			responseNoContent(w)
			return
		}

		delete(instance.Devices, "boot-media")

		op, err := s.client.UpdateInstance(s.instanceName, instance.Writable(), etag)
		if err != nil {
			responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
			return
		}

		err = op.Wait()
		if err != nil {
			responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
			return
		}

		err = s.client.DeleteStoragePoolVolume("default", "custom", fmt.Sprintf("%s-boot-media.iso", s.instanceName))
		if err != nil {
			responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
			return
		}

		responseNoContent(w)
		return
	}

	// insert
	resp, err := http.Get(deref(request.Image))
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	op, err := s.client.CreateStoragePoolVolumeFromISO("default", incusclient.StorageVolumeBackupArgs{
		Name:       fmt.Sprintf("%s-boot-media.iso", s.instanceName),
		BackupFile: resp.Body,
	})
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	err = op.Wait()
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	instance.Devices["boot-media"] = map[string]string{
		"boot.priority": "10",
		"pool":          "default",
		"source":        fmt.Sprintf("%s-boot-media.iso", s.instanceName),
		"type":          "disk",
	}

	op, err = s.client.UpdateInstance(s.instanceName, instance.Writable(), etag)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	err = op.Wait()
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}
}

func (s redfishServer) PutRedfishV1ManagersManagerIDVirtualMediaVirtualMediaID(w http.ResponseWriter, r *http.Request, managerID string, virtualMediaID string) {
	responseNotImplemented(w)
}

func (s redfishServer) PostRedfishV1ManagersManagerIDVirtualMediaVirtualMediaIDActionsVirtualMediaEjectMedia(w http.ResponseWriter, r *http.Request, managerID string, virtualMediaID string) {
	responseNotImplemented(w)
}

func (s redfishServer) PostRedfishV1ManagersManagerIDVirtualMediaVirtualMediaIDActionsVirtualMediaInsertMedia(w http.ResponseWriter, r *http.Request, managerID string, virtualMediaID string) {
	responseNotImplemented(w)
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
		_ = powerState.FromResourcePowerState(On)

	case "Stopped":
		fallthrough

	default:
		_ = powerState.FromResourcePowerState(Off)
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
		// TODO: add memory summary
		// MemorySummary: &ComputerSystemV1280MemorySummary{},
		Model:      ref("Incus"),
		Name:       s.instanceName,
		PowerState: &powerState,
		// TODO: add processor summary
		ProcessorSummary: &ComputerSystemV1280ProcessorSummary{},
		SecureBoot: &OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot", s.instanceName)),
		},
		SerialNumber: ref(s.instanceName),
		// TODO: add trusted modules info if vtpm is present
		// TrustedModules: &[]ComputerSystemV1280TrustedModules{},
		// TODO: add virtual media for system
		// VirtualMedia: &OdataV4IdRef{
		// 	OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/VirtualMedia", s.instanceName)),
		// },
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

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDBios(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return
	}

	instance, _, err := s.client.GetInstance(computerSystemID)
	if err != nil {
		responseErrWithMessage(w, http.StatusInternalServerError, err.Error())
		return
	}

	biosAttributes := BiosV130Attributes{}

	_, ok := instance.Devices["vtpm"]
	if ok {
		biosAttributes["vTPM"] = "On"
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
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return
	}

	request := BiosV130Bios{}
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		responseErrWithMessage(w, http.StatusBadRequest, err.Error())
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

	if request.Attributes == nil {
		responseNoContent(w)
	}

	value, ok := (*request.Attributes)["vTPM"]
	if ok {
		tpmValue, ok := value.(string)
		if ok {
			if instance.Devices == nil {
				instance.Devices = incusapi.DevicesMap{}
			}

			if tpmValue == "On" {
				instance.Devices["vtpm"] = map[string]string{"type": "tpm"}
			} else {
				delete(instance.Devices, "vtpm")
			}
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
	responseNotImplemented(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDBiosSettings(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	s.GetRedfishV1SystemsComputerSystemIDBios(w, r, computerSystemID)
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemIDBiosSettings(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	s.PatchRedfishV1SystemsComputerSystemIDBios(w, r, computerSystemID)
}

func (s redfishServer) PutRedfishV1SystemsComputerSystemIDBiosSettings(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	responseNotImplemented(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBoot(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return
	}

	secureBootCurrentBootType := SecureBootV120SecureBoot_SecureBootCurrentBoot{}
	_ = secureBootCurrentBootType.FromSecureBootV120SecureBootCurrentBootType(Disabled)

	secureBootMode := SecureBootV120SecureBoot_SecureBootMode{}
	_ = secureBootMode.FromSecureBootV120SecureBootModeType(UserMode)

	response(w, SecureBootV120SecureBoot{
		OdataID:               ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot", s.instanceName)),
		OdataType:             ref("#SecureBoot.v1_2_0.SecureBoot"),
		ID:                    "SecureBoot",
		Name:                  "UEFI Secure Boot",
		SecureBootCurrentBoot: &secureBootCurrentBootType,
		SecureBootDatabases: &OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases", s.instanceName)),
		},
		SecureBootEnable: ref(false),
		SecureBootMode:   &secureBootMode,
	})
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemIDSecureBoot(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	responseNotImplemented(w)
}

func (s redfishServer) PutRedfishV1SystemsComputerSystemIDSecureBoot(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	responseNotImplemented(w)
}

var secureBootDatabases = []string{
	"DB",
	"DBX",
	"KEK",
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabases(w http.ResponseWriter, r *http.Request, computerSystemID string) {
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return
	}

	members := []OdataV4IdRef{}
	for _, cert := range secureBootDatabases {
		members = append(members, OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s", s.instanceName, cert)),
		})
	}

	response(w, SecureBootDatabaseCollectionSecureBootDatabaseCollection{
		OdataID:           ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases", s.instanceName)),
		OdataType:         ref("#SecureBootDatabaseCollection.SecureBootDatabaseCollection"),
		Members:           &members,
		MembersOdataCount: ref(OdataV4Count(len(secureBootDatabases))),
		Name:              "UEFI SecureBoot Database Collection",
	})
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string) {
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return
	}

	if !slices.Contains(secureBootDatabases, databaseID) {
		responseErr(w, http.StatusNotFound)
		return
	}

	response(w, SecureBootDatabaseV103SecureBootDatabase{
		OdataID:   ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s", s.instanceName, databaseID)),
		OdataType: ref("#SecureBootDatabase.v1_0_3.SecureBootDatabase"),
		Certificates: &OdataV4IdRef{
			OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Certificates", s.instanceName, databaseID)),
		},
		ID:   databaseID,
		Name: fmt.Sprintf("%s - database", databaseID),
	})
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificates(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string) {
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return
	}

	if !slices.Contains(secureBootDatabases, databaseID) {
		responseErr(w, http.StatusNotFound)
		return
	}

	response(w, CertificateCollectionCertificateCollection{
		OdataID:   ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Certificates", s.instanceName, databaseID)),
		OdataType: ref("#CertificateCollection.CertificateCollection"),
		Members: &[]OdataV4IdRef{
			// TODO: this is only a dummy entry
			{
				OdataID: ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Certificates/1", s.instanceName, databaseID)),
			},
		},
		MembersOdataCount: ref(OdataV4Count(0)),
		Name:              "Certificate Collection",
	})
}

func (s redfishServer) PostRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificates(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string) {
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return
	}

	if !slices.Contains(secureBootDatabases, databaseID) {
		responseErr(w, http.StatusNotFound)
		return
	}

	// TODO: process provided request payload containing the certificate

	responseNoContent(w)
}

func (s redfishServer) DeleteRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificatesCertificateID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string, certificateID string) {
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return
	}

	if !slices.Contains(secureBootDatabases, databaseID) {
		responseErr(w, http.StatusNotFound)
		return
	}

	// TODO: this is only a dummy entry

	if certificateID != "1" {
		responseErr(w, http.StatusNotFound)
		return
	}

	responseNoContent(w)
}

func (s redfishServer) GetRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificatesCertificateID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string, certificateID string) {
	if computerSystemID != s.instanceName {
		responseErr(w, http.StatusNotFound)
		return
	}

	if !slices.Contains(secureBootDatabases, databaseID) {
		responseErr(w, http.StatusNotFound)
		return
	}

	// TODO: this is only a dummy entry

	if certificateID != "1" {
		responseErr(w, http.StatusNotFound)
		return
	}

	certificateType := CertificateV1110Certificate_CertificateType{}
	_ = certificateType.FromCertificateCertificateType(PEM)

	response(w, CertificateV1110Certificate{
		OdataID:           ref(fmt.Sprintf("/redfish/v1/Systems/%s/SecureBoot/SecureBootDatabases/%s/Certificates/%s", s.instanceName, databaseID, certificateID)),
		OdataType:         ref("#Certificate.v1_11_0.Certificate"),
		CertificateString: ref(""),
		CertificateType:   ref(certificateType),
		ID:                certificateID,
		Name:              certificateID,
	})
}

func (s redfishServer) PatchRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificatesCertificateID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string, certificateID string) {
	responseNotImplemented(w)
}

func (s redfishServer) PutRedfishV1SystemsComputerSystemIDSecureBootSecureBootDatabasesDatabaseIDCertificatesCertificateID(w http.ResponseWriter, r *http.Request, computerSystemID string, databaseID string, certificateID string) {
	responseNotImplemented(w)
}
