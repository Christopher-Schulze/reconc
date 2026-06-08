package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		exit(err)
	}
	if err := auditGeneratedReferenceDrift(root); err != nil {
		exit(err)
	}
}

func auditGeneratedReferenceDrift(root string) error {
	cmd := exec.Command("go", "run", "./codebase/scripts/generators/generated_reference", "-check")
	cmd.Dir = root
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generated reference drift audit failed: %w\n%s", err, string(output))
	}
	return nil
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
