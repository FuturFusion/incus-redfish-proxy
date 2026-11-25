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
	"strconv"
	"strings"

	"github.com/spf13/pflag"
	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
	"go.yaml.in/yaml/v4"
)

var commands = []string{
	"info",
	"start",
	"stop",
	"restart",
	"get-bios-settings",
	"add-tpm",
	"remove-tpm",
	"get-virtual-media",
	"insert-virtual-media",
	"eject-virtual-media",
	"get-secureboot",
	"set-secureboot",
	"get-secureboot-databases",
	"get-secureboot-certificates",
	"get-secureboot-certificate",
	"add-secureboot-certificate",
	"delete-secureboot-certificate",
	"get-secureboot-signatures",
}

var requiredArguments = map[string]string{
	"insert-virtual-media":          "file to insert as boot media",
	"set-secureboot":                "enabled state",
	"get-secureboot-certificate":    "certificate name",
	"add-secureboot-certificate":    "certificate file",
	"delete-secureboot-certificate": "certificate name",
}

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := pflag.NewFlagSet("incus-redfish", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(flags, stderr) }

	debug := flags.Bool("debug", false, "debug output")
	endpoint := flags.String("endpoint", "http://localhost:8080", "Redfish API base URL")
	user := flags.String("user", "", "Redfish API user name")
	password := flags.String("password", "", "Redfish API password")
	help := flags.BoolP("help", "h", false, "show this help and exit")

	err := flags.Parse(args)
	if err != nil {
		return err
	}

	if *help {
		usage(flags, stdout)

		return nil
	}

	command := flags.Arg(0)
	if !slices.Contains(commands, command) {
		flags.Usage()

		return fmt.Errorf("unknown command %q", command)
	}

	argument, ok := requiredArguments[command]
	if ok && flags.NArg() < 2 {
		flags.Usage()

		return fmt.Errorf("missing argument: %s", argument)
	}

	dumpWriter := io.Writer(nil)
	if *debug {
		dumpWriter = stdout
	}

	c, err := gofish.Connect(gofish.ClientConfig{
		Endpoint: *endpoint,
		Username: *user,
		Password: *password,
		Insecure: true,

		ReuseConnections: true,

		DumpWriter: dumpWriter,
	})
	die(err)
	defer c.Logout()

	enc := yaml.NewEncoder(stdout)
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

	case "restart":
		_, err := getSystem(c).Reset(schemas.ForceRestartResetType)
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
		virtualMedia := getManagerVirtualMedia(c)

		address, shutdown := serveFileOnce(flags.Arg(1))
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

	case "get-secureboot":
		secureBoot, err := getSystem(c).SecureBoot()
		die(err)

		secureBoot.RawData = nil

		err = enc.Encode(secureBoot)
		die(err)

	case "set-secureboot":
		enabled, err := strconv.ParseBool(flags.Arg(1))
		die(err)

		secureBoot, err := getSystem(c).SecureBoot()
		die(err)

		secureBoot.SecureBootEnable = enabled

		err = secureBoot.Update()
		die(err)

	case "get-secureboot-databases":
		for _, database := range getSecureBootDatabases(c) {
			_, _ = fmt.Fprintf(stdout, "%s, %s\n", database.ID, database.Name)
		}

	case "get-secureboot-certificates":
		for _, certificate := range getSecureBootCertificates(c, flags.Arg(1)) {
			_, _ = fmt.Fprintf(stdout, "%s, %s\n", certificate.ID, certificate.Subject.CommonName)
		}

	case "get-secureboot-certificate":
		certificateID := flags.Arg(1)
		var certificate *schemas.Certificate

		found := false
		for _, certificate = range getSecureBootCertificates(c, flags.Arg(2)) {
			if certificate.ID == certificateID {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("certificate ID %s not found", certificateID)
		}

		certificate.RawData = nil

		err = enc.Encode(certificate)
		die(err)

	case "add-secureboot-certificate":
		certificate, err := os.ReadFile(flags.Arg(1))
		die(err)

		database := getSecureBootDatabase(c, flags.Arg(2))

		payload := map[string]string{
			"CertificateString": string(certificate),
			"CertificateType":   string(schemas.PEMCertificateType),
		}

		if flags.Arg(3) != "" {
			payload["UefiSignatureOwner"] = flags.Arg(3)
		}

		resp, err := c.Post(database.ODataID+"/Certificates", payload)
		die(err)
		defer resp.Body.Close()

		_, _ = fmt.Fprintln(stdout, resp.Header.Get("Location"))

	case "delete-secureboot-certificate":
		database := getSecureBootDatabase(c, flags.Arg(2))

		resp, err := c.Delete(fmt.Sprintf("%s/Certificates/%s", database.ODataID, flags.Arg(1)))
		die(err)
		defer resp.Body.Close()

	case "get-secureboot-signatures":
		database := flags.Arg(1)
		if database == "" {
			database = "dbx"
		}

		signatures, err := getSecureBootDatabase(c, database).Signatures()
		die(err)

		sort.Slice(signatures, func(i, j int) bool { return signatures[i].ID < signatures[j].ID })

		for _, signature := range signatures {
			_, _ = fmt.Fprintf(stdout, "%s, %s, %s\n", signature.ID, signature.SignatureType, signature.SignatureString)
		}
	}

	return nil
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

func getSecureBootDatabases(c *gofish.APIClient) []*schemas.SecureBootDatabase {
	secureboot, err := getSystem(c).SecureBoot()
	die(err)

	secureBootDBs, err := secureboot.SecureBootDatabases()
	die(err)

	if len(secureBootDBs) < 1 {
		die(fmt.Errorf("no secure boot database found"))
	}

	sort.Slice(secureBootDBs, func(i, j int) bool { return secureBootDBs[i].ID < secureBootDBs[j].ID })

	return secureBootDBs
}

func getSecureBootDatabase(c *gofish.APIClient, databaseID string) *schemas.SecureBootDatabase {
	if databaseID == "" {
		databaseID = "db"
	}

	for _, database := range getSecureBootDatabases(c) {
		if strings.EqualFold(database.ID, databaseID) {
			return database
		}
	}

	die(fmt.Errorf("secure boot database %q not found", databaseID))

	return nil
}

func getSecureBootCertificates(c *gofish.APIClient, databaseID string) []*schemas.Certificate {
	certificates, err := getSecureBootDatabase(c, databaseID).Certificates()
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

func usage(flags *pflag.FlagSet, w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: incus-redfish [flags] <command> [arguments]")
	_, _ = fmt.Fprintln(w, "Commands:")
	for _, cmd := range commands {
		_, _ = fmt.Fprintf(w, "  * %s\n", cmd)
	}

	_, _ = fmt.Fprintln(w, "Flags:")
	flags.SetOutput(w)
	flags.PrintDefaults()
}

func die(err error) {
	if err != nil {
		panic(err)
	}
}
