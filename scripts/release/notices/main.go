package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"reconc.dev/reconc/internal/atomicfile"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "license-notices:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("license-notices", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	root := flags.String("root", "", "repository root")
	output := flags.String("output", "", "notice output path")
	goBinary := flags.String("go", "go", "Go command")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *root == "" || *output == "" || *goBinary == "" || len(flags.Args()) == 0 {
		return errors.New("usage requires --root, --output, --go, and at least one OS/ARCH target")
	}
	inventory, err := discoverInventory(*root, *goBinary, flags.Args())
	if err != nil {
		return err
	}
	body, err := renderNotices(inventory)
	if err != nil {
		return err
	}
	if _, err := atomicfile.WriteIfChanged(*output, body, 0o644); err != nil {
		return fmt.Errorf("publish notices: %w", err)
	}
	return nil
}
