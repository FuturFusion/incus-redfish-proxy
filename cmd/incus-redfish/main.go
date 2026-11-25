package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

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
		flags.Usage()

		return err
	}

	if *help {
		usage(flags, stdout)

		return nil
	}

	if flags.NArg() < 1 {
		flags.Usage()

		return errors.New("missing argument: command")
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
	if err != nil {
		return fmt.Errorf("connect to %q: %w", *endpoint, err)
	}

	defer c.Logout()

	enc := yaml.NewEncoder(stdout)
	enc.SetIndent(2)

	switch command {
	case "info":
		system, err := getSystem(c)
		if err != nil {
			return err
		}

		system.RawData = nil

		return enc.Encode(system.Entity)

	case "start":
		return reset(c, schemas.ForceOnResetType)

	case "stop":
		return reset(c, schemas.ForceOffResetType)

	case "restart":
		return reset(c, schemas.ForceRestartResetType)

	case "get-bios-settings":
		bios, err := getBios(c)
		if err != nil {
			return err
		}

		return enc.Encode(bios.Attributes)

	case "add-tpm":
		return setBiosAttribute(c, "incus.devices.vtpm", `{"type": "tpm"}`)

	case "remove-tpm":
		return setBiosAttribute(c, "incus.devices.vtpm", "")

	case "get-virtual-media":
		virtualMedia, err := getManagerVirtualMedia(c)
		if err != nil {
			return err
		}

		return enc.Encode(virtualMedia.Entity)

	case "insert-virtual-media":
		virtualMedia, err := getManagerVirtualMedia(c)
		if err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		fs, err := serveFile(flags.Arg(1), stderr)
		if err != nil {
			return err
		}

		_, err = virtualMedia.InsertMedia(&schemas.VirtualMediaInsertMediaParameters{
			Image:                fs.address,
			Inserted:             ref(true),
			TransferMethod:       ref(schemas.StreamTransferMethod),
			TransferProtocolType: ref(schemas.HTTPTransferProtocolType),
			WriteProtected:       ref(true),
		})
		if err != nil {
			return errors.Join(fmt.Errorf("insert virtual media: %w", err), fs.close())
		}

		// The proxy reads the media on demand, so the file must stay available.
		_, _ = fmt.Fprintf(stderr, "serve %s at %s, press Ctrl-C to stop\n", flags.Arg(1), fs.address)

		return fs.serveUntil(ctx)

	case "eject-virtual-media":
		virtualMedia, err := getManagerVirtualMedia(c)
		if err != nil {
			return err
		}

		_, err = virtualMedia.EjectMedia()
		if err != nil {
			return fmt.Errorf("eject virtual media: %w", err)
		}

	case "get-secureboot":
		secureBoot, err := getSecureBoot(c)
		if err != nil {
			return err
		}

		secureBoot.RawData = nil

		return enc.Encode(secureBoot)

	case "set-secureboot":
		enabled, err := strconv.ParseBool(flags.Arg(1))
		if err != nil {
			return fmt.Errorf("parse enabled state: %w", err)
		}

		secureBoot, err := getSecureBoot(c)
		if err != nil {
			return err
		}

		secureBoot.SecureBootEnable = enabled

		err = secureBoot.Update()
		if err != nil {
			return fmt.Errorf("update secure boot: %w", err)
		}

	case "get-secureboot-databases":
		databases, err := getSecureBootDatabases(c)
		if err != nil {
			return err
		}

		for _, database := range databases {
			_, err = fmt.Fprintf(stdout, "%s, %s\n", database.ID, database.Name)
			if err != nil {
				return fmt.Errorf("write output: %w", err)
			}
		}

	case "get-secureboot-certificates":
		certificates, err := getSecureBootCertificates(c, flags.Arg(1))
		if err != nil {
			return err
		}

		for _, certificate := range certificates {
			_, err = fmt.Fprintf(stdout, "%s, %s\n", certificate.ID, certificate.Subject.CommonName)
			if err != nil {
				return fmt.Errorf("write output: %w", err)
			}
		}

	case "get-secureboot-certificate":
		certificateID := flags.Arg(1)

		certificates, err := getSecureBootCertificates(c, flags.Arg(2))
		if err != nil {
			return err
		}

		i := slices.IndexFunc(certificates, func(certificate *schemas.Certificate) bool { return certificate.ID == certificateID })
		if i < 0 {
			return fmt.Errorf("certificate ID %s not found", certificateID)
		}

		certificate := certificates[i]
		certificate.RawData = nil

		return enc.Encode(certificate)

	case "add-secureboot-certificate":
		certificate, err := os.ReadFile(flags.Arg(1))
		if err != nil {
			return err
		}

		database, err := getSecureBootDatabase(c, flags.Arg(2))
		if err != nil {
			return err
		}

		payload := map[string]string{
			"CertificateString": string(certificate),
			"CertificateType":   string(schemas.PEMCertificateType),
		}

		if flags.Arg(3) != "" {
			payload["UefiSignatureOwner"] = flags.Arg(3)
		}

		resp, err := c.Post(database.ODataID+"/Certificates", payload)
		if err != nil {
			return fmt.Errorf("add certificate: %w", err)
		}

		defer resp.Body.Close()

		_, err = fmt.Fprintln(stdout, resp.Header.Get("Location"))
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}

	case "delete-secureboot-certificate":
		database, err := getSecureBootDatabase(c, flags.Arg(2))
		if err != nil {
			return err
		}

		resp, err := c.Delete(fmt.Sprintf("%s/Certificates/%s", database.ODataID, flags.Arg(1)))
		if err != nil {
			return fmt.Errorf("delete certificate: %w", err)
		}

		defer resp.Body.Close()

	case "get-secureboot-signatures":
		databaseID := flags.Arg(1)
		if databaseID == "" {
			databaseID = "dbx"
		}

		database, err := getSecureBootDatabase(c, databaseID)
		if err != nil {
			return err
		}

		signatures, err := database.Signatures()
		if err != nil {
			return fmt.Errorf("get signatures: %w", err)
		}

		sort.Slice(signatures, func(i, j int) bool { return signatures[i].ID < signatures[j].ID })

		for _, signature := range signatures {
			_, err = fmt.Fprintf(stdout, "%s, %s, %s\n", signature.ID, signature.SignatureType, signature.SignatureString)
			if err != nil {
				return fmt.Errorf("write output: %w", err)
			}
		}
	}

	return nil
}

