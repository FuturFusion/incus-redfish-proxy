package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
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
	"get-virtual-media",
	"insert-virtual-media",
	"eject-virtual-media",
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

	case "get-virtual-media":
		virtualMedia := getManagerVirtualMedia(c)

		err = enc.Encode(virtualMedia.Entity)
		die(err)

	case "insert-virtual-media":
		if len(pflag.Args()) < 2 {
			fmt.Println("file to insert as boot media missing")
			usage()
			os.Exit(1)
		}

		virtualMedia := getManagerVirtualMedia(c)

		address, shutdown := serveFileOnce(pflag.Arg(1))
		defer shutdown()

		taskMonitor, err := virtualMedia.InsertMedia(&schemas.VirtualMediaInsertMediaParameters{
			Image:                address,
			Inserted:             ref(true),
			TransferMethod:       ref(schemas.StreamTransferMethod),
			TransferProtocolType: ref(schemas.HTTPTransferProtocolType),
			WriteProtected:       ref(true),
		})
		die(err)

		_ = taskMonitor

	case "eject-virtual-media":
		virtualMedia := getManagerVirtualMedia(c)

		taskMonitor, err := virtualMedia.EjectMedia()
		die(err)

		_ = taskMonitor
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

func getManagerVirtualMedia(c *gofish.APIClient) *schemas.VirtualMedia {
	managers, err := c.Service.Managers()
	die(err)

	if len(managers) < 1 {
		die(fmt.Errorf("no manager found"))
	}

	manager := managers[0]

	virtualMedias, err := manager.VirtualMedia()
	die(err)

	if len(virtualMedias) < 1 {
		die(fmt.Errorf("no virtual media found"))
	}

	return virtualMedias[0]
}

func serveFileOnce(filename string) (string, func()) {
	ln, err := net.Listen("tcp", ":0")
	die(err)

	srv := &http.Server{}
	done := make(chan struct{})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		http.ServeFile(w, r, filename)
	})

	port := ln.Addr().(*net.TCPAddr).Port
	address := fmt.Sprintf("http://localhost:%d/", port)

	shutdown := func() {
		<-done
		err = srv.Shutdown(context.Background())
		die(err)
	}

	go func() {
		err = srv.Serve(ln)
		if err != http.ErrServerClosed {
			die(err)
		}
	}()

	return address, shutdown
}

func ref[T any](v T) *T {
	return &v
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
