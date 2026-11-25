package profiles_test

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// leakCheck finds candidates for leaked data. A candidate is fine, if it fully
// matches allowed.
type leakCheck struct {
	name    string
	find    *regexp.Regexp
	allowed *regexp.Regexp
}

var never = regexp.MustCompile(`^$`)

var leakChecks = []leakCheck{
	{
		name:    "mac address",
		find:    regexp.MustCompile(`(?i)\b[0-9a-f]{2}(?:[:-][0-9a-f]{2}){5}\b`),
		allowed: regexp.MustCompile(`^02[:-]00[:-]00[:-]00[:-]00[:-][0-9A-Fa-f]{2}$`),
	},
	{
		name:    "mac address in a uefi device path",
		find:    regexp.MustCompile(`MAC\([0-9A-Fa-f]+`),
		allowed: regexp.MustCompile(`^MAC\(0200000000[0-9A-Fa-f]{2}$`),
	},
	{
		name:    "uuid",
		find:    regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`),
		allowed: regexp.MustCompile(`^00000000-0000-4000-8000-[0-9a-f]{12}$`),
	},
	{
		name:    "license key",
		find:    regexp.MustCompile(`\b[0-9A-Z]{5}(?:-[0-9A-Z]{5}){4}\b`),
		allowed: regexp.MustCompile(`^XXXXX(?:-XXXXX){4}$`),
	},
	{
		name:    "time zone offset",
		find:    regexp.MustCompile(`\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?[+-]\d{2}:\d{2}\b|^[+-]\d{2}:\d{2}$`),
		allowed: regexp.MustCompile(`^(?:\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?)?\+00:00$`),
	},
	{
		name:    "time zone name",
		find:    regexp.MustCompile(`\b[A-Z]{2,4}T-?\d{1,2}[A-Z]{2,4}T\b|\b(?:Africa|America|Antarctica|Asia|Atlantic|Australia|Europe|Indian|Pacific)/[A-Za-z_]+`),
		allowed: never,
	},
	{
		name:    "media url id",
		find:    regexp.MustCompile(`[A-Za-z0-9_-]{12}\.(?:iso|raw|img)\b|deployment/[A-Za-z0-9_-]+`),
		allowed: regexp.MustCompile(`^(?:(?:SEEDIMAGE\d{3}|SBMEDIA\d{5})\.(?:iso|raw|img)|deployment/DEPLOYMENT\d{2})$`),
	},
	{
		name:    "private ipv4 address",
		find:    regexp.MustCompile(`\b(?:10\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])|192\.168|169\.254)\.\d{1,3}\.\d{1,3}\b`),
		allowed: never,
	},
	{
		name:    "url host",
		find:    regexp.MustCompile(`https?://[^/"\s]+`),
		allowed: regexp.MustCompile(`^https?://(?:\[?[a-z0-9.-]*\.example\]?|example\.com|(?:[a-z0-9-]+\.)*(?:dmtf\.org|dell\.com|hpe\.com|lenovo\.com)|203\.0\.113\.\d+|198\.51\.100\.\d+|192\.0\.2\.\d+|\[2001:db8:[0-9a-f:]*\]|FQDN|IPv4_address|IPv6)(?::\d+|:port)?[.,]?$`),
	},
	{
		name:    "domain name",
		find:    regexp.MustCompile(`\b(?:[a-z0-9-]+\.)+(?:com|net|org|io|ch|de|at|fr|uk|eu|us|local|lan|internal|corp|home|intra)\b`),
		allowed: regexp.MustCompile(`^(?:example\.com|(?:[a-z0-9-]+\.)*(?:dmtf\.org|dell\.com|hpe\.com|lenovo\.com))$`),
	},
	{
		name:    "email address",
		find:    regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`),
		allowed: regexp.MustCompile(`^[A-Za-z0-9._%+-]+@(?:(?:odata|Redfish|Message)\.[A-Za-z.]+|example\.(?:com|org|net))$`),
	},
}

func TestLeakChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{
			name:  "placeholders",
			value: `02:00:00:00:00:01 MAC(020000000001,0x1) 00000000-0000-4000-8000-000000000001 XXXXX-XXXXX-XXXXX-XXXXX-XXXXX 2026-11-09T12:29:53+00:00 https://[operations-center.example]:8443/deployment/DEPLOYMENT01/type/iso/SEEDIMAGE001.iso https://bmc.example www.hpe.com Members@odata.count`,
			want:  nil,
		},
		{
			name:  "mac address",
			value: "aa:bb:cc:00:10:10",
			want:  []string{"mac address: aa:bb:cc:00:10:10"},
		},
		{
			name:  "mac address in a uefi device path",
			value: "PciRoot(0x0)/Pci(0x1,0x1)/Pci(0x0,0x0)/MAC(AABBCC001010,0x1)/IPv4(0.0.0.0)",
			want:  []string{"mac address in a uefi device path: MAC(AABBCC001010"},
		},
		{
			name:  "uuid",
			value: "HD(1,GPT,12345678-9abc-4def-8123-456789abcdef,0x800,0x400000)",
			want:  []string{"uuid: 12345678-9abc-4def-8123-456789abcdef"},
		},
		{
			name:  "license key",
			value: "XXXXX-XXXXX-XXXXX-XXXXX-Z9Y8W",
			want:  []string{"license key: XXXXX-XXXXX-XXXXX-XXXXX-Z9Y8W"},
		},
		{
			name:  "time zone offset in a timestamp",
			value: "2026-11-09T12:29:53-06:00",
			want:  []string{"time zone offset: 12:29:53-06:00"},
		},
		{
			name:  "time zone offset alone",
			value: "-06:00",
			want:  []string{"time zone offset: -06:00"},
		},
		{
			name:  "time zone name",
			value: "EST5EDT Europe/Paris",
			want:  []string{"time zone name: EST5EDT", "time zone name: Europe/Paris"},
		},
		{
			name:  "media url ids",
			value: "https://[operations-center.example]:8443/1.0/provisioning/tokens/00000000-0000-4000-8000-000000000002/seeds/server01/architecture/x86_64/deployment/aB3dE5fG7hJ9/type/iso/kL2mN4pQ6rS8.iso",
			want:  []string{"media url id: deployment/aB3dE5fG7hJ9", "media url id: kL2mN4pQ6rS8.iso"},
		},
		{
			name:  "private ipv4 address",
			value: "Ipv4Address 192.168.1.20",
			want:  []string{"private ipv4 address: 192.168.1.20"},
		},
		{
			name:  "url host and domain name",
			value: "https://ilo.lab.corp/redfish",
			want:  []string{"domain name: ilo.lab.corp", "url host: https://ilo.lab.corp"},
		},
		{
			name:  "email address",
			value: "admin@company.com",
			want:  []string{"domain name: company.com", "email address: admin@company.com"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := leaks(map[string]struct{}{tc.value: {}})

			require.Equal(t, tc.want, got, "leaks found in %q", tc.value)
		})
	}
}

