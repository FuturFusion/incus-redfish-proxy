package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/pflag"
	"github.com/stmcginnis/gofish"
)

// clientOptions holds the Redfish client connection parameters.
type clientOptions struct {
	endpoint string
	user     string
	password string
	insecure bool
}

func main() {
	var debug bool
	var opts clientOptions
	var concurrency int

	pflag.BoolVar(&debug, "debug", false, "debug output")
	pflag.StringVar(&opts.endpoint, "endpoint", "http://localhost:8080", "Redfish API base URL")
	pflag.StringVar(&opts.user, "user", "", "Redfish API user name")
	pflag.StringVar(&opts.password, "password", "", "Redfish API password")
	pflag.BoolVar(&opts.insecure, "insecure", false, "skip TLS certificate verification")
	pflag.IntVarP(&concurrency, "concurrency", "c", 1, "maximum number of concurrent requests")

	pflag.Parse()

	logLevel := slog.LevelInfo
	if debug {
		logLevel = slog.LevelDebug
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	if pflag.NArg() != 1 {
		log.Error("missing required argument: output directory for scraped resources")
		os.Exit(1)
	}

	output := pflag.Arg(0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := run(ctx, log, opts, output, concurrency, debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scrape failed:\n%s\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, opts clientOptions, output string, concurrency int, debug bool) error {
	base, err := url.Parse(opts.endpoint)
	if err != nil {
		return fmt.Errorf("failed to parse endpoint %q: %w", opts.endpoint, err)
	}

	dumpWriter := io.Writer(nil)
	if debug {
		dumpWriter = os.Stderr
	}

	client, err := gofish.ConnectContext(ctx, gofish.ClientConfig{
		Endpoint: opts.endpoint,
		Username: opts.user,
		Password: opts.password,
		Insecure: opts.insecure,

		ReuseConnections: true,

		DumpWriter: dumpWriter,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to %q: %w", opts.endpoint, err)
	}
	defer client.Logout()

	scraper := NewScraper(client, output, base, concurrency, log)

	return scraper.Run(ctx)
}
