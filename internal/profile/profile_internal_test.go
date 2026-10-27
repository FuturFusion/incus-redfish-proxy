package profile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenericEqualsDefaults(t *testing.T) {
	t.Parallel()

	generic, err := Load(DefaultName)
	require.NoError(t, err, "load the generic profile")

	want := defaults()
	want.Version = generic.Version
	want.Name = generic.Name
	want.Description = generic.Description

	require.Equal(t, want, generic)
}
