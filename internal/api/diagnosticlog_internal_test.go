package api

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

func TestTail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content io.Reader

		assertErr require.ErrorAssertionFunc
		wantEnd   string
		wantSize  int64
	}{
		{
			name:    "short content",
			content: strings.NewReader("abc"),

			assertErr: require.NoError,
			wantEnd:   "abc",
			wantSize:  3,
		},
		{
			name:    "content of exactly n bytes",
			content: strings.NewReader("abcd"),

			assertErr: require.NoError,
			wantEnd:   "abcd",
			wantSize:  4,
		},
		{
			name:    "content longer than the read buffer",
			content: strings.NewReader(strings.Repeat("a", 100*1024) + "xyz"),

			assertErr: require.NoError,
			wantEnd:   "axyz",
			wantSize:  100*1024 + 3,
		},
		{
			name:    "small reads",
			content: iotest.OneByteReader(strings.NewReader("abcdef")),

			assertErr: require.NoError,
			wantEnd:   "cdef",
			wantSize:  6,
		},
		{
			name:    "read failure",
			content: iotest.ErrReader(errors.New("boom")),

			assertErr: require.Error,
			wantEnd:   "",
			wantSize:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			end, size, err := tail(tc.content, 4)

			tc.assertErr(t, err, "only a failed read is an error")
			require.Equal(t, tc.wantEnd, string(end), "the end of the content is kept")
			require.Equal(t, tc.wantSize, size, "the full size is counted")
		})
	}
}

func TestDiagnosticMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		end  string
		size int64

		wantMessage string
	}{
		{
			name: "complete log",
			end:  "line 1\nline 2\n",
			size: 14,

			wantMessage: "line 1\nline 2\n",
		},
		{
			name: "cut at the start of a line",
			end:  "ne 1\nline 2\n",
			size: 100,

			wantMessage: "[truncated, full log: /attachment]\nline 2\n",
		},
		{
			name: "one long line",
			end:  "xxxx",
			size: 100,

			wantMessage: "[truncated, full log: /attachment]\nxxxx",
		},
		{
			name: "invalid UTF-8",
			end:  "a\xffb",
			size: 3,

			wantMessage: "ab",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			message := diagnosticMessage([]byte(tc.end), tc.size, "/attachment")

			require.Equal(t, tc.wantMessage, message, "the message is the end of the log")
		})
	}
}
