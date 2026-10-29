package media_test

import (
	"bytes"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/media"
)

// image returns size bytes, which differ from chunk to chunk.
func image(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i*7 + i/251)
	}

	return data
}

// serve serves the image with range support. wrap may change the handling of a request.
func serve(t *testing.T, data []byte, wrap func(next http.Handler) http.Handler) string {
	t.Helper()

	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "image.iso", time.Time{}, bytes.NewReader(data))
	})

	if wrap != nil {
		handler = wrap(handler)
	}

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return server.URL
}

func open(t *testing.T, url string, opts media.Options) *media.Fetcher {
	t.Helper()

	fetcher, err := media.Open(t.Context(), nil, url, opts)
	require.NoError(t, err, "open the fetcher")
	t.Cleanup(func() { _ = fetcher.Close() })

	return fetcher
}

func sizeAndClose(t *testing.T, fetcher *media.Fetcher) int64 {
	t.Helper()

	if fetcher == nil {
		return 0
	}

	size := fetcher.Size()
	require.NoError(t, fetcher.Close(), "close the fetcher")

	return size
}

func TestOpen(t *testing.T) {
	t.Parallel()

	data := image(10_000)

	tests := []struct {
		name string
		wrap func(next http.Handler) http.Handler

		assertErr require.ErrorAssertionFunc
		wantSize  int64
	}{
		{
			name: "success - size from HEAD",

			assertErr: require.NoError,
			wantSize:  10_000,
		},
		{
			name: "success - HEAD refused",
			wrap: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodHead {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}

					next.ServeHTTP(w, r)
				})
			},

			assertErr: require.NoError,
			wantSize:  10_000,
		},
		{
			name: "success - HEAD forbidden like on a pre-signed URL",
			wrap: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodHead {
						w.WriteHeader(http.StatusForbidden)
						return
					}

					next.ServeHTTP(w, r)
				})
			},

			assertErr: require.NoError,
			wantSize:  10_000,
		},
		{
			name: "success - HEAD without Accept-Ranges",
			wrap: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodHead {
						w.Header().Set("Content-Length", "1")
						return
					}

					next.ServeHTTP(w, r)
				})
			},

			assertErr: require.NoError,
			wantSize:  10_000,
		},
		{
			name: "error - no range support",
			wrap: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Length", strconv.Itoa(len(data)))
					_, _ = w.Write(data)
				})
			},

			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorIs(tt, err, media.ErrRangeNotSupported, a...)
			},
		},
		{
			name: "error - not found",
			wrap: func(next http.Handler) http.Handler {
				return http.NotFoundHandler()
			},

			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorIs(tt, err, media.ErrImage, a...)
			},
		},
		{
			name: "error - HEAD stalls",
			wrap: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					<-r.Context().Done()
				})
			},

			assertErr: require.Error,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			url := serve(t, data, tc.wrap)

			fetcher, err := media.Open(t.Context(), nil, url, media.Options{StallTimeout: 500 * time.Millisecond})

			tc.assertErr(t, err, "open the image")
			require.Equal(t, tc.wantSize, sizeAndClose(t, fetcher), "the probe reads the size of the image")
		})
	}
}

func TestFetcher_ReadAt(t *testing.T) {
	t.Parallel()

	data := image(10_007)
	fetcher := open(t, serve(t, data, nil), media.Options{ReadAhead: 1000, CacheSize: 3000})

	tests := []struct {
		name   string
		off    int64
		length int

		wantN   int
		wantErr error
	}{
		{name: "start", off: 0, length: 10, wantN: 10},
		{name: "within a chunk", off: 1234, length: 100, wantN: 100},
		{name: "across chunks", off: 999, length: 2002, wantN: 2002},
		{name: "last bytes", off: 10_000, length: 7, wantN: 7},
		{name: "short read at the end", off: 9_990, length: 100, wantN: 17, wantErr: io.EOF},
		{name: "beyond the end", off: 10_007, length: 1, wantN: 0, wantErr: io.EOF},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := make([]byte, tc.length)

			n, err := fetcher.ReadAt(p, tc.off)

			require.Equal(t, tc.wantErr, err, "error of the read")
			require.Equal(t, tc.wantN, n, "number of bytes read")
			require.Equal(t, data[tc.off:tc.off+int64(n)], p[:n], "the read returns the bytes of the image")
		})
	}

	random := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		off := random.Int64N(int64(len(data)))
		p := make([]byte, random.IntN(3000))

		n, _ := fetcher.ReadAt(p, off)
		require.Equal(t, data[off:off+int64(n)], p[:n], "read of %d bytes at %d", len(p), off)
	}
}