func getSystem(c *gofish.APIClient) (*schemas.ComputerSystem, error) {
	systems, err := c.Service.Systems()
	if err != nil {
		return nil, fmt.Errorf("get systems: %w", err)
	}

	if len(systems) < 1 {
		return nil, errors.New("no system found")
	}

	sort.Slice(systems, func(i, j int) bool { return systems[i].ID < systems[j].ID })

	return systems[0], nil
}

func reset(c *gofish.APIClient, resetType schemas.ResetType) error {
	system, err := getSystem(c)
	if err != nil {
		return err
	}

	_, err = system.Reset(resetType)
	if err != nil {
		return fmt.Errorf("reset system: %w", err)
	}

	return nil
}

func getBios(c *gofish.APIClient) (*schemas.Bios, error) {
	system, err := getSystem(c)
	if err != nil {
		return nil, err
	}

	bios, err := system.Bios()
	if err != nil {
		return nil, fmt.Errorf("get bios: %w", err)
	}

	return bios, nil
}

func setBiosAttribute(c *gofish.APIClient, name string, value string) error {
	bios, err := getBios(c)
	if err != nil {
		return err
	}

	bios.Attributes[name] = value

	err = bios.UpdateBiosAttributesApplyAt(bios.Attributes, schemas.OnResetSettingsApplyTime)
	if err != nil {
		return fmt.Errorf("update bios attributes: %w", err)
	}

	return nil
}

