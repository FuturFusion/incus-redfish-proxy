package main

import (
	"bufio"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FuturFusion/incus-redfish-proxy/internal/util/testing/boom"
)

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.events = append(l.events, event)
}

func (l *eventLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string(nil), l.events...)
}

type closer struct {
	log *eventLog
	err error
}

func (c closer) Close() error {
	c.log.add("close")
	return c.err
}

func waitFor(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		require.FailNow(t, msg)
	}
}

func sendRequest(t *testing.T, ln net.Listener) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err, "connect to the server")
	t.Cleanup(func() { _ = conn.Close() })

	_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n")
	require.NoError(t, err, "send the request")

	return conn
}

func TestServe(t *testing.T) {
	t.Parallel()

	// Setup
	events := &eventLog{}
	started := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		events.add("request done")
	})}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen")

	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)

	go func() { served <- serve(ctx, srv, ln, closer{log: events}, time.Minute, log.New(io.Discard, "", 0)) }()

	conn := sendRequest(t, ln)
	waitFor(t, started, "the request reaches the handler")

	// Run test
	cancel()

	// Assert
	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err == nil {
			_ = conn.Close()
		}

		return err != nil
	}, time.Second, time.Millisecond, "serve stops accepting connections")
	require.Empty(t, events.all(), "serve waits for the open request before it closes the handler")

	releaseOnce()

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err, "read the response")
	require.NoError(t, resp.Body.Close(), "close the response body")
	require.Equal(t, http.StatusOK, resp.StatusCode, "the open request finishes")
	require.NoError(t, <-served, "serve ends without an error")
	require.Equal(t, []string{"request done", "close"}, events.all(), "serve closes the handler after the open request")
}

func TestServe_errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		closeErr error
		// stop runs while serve runs. It makes serve return.
		stop func(t *testing.T, ln net.Listener, started <-chan struct{}, cancel context.CancelFunc)

		assertErr require.ErrorAssertionFunc
	}{
		{
			name:     "close fails",
			closeErr: boom.Error,
			stop: func(t *testing.T, ln net.Listener, started <-chan struct{}, cancel context.CancelFunc) {
				t.Helper()

				cancel()
			},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorIs(tt, err, boom.Error, a...)
				require.ErrorContains(tt, err, "close the handler", a...)
			},
		},
		{
			name: "open request outlives the timeout",
			stop: func(t *testing.T, ln net.Listener, started <-chan struct{}, cancel context.CancelFunc) {
				t.Helper()

				sendRequest(t, ln)
				waitFor(t, started, "the request reaches the handler")
				cancel()
			},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorIs(tt, err, context.DeadlineExceeded, a...)
				require.ErrorContains(tt, err, "shut down the HTTP server", a...)
			},
		},
		{
			name: "serve fails",
			stop: func(t *testing.T, ln net.Listener, started <-chan struct{}, cancel context.CancelFunc) {
				t.Helper()

				require.NoError(t, ln.Close(), "close the listener")
			},
			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorContains(tt, err, "serve: ", a...)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			events := &eventLog{}
			started := make(chan struct{})
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })

			srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-release
			})}

			ln, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err, "listen")

			ctx, cancel := context.WithCancel(t.Context())
			served := make(chan error, 1)

			go func() {
				served <- serve(ctx, srv, ln, closer{log: events, err: tc.closeErr}, 50*time.Millisecond, log.New(io.Discard, "", 0))
			}()

			// Run test
			tc.stop(t, ln, started, cancel)
			err = <-served

			// Assert
			tc.assertErr(t, err, "the error of serve")
			require.Equal(t, []string{"close"}, events.all(), "serve always closes the handler")
		})
	}
}
