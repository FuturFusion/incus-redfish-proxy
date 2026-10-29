// Command redfish-profile-draft drafts a profile from a baseboard management controller (BMC) recording of Operations Center.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/pflag"

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
	flags := pflag.NewFlagSet("redfish-profile-draft", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage: redfish-profile-draft [--name <name>] [--out profile.yaml] <recording>")
		flags.PrintDefaults()
	}

	name := flags.String("name", "draft", "name of the drafted profile")
	out := flags.String("out", "", "file to write the draft to, standard output if empty")

	err := flags.Parse(args)
	if err != nil {
		return err
	}

	if flags.NArg() != 1 {
		flags.Usage()

		return errors.New("expected exactly one recording")
	}

	rec, err := recording.Open(flags.Arg(0))
	if err != nil {
		return err
	}

	data, noEvidence, err := recording.NewDraft(rec, *name).Render()
	if err != nil {
		return err
	}

	if *out == "" {
		_, err = stdout.Write(data)
	} else {
		err = os.WriteFile(*out, data, 0o644)
	}

	if err != nil {
		return fmt.Errorf("write draft: %w", err)
	}

	summary := fmt.Sprintf("Read %d exchanges. These keys keep their default for lack of evidence:\n", len(rec.Exchanges))
	for _, key := range noEvidence {
		summary += "  " + key + "\n"
	}

	_, err = io.WriteString(stderr, summary)

	return err
}
