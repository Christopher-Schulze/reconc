package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const generatedReferenceAuditTimeout = 2 * time.Minute

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
	return auditGeneratedReferenceDriftWithTimeout(root, generatedReferenceAuditTimeout)
}

func auditGeneratedReferenceDriftWithTimeout(root string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./codebase/scripts/generators/generated_reference", "-check")
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = root
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("generated reference drift audit timed out after %s", timeout)
	}
	if err != nil {
		return fmt.Errorf("generated reference drift audit failed: %w\n%s", err, string(output))
	}
	return nil
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
