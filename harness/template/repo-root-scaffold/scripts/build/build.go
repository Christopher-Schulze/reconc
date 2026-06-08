package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/build <build|test|lint|validate|clean>")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(action string) error {
	switch action {
	case "build":
		return runCommand("go", "build", "./backend/project")
	case "test":
		return runCommand("go", "test", "./...")
	case "lint":
		return runCommand("go", "vet", "./...")
	case "validate":
		if err := run("lint"); err != nil {
			return err
		}
		return run("test")
	case "clean":
		return runCommand("go", "clean", "-cache", "-testcache")
	default:
		return fmt.Errorf("unknown build action %q", action)
	}
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
