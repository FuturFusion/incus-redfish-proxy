package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/spf13/pflag"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/redfishproxy"
)

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := pflag.NewFlagSet("incus-redfish-proxy", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(flags, stderr) }

	remote := flags.String("remote", "", "incus remote to connect to")
	project := flags.String("project", "default", "incus project to connect to")
	listenAddr := flags.String("listen", "0.0.0.0:8080", "address to listen on")
	profileName := flags.String("profile", profile.DefaultName, "name of an embedded profile or path of a profile file")
	listProfiles := flags.Bool("list-profiles", false, "list the embedded profiles and exit")
	mediaDir := flags.String("media-dir", redfishproxy.DefaultMediaDir, "directory for streamed media, must be visible to incusd")
	timingScale := flags.Float64("timing-scale", 0, "multiply every duration of the profile, replaces timing.scale of the profile if set")
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

	if *listProfiles {
		return printProfiles(stdout)
	}

	if flags.NArg() < 1 {
		flags.Usage()

		return errors.New("missing argument: instance-name")
	}

	h, err := redfishproxy.NewHandler(redfishproxy.Config{
		InstanceName: flags.Arg(0),
		Remote:       *remote,
		Project:      *project,
		Profile:      *profileName,
		TimingScale:  *timingScale,
		MediaDir:     *mediaDir,
	})
	if err != nil {
		return err
	}

	s := &http.Server{
		Handler: h,
		Addr:    *listenAddr,
	}

	logger := log.New(stderr, "", log.LstdFlags)
	logger.Printf("listen on %q with profile %q", *listenAddr, *profileName)

	err = s.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return fmt.Errorf("serve: %w", err)
}

func printProfiles(w io.Writer) error {
	for _, name := range redfishproxy.Profiles() {
		p, err := profile.Load(name)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(w, "%s\t%s\n", p.Name, p.Description)
		if err != nil {
			return err
		}
	}

	return nil
}

func usage(flags *pflag.FlagSet, w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: incus-redfish-proxy [flags] <instance-name>")
	flags.SetOutput(w)
	flags.PrintDefaults()
}
