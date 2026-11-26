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
	"syscall"
	"time"

	"github.com/spf13/pflag"

	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/redfishproxy"
)

const shutdownTimeout = 30 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	// A second signal ends the process at once.
	context.AfterFunc(ctx, stop)

	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
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

	// Listen first. A failed listen must not restore the media.
	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
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
		return errors.Join(err, ln.Close())
	}

	logger := log.New(stderr, "", log.LstdFlags)
	logger.Printf("listen on %q with profile %q", ln.Addr(), *profileName)

	srv := &http.Server{Handler: h}
	srv.RegisterOnShutdown(h.ReleaseHeld)

	return serve(ctx, srv, ln, h, shutdownTimeout, logger)
}

func serve(ctx context.Context, srv *http.Server, ln net.Listener, handler io.Closer, timeout time.Duration, logger *log.Logger) error {
	served := make(chan error, 1)

	go func() {
		served <- srv.Serve(ln)
	}()

	select {
	case err := <-served:
		return errors.Join(fmt.Errorf("serve: %w", err), closeHandler(handler))

	case <-ctx.Done():
	}

	logger.Printf("shut down: %v", context.Cause(ctx))

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		// Shutdown keeps the open connections after the timeout. Close them, so no request runs during the Close of the handler.
		logger.Printf("close the open connections, they are still open after %v", timeout)
		err = errors.Join(fmt.Errorf("shut down the HTTP server: %w", err), srv.Close())
	}

	err = errors.Join(err, closeHandler(handler))

	serveErr := <-served
	if !errors.Is(serveErr, http.ErrServerClosed) {
		err = errors.Join(err, fmt.Errorf("serve: %w", serveErr))
	}

	logger.Printf("shutdown done")

	return err
}

func closeHandler(handler io.Closer) error {
	err := handler.Close()
	if err != nil {
		return fmt.Errorf("close the handler: %w", err)
	}

	return nil
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
