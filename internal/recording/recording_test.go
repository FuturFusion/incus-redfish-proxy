package recording_test

import (
	"archive/zip"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

func TestOpen(t *testing.T) {
	t.Parallel()

	manifest := readFile(t, "testdata/generic/manifest.json")
	exchanges := readFile(t, "testdata/generic/exchanges.jsonl")
	genericSeqs := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}

	tests := []struct {
		name  string
		setup func(t *testing.T) string

		assertErr require.ErrorAssertionFunc
		wantID    string
		wantSeqs  []int64
	}{
		{
			name:      "directory with exchanges.jsonl",
			setup:     func(*testing.T) string { return "testdata/generic" },
			assertErr: require.NoError,
			wantID:    "20261027T081500Z",
			wantSeqs:  genericSeqs,
		},
		{
			name: "archived directory with recording.jsonl.gz",
			setup: func(t *testing.T) string {
				t.Helper()

				dir := t.TempDir()
				writeFile(t, filepath.Join(dir, "manifest.json"), manifest)
				writeFile(t, filepath.Join(dir, "recording.jsonl.gz"), gzipped(t, exchanges))

				return dir
			},
			assertErr: require.NoError,
			wantID:    "20261027T081500Z",
			wantSeqs:  genericSeqs,
		},
		{
			name: "download zip",
			setup: func(t *testing.T) string {
				t.Helper()

				path := filepath.Join(t.TempDir(), "recording.zip")
				writeFile(t, path, zipped(t, map[string][]byte{"manifest.json": manifest, "exchanges.jsonl": exchanges}))

				return path
			},
			assertErr: require.NoError,
			wantID:    "20261027T081500Z",
			wantSeqs:  genericSeqs,
		},
		{
			name: "recording.jsonl.gz with the manifest next to it",
			setup: func(t *testing.T) string {
				t.Helper()

				dir := t.TempDir()
				writeFile(t, filepath.Join(dir, "manifest.json"), manifest)
				writeFile(t, filepath.Join(dir, "recording.jsonl.gz"), gzipped(t, exchanges))

				return filepath.Join(dir, "recording.jsonl.gz")
			},
			assertErr: require.NoError,
			wantID:    "20261027T081500Z",
			wantSeqs:  genericSeqs,
		},
		{
			name:      "exchanges.jsonl without manifest",
			setup:     func(*testing.T) string { return "testdata/patch-media.jsonl" },
			assertErr: require.NoError,
			wantSeqs:  []int64{1, 2, 3},
		},
		{
			name: "line over 1 MiB, sorted by sequence",
			setup: func(t *testing.T) string {
				t.Helper()

				body := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 3<<20/2)))
				lines := jsonLines(t,
					recording.Exchange{Sequence: 2, ResponseBody: body, ResponseBodyEncoding: "base64"},
					recording.Exchange{Sequence: 1, RequestBody: body, RequestBodyEncoding: "base64"},
				)

				path := filepath.Join(t.TempDir(), "exchanges.jsonl")
				writeFile(t, path, lines)

				return path
			},
			assertErr: require.NoError,
			wantSeqs:  []int64{1, 2},
		},
		{
			name: "wrong format version",
			setup: func(t *testing.T) string {
				t.Helper()

				dir := t.TempDir()
				writeFile(t, filepath.Join(dir, "manifest.json"), []byte(`{"format_version": 2}`))
				writeFile(t, filepath.Join(dir, "exchanges.jsonl"), exchanges)

				return dir
			},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "format version 2 is not supported, only version 1 is", a...)
			},
		},
		{
			name:  "missing recording",
			setup: func(t *testing.T) string { t.Helper(); return filepath.Join(t.TempDir(), "missing") },
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorIs(tt, err, os.ErrNotExist, a...)
			},
		},
		{
			name: "invalid exchange",
			setup: func(t *testing.T) string {
				t.Helper()

				path := filepath.Join(t.TempDir(), "exchanges.jsonl")
				writeFile(t, path, []byte("{\"sequence\": 1}\n{\"sequence\": \"two\"}\n"))

				return path
			},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "exchange 2", a...)
			},
		},
		{
			name: "zip without exchanges",
			setup: func(t *testing.T) string {
				t.Helper()

				path := filepath.Join(t.TempDir(), "recording.zip")
				writeFile(t, path, zipped(t, map[string][]byte{"manifest.json": manifest}))

				return path
			},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "exchanges.jsonl", a...)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			path := tc.setup(t)

			// Run test
			rec, err := recording.Open(path)

			// Assert
			tc.assertErr(t, err, "open the recording")
			require.Equal(t, tc.wantID, recordingID(rec), "recording ID")
			require.Equal(t, tc.wantSeqs, sequences(rec), "sequences of the exchanges")
		})
	}
}

