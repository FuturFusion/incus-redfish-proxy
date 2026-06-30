package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/util/testing/boom"
)

type httpGetter struct {
	client *http.Client
	base   string
}

func (g *httpGetter) Get(path string) (*http.Response, error) {
	return g.client.Get(g.base + path)
}

func TestScraper_Run(t *testing.T) {
	resources := map[string]any{
		"/redfish/v1/": map[string]any{
			"@odata.id": "/redfish/v1/",
			"Systems":   map[string]any{"@odata.id": "/redfish/v1/Systems"},
		},
		"/redfish/v1/Systems": map[string]any{
			"@odata.id": "/redfish/v1/Systems",
			"Members": []any{
				map[string]any{"@odata.id": "/redfish/v1/Systems/1"},
				map[string]any{"@odata.id": "/redfish/v1/Systems/2"},
			},
		},
		"/redfish/v1/Systems/1": map[string]any{
			"@odata.id": "/redfish/v1/Systems/1",
			"Links": map[string]any{
				"Chassis": []any{
					map[string]any{"@odata.id": "/redfish/v1/Chassis/1"},
				},
			},
			"Oem": map[string]any{
				"External": map[string]any{"@odata.id": "https://external.example.com/redfish/v1/Foo"},
			},
		},
		"/redfish/v1/Systems/2": map[string]any{
			"@odata.id": "/redfish/v1/Systems/2",
		},
		"/redfish/v1/Chassis/1": map[string]any{
			"@odata.id": "/redfish/v1/Chassis/1",
			"Links": map[string]any{
				"ComputerSystems": []any{
					// Cycle back to a resource already scheduled.
					map[string]any{"@odata.id": "/redfish/v1/Systems/1"},
				},
			},
		},
		"/redfish/v1/Systems/1/NetworkInterfaces": map[string]any{
			"@odata.id": "/redfish/v1/Systems/1/NetworkInterfaces",
		},
	}

	var mu sync.Mutex
	hits := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()

		body, ok := resources[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		if r.URL.Path == "/redfish/v1/Systems/1" {
			w.Header().Add("Link", "</redfish/v1/Systems/1/NetworkInterfaces>; path=/NetworkInterfaces")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	base, err := url.Parse(srv.URL)
	require.NoError(t, err)

	outDir := t.TempDir()
	log := slog.New(slog.DiscardHandler)
	getter := &httpGetter{client: srv.Client(), base: srv.URL}

	scraper := NewScraper(getter, outDir, base, 4, log, nil)

	err = scraper.Run(context.Background())
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, hits, len(resources), "expected exactly the known internal resources to have been fetched")

	for path := range resources {
		require.Equal(t, 1, hits[path], "expected %s to be fetched exactly once", path)

		dir, err := resourceDir(outDir, path)
		require.NoError(t, err)
		require.FileExists(t, filepath.Join(dir, "index.json"))
		require.FileExists(t, filepath.Join(dir, "headers.json"))
	}
}

type mockGetter struct {
	mu        sync.Mutex
	responses map[string]stubResponse
	calls     map[string]int
}

type stubResponse struct {
	status int
	body   string
	err    error
}

func (s *mockGetter) Get(path string) (*http.Response, error) {
	s.mu.Lock()
	s.calls[path]++
	s.mu.Unlock()

	r, ok := s.responses[path]
	if !ok {
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}

	if r.err != nil {
		return nil, r.err
	}

	header := http.Header{}
	header.Set("Content-Type", "application/json")

	return &http.Response{StatusCode: r.status, Header: header, Body: io.NopCloser(strings.NewReader(r.body))}, nil
}

func TestScraper_Run_CollectsErrorsAndContinuesCrawl(t *testing.T) {
	getter := &mockGetter{
		calls: map[string]int{},
		responses: map[string]stubResponse{
			"/redfish/v1/": {
				status: http.StatusOK,
				body: `{
					"@odata.id": "/redfish/v1/",
					"Bad":  {"@odata.id": "/redfish/v1/Bad"},
					"Good": {"@odata.id": "/redfish/v1/Good"}
				}`,
			},
			"/redfish/v1/Bad": {
				err: boom.Error,
			},
			"/redfish/v1/Good": {
				status: http.StatusOK,
				body:   `{"@odata.id": "/redfish/v1/Good"}`,
			},
		},
	}

	base, err := url.Parse("http://bmc.example.com")
	require.NoError(t, err)

	outDir := t.TempDir()
	log := slog.New(slog.DiscardHandler)

	scraper := NewScraper(getter, outDir, base, 1, log, nil)

	err = scraper.Run(context.Background())
	boom.ErrorIs(t, err)

	goodDir, err := resourceDir(outDir, "/redfish/v1/Good")
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(goodDir, "index.json"))
	require.NoError(t, statErr, "the failure of one resource must not prevent others from being scraped")

	badDir, err := resourceDir(outDir, "/redfish/v1/Bad")
	require.NoError(t, err)

	_, statErr = os.Stat(filepath.Join(badDir, "index.json"))
	require.Error(t, statErr)
}