func getManagerVirtualMedia(c *gofish.APIClient) (*schemas.VirtualMedia, error) {
	managers, err := c.Service.Managers()
	if err != nil {
		return nil, fmt.Errorf("get managers: %w", err)
	}

	if len(managers) < 1 {
		return nil, errors.New("no manager found")
	}

	sort.Slice(managers, func(i, j int) bool { return managers[i].ID < managers[j].ID })

	manager := managers[0]

	virtualMedias, err := manager.VirtualMedia()
	if err != nil {
		return nil, fmt.Errorf("get virtual media: %w", err)
	}

	if len(virtualMedias) < 1 {
		return nil, errors.New("no virtual media found")
	}

	sort.Slice(virtualMedias, func(i, j int) bool { return virtualMedias[i].ID < virtualMedias[j].ID })

	return virtualMedias[0], nil
}

func getSecureBoot(c *gofish.APIClient) (*schemas.SecureBoot, error) {
	system, err := getSystem(c)
	if err != nil {
		return nil, err
	}

	secureBoot, err := system.SecureBoot()
	if err != nil {
		return nil, fmt.Errorf("get secure boot: %w", err)
	}

	return secureBoot, nil
}

func getSecureBootDatabases(c *gofish.APIClient) ([]*schemas.SecureBootDatabase, error) {
	secureBoot, err := getSecureBoot(c)
	if err != nil {
		return nil, err
	}

	secureBootDBs, err := secureBoot.SecureBootDatabases()
	if err != nil {
		return nil, fmt.Errorf("get secure boot databases: %w", err)
	}

	if len(secureBootDBs) < 1 {
		return nil, errors.New("no secure boot database found")
	}

	sort.Slice(secureBootDBs, func(i, j int) bool { return secureBootDBs[i].ID < secureBootDBs[j].ID })

	return secureBootDBs, nil
}

func getSecureBootDatabase(c *gofish.APIClient, databaseID string) (*schemas.SecureBootDatabase, error) {
	if databaseID == "" {
		databaseID = "db"
	}

	databases, err := getSecureBootDatabases(c)
	if err != nil {
		return nil, err
	}

	for _, database := range databases {
		if strings.EqualFold(database.ID, databaseID) {
			return database, nil
		}
	}

	return nil, fmt.Errorf("secure boot database %q not found", databaseID)
}

func getSecureBootCertificates(c *gofish.APIClient, databaseID string) ([]*schemas.Certificate, error) {
	database, err := getSecureBootDatabase(c, databaseID)
	if err != nil {
		return nil, err
	}

	certificates, err := database.Certificates()
	if err != nil {
		return nil, fmt.Errorf("get certificates: %w", err)
	}

	sort.Slice(certificates, func(i, j int) bool { return certificates[i].ID < certificates[j].ID })

	return certificates, nil
}

type fileServer struct {
	address  string
	srv      *http.Server
	serveErr chan error
}

const shutdownTimeout = 5 * time.Second

func serveFile(filename string, errorLog io.Writer) (*fileServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filename)
	})

	fs := &fileServer{
		address: fmt.Sprintf("http://127.0.0.1:%d/", ln.Addr().(*net.TCPAddr).Port),
		srv: &http.Server{
			Handler:  mux,
			ErrorLog: log.New(errorLog, "", log.LstdFlags),
		},
		serveErr: make(chan error, 1),
	}

	go func() {
		err := fs.srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}

		fs.serveErr <- err
	}()

	return fs, nil
}

func (fs *fileServer) serveUntil(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return fs.close()

	case err := <-fs.serveErr:
		return fmt.Errorf("serve file: %w", err)
	}
}

func (fs *fileServer) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var err error

	shutdownErr := fs.srv.Shutdown(ctx)
	if shutdownErr != nil {
		err = errors.Join(fmt.Errorf("stop file server: %w", shutdownErr), fs.srv.Close())
	}

	serveErr := <-fs.serveErr
	if serveErr != nil {
		err = errors.Join(err, fmt.Errorf("serve file: %w", serveErr))
	}

	return err
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
