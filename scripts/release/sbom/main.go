package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

type commandOptions struct {
	root      string
	outputDir string
	version   string
	commit    string
	epoch     string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sbom:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sbom generate|verify [flags]")
	}
	options, err := parseOptions(args[0], args[1:])
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	inventory, err := collectInventory(ctx, options)
	if err != nil {
		return err
	}
	spdx, cyclonedx, err := renderDocuments(inventory)
	if err != nil {
		return err
	}
	switch args[0] {
	case "generate":
		return writeDocuments(options.outputDir, options.version, spdx, cyclonedx)
	case "verify":
		return verifyDocuments(options.outputDir, options.version, spdx, cyclonedx)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func parseOptions(command string, args []string) (commandOptions, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := commandOptions{}
	flags.StringVar(&options.root, "root", ".", "repository root")
	flags.StringVar(&options.outputDir, "output-dir", "dist", "release output directory")
	flags.StringVar(&options.version, "version", "", "release version")
	flags.StringVar(&options.commit, "commit", "", "release commit")
	flags.StringVar(&options.epoch, "source-date-epoch", "", "release commit timestamp")
	if err := flags.Parse(args); err != nil {
		return commandOptions{}, err
	}
	if flags.NArg() != 0 || options.version == "" || options.commit == "" || options.epoch == "" {
		return commandOptions{}, fmt.Errorf("--version, --commit, and --source-date-epoch are required")
	}
	return options, nil
}