func TestFetcher_SequentialReads(t *testing.T) {
	t.Parallel()

	const size = 8 << 20
	const readAhead = 1 << 20

	data := image(size)
	fetcher := open(t, serve(t, data, nil), media.Options{ReadAhead: readAhead})

	got := make([]byte, 0, size)
	p := make([]byte, 512)

	for off := int64(0); off < size; off += 512 {
		n, err := fetcher.ReadAt(p, off)
		require.NoError(t, err, "read at %d", off)

		got = append(got, p[:n]...)
	}

	require.Equal(t, data, got, "the reads return the whole image")
	require.Equal(t, int64(size/readAhead), fetcher.Stats().Requests, "every chunk is fetched once")
	require.Equal(t, int64(size), fetcher.Stats().BytesFetched, "every byte is fetched once")
}

func TestFetcher_ConcurrentReads(t *testing.T) {
	t.Parallel()

	data := image(4000)
	release := make(chan struct{})

	fetcher := open(t, serve(t, data, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.Header.Get("Range") != "bytes=0-0" {
				<-release
			}

			next.ServeHTTP(w, r)
		})
	}), media.Options{ReadAhead: 1000})

	got := make([][]byte, 16)
	errs := make([]error, 16)

	var wg sync.WaitGroup
	for i := range got {
		wg.Go(func() {
			got[i] = make([]byte, 10)
			_, errs[i] = fetcher.ReadAt(got[i], int64(i*10))
		})
	}

	require.Eventually(t, func() bool { return fetcher.Stats().Requests == 1 }, time.Second, time.Millisecond, "the first read starts a request")
	close(release)
	wg.Wait()

	for i := range got {
		require.NoError(t, errs[i], "read %d", i)
		require.Equal(t, data[i*10:i*10+10], got[i], "read %d returns the bytes of the image", i)
	}

	require.Equal(t, int64(1), fetcher.Stats().Requests, "the reads share one request")
	require.Equal(t, int64(15), fetcher.Stats().CacheHits, "the other reads use the cached chunk")
}

func TestFetcher_Eviction(t *testing.T) {
	t.Parallel()

	data := image(3000)
	fetcher := open(t, serve(t, data, nil), media.Options{ReadAhead: 1000, CacheSize: 2000})

	p := make([]byte, 1)
	for _, off := range []int64{0, 1000, 2000, 2000, 0} {
		_, err := fetcher.ReadAt(p, off)
		require.NoError(t, err, "read at %d", off)
	}

	require.Equal(t, media.Stats{Requests: 4, BytesFetched: 4000, CacheHits: 1}, fetcher.Stats(), "the first chunk is evicted and fetched again")
}

func TestFetcher_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		wrap func(next http.Handler) http.Handler

		assertErr    require.ErrorAssertionFunc
		wantRequests int64
	}{
		{
			name: "success - server errors are retried",
			wrap: func(next http.Handler) http.Handler {
				var failures atomic.Int64

				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && failures.Add(1) <= 2 {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}

					next.ServeHTTP(w, r)
				})
			},

			assertErr:    require.NoError,
			wantRequests: 3,
		},
		{
			name: "error - range ignored",
			wrap: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						r.Header.Del("Range")
					}

					next.ServeHTTP(w, r)
				})
			},

			assertErr:    require.Error,
			wantRequests: 1,
		},
		{
			name: "error - server errors persist",
			wrap: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						w.WriteHeader(http.StatusBadGateway)
						return
					}

					next.ServeHTTP(w, r)
				})
			},

			assertErr:    require.Error,
			wantRequests: 4,
		},
		{
			name: "error - server stalls before the headers",
			wrap: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						<-r.Context().Done()
						return
					}

					next.ServeHTTP(w, r)
				})
			},

			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorIs(tt, err, media.ErrStalled, a...)
			},
			wantRequests: 4,
		},
		{
			name: "error - server stalls in the body",
			wrap: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						w.Header().Set("Content-Range", "bytes 0-99/100")
						w.Header().Set("Content-Length", "100")
						w.WriteHeader(http.StatusPartialContent)
						_, _ = w.Write(make([]byte, 10))
						w.(http.Flusher).Flush()
						<-r.Context().Done()

						return
					}

					next.ServeHTTP(w, r)
				})
			},

			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorIs(tt, err, media.ErrStalled, a...)
			},
			wantRequests: 4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fetcher := open(t, serve(t, image(100), tc.wrap), media.Options{StallTimeout: 200 * time.Millisecond})

			_, err := fetcher.ReadAt(make([]byte, 10), 0)

			tc.assertErr(t, err, "read from the image")
			require.Equal(t, tc.wantRequests, fetcher.Stats().Requests, "number of requests with retries")
		})
	}
}

