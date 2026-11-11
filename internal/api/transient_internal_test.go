package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"
	"github.com/stretchr/testify/require"
)

// failingClient fails the reads with the errors in turn, then succeeds.
type failingClient struct {
	IncusClient

	errs  []error
	calls *int
}

func (c failingClient) next() error {
	*c.calls++
	if *c.calls > len(c.errs) {
		return nil
	}

	return c.errs[*c.calls-1]
}

func (c failingClient) GetInstanceState(string) (*incusapi.InstanceState, string, error) {
	err := c.next()
	if err != nil {
		return nil, "", err
	}

	return &incusapi.InstanceState{Status: "Running"}, "etag", nil
}

func (c failingClient) GetInstanceNVRAMGUID(string, string) (map[string]*incusapi.InstanceNVRAMVariable, error) {
	err := c.next()
	if err != nil {
		return nil, err
	}

	return map[string]*incusapi.InstanceNVRAMVariable{}, nil
}

func (c failingClient) GetInstanceNVRAMGUIDVar(string, string, string) (*incusapi.InstanceNVRAMVariable, string, error) {
	err := c.next()
	if err != nil {
		return nil, "", err
	}

	return &incusapi.InstanceNVRAMVariable{}, "etag", nil
}

func TestTransientRetryClient(t *testing.T) {
	t.Parallel()

	pidErr := errors.New("Invalid PID -1")
	nvramErr := errors.New("Unexpected data")

	tests := []struct {
		name string
		errs []error
		read func(c transientRetryClient) error

		assertErr  require.ErrorAssertionFunc
		wantCalls  int
		wantSleeps []time.Duration
	}{
		{
			name: "success - state after a restart error",
			errs: []error{pidErr},
			read: func(c transientRetryClient) error {
				_, _, err := c.GetInstanceState("test-instance")
				return err
			},

			assertErr:  require.NoError,
			wantCalls:  2,
			wantSleeps: []time.Duration{transientRetryDelay},
		},
		{
			name: "success - NVRAM after two partly written stores",
			errs: []error{nvramErr, nvramErr},
			read: func(c transientRetryClient) error {
				_, err := c.GetInstanceNVRAMGUID("test-instance", "guid")
				return err
			},

			assertErr:  require.NoError,
			wantCalls:  3,
			wantSleeps: []time.Duration{transientRetryDelay, transientRetryDelay},
		},
		{
			name: "error - NVRAM variable stays busy",
			errs: []error{nvramErr, nvramErr, nvramErr},
			read: func(c transientRetryClient) error {
				_, _, err := c.GetInstanceNVRAMGUIDVar("test-instance", "guid", "db")
				return err
			},

			assertErr: func(tt require.TestingT, err error, a ...any) {
				var statusErr *statusError
				require.ErrorAs(tt, err, &statusErr, a...)
				require.Equal(tt, http.StatusServiceUnavailable, statusErr.status, a...)
				require.ErrorIs(tt, err, nvramErr, a...)
			},
			wantCalls:  3,
			wantSleeps: []time.Duration{transientRetryDelay, transientRetryDelay},
		},
		{
			name: "error - other errors are not retried",
			errs: []error{errors.New("not found")},
			read: func(c transientRetryClient) error {
				_, _, err := c.GetInstanceState("test-instance")
				return err
			},

			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.EqualError(tt, err, "not found", a...)
			},
			wantCalls: 1,
		},
		{
			name: "error - NVRAM errors are not retried for the state",
			errs: []error{nvramErr},
			read: func(c transientRetryClient) error {
				_, _, err := c.GetInstanceState("test-instance")
				return err
			},

			assertErr: func(tt require.TestingT, err error, a ...any) {
				require.ErrorIs(tt, err, nvramErr, a...)
			},
			wantCalls: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			calls := 0

			var sleeps []time.Duration

			client := transientRetryClient{
				IncusClient: failingClient{errs: tc.errs, calls: &calls},
				sleep:       func(d time.Duration) { sleeps = append(sleeps, d) },
			}

			// Run test
			err := tc.read(client)

			// Assert
			tc.assertErr(t, err, "the error of the read")
			require.Equal(t, tc.wantCalls, calls, "number of reads")
			require.Equal(t, tc.wantSleeps, sleeps, "waits between the reads")
		})
	}
}

func TestRespondStatusError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error

		wantStatus     int
		wantRetryAfter string
		wantBody       string
	}{
		{
			name: "busy instance",
			err:  transientRetryClient{sleep: func(time.Duration) {}}.retry(restartErrors, func() error { return errors.New("Invalid PID -1") }),

			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "5",
			wantBody:       `"MessageId": "Base.1.18.ServiceTemporarilyUnavailable"`,
		},
		{
			name: "any other error",
			err:  errors.New("boom"),

			wantStatus: http.StatusInternalServerError,
			wantBody:   `"MessageId": "Base.1.18.InternalError"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			rec := httptest.NewRecorder()

			// Run test
			respondStatusError(rec, tc.err)

			// Assert
			require.Equal(t, tc.wantStatus, rec.Code, "status")
			require.Equal(t, tc.wantRetryAfter, rec.Header().Get("Retry-After"), "Retry-After")
			require.Contains(t, rec.Body.String(), tc.wantBody, "message ID")
		})
	}
}

func TestRespondNVRAMError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error

		wantStatus     int
		wantRetryAfter string
		wantBody       string
	}{
		{
			name: "busy NVRAM store",
			err:  transientRetryClient{sleep: func(time.Duration) {}}.retry(nvramErrors, func() error { return errors.New("Unexpected data") }),

			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "5",
			wantBody:       `"MessageId": "Base.1.18.ServiceTemporarilyUnavailable"`,
		},
		{
			name: "Incus status passed on",
			err:  incusapi.StatusErrorf(http.StatusNotFound, "not found"),

			wantStatus: http.StatusNotFound,
			wantBody:   `"message": "not found"`,
		},
		{
			name: "any other error",
			err:  errors.New("boom"),

			wantStatus: http.StatusInternalServerError,
			wantBody:   `"MessageId": "Base.1.18.InternalError"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Setup
			rec := httptest.NewRecorder()

			// Run test
			respondNVRAMError(rec, tc.err)

			// Assert
			require.Equal(t, tc.wantStatus, rec.Code, "status")
			require.Equal(t, tc.wantRetryAfter, rec.Header().Get("Retry-After"), "Retry-After")
			require.Contains(t, rec.Body.String(), tc.wantBody, "body")
		})
	}
}