func TestRecordingsAreAnonymized(t *testing.T) {
	t.Parallel()

	archives, err := filepath.Glob("*/recording.jsonl.gz")
	require.NoError(t, err, "find the archived recordings")
	require.NotEmpty(t, archives, "there are archived recordings to check")

	for _, archive := range archives {
		dir := filepath.Dir(archive)

		t.Run(dir, func(t *testing.T) {
			t.Parallel()

			values := map[string]struct{}{}
			collectFileStrings(t, filepath.Join(dir, "manifest.json"), values)
			collectArchiveStrings(t, archive, values)

			got := leaks(values)

			require.Empty(t, got, "values of the recording in %s that are not anonymized", dir)
		})
	}
}

// leaks returns "<check>: <match>" for every candidate that is not allowed, sorted.
func leaks(values map[string]struct{}) []string {
	found := map[string]struct{}{}

	for value := range values {
		for _, check := range leakChecks {
			for _, match := range check.find.FindAllString(value, -1) {
				if !check.allowed.MatchString(match) {
					found[check.name+": "+match] = struct{}{}
				}
			}
		}
	}

	return slices.Sorted(maps.Keys(found))
}

func collectFileStrings(t *testing.T, path string, values map[string]struct{}) {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)

	var doc any

	err = json.Unmarshal(data, &doc)
	require.NoError(t, err, "parse %s", path)

	collectStrings(doc, values)
}

func collectArchiveStrings(t *testing.T, path string, values map[string]struct{}) {
	t.Helper()

	file, err := os.Open(path)
	require.NoError(t, err, "open %s", path)

	defer file.Close()

	reader, err := gzip.NewReader(file)
	require.NoError(t, err, "decompress %s", path)

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(nil, 64*1024*1024)

	for scanner.Scan() {
		var exchange any

		err = json.Unmarshal(scanner.Bytes(), &exchange)
		require.NoError(t, err, "parse an exchange of %s", path)

		collectStrings(exchange, values)
	}

	require.NoError(t, scanner.Err(), "read %s", path)
}

// collectStrings adds every string of doc to values. Bodies hold JSON as a
// string, so it also adds the strings of every string that is JSON.
func collectStrings(doc any, values map[string]struct{}) {
	switch v := doc.(type) {
	case map[string]any:
		for _, item := range v {
			collectStrings(item, values)
		}

	case []any:
		for _, item := range v {
			collectStrings(item, values)
		}

	case string:
		var nested any

		trimmed := strings.TrimSpace(v)
		if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Unmarshal([]byte(trimmed), &nested) == nil {
			collectStrings(nested, values)

			return
		}

		values[v] = struct{}{}
	}
}