func TestFetcher_Prefetch(t *testing.T) {
	t.Parallel()

	data := image(3000)
	release := make(chan struct{})

	tempDir := t.TempDir()

	fetcher := open(t, serve(t, data, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Range") == "bytes=1000-1999" {
				<-release
			}

			next.ServeHTTP(w, r)
		})
	}), media.Options{ReadAhead: 1000, Prefetch: true, TempDir: tempDir})

	p := make([]byte, 10)

	_, err := fetcher.ReadAt(p, 5)
	require.NoError(t, err, "read from the first chunk")
	require.Equal(t, data[5:15], p, "the read returns the bytes of the image")

	p = make([]byte, 1000)
	done := make(chan error, 1)

	go func() {
		_, err := fetcher.ReadAt(p, 2000)
		done <- err
	}()

	require.Never(t, func() bool { return len(done) > 0 }, 50*time.Millisecond, 5*time.Millisecond, "a read waits for the chunks before it")

	close(release)
	require.NoError(t, <-done, "read the last chunk")
	require.Equal(t, data[2000:], p, "the read returns the last chunk")

	require.NoError(t, fetcher.Close(), "close the fetcher")

	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err, "read the temp directory")
	require.Empty(t, entries, "Close removes the prefetch file")
}

func TestFetcher_PrefetchRetry(t *testing.T) {
	t.Parallel()

	// Setup
	data := image(3000)
	var recovered atomic.Bool

	fetcher := open(t, serve(t, data, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Range") == "bytes=1000-1999" && !recovered.Load() {
				w.WriteHeader(http.StatusBadGateway)
				return
			}

			next.ServeHTTP(w, r)
		})
	}), media.Options{ReadAhead: 1000, Prefetch: true, TempDir: t.TempDir(), PrefetchRetry: 10 * time.Millisecond})

	head := make([]byte, 1000)
	tail := make([]byte, 1000)

	// Run test
	_, headErr := fetcher.ReadAt(head, 0)
	_, failedErr := fetcher.ReadAt(tail, 2000)

	recovered.Store(true)

	// Assert
	require.NoError(t, headErr, "chunks before the failed one are read")
	require.Equal(t, data[:1000], head, "the read returns the first chunk")
	require.Error(t, failedErr, "a read behind the failed chunk fails and does not wait")

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := fetcher.ReadAt(tail, 2000)
		require.NoError(c, err, "read the last chunk")
	}, 5*time.Second, 10*time.Millisecond, "the prefetch continues, once the server answers again")
	require.Equal(t, data[2000:], tail, "the read returns the last chunk")
}

func TestFetcher_CloseDuringRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts media.Options
	}{
		{name: "cache", opts: media.Options{ReadAhead: 1000}},
		{name: "prefetch", opts: media.Options{ReadAhead: 1000, Prefetch: true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.opts.TempDir = t.TempDir()
			blocked := make(chan struct{})

			url := serve(t, image(2000), func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && r.Header.Get("Range") != "bytes=0-0" {
						close(blocked)
						<-r.Context().Done()

						return
					}

					next.ServeHTTP(w, r)
				})
			})

			fetcher, err := media.Open(t.Context(), nil, url, tc.opts)
			require.NoError(t, err, "open the fetcher")

			result := make(chan error)
			go func() {
				_, err := fetcher.ReadAt(make([]byte, 10), 0)
				result <- err
			}()

			<-blocked
			require.NoError(t, fetcher.Close(), "close the fetcher")

			require.ErrorIs(t, <-result, os.ErrClosed, "Close ends the pending read")
		})
	}
}
