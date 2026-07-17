package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"reconc.dev/reconc/buildprovenance"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("reconc-build-provenance", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", ".", "Reconc module root")
	goos := flags.String("goos", "", "target GOOS")
	goarch := flags.String("goarch", "", "target GOARCH")
	version := flags.String("version", "", "Reconc version")
	verifyBinary := flags.String("verify-binary", "", "verify one built binary without executing it")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse build provenance flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if *goos == "" || *goarch == "" || *version == "" {
		return fmt.Errorf("--goos, --goarch, and --version are required")
	}
	digest, err := buildprovenance.ComputeSourceDigest(*root, *goos, *goarch)
	if err != nil {
		return err
	}
	marker, err := buildprovenance.FormatMarker(buildprovenance.Provenance{
		Version:      *version,
		GOOS:         *goos,
		GOARCH:       *goarch,
		SourceDigest: digest,
	})
	if err != nil {
		return err
	}
	if *verifyBinary != "" {
		actual, inspectErr := buildprovenance.InspectBinary(*verifyBinary)
		if inspectErr != nil {
			return inspectErr
		}
		expected, parseErr := buildprovenance.ParseMarker(marker)
		if parseErr != nil {
			return parseErr
		}
		if actual != expected {
			return fmt.Errorf("binary provenance mismatch: got version=%s target=%s/%s source=%s, want version=%s target=%s/%s source=%s", actual.Version, actual.GOOS, actual.GOARCH, actual.SourceDigest, expected.Version, expected.GOOS, expected.GOARCH, expected.SourceDigest)
		}
		return nil
	}
	_, err = fmt.Fprintln(stdout, marker)
	return err
}