func TestScraper_Run_RejectsPathTraversal(t *testing.T) {
	const evilPath = "/redfish/v1/../../../../etc/cron.d/evil"

	getter := &mockGetter{
		calls: map[string]int{},
		responses: map[string]stubResponse{
			"/redfish/v1/": {
				status: http.StatusOK,
				body:   `{"@odata.id": "/redfish/v1/", "Evil": {"@odata.id": "` + evilPath + `"}}`,
			},
			evilPath: {
				status: http.StatusOK,
				body:   `{"@odata.id": "` + evilPath + `"}`,
			},
		},
	}

	base, err := url.Parse("http://bmc.example.com")
	require.NoError(t, err)

	outDir := t.TempDir()
	log := slog.New(slog.DiscardHandler)

	scraper := NewScraper(getter, outDir, base, 1, log, nil)

	err = scraper.Run(context.Background())
	require.ErrorContains(t, err, "unsafe resource path")

	require.NoFileExists(t, filepath.Join(outDir, "etc", "cron.d", "evil", "index.json"))

	var written []string

	walkErr := filepath.WalkDir(outDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !d.IsDir() {
			written = append(written, p)
		}

		return nil
	})
	require.NoError(t, walkErr)

	for _, p := range written {
		require.NotContains(t, p, "evil", "the malicious resource must not have been written anywhere under outDir")
	}
}

func TestNewScraper_ConcurrencyDefaultsToOne(t *testing.T) {
	getter := &mockGetter{calls: map[string]int{}, responses: map[string]stubResponse{}}

	base, err := url.Parse("http://bmc.example.com")
	require.NoError(t, err)

	s := NewScraper(getter, t.TempDir(), base, 0, slog.New(slog.DiscardHandler), nil)

	require.Equal(t, 1, cap(s.sem))
}

func TestScraper_Run_RetriesAfter401WithFreshSession(t *testing.T) {
	stale := &mockGetter{
		calls: map[string]int{},
		responses: map[string]stubResponse{
			"/redfish/v1/": {status: http.StatusUnauthorized},
		},
	}
	fresh := &mockGetter{
		calls: map[string]int{},
		responses: map[string]stubResponse{
			"/redfish/v1/": {status: http.StatusOK, body: `{"@odata.id": "/redfish/v1/"}`},
		},
	}

	var reloginCalls int

	relogin := func(_ context.Context) (redfishGetter, error) {
		reloginCalls++

		return fresh, nil
	}

	base, err := url.Parse("http://bmc.example.com")
	require.NoError(t, err)

	outDir := t.TempDir()
	log := slog.New(slog.DiscardHandler)

	scraper := NewScraper(stale, outDir, base, 1, log, relogin)

	err = scraper.Run(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, reloginCalls, "expected exactly one re-login attempt")
	require.Equal(t, 1, stale.calls["/redfish/v1/"], "expected the stale session to be tried exactly once")
	require.Equal(t, 1, fresh.calls["/redfish/v1/"], "expected the retry to go through the fresh session")

	dir, err := resourceDir(outDir, "/redfish/v1/")
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dir, "index.json"))
}

