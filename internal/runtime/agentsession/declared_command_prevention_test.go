package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/runtime"
)

func declaredCommandFixture(t *testing.T, policy string) nativeApprovalFixture {
	t.Helper()
	fixture := newNativeApprovalFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.repo, ".reconc.yml"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(fixture.repo, "test"); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestDeclaredCommandEffectsReachPreventionAndCache(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprint(bound), func(t *testing.T) {
			policy := `rules:
  - id: command-gate
    kind: all_of
    when_paths: ['notes.md']
    checks:
      - kind: forbid_command
        commands: ['printf']
        command_match: prefix
    mode: block
    message: declared command blocked
`
			if bound {
				policy += "  - id: authority-files\n    template: authority-change-approval\n    when_paths: ['AGENTS.md']\n"
			}
			fixture := declaredCommandFixture(t, policy)
			root, err := ResolveRepoRootRef(fixture.repo)
			if err != nil {
				t.Fatal(err)
			}
			command := "printf changed > notes.md"
			for _, paths := range [][]string{{"other.md"}, {"./notes.md"}, {"other.md"}, {"notes.md", "notes.md"}} {
				inputs := runtime.Empty()
				inputs.Commands, inputs.WritePaths = []string{command}, paths
				control, err := runtime.CheckRepoPolicyForPreCommand(fixture.repo, inputs)
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if paths[0] != "other.md" {
					want = 2
				}
				if (control.Decision == runtime.DecisionBlock) != (want == 2) {
					t.Fatalf("invalid evaluator control: %+v", control)
				}
				payload := nativeCommandPayload(t, fixture, "same-tool-id", command, paths)
				for _, result := range []Result{
					RunPreToolUse(fixture.repo, payload),
					RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload),
					RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload),
				} {
					if result.ExitCode != want {
						t.Fatalf("paths=%v result=%+v, want exit %d", paths, result, want)
					}
				}
			}
			state, err := LoadSessionState(fixture.repo, fixture.sessionID)
			if err != nil || len(state.WritePaths) != 0 || len(state.Commands) != 0 || len(state.WriteEpochs) != 0 {
				t.Fatalf("prospective effects became completed evidence: %+v, err %v", state, err)
			}
		})
	}
}

func TestDeclaredCommandDependenciesInvalidateCachedPass(t *testing.T) {
	for _, mutation := range []string{"missing", "changed"} {
		t.Run(mutation, func(t *testing.T) {
			check := "      - kind: forbid_command\n        commands: ['printf']\n        command_match: prefix\n"
			command := "printf changed"
			fixture := declaredCommandFixture(t, "rules:\n  - id: dependency-gate\n    kind: any_of\n    when_paths: ['notes.md']\n    checks:\n"+check+"      - kind: require_evidence\n        file: 'proof.txt'\n        must_exist: true\n        must_contain: ['ready']\n    mode: block\n    message: proof required\n")
			proof := filepath.Join(fixture.repo, "proof.txt")
			if err := os.WriteFile(proof, []byte("ready\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			root, err := ResolveRepoRootRef(fixture.repo)
			if err != nil {
				t.Fatal(err)
			}
			payload := nativeCommandPayload(t, fixture, "same-tool-id", command, []string{"notes.md"})
			warm := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
			if warm.ExitCode != 0 {
				t.Fatalf("initial evidence did not permit command: %+v", warm)
			}
			if _, err := os.Stat(preDecisionCachePath(root.Path(), payload)); err != nil {
				t.Fatalf("pass was not cached: %v", err)
			}
			if mutation == "missing" {
				if err := os.Rename(proof, proof+".previous"); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(proof, []byte("other\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			fresh := RunPreToolUse(fixture.repo, payload)
			cached := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload)
			if fresh.ExitCode != 2 || cached.ExitCode != 2 || cached.Stderr != fresh.Stderr {
				t.Fatalf("changed dependency not enforced: fresh=%+v cached=%+v", fresh, cached)
			}
		})
	}
}

func TestDeclaredWritesReachPreWriteWithoutBoundApprovalRules(t *testing.T) {
	fixture := declaredCommandFixture(t, "rules:\n  - id: protected-notes\n    kind: deny_write\n    paths: ['notes.md']\n    mode: block\n    message: protected notes\n")
	for _, command := range []string{"echo unchanged", "printf changed > notes.md"} {
		t.Run(command, func(t *testing.T) {
			payload := nativeCommandPayload(t, fixture, "same-id", command, []string{"notes.md"})
			result := RunPreToolUse(fixture.repo, payload)
			if result.ExitCode != 2 || !strings.Contains(result.Stderr, "protected-notes") {
				t.Fatalf("declared write bypassed pre-write policy: %+v", result)
			}
		})
	}
}

func TestMalformedDeclaredCommandEffectsFailClosed(t *testing.T) {
	fixture := declaredCommandFixture(t, "rules:\n  - id: no-git\n    kind: forbid_command\n    commands: ['git']\n    mode: block\n    message: blocked\n")
	for _, declaration := range []string{`null`, `[]`, `"notes.md"`, `[1]`, `[""]`, `[" notes.md"]`} {
		t.Run(declaration, func(t *testing.T) {
			payload := []byte(fmt.Sprintf(`{"session_id":%q,"tool_use_id":"same-id","tool_name":"Bash","tool_input":{"command":"echo unchanged"},"reconc_write_paths":%s}`, fixture.sessionID, declaration))
			result := RunPreToolUse(fixture.repo, payload)
			if result.ExitCode != 2 || !strings.Contains(result.Stderr, "command write declaration") {
				t.Fatalf("malformed declaration accepted: %+v", result)
			}
		})
	}
}

func TestDeclaredWriteCacheRejectsRetargetedPath(t *testing.T) {
	fixture := declaredCommandFixture(t, "rules:\n  - id: protected-notes\n    kind: deny_write\n    paths: ['notes.md']\n    mode: block\n    message: protected notes\n")
	for _, name := range []string{"other.md", "notes.md"} {
		if err := os.WriteFile(filepath.Join(fixture.repo, name), []byte("original\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := ResolveRepoRootRef(fixture.repo)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(fixture.repo, "target")
	if err := os.Symlink("other.md", link); err != nil {
		t.Fatal(err)
	}
	payload := nativeCommandPayload(t, fixture, "same-id", "echo unchanged", []string{"target"})
	if warm := RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload); warm.ExitCode != 0 {
		t.Fatalf("unprotected target denied: %+v", warm)
	}
	if _, err := os.Stat(preDecisionCachePath(root.Path(), payload)); err != nil {
		t.Fatalf("pass was not cached: %v", err)
	}
	if err := os.Rename(link, link+".previous"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("notes.md", link); err != nil {
		t.Fatal(err)
	}
	for _, result := range []Result{
		RunPreToolUse(fixture.repo, payload),
		RunHookRequest(root, HookHandlerPreToolUse, "claude-pre-tool-use", payload),
	} {
		if result.ExitCode != 2 || !strings.Contains(result.Stderr, "protected-notes") {
			t.Fatalf("retargeted declaration reused stale pass: %+v", result)
		}
	}
}
