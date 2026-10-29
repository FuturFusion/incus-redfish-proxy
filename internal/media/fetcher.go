// Package media reads virtual media images over HTTP on demand.
package media

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrImage wraps the errors of Open that the image or its server causes.
var ErrImage = errors.New("image cannot be streamed")

// ErrStalled ends a request, if the server sends no data within the stall timeout.
var ErrStalled = errors.New("image server sent no data within the stall timeout")

// ErrRangeNotSupported is the error Open returns, if the server does not answer range requests.
var ErrRangeNotSupported = errors.New("server does not support range requests")

// ErrNotSupported is the error NewMount returns on platforms without FUSE (Filesystem in Userspace) support.
var ErrNotSupported = errors.New("media mounts are only supported on Linux")

const (
	defaultReadAhead = 4 << 20
	defaultCacheSize = 64 << 20

	defaultStallTimeout  = 30 * time.Second
	defaultPrefetchRetry = time.Second
	maxPrefetchRetry     = 30 * time.Second

	// fetchAttempts is the first request and its retries.
	fetchAttempts = 4
	retryDelay    = 50 * time.Millisecond
)

// Options configure a Fetcher.
type Options struct {
	// ReadAhead is the size the fetcher rounds every range request up to, 4 MiB if zero.
	ReadAhead int64

	// CacheSize bounds the bytes kept in memory, 64 MiB if zero.
	CacheSize int64

	// Prefetch downloads the whole image sequentially into a file in TempDir, like a baseboard management controller (BMC) that downloads an image before it boots.
	Prefetch bool
	TempDir  string

	// StallTimeout ends a request that receives no data for this long, 30 seconds if zero.
	StallTimeout time.Duration

	// PrefetchRetry is the first wait before the prefetch requests a failed chunk again, 1 second if zero.
	// The wait doubles up to 30 seconds.
	PrefetchRetry time.Duration
}

// Stats counts the work of a Fetcher.
type Stats struct {
	Requests     int64
	BytesFetched int64
	CacheHits    int64
}

// Fetcher reads an image over HTTP on demand.
type Fetcher struct {
	client    *http.Client
	url       string
	size      int64
	readAhead int64
	cacheSize int64

	stallTimeout  time.Duration
	prefetchRetry time.Duration

	// ctx ends the requests on Close.
	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
	closeErr  error

	mu     sync.Mutex
	stats  Stats
	chunks map[int64]*chunk
	lru    *list.List
	cached int64

	// The prefetch mode keeps the chunks in file, present marks the ones written.
	file        *os.File
	present     []bool
	prefetchErr error
	progress    chan struct{}
	prefetched  chan struct{}
}

// chunk is a ReadAhead aligned part of the image, ready is closed once data or err is set.
type chunk struct {
	ready   chan struct{}
	data    []byte
	err     error
	element *list.Element
}

// Open probes the URL and returns a fetcher for it.
func Open(ctx context.Context, client *http.Client, url string, opts Options) (*Fetcher, error) {
	if client == nil {
		client = http.DefaultClient
	}

	f := &Fetcher{
		client:        client,
		url:           url,
		readAhead:     opts.ReadAhead,
		cacheSize:     opts.CacheSize,
		stallTimeout:  opts.StallTimeout,
		prefetchRetry: opts.PrefetchRetry,
		chunks:        map[int64]*chunk{},
		lru:           list.New(),
	}

	if f.readAhead <= 0 {
		f.readAhead = defaultReadAhead
	}

	if f.cacheSize <= 0 {
		f.cacheSize = defaultCacheSize
	}

	if f.stallTimeout <= 0 {
		f.stallTimeout = defaultStallTimeout
	}

	if f.prefetchRetry <= 0 {
		f.prefetchRetry = defaultPrefetchRetry
	}

	// A server that does not answer must not block the insert or the start of the proxy.
	probeCtx, cancel := context.WithTimeout(ctx, f.stallTimeout)
	size, err := f.probe(probeCtx)
	cancel()

	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrImage, err)
	}

	f.size = size

	// The fetcher outlives the context of Open, Close ends it.
	f.ctx, f.cancel = context.WithCancel(context.WithoutCancel(ctx))

	if !opts.Prefetch {
		return f, nil
	}

	err = f.startPrefetch(opts.TempDir)
	if err != nil {
		f.cancel()
		return nil, err
	}

	return f, nil
}

