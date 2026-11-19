package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWithPOSTDuration(t *testing.T) {
	t.Parallel()

	const section = "[boot-opts]\nmenu = \"on\"\nsplash-time = \"60000\""

	tests := []struct {
		name   string
		conf   string
		splash time.Duration

		want string
	}{
		{name: "add to empty", conf: "", splash: time.Minute, want: section},
		{name: "keep empty", conf: "", splash: 0, want: ""},
		{name: "keep unrelated config untouched", conf: "[machine]\ngraphics = \"off\"\n", splash: 0, want: "[machine]\ngraphics = \"off\"\n"},
		{name: "append to other sections", conf: "[machine]\ngraphics = \"off\"\n", splash: time.Minute, want: "[machine]\ngraphics = \"off\"\n\n" + section},
		{name: "idempotent", conf: "[machine]\ngraphics = \"off\"\n\n" + section, splash: time.Minute, want: "[machine]\ngraphics = \"off\"\n\n" + section},
		{name: "replace the splash time", conf: "[boot-opts]\nmenu = \"on\"\nsplash-time = \"1000\"", splash: time.Minute, want: section},
		{name: "remove", conf: section, splash: 0, want: ""},
		{name: "remove and keep other sections", conf: "[machine]\ngraphics = \"off\"\n\n" + section + "\n", splash: 0, want: "[machine]\ngraphics = \"off\""},
		{name: "remove from a section with other keys", conf: "[boot-opts]\nstrict = \"off\"\nsplash-time=500\n", splash: 0, want: "[boot-opts]\nstrict = \"off\""},
		{name: "keep an empty section of the user", conf: "[boot-opts]\n", splash: time.Minute, want: "[boot-opts]\n\n" + section},
		{name: "explicit index 0", conf: "[boot-opts][0]\nmenu = \"off\"", splash: 0, want: ""},
		{name: "ignore other indexes", conf: "[boot-opts][1]\nmenu = \"off\"", splash: 0, want: "[boot-opts][1]\nmenu = \"off\""},
		{name: "ignore keys of other sections", conf: "[device \"dev-qemu_rng\"]\nsplash-time = \"1\"", splash: 0, want: "[device \"dev-qemu_rng\"]\nsplash-time = \"1\""},
		{name: "remove below a millisecond", conf: section, splash: 400 * time.Microsecond, want: ""},
		{name: "round to milliseconds", conf: "", splash: 1500*time.Microsecond + 10*time.Second, want: "[boot-opts]\nmenu = \"on\"\nsplash-time = \"10002\""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Run test
			got := withPOSTDuration(tc.conf, tc.splash)

			// Assert
			require.Equal(t, tc.want, got, "the raw.qemu.conf with the POST duration")
		})
	}
}
