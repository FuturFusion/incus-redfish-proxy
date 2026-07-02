package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"sort"

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
	"get-secureboot-certificates",
	"get-secureboot-certificate",
}

func main() {
	var debug bool
	var endpoint string
	var user string
	var password string

	pflag.Usage = usage
	pflag.BoolVar(&debug, "debug", false, "debug output")
	pflag.StringVar(&endpoint, "endpoint", "http://localhost:8080", "Redfish API base URL")
	pflag.StringVar(&user, "user", "", "Redfish API user name")
	pflag.StringVar(&password, "password", "", "Redfish API password")

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
		Endpoint: endpoint,
		Username: user,
		Password: password,
		Insecure: true,

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

		bios.Attributes["incus.devices.vtpm"] = `{"type": "tpm"}`

		err = bios.UpdateBiosAttributesApplyAt(bios.Attributes, schemas.OnResetSettingsApplyTime)
		die(err)

	case "remove-tpm":
		bios, err := getSystem(c).Bios()
		die(err)

		bios.Attributes["incus.devices.vtpm"] = ""

		err = bios.UpdateBiosAttributesApplyAt(bios.Attributes, schemas.OnResetSettingsApplyTime)
		die(err)

	case "get-virtual-media":
		virtualMedia := getManagerVirtualMedia(c)

		err = enc.Encode(virtualMedia.Entity)
		die(err)

	case "insert-virtual-media":
		if len(pflag.Args()) < 2 {
			fmt.Println("error: file to insert as boot media missing")
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

	case "get-secureboot-certificates":
		for _, certificate := range getSecureBootCertificates(c) {
			fmt.Printf("%s, %s\n", certificate.ID, certificate.Name)
		}

	case "get-secureboot-certificate":
		if len(pflag.Args()) < 2 {
			fmt.Println("error: certificate name missing")
			usage()
			os.Exit(1)
		}

		certificateID := pflag.Arg(1)
		var certificate *schemas.Certificate

		found := false
		for _, certificate = range getSecureBootCertificates(c) {
			if certificate.ID == certificateID {
				found = true
				break
			}
		}

		if !found {
			fmt.Printf("certificate ID %s not found", certificateID)
			os.Exit(1)
		}

		err = enc.Encode(certificate.Entity)
		die(err)
	}
}

func getSystem(c *gofish.APIClient) *schemas.ComputerSystem {
	systems, err := c.Service.Systems()
	die(err)

	if len(systems) < 1 {
		die(fmt.Errorf("no system found"))
	}

	sort.Slice(systems, func(i, j int) bool { return systems[i].ID < systems[j].ID })

	return systems[0]
}

func getManagerVirtualMedia(c *gofish.APIClient) *schemas.VirtualMedia {
	managers, err := c.Service.Managers()
	die(err)

	if len(managers) < 1 {
		die(fmt.Errorf("no manager found"))
	}

	sort.Slice(managers, func(i, j int) bool { return managers[i].ID < managers[j].ID })

	manager := managers[0]

	virtualMedias, err := manager.VirtualMedia()
	die(err)

	if len(virtualMedias) < 1 {
		die(fmt.Errorf("no virtual media found"))
	}

	sort.Slice(virtualMedias, func(i, j int) bool { return virtualMedias[i].ID < virtualMedias[j].ID })

	return virtualMedias[0]
}

func getSecureBootCertificates(c *gofish.APIClient) []*schemas.Certificate {
	system := getSystem(c)

	secureboot, err := system.SecureBoot()
	die(err)

	sercureBootDBs, err := secureboot.SecureBootDatabases()
	die(err)

	if len(sercureBootDBs) < 1 {
		die(fmt.Errorf("no secure boot database found"))
	}

	sort.Slice(sercureBootDBs, func(i, j int) bool { return sercureBootDBs[i].ID < sercureBootDBs[j].ID })

	secureBootDB := sercureBootDBs[0]

	certificates, err := secureBootDB.Certificates()
	die(err)

	sort.Slice(certificates, func(i, j int) bool { return certificates[i].ID < certificates[j].ID })

	return certificates
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
