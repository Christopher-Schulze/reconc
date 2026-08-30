package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunScriptRejectsRenameSwapAtEveryExecutionBoundary(t *testing.T) {
	stages := []scriptExecutionStage{
		scriptStagePathValidated,
		scriptStageSourceOpened,
		scriptStageSnapshotCreated,
		scriptStageCommandPrepared,
	}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			repo := t.TempDir()
			writeContextScript(t, repo, "scripts/check.sh", "#!/bin/sh\ntouch original-ran\n")
			writeContextScript(t, repo, "scripts/replacement.sh", "#!/bin/sh\ntouch replacement-ran\n")
			hook := func(current scriptExecutionStage, sourcePath string) error {
				if current != stage {
					return nil
				}
				if err := os.Rename(sourcePath, sourcePath+".validated"); err != nil {
					return err
				}
				return os.Rename(filepath.Join(repo, "scripts", "replacement.sh"), sourcePath)
			}
			assertScriptAttackFailsClosed(t, repo, hook)
		})
	}
}

func TestRunScriptRejectsContentReplacementAtEveryExecutionBoundary(t *testing.T) {
	stages := []scriptExecutionStage{
		scriptStagePathValidated,
		scriptStageSourceOpened,
		scriptStageSnapshotCreated,
		scriptStageCommandPrepared,
	}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			repo := t.TempDir()
			writeContextScript(t, repo, "scripts/check.sh", "#!/bin/sh\ntouch original-ran\n")
			hook := func(current scriptExecutionStage, sourcePath string) error {
				if current != stage {
					return nil
				}
				return os.WriteFile(sourcePath, []byte("#!/bin/sh\ntouch replacement-ran\n# changed size\n"), 0o755)
			}
			assertScriptAttackFailsClosed(t, repo, hook)
		})
	}
}

func TestRunScriptDigestRejectsSameMetadataContentReplacement(t *testing.T) {
	stages := []scriptExecutionStage{
		scriptStagePathValidated,
		scriptStageSourceOpened,
		scriptStageSnapshotCreated,
		scriptStageCommandPrepared,
	}
	for _, target := range stages {
		t.Run(string(target), func(t *testing.T) {
			repo := t.TempDir()
			original := fixedSizeScript("exit 0")
			replacement := fixedSizeScript("touch replacement-ran")
			writeContextScript(t, repo, "scripts/check.sh", original)
			hook := func(stage scriptExecutionStage, sourcePath string) error {
				if stage != target {
					return nil
				}
				info, err := os.Stat(sourcePath)
				if err != nil {
					return err
				}
				if err := os.WriteFile(sourcePath, []byte(replacement), info.Mode().Perm()); err != nil {
					return err
				}
				return os.Chtimes(sourcePath, info.ModTime(), info.ModTime())
			}
			assertScriptAttackFailsClosed(t, repo, hook)
		})
	}
}

func TestBoundScriptPreservesArgumentsWorkingDirectoryEnvironmentAndInput(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "MARKER"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeContextScript(t, repo, "scripts/check.sh", `#!/bin/sh
set -eu
[ "$1" = "expected-argument" ] || { printf 'argument=%s\n' "$1" >&2; exit 1; }
[ "${RECONC_SCRIPT:-}" = "1" ] || { printf 'environment=%s\n' "${RECONC_SCRIPT:-}" >&2; exit 1; }
[ -f MARKER ] || { printf 'marker missing\n' >&2; exit 1; }
grep -q '"rule_id":"expected-rule"' || { printf 'input missing\n' >&2; exit 1; }
`)
	outcome, err := RunScript(repo, "scripts/check.sh", []string{"expected-argument"}, ScriptInput{RuleID: "expected-rule"}, 5, 1)
	if err != nil || outcome.Status != "pass" || outcome.ExitCode != 0 {
		t.Fatalf("bound script compatibility = (%#v, %v)", outcome, err)
	}
}

func assertScriptAttackFailsClosed(t *testing.T, repo string, hook scriptExecutionHook) {
	t.Helper()
	outcome, err := runScriptContext(context.Background(), repo, "scripts/check.sh", nil, ScriptInput{}, 5, 1, hook)
	if err == nil || outcome.Status != "error" {
		t.Fatalf("repository replacement was not rejected: outcome=%#v error=%v", outcome, err)
	}
	for _, marker := range []string{"original-ran", "replacement-ran"} {
		if _, statErr := os.Stat(filepath.Join(repo, marker)); !os.IsNotExist(statErr) {
			t.Fatalf("refused script created %s: %v", marker, statErr)
		}
	}
}

func fixedSizeScript(command string) string {
	const size = 128
	prefix := "#!/bin/sh\n" + command + "\n#"
	return prefix + strings.Repeat("x", size-len(prefix)-1) + "\n"
}
