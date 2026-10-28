// Package profile describes how the proxy behaves towards a Redfish client.
package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/FuturFusion/incus-redfish-proxy/profiles"
)

// SchemaVersion is the version of the profile file format.
const SchemaVersion = 1

// DefaultName is the profile the proxy uses when the user names none.
const DefaultName = "generic"

// profileFile is the name of the profile within its directory.
const profileFile = "profile.yaml"

// namePattern keeps profile names usable in the model string and as directory names.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Profile describes how the proxy behaves towards a Redfish client.
type Profile struct {
	Version     int    `yaml:"version"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	BIOS        BIOS   `yaml:"bios"`
}

// Default returns the generic profile, which reproduces the behavior of the proxy before profiles existed.
func Default() Profile {
	p, err := Load("")
	if err != nil {
		panic(fmt.Sprintf("load embedded profile %q: %v", DefaultName, err))
	}

	return p
}

// defaults returns the values every profile starts from before Parse decodes its file.
func defaults() Profile {
	return Profile{
		BIOS: BIOS{
			PatchResponse:         PatchResponseTaskMonitor,
			PatchRequiresPowerOff: true,
			ApplyTime: ApplyTimes{
				Supported: []ApplyTime{},
			},
			Registry: Registry{
				Attributes: []Attribute{},
			},
			Errors: BIOSErrors{
				UnknownAttribute: ErrorShape{Status: 400, MessageID: "Base.1.18.PropertyUnknown"},
			},
		},
	}
}

// Load resolves an embedded profile by name, or reads the profile file at the given path.
func Load(nameOrPath string) (Profile, error) {
	if nameOrPath == "" {
		nameOrPath = DefaultName
	}

	if !strings.ContainsRune(nameOrPath, os.PathSeparator) && !strings.HasSuffix(nameOrPath, ".yaml") {
		return loadEmbedded(nameOrPath)
	}

	data, err := os.ReadFile(nameOrPath)
	if err != nil {
		return Profile{}, fmt.Errorf("read profile: %w", err)
	}

	p, err := decode(data)
	if err != nil {
		return Profile{}, fmt.Errorf("profile %q: %w", nameOrPath, err)
	}

	return p, nil
}

// loadEmbedded loads the embedded profile with the given name.
func loadEmbedded(name string) (Profile, error) {
	names := List()
	if !slices.Contains(names, name) {
		return Profile{}, fmt.Errorf("unknown profile %q, known profiles are: %s", name, strings.Join(names, ", "))
	}

	data, err := fs.ReadFile(profiles.FS, path.Join(name, profileFile))
	if err != nil {
		return Profile{}, fmt.Errorf("read embedded profile %q: %w", name, err)
	}

	p, err := decode(data)
	if err != nil {
		return Profile{}, fmt.Errorf("embedded profile %q: %w", name, err)
	}

	if p.Name != name {
		return Profile{}, fmt.Errorf("embedded profile %q has the name %q", name, p.Name)
	}

	return p, nil
}

// decode reads a profile over the defaults and validates it.
func decode(data []byte) (Profile, error) {
	p := defaults()

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	err := dec.Decode(&p)
	if errors.Is(err, io.EOF) {
		return Profile{}, errors.New("profile is empty")
	}

	if err != nil {
		return Profile{}, fmt.Errorf("decode: %w", err)
	}

	err = p.Validate()
	if err != nil {
		return Profile{}, err
	}

	p.BIOS.resolve()

	return p, nil
}

// List returns the names of the embedded profiles, sorted.
func List() []string {
	entries, err := fs.ReadDir(profiles.FS, ".")
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}

	slices.Sort(names)

	return names
}

// Validate checks the profile for consistency.
func (p Profile) Validate() error {
	if p.Version != SchemaVersion {
		return fmt.Errorf("unsupported profile version %d, expected %d", p.Version, SchemaVersion)
	}

	if !namePattern.MatchString(p.Name) {
		return fmt.Errorf("invalid profile name %q, lower case letters, digits and dashes expected", p.Name)
	}

	return p.BIOS.validate()
}
