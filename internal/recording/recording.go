// Package recording reads the baseboard management controller (BMC) recordings of Operations Center and drafts a profile from them.
package recording

import (
	"archive/zip"
	"cmp"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	manifestFile         = "manifest.json"
	exchangesFile        = "exchanges.jsonl"
	archivedExchangeFile = "recording.jsonl.gz"
)

// Recording is a manifest with its exchanges.
type Recording struct {
	Manifest  Manifest
	Exchanges []Exchange
}

// Open reads a recording from an Operations Center download zip, a directory, an exchanges.jsonl or a recording.jsonl.gz.
func Open(path string) (*Recording, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open recording: %w", err)
	}

	var rec *Recording

	switch {
	case info.IsDir():
		rec, err = openDir(path)

	case strings.HasSuffix(path, ".zip"):
		rec, err = openZip(path)

	default:
		rec, err = openExchanges(path)
	}

	if err != nil {
		return nil, fmt.Errorf("recording %q: %w", path, err)
	}

	slices.SortStableFunc(rec.Exchanges, func(a, b Exchange) int { return cmp.Compare(a.Sequence, b.Sequence) })

	return rec, nil
}

func openDir(dir string) (*Recording, error) {
	fsys := os.DirFS(dir)

	manifest, err := readManifest(fsys)
	if err != nil {
		return nil, err
	}

	name := exchangesFile

	_, err = fs.Stat(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		name = archivedExchangeFile
	}

	exchanges, err := readExchangesFile(fsys, name)
	if err != nil {
		return nil, err
	}

	return &Recording{Manifest: manifest, Exchanges: exchanges}, nil
}

func openZip(path string) (*Recording, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}

	defer func() { _ = archive.Close() }()

	manifest, err := readManifest(archive)
	if err != nil {
		return nil, err
	}

	exchanges, err := readExchangesFile(archive, exchangesFile)
	if err != nil {
		return nil, err
	}

	return &Recording{Manifest: manifest, Exchanges: exchanges}, nil
}

// openExchanges reads a single exchanges file. The manifest next to it is optional.
func openExchanges(path string) (*Recording, error) {
	dir := os.DirFS(filepath.Dir(path))

	manifest, err := readManifest(dir)
	if errors.Is(err, fs.ErrNotExist) {
		manifest = Manifest{FormatVersion: FormatVersion}
		err = nil
	}

	if err != nil {
		return nil, err
	}

	exchanges, err := readExchangesFile(dir, filepath.Base(path))
	if err != nil {
		return nil, err
	}

	return &Recording{Manifest: manifest, Exchanges: exchanges}, nil
}

func readManifest(fsys fs.FS) (Manifest, error) {
	data, err := fs.ReadFile(fsys, manifestFile)
	if err != nil {
		return Manifest{}, err
	}

	var manifest Manifest

	err = json.Unmarshal(data, &manifest)
	if err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", manifestFile, err)
	}

	if manifest.FormatVersion != FormatVersion {
		return Manifest{}, fmt.Errorf("format version %d is not supported, only version %d is", manifest.FormatVersion, FormatVersion)
	}

	return manifest, nil
}

func readExchangesFile(fsys fs.FS, name string) ([]Exchange, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	var r io.Reader = f

	if strings.HasSuffix(name, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}

		defer func() { _ = gz.Close() }()

		r = gz
	}

	exchanges, err := readExchanges(r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	return exchanges, nil
}

// readExchanges decodes one exchange per line. A decoder has no line length limit, two base64 encoded bodies of 1 MiB exceed the usual scanner buffer.
func readExchanges(r io.Reader) ([]Exchange, error) {
	var exchanges []Exchange

	dec := json.NewDecoder(r)

	for {
		var exchange Exchange

		err := dec.Decode(&exchange)
		if errors.Is(err, io.EOF) {
			return exchanges, nil
		}

		if err != nil {
			return nil, fmt.Errorf("exchange %d: %w", len(exchanges)+1, err)
		}

		exchanges = append(exchanges, exchange)
	}
}

// Path returns the path of the request URL in lower case, without a trailing slash. BMCs differ in the case of their paths.
func (e Exchange) Path() string {
	u, err := url.Parse(e.URL)
	if err != nil {
		return ""
	}

	return strings.TrimSuffix(strings.ToLower(u.Path), "/")
}

// Succeeded reports a 2xx answer.
func (e Exchange) Succeeded() bool {
	return e.Status >= 200 && e.Status < 300
}

// ResponseHeader returns the first value of a response header.
func (e Exchange) ResponseHeader(name string) string {
	return http.Header(e.ResponseHeaders).Get(name)
}

// ResponseJSON decodes the response body into v.
func (e Exchange) ResponseJSON(v any) error {
	return decodeBody(e.ResponseBody, e.ResponseBodyEncoding, e.ResponseTruncated, v)
}

// RequestJSON decodes the request body into v.
func (e Exchange) RequestJSON(v any) error {
	return decodeBody(e.RequestBody, e.RequestBodyEncoding, e.RequestTruncated, v)
}

func decodeBody(body string, encoding string, truncated bool, v any) error {
	if truncated {
		return errors.New("body is truncated")
	}

	data := []byte(body)

	if encoding == bodyEncodingBase64 {
		var err error

		data, err = base64.StdEncoding.DecodeString(body)
		if err != nil {
			return fmt.Errorf("decode base64 body: %w", err)
		}
	}

	return json.Unmarshal(data, v)
}
