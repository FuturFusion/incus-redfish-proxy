package boom

import (
	"errors"

	"github.com/stretchr/testify/require"
)

var Error = errors.New("boom!")

var _ require.ErrorAssertionFunc = ErrorIs

func ErrorIs(tt require.TestingT, err error, a ...any) {
	tHelper, ok := tt.(interface{ Helper() })
	if ok {
		tHelper.Helper()
	}

	require.ErrorIs(tt, err, Error, a...)
}

func ErrorContains(tt require.TestingT, err error, a ...any) {
	tHelper, ok := tt.(interface{ Helper() })
	if ok {
		tHelper.Helper()
	}

	require.ErrorContains(tt, err, Error.Error(), a...)
}
