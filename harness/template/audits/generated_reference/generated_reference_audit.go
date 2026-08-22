package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const generatedReferenceAuditTimeout = 2 * time.Minute
const maxGeneratedReferenceOutput = 1 << 20

func main() {
	root, err := os.Getwd()
	if err != nil {
		exit(err)
	}
	if len(os.Args) != 2 {
		exit(fmt.Errorf("usage: generated-reference-audit GENERATOR_PATH"))
	}
	if err := auditGeneratedReferenceDrift(root, os.Args[1]); err != nil {
		exit(err)
	}
}

func auditGeneratedReferenceDrift(root string, generatorRel string) error {
	return auditGeneratedReferenceDriftWithTimeout(root, generatorRel, generatedReferenceAuditTimeout)
}

func auditGeneratedReferenceDriftWithTimeout(root string, generatorRel string, timeout time.Duration) error {
	generatorRel = filepath.ToSlash(filepath.Clean(filepath.FromSlash(generatorRel)))
	if generatorRel == "." || filepath.IsAbs(filepath.FromSlash(generatorRel)) ||
		generatorRel == ".." || strings.HasPrefix(generatorRel, "../") {
		return fmt.Errorf("generated reference generator path must stay repo-relative: %s", generatorRel)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./"+generatorRel, "-check")
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = root
	cmd.Env = os.Environ()
	var stdout boundedGeneratedReferenceOutput
	var stderr boundedGeneratedReferenceOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("generated reference drift audit timed out after %s", timeout)
	}
	if stdout.truncated || stderr.truncated {
		return fmt.Errorf("generated reference drift audit output exceeded %d bytes per stream", maxGeneratedReferenceOutput)
	}
	if err != nil {
		return fmt.Errorf("generated reference drift audit failed: %w\n%s", err, stdout.String()+stderr.String())
	}
	return nil
}

type boundedGeneratedReferenceOutput struct {
	buffer    bytes.Buffer
	truncated bool
}

func (output *boundedGeneratedReferenceOutput) Write(value []byte) (int, error) {
	remaining := maxGeneratedReferenceOutput - output.buffer.Len()
	if remaining > len(value) {
		remaining = len(value)
	}
	if remaining > 0 {
		_, _ = output.buffer.Write(value[:remaining])
	}
	if remaining < len(value) {
		output.truncated = true
	}
	return len(value), nil
}

func (output *boundedGeneratedReferenceOutput) String() string {
	return output.buffer.String()
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
