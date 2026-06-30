package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"sync"
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
	var clearOutput bool

	pflag.BoolVar(&debug, "debug", false, "debug output")
	pflag.StringVar(&opts.endpoint, "endpoint", "http://localhost:8080", "Redfish API base URL")
	pflag.StringVar(&opts.user, "user", "", "Redfish API user name")
	pflag.StringVar(&opts.password, "password", "", "Redfish API password")
	pflag.BoolVar(&opts.insecure, "insecure", false, "skip TLS certificate verification")
	pflag.IntVarP(&concurrency, "concurrency", "c", 1, "maximum number of concurrent requests")
	pflag.BoolVar(&clearOutput, "clear", false, "remove the output directory before scraping, instead of resuming from previously scraped resources")

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

	err := run(ctx, log, opts, output, concurrency, debug, clearOutput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scrape failed:\n%s\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, opts clientOptions, output string, concurrency int, debug, clearOutput bool) error {
	base, err := url.Parse(opts.endpoint)
	if err != nil {
		return fmt.Errorf("failed to parse endpoint %q: %w", opts.endpoint, err)
	}

	if clearOutput {
		err = os.RemoveAll(output)
		if err != nil {
			return fmt.Errorf("failed to clear output directory %q: %w", output, err)
		}
	}

	dumpWriter := io.Writer(nil)
	if debug {
		dumpWriter = os.Stderr
	}

	var (
		clientsMu sync.Mutex
		clients   []*gofish.APIClient
	)

	connect := func(ctx context.Context) (*gofish.APIClient, error) {
		c, err := gofish.ConnectContext(ctx, gofish.ClientConfig{
			Endpoint: opts.endpoint,
			Username: opts.user,
			Password: opts.password,
			Insecure: opts.insecure,

			ReuseConnections: true,

			DumpWriter: dumpWriter,
		})
		if err != nil {
			return nil, err
		}

		clientsMu.Lock()
		clients = append(clients, c)
		clientsMu.Unlock()

		return c, nil
	}

	client, err := connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to %q: %w", opts.endpoint, err)
	}

	defer func() {
		clientsMu.Lock()
		defer clientsMu.Unlock()

		for _, c := range clients {
			c.Logout()
		}
	}()

	relogin := func(ctx context.Context) (redfishGetter, error) {
		return connect(ctx)
	}

	scraper := NewScraper(client, output, base, concurrency, log, relogin)

	return scraper.Run(ctx)
}
