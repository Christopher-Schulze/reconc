package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"reconc.dev/reconc/internal/atomicfile"
	skill "reconc.dev/reconc/skills/reconc"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "skill asset:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("skill-asset", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifestPath := flags.String("manifest", "", "output manifest")
	archivePath := flags.String("archive", "", "output archive")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *manifestPath == "" || *archivePath == "" || *manifestPath == *archivePath {
		return errors.New("usage: skill-asset --manifest FILE --archive FILE")
	}
	manifest, err := skill.EncodeManifest()
	if err != nil {
		return err
	}
	archive, err := skill.Archive()
	if err != nil {
		return err
	}
	if _, err := atomicfile.WriteIfChanged(*manifestPath, manifest, 0o644); err != nil {
		return fmt.Errorf("publish skill manifest: %w", err)
	}
	if _, err := atomicfile.WriteIfChanged(*archivePath, archive, 0o644); err != nil {
		return fmt.Errorf("publish skill archive: %w", err)
	}
	return nil
}
