package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/spf13/pflag"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/redfishproxy"
)

func main() {
	var remote string
	var project string
	var listenAddr string
	var profileName string
	var listProfiles bool
	var timingScale float64
	var mediaDir string

	pflag.Usage = usage
	pflag.StringVar(&remote, "remote", "", "incus remote to connect to")
	pflag.StringVar(&project, "project", "default", "incus project to connect to")
	pflag.StringVar(&listenAddr, "listen", "0.0.0.0:8080", "address to listen on")
	pflag.StringVar(&profileName, "profile", profile.DefaultName, "name of an embedded profile or path of a profile file")
	pflag.BoolVar(&listProfiles, "list-profiles", false, "list the embedded profiles and exit")
	pflag.StringVar(&mediaDir, "media-dir", redfishproxy.DefaultMediaDir, "directory for streamed media, must be visible to incusd")
	pflag.Float64Var(&timingScale, "timing-scale", 0, "multiply every duration of the profile, replaces timing.scale of the profile if set")

	pflag.Parse()

	if listProfiles {
		printProfiles()
		return
	}

	if len(pflag.Args()) < 1 {
		fmt.Println("missing argument: instance-name")
		usage()
		os.Exit(1)
	}

	instanceName := pflag.Arg(0)

	h, err := redfishproxy.NewHandler(redfishproxy.Config{
		InstanceName: instanceName,
		Remote:       remote,
		Project:      project,
		Profile:      profileName,
		TimingScale:  timingScale,
		MediaDir:     mediaDir,
	})
	die(err)

	s := &http.Server{
		Handler: h,
		Addr:    listenAddr,
	}

	log.Printf("listen on %q with profile %q", listenAddr, profileName)
	log.Fatal(s.ListenAndServe())
}

func printProfiles() {
	for _, name := range redfishproxy.Profiles() {
		p, err := profile.Load(name)
		die(err)

		fmt.Printf("%s\t%s\n", p.Name, p.Description)
	}
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
