package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergePatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target any
		patch  any

		want any
	}{
		{name: "add", target: map[string]any{"a": "b"}, patch: map[string]any{"c": "d"}, want: map[string]any{"a": "b", "c": "d"}},
		{name: "replace", target: map[string]any{"a": "b"}, patch: map[string]any{"a": "c"}, want: map[string]any{"a": "c"}},
		{name: "nested merge", target: map[string]any{"a": map[string]any{"b": "c", "d": "e"}}, patch: map[string]any{"a": map[string]any{"b": "f"}}, want: map[string]any{"a": map[string]any{"b": "f", "d": "e"}}},
		{name: "null deletes", target: map[string]any{"a": "b", "c": "d"}, patch: map[string]any{"a": nil}, want: map[string]any{"c": "d"}},
		{name: "null in a new object is dropped", target: map[string]any{}, patch: map[string]any{"a": map[string]any{"b": nil, "c": "d"}}, want: map[string]any{"a": map[string]any{"c": "d"}}},
		{name: "array replaces", target: map[string]any{"a": []any{"b", "c"}}, patch: map[string]any{"a": []any{"d"}}, want: map[string]any{"a": []any{"d"}}},
		{name: "object replaces a value", target: map[string]any{"a": "b"}, patch: map[string]any{"a": map[string]any{"c": "d"}}, want: map[string]any{"a": map[string]any{"c": "d"}}},
		{name: "non object patch replaces", target: map[string]any{"a": "b"}, patch: []any{"c"}, want: []any{"c"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, mergePatch(tc.target, tc.patch), "the result of the JSON merge patch")
		})
	}
}

func TestApplyOverlay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string

		want string
	}{
		{name: "success - object", body: `{"Count": 9007199254740993}`, want: "{\n  \"Count\": 9007199254740993,\n  \"Oem\": \"x\"\n}\n"},
		{name: "success - not an object", body: `[1]`, want: `[1]`},
		{name: "success - not JSON", body: `text`, want: `text`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := applyOverlay([]byte(tc.body), map[string]any{"Oem": "x"})

			require.Equal(t, tc.want, string(got), "large integers keep their precision")
		})
	}
}
