package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedReferenceAuditPassesGeneratedReferenceRepo(t *testing.T) {
	for _, generatorRel := range []string{
		"scripts/generators/generated_reference",
		"codebase/scripts/generators/generated_reference",
	} {
		t.Run(generatorRel, func(t *testing.T) {
			root := t.TempDir()
			writeGeneratedReferenceGenerator(t, root, generatorRel, `package main

import (
	"fmt"
	"os"
)

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" {
			fmt.Println("-check")
			return
		}
	}
}
`)
			if err := auditGeneratedReferenceDrift(root, generatorRel); err != nil {
				t.Fatalf("expected generated references to be fresh: %v", err)
			}
		})
	}
}

func TestGeneratedReferenceAuditReportsGeneratorFailure(t *testing.T) {
	root := t.TempDir()
	generatorRel := "scripts/generators/generated_reference"
	writeGeneratedReferenceGenerator(t, root, generatorRel, "package main\nfunc main(){panic(\"boom\")}\n")
	err := auditGeneratedReferenceDrift(root, generatorRel)
	if err == nil || !strings.Contains(err.Error(), "generated reference drift audit failed") {
		t.Fatalf("expected generator failure, got %v", err)
	}
}

func TestGeneratedReferenceAuditOutputIsBounded(t *testing.T) {
	var output boundedGeneratedReferenceOutput
	payload := []byte(strings.Repeat("x", maxGeneratedReferenceOutput+1))
	written, err := output.Write(payload)
	if err != nil || written != len(payload) {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if !output.truncated || len(output.String()) != maxGeneratedReferenceOutput {
		t.Fatalf("bounded output length=%d truncated=%v", len(output.String()), output.truncated)
	}
}

func TestGeneratedReferenceAuditCommandUsesRepoRoot(t *testing.T) {
	root := t.TempDir()
	generatorRel := "codebase/scripts/generators/generated_reference"
	writeGeneratedReferenceGenerator(t, root, generatorRel, `package main

import (
	"fmt"
	"os"
)

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" {
			fmt.Println("-check")
			return
		}
	}
}
`)
	cmd := exec.Command("go", "run", "./"+generatorRel, "-h")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GO111MODULE=off")
	if output, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(output), "-check") {
		t.Fatalf("expected generator help from repo root, err=%v output=%s", err, string(output))
	}
}

func TestGeneratedReferenceAuditRejectsEscapingGeneratorPath(t *testing.T) {
	err := auditGeneratedReferenceDrift(t.TempDir(), "../generator")
	if err == nil || !strings.Contains(err.Error(), "must stay repo-relative") {
		t.Fatalf("escaping generator path was accepted: %v", err)
	}
}

func writeGeneratedReferenceGenerator(t *testing.T, root string, generatorRel string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module project\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	dir := filepath.Join(root, filepath.FromSlash(generatorRel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir generator: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("write generator: %v", err)
	}
}