func TestScraper_Run_ReportsFailureWhenReloginFails(t *testing.T) {
	stale := &mockGetter{
		calls: map[string]int{},
		responses: map[string]stubResponse{
			"/redfish/v1/": {status: http.StatusUnauthorized},
		},
	}

	relogin := func(_ context.Context) (redfishGetter, error) {
		return nil, boom.Error
	}

	base, err := url.Parse("http://bmc.example.com")
	require.NoError(t, err)

	outDir := t.TempDir()
	log := slog.New(slog.DiscardHandler)

	scraper := NewScraper(stale, outDir, base, 1, log, relogin)

	err = scraper.Run(context.Background())
	boom.ErrorIs(t, err)

	dir, err := resourceDir(outDir, "/redfish/v1/")
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(dir, "index.json"))
}

func TestScraper_Run_ResumesFromPreviouslyScrapedResources(t *testing.T) {
	outDir := t.TempDir()

	cachedDir, err := resourceDir(outDir, "/redfish/v1/")
	require.NoError(t, err)

	cachedHeader := http.Header{}
	cachedHeader.Set("Content-Type", "application/json")
	cachedHeader.Set("Link", "</redfish/v1/Systems/1/NetworkInterfaces>; path=/NetworkInterfaces")

	err = writeResource(cachedDir, map[string]any{
		"@odata.id": "/redfish/v1/",
		"Systems":   map[string]any{"@odata.id": "/redfish/v1/Systems"},
	}, cachedHeader)
	require.NoError(t, err)

	getter := &mockGetter{
		calls: map[string]int{},
		responses: map[string]stubResponse{
			"/redfish/v1/Systems": {
				status: http.StatusOK,
				body:   `{"@odata.id": "/redfish/v1/Systems"}`,
			},
			"/redfish/v1/Systems/1/NetworkInterfaces": {
				status: http.StatusOK,
				body:   `{"@odata.id": "/redfish/v1/Systems/1/NetworkInterfaces"}`,
			},
		},
	}

	base, err := url.Parse("http://bmc.example.com")
	require.NoError(t, err)

	log := slog.New(slog.DiscardHandler)

	scraper := NewScraper(getter, outDir, base, 1, log, nil)

	err = scraper.Run(context.Background())
	require.NoError(t, err)

	require.Zero(t, getter.calls["/redfish/v1/"], "the cached resource must not be re-fetched over the network")
	require.Equal(t, 1, getter.calls["/redfish/v1/Systems"], "a resource discovered via the cached body must still be fetched")
	require.Equal(t, 1, getter.calls["/redfish/v1/Systems/1/NetworkInterfaces"], "a resource discovered via the cached Link header must still be fetched")
}

func TestScraper_Run_RefetchesWhenCachedResourceIsCorrupt(t *testing.T) {
	outDir := t.TempDir()

	dir, err := resourceDir(outDir, "/redfish/v1/")
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.json"), []byte("not valid json"), 0o600))

	getter := &mockGetter{
		calls: map[string]int{},
		responses: map[string]stubResponse{
			"/redfish/v1/": {status: http.StatusOK, body: `{"@odata.id": "/redfish/v1/"}`},
		},
	}

	base, err := url.Parse("http://bmc.example.com")
	require.NoError(t, err)

	log := slog.New(slog.DiscardHandler)

	scraper := NewScraper(getter, outDir, base, 1, log, nil)

	err = scraper.Run(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, getter.calls["/redfish/v1/"], "a corrupt cache entry must trigger a re-fetch")
}

func TestScraper_Run_ReportsFailureWhenRetryAlsoUnauthorized(t *testing.T) {
	stale := &mockGetter{
		calls: map[string]int{},
		responses: map[string]stubResponse{
			"/redfish/v1/": {status: http.StatusUnauthorized},
		},
	}
	fresh := &mockGetter{
		calls: map[string]int{},
		responses: map[string]stubResponse{
			"/redfish/v1/": {status: http.StatusUnauthorized},
		},
	}

	relogin := func(_ context.Context) (redfishGetter, error) {
		return fresh, nil
	}

	base, err := url.Parse("http://bmc.example.com")
	require.NoError(t, err)

	outDir := t.TempDir()
	log := slog.New(slog.DiscardHandler)

	scraper := NewScraper(stale, outDir, base, 1, log, relogin)

	err = scraper.Run(context.Background())
	require.ErrorContains(t, err, "returned status 401")
	require.Equal(t, 1, fresh.calls["/redfish/v1/"], "expected exactly one retry, no relogin loop")

	dir, err := resourceDir(outDir, "/redfish/v1/")
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(dir, "index.json"))
}