// Size returns the size of the image.
func (f *Fetcher) Size() int64 {
	return f.size
}

// Stats returns the counters of the fetcher.
func (f *Fetcher) Stats() Stats {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.stats
}

// ReadAt reads from the image. Pending reads end with os.ErrClosed, once the fetcher is closed.
func (f *Fetcher) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("negative offset %d", off)
	}

	if off >= f.size {
		return 0, io.EOF
	}

	n := int(min(int64(len(p)), f.size-off))

	var err error
	if f.file != nil {
		err = f.readPrefetched(p[:n], off)
	} else {
		err = f.readCached(p[:n], off)
	}

	if err != nil {
		return 0, err
	}

	if n < len(p) {
		return n, io.EOF
	}

	return n, nil
}

// Close ends pending reads and the prefetch, and removes the prefetch file.
func (f *Fetcher) Close() error {
	f.closeOnce.Do(func() {
		f.cancel()

		if f.file == nil {
			return
		}

		<-f.prefetched
		f.closeErr = errors.Join(f.file.Close(), os.Remove(f.file.Name()))
	})

	return f.closeErr
}

func (f *Fetcher) chunkRange(index int64) (int64, int64) {
	start := index * f.readAhead
	return start, min(start+f.readAhead, f.size) - 1
}

func (f *Fetcher) readCached(p []byte, off int64) error {
	for n := 0; n < len(p); {
		pos := off + int64(n)
		index := pos / f.readAhead

		data, err := f.cachedChunk(index)
		if err != nil {
			return err
		}

		n += copy(p[n:], data[pos-index*f.readAhead:])
	}

	return nil
}

// cachedChunk returns the chunk from the cache. Concurrent reads of a missing chunk share one request.
func (f *Fetcher) cachedChunk(index int64) ([]byte, error) {
	f.mu.Lock()

	c, found := f.chunks[index]
	if found {
		f.stats.CacheHits++

		if c.element != nil {
			f.lru.MoveToFront(c.element)
		}
	} else {
		c = &chunk{ready: make(chan struct{})}
		f.chunks[index] = c
	}

	f.mu.Unlock()

	if !found {
		f.load(index, c)
	}

	select {
	case <-c.ready:
	case <-f.ctx.Done():
	}

	if f.ctx.Err() != nil {
		return nil, os.ErrClosed
	}

	return c.data, c.err
}

func (f *Fetcher) load(index int64, c *chunk) {
	data, err := f.fetchRange(f.chunkRange(index))

	f.mu.Lock()
	defer f.mu.Unlock()

	c.data, c.err = data, err
	close(c.ready)

	// The next read fetches a failed chunk again.
	if err != nil {
		delete(f.chunks, index)
		return
	}

	c.element = f.lru.PushFront(index)
	f.cached += int64(len(data))

	for f.cached > f.cacheSize && f.lru.Len() > 1 {
		oldest, ok := f.lru.Remove(f.lru.Back()).(int64)
		if !ok {
			return
		}

		f.cached -= int64(len(f.chunks[oldest].data))
		delete(f.chunks, oldest)
	}
}

func (f *Fetcher) startPrefetch(tempDir string) error {
	file, err := os.CreateTemp(tempDir, "media-*.img")
	if err != nil {
		return err
	}

	// A sparse file only takes the space of the chunks written.
	err = file.Truncate(f.size)
	if err != nil {
		return errors.Join(err, file.Close(), os.Remove(file.Name()))
	}

	f.file = file
	f.present = make([]bool, (f.size+f.readAhead-1)/f.readAhead)
	f.progress = make(chan struct{})
	f.prefetched = make(chan struct{})

	go f.prefetch()

	return nil
}

// prefetch downloads the chunks in order and tells waiting reads about each one.
// It requests a failed chunk again until the server answers, reads of the missing chunks fail meanwhile.
func (f *Fetcher) prefetch() {
	defer close(f.prefetched)

	wait := f.prefetchRetry

	for index := int64(0); index < int64(len(f.present)); {
		start, end := f.chunkRange(index)

		data, err := f.fetchRange(start, end)
		if err == nil {
			_, err = f.file.WriteAt(data, start)
		}

		f.mu.Lock()
		f.present[index] = err == nil
		f.prefetchErr = err
		close(f.progress)
		f.progress = make(chan struct{})
		f.mu.Unlock()

		if err == nil {
			index++
			wait = f.prefetchRetry

			continue
		}

		if f.ctx.Err() != nil {
			return
		}

		log.Printf("prefetch bytes %d-%d of %q failed, retry in %s: %v", start, end, f.url, wait, err)

		select {
		case <-time.After(wait):
		case <-f.ctx.Done():
			return
		}

		wait = min(2*wait, maxPrefetchRetry)
	}
}

