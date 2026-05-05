// Package main is the reconc CLI entry point.
//
// reconc (Repository Control Compiler) compiles repository policy from
// AGENTS.md / start.md and related YAML files into a deterministic policy
// lockfile, then enforces that policy over runtime evidence and git diffs.
//
// This is the standalone Go implementation of the tool.
package main

import (
	"fmt"
	"os"

	"reconc.dev/reconc/internal/cli"
)

// Version is the reconc build version. Overridden at build time via
// -ldflags "-X main.Version=<semver>" for release builds.
var Version = "0.4.0"

func main() {
	if err := cli.Run(os.Args[1:], Version, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
