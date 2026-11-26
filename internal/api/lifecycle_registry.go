package api

import (
	"net/http"
	"slices"
)

const (
	lifecycleRegistryPrefix  = "IncusLifecycle"
	lifecycleRegistryVersion = "1.0.0"
	lifecycleRegistryID      = lifecycleRegistryPrefix + "." + lifecycleRegistryVersion

	// Redfish leaves out the patch version in the registry name and the MessageId.
	lifecycleRegistry      = lifecycleRegistryPrefix + ".1.0"
	lifecycleMessagePrefix = lifecycleRegistry + "."

	lifecycleRegistryFilePath = registriesPath + "/" + lifecycleRegistryID
	lifecycleRegistryPath     = lifecycleRegistryFilePath + "/" + lifecycleRegistryID + ".json"
)

type messageRegistry struct {
	OdataID         string                            `json:"@odata.id"`
	OdataType       string                            `json:"@odata.type"`
	ID              string                            `json:"Id"`
	Name            string                            `json:"Name"`
	Description     string                            `json:"Description"`
	Language        string                            `json:"Language"`
	OwningEntity    string                            `json:"OwningEntity"`
	RegistryPrefix  string                            `json:"RegistryPrefix"`
	RegistryVersion string                            `json:"RegistryVersion"`
	Messages        map[string]messageRegistryMessage `json:"Messages"`
}

type messageRegistryMessage struct {
	Description     string   `json:"Description"`
	Message         string   `json:"Message"`
	MessageSeverity string   `json:"MessageSeverity"`
	NumberOfArgs    int      `json:"NumberOfArgs"`
	ParamTypes      []string `json:"ParamTypes,omitempty"`
	Resolution      string   `json:"Resolution"`
}

func getLifecycleRegistryFile(w http.ResponseWriter, r *http.Request) {
	response(w, messageRegistryFile{
		OdataID:   lifecycleRegistryFilePath,
		OdataType: "#MessageRegistryFile.v1_1_5.MessageRegistryFile",
		ID:        lifecycleRegistryID,
		Name:      "Incus Lifecycle Message Registry File",
		Registry:  lifecycleRegistry,
		Languages: []string{"en"},
		Location:  []messageRegistryFileLocation{{Language: "en", URI: lifecycleRegistryPath}},
	})
}

func getLifecycleRegistry(w http.ResponseWriter, r *http.Request) {
	messages := make(map[string]messageRegistryMessage, len(lifecycleMessages))
	for action, message := range lifecycleMessages {
		numberOfArgs := len(message.args("", ""))

		messages[message.key] = messageRegistryMessage{
			Description:     "Incus reported the lifecycle event " + action + " for the instance.",
			Message:         message.message,
			MessageSeverity: string(message.eventSeverity()),
			NumberOfArgs:    numberOfArgs,
			ParamTypes:      slices.Repeat([]string{"string"}, numberOfArgs),
			Resolution:      "None.",
		}
	}

	response(w, messageRegistry{
		OdataID:         lifecycleRegistryPath,
		OdataType:       "#MessageRegistry.v1_6_3.MessageRegistry",
		ID:              lifecycleRegistryID,
		Name:            "Incus Lifecycle Message Registry",
		Description:     "The messages of the lifecycle events that Incus reports for the instance.",
		Language:        "en",
		OwningEntity:    "linuxcontainers.org",
		RegistryPrefix:  lifecycleRegistryPrefix,
		RegistryVersion: lifecycleRegistryVersion,
		Messages:        messages,
	})
}