// readPrefetched waits until the prefetch wrote the chunks of the read, there is no out of order fetch.
func (f *Fetcher) readPrefetched(p []byte, off int64) error {
	last := (off + int64(len(p)) - 1) / f.readAhead

	for index := off / f.readAhead; index <= last; {
		f.mu.Lock()
		present, err, progress := f.present[index], f.prefetchErr, f.progress
		f.mu.Unlock()

		if present {
			index++
			continue
		}

		if err != nil {
			return err
		}

		select {
		case <-progress:
		case <-f.ctx.Done():
			return os.ErrClosed
		}
	}

	_, err := f.file.ReadAt(p, off)
	if f.ctx.Err() != nil {
		return os.ErrClosed
	}

	return err
}

// fetchRange gets the bytes from start to end, both included. It retries on transport errors and server errors.
func (f *Fetcher) fetchRange(start int64, end int64) ([]byte, error) {
	var err error

	for attempt := range fetchAttempts {
		if attempt > 0 {
			select {
			case <-time.After(retryDelay):
			case <-f.ctx.Done():
				return nil, os.ErrClosed
			}
		}

		var data []byte
		var retry bool

		data, retry, err = f.get(start, end)
		if err == nil || !retry {
			return data, err
		}
	}

	return nil, err
}

func (f *Fetcher) get(start int64, end int64) ([]byte, bool, error) {
	ctx, cancel := context.WithCancelCause(f.ctx)
	defer cancel(nil)

	// A stalled server would block the reads of the instance forever.
	stall := time.AfterFunc(f.stallTimeout, func() { cancel(ErrStalled) })
	defer stall.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return nil, false, err
	}

	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	f.mu.Lock()
	f.stats.Requests++
	f.mu.Unlock()

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, true, stallError(ctx, err)
	}

	defer resp.Body.Close()

	// A 200 means the server ignored the range, the full image is not downloaded.
	if resp.StatusCode != http.StatusPartialContent {
		return nil, resp.StatusCode >= 500, fmt.Errorf("range request for bytes %d-%d answered %s", start, end, resp.Status)
	}

	data := make([]byte, end-start+1)

	_, err = io.ReadFull(stallReader{reader: resp.Body, timer: stall, timeout: f.stallTimeout}, data)
	if err != nil {
		return nil, true, stallError(ctx, err)
	}

	f.mu.Lock()
	f.stats.BytesFetched += int64(len(data))
	f.mu.Unlock()

	return data, false, nil
}

func stallError(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), ErrStalled) {
		return ErrStalled
	}

	return err
}

// stallReader restarts the stall timer, whenever data arrives.
type stallReader struct {
	reader  io.Reader
	timer   *time.Timer
	timeout time.Duration
}

func (r stallReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.timer.Reset(r.timeout)
	}

	return n, err
}

// probe returns the size of the image from a HEAD request, or from a range request if HEAD fails or tells too little.
func (f *Fetcher) probe(ctx context.Context) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, f.url, nil)
	if err != nil {
		return 0, err
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return 0, err
	}

	resp.Body.Close()

	if resp.StatusCode == http.StatusOK && resp.ContentLength >= 0 && resp.Header.Get("Accept-Ranges") == "bytes" {
		return resp.ContentLength, nil
	}

	// A pre-signed URL refuses HEAD, as its signature covers the method. The range request tells the real status.
	return f.probeRange(ctx)
}

func (f *Fetcher) probeRange(ctx context.Context) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("Range", "bytes=0-0")

	resp, err := f.client.Do(req)
	if err != nil {
		return 0, err
	}

	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:

	case http.StatusOK:
		return 0, ErrRangeNotSupported

	default:
		return 0, fmt.Errorf("probe %q: %s", f.url, resp.Status)
	}

	_, total, found := strings.Cut(resp.Header.Get("Content-Range"), "/")

	size, err := strconv.ParseInt(total, 10, 64)
	if !found || err != nil {
		return 0, fmt.Errorf("probe %q: no size in Content-Range %q", f.url, resp.Header.Get("Content-Range"))
	}

	return size, nil
}
