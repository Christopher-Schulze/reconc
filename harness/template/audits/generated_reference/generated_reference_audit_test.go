package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedReferenceAuditPassesGeneratedReferenceRepo(t *testing.T) {
	root := t.TempDir()
	writeGeneratedReferenceGenerator(t, root, `package main

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
	if err := auditGeneratedReferenceDrift(root); err != nil {
		t.Fatalf("expected generated references to be fresh: %v", err)
	}
}

func TestGeneratedReferenceAuditReportsGeneratorFailure(t *testing.T) {
	root := t.TempDir()
	writeGeneratedReferenceGenerator(t, root, "package main\nfunc main(){panic(\"boom\")}\n")
	err := auditGeneratedReferenceDrift(root)
	if err == nil || !strings.Contains(err.Error(), "generated reference drift audit failed") {
		t.Fatalf("expected generator failure, got %v", err)
	}
}

func TestGeneratedReferenceAuditCommandUsesRepoRoot(t *testing.T) {
	root := t.TempDir()
	writeGeneratedReferenceGenerator(t, root, `package main

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
	cmd := exec.Command("go", "run", "./codebase/scripts/generators/generated_reference", "-h")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GO111MODULE=off")
	if output, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(output), "-check") {
		t.Fatalf("expected generator help from repo root, err=%v output=%s", err, string(output))
	}
}

func writeGeneratedReferenceGenerator(t *testing.T, root string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module project\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	dir := filepath.Join(root, "codebase/scripts/generators/generated_reference")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir generator: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("write generator: %v", err)
	}
}
