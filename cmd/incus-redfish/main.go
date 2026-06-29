package main

import (
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/spf13/pflag"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
	"go.yaml.in/yaml/v4"
)

var commands = []string{
	"info",
	"start",
	"stop",
	"get-bios-settings",
	"add-tpm",
	"remove-tpm",
}

func main() {
	var debug bool

	pflag.Usage = usage
	pflag.BoolVar(&debug, "debug", false, "debug output")

	pflag.Parse()

	if len(pflag.Args()) < 1 || !slices.Contains(commands, pflag.Arg(0)) {
		usage()
		os.Exit(1)
	}

	command := pflag.Arg(0)

	dumpWriter := io.Writer(nil)
	if debug {
		dumpWriter = os.Stdout
	}

	c, err := gofish.Connect(gofish.ClientConfig{
		Endpoint: "http://localhost:8080",

		ReuseConnections: true,

		DumpWriter: dumpWriter,
	})
	die(err)
	defer c.Logout()

	enc := yaml.NewEncoder(os.Stdout)
	enc.SetIndent(2)

	switch command {
	case "info":
		system := getSystem(c)

		system.RawData = nil

		err = enc.Encode(system.Entity)
		die(err)

	case "start":
		_, err := getSystem(c).Reset(schemas.ForceOnResetType)
		die(err)

	case "stop":
		_, err := getSystem(c).Reset(schemas.ForceOffResetType)
		die(err)

	case "get-bios-settings":
		bios, err := getSystem(c).Bios()
		die(err)

		err = enc.Encode(bios.Attributes)
		die(err)

	case "add-tpm":
		bios, err := getSystem(c).Bios()
		die(err)

		bios.Attributes["vTPM"] = "On"

		err = bios.UpdateBiosAttributesApplyAt(bios.Attributes, schemas.OnResetSettingsApplyTime)
		die(err)

	case "remove-tpm":
		bios, err := getSystem(c).Bios()
		die(err)

		bios.Attributes["vTPM"] = "Off"

		err = bios.UpdateBiosAttributesApplyAt(bios.Attributes, schemas.OnResetSettingsApplyTime)
		die(err)
	}
}

func getSystem(c *gofish.APIClient) *schemas.ComputerSystem {
	systems, err := c.Service.Systems()
	die(err)

	if len(systems) < 1 {
		die(fmt.Errorf("no system found"))
	}

	return systems[0]
}

func usage() {
	fmt.Println("Usage: redfish-incus <command>")
	fmt.Println("Commands:")
	for _, cmd := range commands {
		fmt.Printf("  * %s\n", cmd)
	}
}

func die(err error) {
	if err != nil {
		panic(err)
	}
}