func TestExchange_JSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		exchange recording.Exchange

		assertResponseErr require.ErrorAssertionFunc
		wantResponse      map[string]any
		assertRequestErr  require.ErrorAssertionFunc
		wantRequest       map[string]any
	}{
		{
			name:              "text bodies",
			exchange:          recording.Exchange{RequestBody: `{"Image": "a"}`, ResponseBody: `{"Id": "1"}`},
			assertResponseErr: require.NoError,
			wantResponse:      map[string]any{"Id": "1"},
			assertRequestErr:  require.NoError,
			wantRequest:       map[string]any{"Image": "a"},
		},
		{
			name: "base64 bodies",
			exchange: recording.Exchange{
				RequestBody:          base64.StdEncoding.EncodeToString([]byte(`{"Image": "a"}`)),
				RequestBodyEncoding:  "base64",
				ResponseBody:         base64.StdEncoding.EncodeToString([]byte(`{"Id": "1"}`)),
				ResponseBodyEncoding: "base64",
			},
			assertResponseErr: require.NoError,
			wantResponse:      map[string]any{"Id": "1"},
			assertRequestErr:  require.NoError,
			wantRequest:       map[string]any{"Image": "a"},
		},
		{
			name: "truncated and invalid base64 bodies",
			exchange: recording.Exchange{
				RequestBody:         "not base64!",
				RequestBodyEncoding: "base64",
				ResponseBody:        `{"Id": `,
				ResponseTruncated:   true,
			},
			assertResponseErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "body is truncated", a...)
			},
			assertRequestErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "decode base64 body", a...)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Run test
			var response, request map[string]any

			responseErr := tc.exchange.ResponseJSON(&response)
			requestErr := tc.exchange.RequestJSON(&request)

			// Assert
			tc.assertResponseErr(t, responseErr, "decode the response body")
			require.Equal(t, tc.wantResponse, response, "response body")
			tc.assertRequestErr(t, requestErr, "decode the request body")
			require.Equal(t, tc.wantRequest, request, "request body")
		})
	}
}

func recordingID(rec *recording.Recording) string {
	if rec == nil {
		return ""
	}

	return rec.Manifest.RecordingID
}

func sequences(rec *recording.Recording) []int64 {
	if rec == nil {
		return nil
	}

	seqs := make([]int64, 0, len(rec.Exchanges))
	for _, e := range rec.Exchanges {
		seqs = append(seqs, e.Sequence)
	}

	return seqs
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)

	return data
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()

	err := os.WriteFile(path, data, 0o600)
	require.NoError(t, err, "write %s", path)
}

func jsonLines(t *testing.T, exchanges ...recording.Exchange) []byte {
	t.Helper()

	var b strings.Builder

	for _, e := range exchanges {
		data, err := json.Marshal(e)
		require.NoError(t, err, "marshal exchange %d", e.Sequence)

		b.Write(data)
		b.WriteString("\n")
	}

	return []byte(b.String())
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()

	var b strings.Builder

	w := gzip.NewWriter(&b)
	_, err := w.Write(data)
	require.NoError(t, err, "compress the data")
	require.NoError(t, w.Close(), "close the gzip writer")

	return []byte(b.String())
}

func zipped(t *testing.T, files map[string][]byte) []byte {
	t.Helper()

	var b strings.Builder

	w := zip.NewWriter(&b)

	for name, data := range files {
		f, err := w.Create(name)
		require.NoError(t, err, "create %s in the zip archive", name)

		_, err = f.Write(data)
		require.NoError(t, err, "write %s to the zip archive", name)
	}

	require.NoError(t, w.Close(), "close the zip writer")

	return []byte(b.String())
}
