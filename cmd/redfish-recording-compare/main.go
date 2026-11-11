// Command redfish-recording-compare compares a profile recording with the recording of the real server it is drawn from.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/pflag"

	"github.com/FuturFusion/incus-redfish-proxy/internal/compare"
	"github.com/FuturFusion/incus-redfish-proxy/internal/profile"
	"github.com/FuturFusion/incus-redfish-proxy/internal/recording"
)

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := pflag.NewFlagSet("redfish-recording-compare", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage: redfish-recording-compare [--verbose] [--out report.md] <reference> <candidate>")
		flags.PrintDefaults()
	}

	verbose := flags.Bool("verbose", false, "add the aligned operations of every step")
	out := flags.String("out", "", "file to write the report to, standard output if empty")

	err := flags.Parse(args)
	if err != nil {
		return err
	}

	if flags.NArg() != 2 {
		flags.Usage()

		return errors.New("expected a reference and a candidate recording")
	}

	reference, err := recording.Open(flags.Arg(0))
	if err != nil {
		return err
	}

	candidate, err := recording.Open(flags.Arg(1))
	if err != nil {
		return err
	}

	p, err := referenceProfile(flags.Arg(0))
	if err != nil {
		return err
	}

	data := compare.Compare(reference, candidate, p).Markdown(*verbose)

	if *out == "" {
		_, err = stdout.Write(data)
	} else {
		err = os.WriteFile(*out, data, 0o644)
	}

	if err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	return nil
}

// referenceProfile loads the profile.yaml of a reference directory, nil if there is none.
func referenceProfile(reference string) (*profile.Profile, error) {
	info, err := os.Stat(reference)
	if err != nil || !info.IsDir() {
		return nil, err
	}

	path := filepath.Join(reference, "profile.yaml")

	_, err = os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("profile of the reference: %w", err)
	}

	p, err := profile.Load(path)
	if err != nil {
		return nil, err
	}

	return &p, nil
}
