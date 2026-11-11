package profiles_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v4"

	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

func TestLayout(t *testing.T) {
	t.Parallel()

	for _, dir := range profileDirs(t) {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()

			require.FileExists(t, filepath.Join(dir, "profile.yaml"), "every profile has a profile.yaml")
			require.FileExists(t, filepath.Join(dir, "README.md"), "every profile has a README.md")
		})
	}
}

func profileDirs(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	require.NoError(t, err, "list the profiles directory")

	var dirs []string

	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}

	return dirs
}

func TestArchivedRecordings(t *testing.T) {
	t.Parallel()

	archives, err := filepath.Glob("*/recording.jsonl.gz")
	require.NoError(t, err, "find the archived recordings")

	for _, archive := range archives {
		t.Run(filepath.Dir(archive), func(t *testing.T) {
			t.Parallel()

			_, err := recording.Open(filepath.Dir(archive))
			require.NoError(t, err, "the recording and its manifest of format version 1 are readable")
		})
	}
}

func TestReferenceNamesEveryKey(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("generic/profile.yaml")
	require.NoError(t, err, "read the generic profile")

	var doc yaml.Node

	err = yaml.Unmarshal(data, &doc)
	require.NoError(t, err, "parse the generic profile")

	reference, err := os.ReadFile("README.md")
	require.NoError(t, err, "read the reference")

	keys := leafKeys(doc.Content[0], "")
	require.Contains(t, keys, "bios.apply_time.supported", "nested keys are found")

	missing := undescribedKeys(keys, string(reference))

	require.Empty(t, missing, "keys of the generic profile that profiles/README.md does not describe")
}

func undescribedKeys(keys []string, reference string) []string {
	var missing []string

	for _, key := range keys {
		if !strings.Contains(reference, "`"+key+"`") {
			missing = append(missing, key)
		}
	}

	return missing
}

// leafKeys returns the dotted keys of the values that are not block mappings. A flow mapping like an error shape is one value.
func leafKeys(node *yaml.Node, prefix string) []string {
	var keys []string

	for i := 0; i+1 < len(node.Content); i += 2 {
		key := strings.TrimPrefix(prefix+"."+node.Content[i].Value, ".")
		value := node.Content[i+1]

		if value.Kind == yaml.MappingNode && value.Style&yaml.FlowStyle == 0 {
			keys = append(keys, leafKeys(value, key)...)

			continue
		}

		keys = append(keys, key)
	}

	return keys
}
