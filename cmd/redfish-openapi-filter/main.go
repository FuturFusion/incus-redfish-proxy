package main

import (
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"
)

var includedPaths = map[string]bool{
	"/redfish/v1/": true,
	// "/redfish/v1/Managers":                   true,
	// "/redfish/v1/Managers/{ManagerId}":       true,
	"/redfish/v1/Systems":                                                 true,
	"/redfish/v1/Systems/{ComputerSystemId}":                              true,
	"/redfish/v1/Systems/{ComputerSystemId}/Actions/ComputerSystem.Reset": true,
}

var incudedPathPrefixes = []string{
	// "/redfish/v1/Managers/{ManagerId}/VirtualMedia",
	// "/redfish/v1/SessionService",
	// "/redfish/v1/Systems/{ComputerSystemId}/Bios",
	// "/redfish/v1/Systems/{ComputerSystemId}/SecureBoot",
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: redfish-openapi-filter <output-filename>")
		os.Exit(1)
	}

	output := os.Args[1]

	resp, err := http.Get("http://redfish.dmtf.org/schemas/v1/openapi.yaml")
	die(err)

	defer func() {
		err := resp.Body.Close()
		die(err)
	}()

	dec := yaml.NewDecoder(resp.Body)

	data := struct {
		Components map[string]any `yaml:"components"`
		Info       map[string]any `yaml:"info"`
		OpenAPI    string         `yaml:"openapi"`
		Paths      map[string]any `yaml:"paths"`
	}{}

	err = dec.Decode(&data)
	die(err)

	for path := range data.Paths {
		if match(path) {
			continue
		}

		delete(data.Paths, path)
	}

	f, err := os.OpenFile(output, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	die(err)
	defer f.Close()

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	defer func() {
		_ = enc.Close()
	}()

	err = enc.Encode(data)
	die(err)
}

func match(path string) bool {
	if includedPaths[path] {
		return true
	}

	return slices.ContainsFunc(incudedPathPrefixes, func(prefix string) bool {
		return strings.HasPrefix(path, prefix)
	})
}

func die(err error) {
	if err != nil {
		panic(err)
	}
}
