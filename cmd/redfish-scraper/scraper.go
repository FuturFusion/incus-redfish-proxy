package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

type redfishGetter interface {
	Get(path string) (*http.Response, error)
}

// Scraper crawls a Redfish endpoint, following every @odata.id reference it
// finds (recursively, anywhere in a response body, plus URIs in Link
// response headers) and writes each resource to outDir preserving the
// directory layout.
type Scraper struct {
	client redfishGetter
	outDir string
	base   *url.URL
	log    *slog.Logger
	sem    chan struct{}
	wg     sync.WaitGroup

	mu      sync.Mutex
	visited map[string]bool
	errs    []error
}

// NewScraper creates a Scraper that fetches resources through client and
// writes them to outDir. base identifies the scheme+host of the endpoint and
// is used to decide whether a discovered link stays on the same Redfish
// service. concurrency limits the number of in-flight GET requests; a value
// less than 1 is treated as 1.
func NewScraper(client redfishGetter, outDir string, base *url.URL, concurrency int, log *slog.Logger) *Scraper {
	if concurrency < 1 {
		concurrency = 1
	}

	return &Scraper{
		client:  client,
		outDir:  outDir,
		base:    base,
		log:     log,
		sem:     make(chan struct{}, concurrency),
		visited: map[string]bool{},
	}
}

// Run crawls the endpoint starting at "/redfish/v1/" and blocks until the
// crawl is complete. It returns a combined error if any resource failed to
// fetch or store, but the crawl is best-effort: a single failure does not
// abort the rest of the crawl.
func (s *Scraper) Run(ctx context.Context) error {
	s.schedule(ctx, "/redfish/v1/")
	s.wg.Wait()

	if len(s.errs) == 0 {
		return nil
	}

	return scrapeErrors(s.errs)
}

// schedule marks path as visited and, if it was not already visited, spawns
// a goroutine to fetch and process it. Marking the path as visited before
// spawning the goroutine guarantees that every URL is scheduled at most
// once, which handles both cycles and deduplication.
func (s *Scraper) schedule(ctx context.Context, path string) {
	s.mu.Lock()
	already := s.visited[path]
	s.visited[path] = true
	s.mu.Unlock()

	if already {
		return
	}

	s.wg.Add(1)

	go s.visit(ctx, path)
}

func (s *Scraper) addErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.errs = append(s.errs, err)
}

func (s *Scraper) visit(ctx context.Context, path string) {
	defer s.wg.Done()

	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}

	defer func() { <-s.sem }()

	s.log.DebugContext(ctx, "fetching resource", slog.String("path", path))

	resp, err := s.client.Get(path)
	if err != nil {
		s.log.ErrorContext(ctx, "failed to fetch resource", slog.String("path", path), slog.Any("error", err))
		s.addErr(fmt.Errorf("failed to fetch %q: %w", path, err))

		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.log.ErrorContext(ctx, "resource returned non-2xx status", slog.String("path", path), slog.Int("status_code", resp.StatusCode))
		s.addErr(fmt.Errorf("%q returned status %d", path, resp.StatusCode))

		return
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "application/json") {
		s.log.DebugContext(ctx, "skipping non-JSON resource", slog.String("path", path), slog.String("content_type", contentType))

		return
	}

	var decoded any

	err = json.NewDecoder(resp.Body).Decode(&decoded)
	if err != nil {
		s.log.ErrorContext(ctx, "failed to decode resource body", slog.String("path", path), slog.Any("error", err))
		s.addErr(fmt.Errorf("failed to decode body of %q: %w", path, err))

		return
	}

	for _, child := range extractODataIDs(decoded) {
		s.scheduleIfSameEndpoint(ctx, child)
	}

	for _, child := range parseLinkHeader(resp.Header.Values("Link")) {
		s.scheduleIfSameEndpoint(ctx, child)
	}

	dir, err := resourceDir(s.outDir, path)
	if err != nil {
		s.log.ErrorContext(ctx, "refusing to write resource", slog.String("path", path), slog.Any("error", err))
		s.addErr(fmt.Errorf("refusing to write %q: %w", path, err))

		return
	}

	err = writeResource(dir, decoded, resp.Header)
	if err != nil {
		s.log.ErrorContext(ctx, "failed to write resource", slog.String("path", path), slog.Any("error", err))
		s.addErr(fmt.Errorf("failed to write %q: %w", path, err))

		return
	}
}

func (s *Scraper) scheduleIfSameEndpoint(ctx context.Context, raw string) {
	path, ok := sameEndpoint(s.base, raw)
	if !ok {
		s.log.DebugContext(ctx, "skipping link to external host", slog.String("link", raw))

		return
	}

	s.schedule(ctx, path)
}
