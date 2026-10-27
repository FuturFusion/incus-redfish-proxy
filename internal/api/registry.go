package api

import (
	"net/http"
	"strings"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
)

// The registry resources are hand written, the generated schema has no paths below /redfish/v1/Registries.

const registriesPath = "/redfish/v1/Registries"

type resourceCollection struct {
	OdataID           string         `json:"@odata.id"`
	OdataType         string         `json:"@odata.type"`
	Name              string         `json:"Name"`
	Members           []OdataV4IdRef `json:"Members"`
	MembersOdataCount int            `json:"Members@odata.count"`
}

type messageRegistryFile struct {
	OdataID   string                        `json:"@odata.id"`
	OdataType string                        `json:"@odata.type"`
	ID        string                        `json:"Id"`
	Name      string                        `json:"Name"`
	Registry  string                        `json:"Registry"`
	Languages []string                      `json:"Languages"`
	Location  []messageRegistryFileLocation `json:"Location"`
}

type messageRegistryFileLocation struct {
	Language string `json:"Language"`
	URI      string `json:"Uri"`
}

type attributeRegistry struct {
	OdataID         string                   `json:"@odata.id"`
	OdataType       string                   `json:"@odata.type"`
	ID              string                   `json:"Id"`
	Name            string                   `json:"Name"`
	Language        string                   `json:"Language"`
	OwningEntity    string                   `json:"OwningEntity"`
	RegistryVersion string                   `json:"RegistryVersion"`
	RegistryEntries attributeRegistryEntries `json:"RegistryEntries"`
}

type attributeRegistryEntries struct {
	Attributes []attributeRegistryAttribute `json:"Attributes"`
}

type attributeRegistryAttribute struct {
	AttributeName string                   `json:"AttributeName"`
	DisplayName   string                   `json:"DisplayName"`
	Type          profile.AttributeType    `json:"Type"`
	ReadOnly      bool                     `json:"ReadOnly"`
	DefaultValue  any                      `json:"DefaultValue"`
	Value         []attributeRegistryValue `json:"Value,omitempty"`
	LowerBound    *int64                   `json:"LowerBound,omitempty"`
	UpperBound    *int64                   `json:"UpperBound,omitempty"`
}

type attributeRegistryValue struct {
	ValueName        string `json:"ValueName"`
	ValueDisplayName string `json:"ValueDisplayName"`
}

// registriesLink returns the link to the registries of the service root, nil without an attribute registry.
func (s redfishServer) registriesLink() *OdataV4IdRef {
	if s.profile.BIOS.Registry.ID == "" {
		return nil
	}

	return &OdataV4IdRef{OdataID: ref(registriesPath)}
}

func (s redfishServer) registryFilePath() string {
	return registriesPath + "/" + s.profile.BIOS.Registry.ID
}

func (s redfishServer) attributeRegistryPath() string {
	return s.registryFilePath() + "/" + s.profile.BIOS.Registry.ID + ".json"
}

// registerRegistryRoutes adds the routes of the attribute registry, if the profile has one.
func (s redfishServer) registerRegistryRoutes(mux *http.ServeMux) {
	if s.profile.BIOS.Registry.ID == "" {
		return
	}

	mux.HandleFunc("GET "+registriesPath, s.getRegistries)
	mux.HandleFunc("GET "+s.registryFilePath(), s.getRegistryFile)
	mux.HandleFunc("GET "+s.attributeRegistryPath(), s.getAttributeRegistry)
}

func (s redfishServer) getRegistries(w http.ResponseWriter, r *http.Request) {
	response(w, resourceCollection{
		OdataID:           registriesPath,
		OdataType:         "#MessageRegistryFileCollection.MessageRegistryFileCollection",
		Name:              "Registry File Collection",
		Members:           []OdataV4IdRef{{OdataID: ref(s.registryFilePath())}},
		MembersOdataCount: 1,
	})
}

func (s redfishServer) getRegistryFile(w http.ResponseWriter, r *http.Request) {
	id := s.profile.BIOS.Registry.ID

	response(w, messageRegistryFile{
		OdataID:   s.registryFilePath(),
		OdataType: "#MessageRegistryFile.v1_1_5.MessageRegistryFile",
		ID:        id,
		Name:      "BIOS Attribute Registry File",
		Registry:  id,
		Languages: []string{"en"},
		Location:  []messageRegistryFileLocation{{Language: "en", URI: s.attributeRegistryPath()}},
	})
}

func (s redfishServer) getAttributeRegistry(w http.ResponseWriter, r *http.Request) {
	registry := s.profile.BIOS.Registry

	attributes := make([]attributeRegistryAttribute, 0, len(registry.Attributes))
	for _, attribute := range registry.Attributes {
		values := make([]attributeRegistryValue, 0, len(attribute.Values))
		for _, value := range attribute.Values {
			values = append(values, attributeRegistryValue{ValueName: value, ValueDisplayName: value})
		}

		attributes = append(attributes, attributeRegistryAttribute{
			AttributeName: attribute.Name,
			DisplayName:   attribute.Name,
			Type:          attribute.Type,
			ReadOnly:      attribute.ReadOnly,
			DefaultValue:  attribute.DefaultValue(),
			Value:         values,
			LowerBound:    attribute.Min,
			UpperBound:    attribute.Max,
		})
	}

	response(w, attributeRegistry{
		OdataID:         s.attributeRegistryPath(),
		OdataType:       "#AttributeRegistry.v1_3_8.AttributeRegistry",
		ID:              registry.ID,
		Name:            "BIOS Attribute Registry",
		Language:        "en",
		OwningEntity:    "linuxcontainers.org",
		RegistryVersion: registryVersion(registry.ID),
		RegistryEntries: attributeRegistryEntries{Attributes: attributes},
	})
}

// registryVersion turns the version suffix of a registry ID like Name.v1_0_0 into 1.0.0.
func registryVersion(id string) string {
	_, version, _ := strings.Cut(id, ".v")

	return strings.ReplaceAll(version, "_", ".")
}
