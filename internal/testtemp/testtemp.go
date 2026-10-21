// Package testtemp gives test processes a short temporary directory on macOS.
//
// go test records every file access of a package's tests for its test cache
// and, when it looks a cached result up again, resolves each recorded path
// component by component. On macOS the default temporary directory lies eight
// components deep behind the /var symlink, and the largest test packages
// record hundreds of thousands of accesses under it, so a warm test run spent
// most of its time on those lookups. A fresh private root directly under
// /private/tmp keeps every test path three components shorter and free of
// symlinks. Only the test process and the children it starts see the new
// TMPDIR; the go command's own TMPDIR, which keys the test cache, stays
// unchanged.
package testtemp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

// UseShortRoot points TMPDIR of this process and its children at a fresh
// private directory under /private/tmp on macOS and returns the call that
// removes it. It acts only in a test process that writes a test log, which go
// test requests from the top-level process of a cacheable run: test binaries
// that tests start again as helpers or fake servers, often with a rebuilt
// environment, keep the default and never leave a root behind. Elsewhere the
// returned call does nothing.
func UseShortRoot() (cleanup func() error, err error) {
	none := func() error { return nil }
	if runtime.GOOS != "darwin" || !writesTestLog(os.Args[1:]) {
		return none, nil
	}
	root, err := os.MkdirTemp("/private/tmp", "reconc-go-test-")
	if err != nil {
		return nil, fmt.Errorf("create short temporary root: %w", err)
	}
	if err := os.Setenv("TMPDIR", root); err != nil {
		return nil, errors.Join(fmt.Errorf("set TMPDIR: %w", err), os.RemoveAll(root))
	}
	return func() error { return os.RemoveAll(root) }, nil
}

func writesTestLog(arguments []string) bool {
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "-test.testlogfile=") || argument == "-test.testlogfile" {
			return true
		}
	}
	return false
}

// Main runs a package's tests under UseShortRoot and returns the exit code,
// which is nonzero when the root cannot be created or removed.
func Main(testingMain *testing.M) int {
	return MainWithSetup(testingMain, func() (func() error, error) { return nil, nil })
}

// MainWithSetup runs setup under UseShortRoot, then the package's tests, and
// returns the exit code. setup returns its cleanup as soon as it has created
// anything, together with an error when it fails part way. That cleanup and
// the short root run on every path, so a failed setup leaves nothing behind,
// and any failure makes the exit code nonzero.
func MainWithSetup(testingMain *testing.M, setup func() (cleanup func() error, err error)) int {
	return mainWithSetup(UseShortRoot, setup, testingMain.Run, os.Stderr)
}

func mainWithSetup(useRoot, setup func() (func() error, error), run func() int, diagnostics io.Writer) int {
	removeRoot, err := useRoot()
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	exitCode := 1
	cleanup, err := setup()
	if err != nil {
		fmt.Fprintln(diagnostics, err)
	} else {
		exitCode = run()
	}
	var cleanupErr error
	if cleanup != nil {
		cleanupErr = cleanup()
	}
	if err := errors.Join(cleanupErr, removeRoot()); err != nil {
		fmt.Fprintln(diagnostics, err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	return exitCode
}
