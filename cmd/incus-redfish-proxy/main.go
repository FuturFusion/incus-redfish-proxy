package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	config "github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/spf13/pflag"

	"github.com/FuturFusion/incus-redfish-proxy/internal/api"
)

var listenAddr = "0.0.0.0:8080"

func main() {
	var remote string
	var project string

	pflag.Usage = usage
	pflag.StringVar(&remote, "remote", "", "incus remote to connect to")
	pflag.StringVar(&project, "project", "default", "incus project to connect to")

	pflag.Parse()

	if len(pflag.Args()) < 1 {
		fmt.Println("missing argument: instance-name")
		usage()
		os.Exit(1)
	}

	instanceName := pflag.Arg(0)

	cfg, err := config.LoadConfig("")
	if err != nil {
		die(fmt.Errorf("load incus config: %w", err))
	}

	if remote == "" {
		remote = cfg.DefaultRemote
	}

	client, err := cfg.GetInstanceServer(remote)
	die(err)
	client = client.UseProject(project)

	h := api.NewHandler(instanceName, client)

	s := &http.Server{
		Handler: h,
		Addr:    listenAddr,
	}

	log.Printf("listen on %q", listenAddr)
	log.Fatal(s.ListenAndServe())
}

func usage() {
	fmt.Println("Usage: redfish-incus-proxy <instance-name>")
	pflag.PrintDefaults()
}

func die(err error) {
	if err != nil {
		panic(err)
	}
}
