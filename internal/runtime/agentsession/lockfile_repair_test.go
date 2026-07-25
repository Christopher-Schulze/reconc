package agentsession

import (
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rerrors "reconc.dev/reconc/internal/errors"
)

// staleLockfileRepo returns a policy repo whose compiled lockfile no longer
// matches its sources, reproducing the incident state.
func staleLockfileRepo(t *testing.T) string {
	t.Helper()
	repo := setupPolicyRepo(t)
	rules := `rules:
  - id: deny-generated
    kind: deny_write
    paths: ['generated/**']
    mode: block
    message: no writes to generated
  - id: require-ci-green
    kind: require_claim
    when_paths: ['**']
    claims: ['ci-green']
    mode: block
    message: need ci-green
  - id: deny-vendor
    kind: deny_write
    paths: ['vendor/**']
    mode: block
    message: no writes to vendor
`
	if err := os.WriteFile(filepath.Join(repo, "policies", "rules.yml"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func preToolUsePayload(t *testing.T, tool string, input map[string]interface{}) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]interface{}{
		"session_id": "stale-lock",
		"tool_name":  tool,
		"tool_input": input,
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// TestPreToolUseAdmitsLockfileRepairWhileStale is the end-to-end regression for
// the deadlock. It drives the real route, not a helper: with a stale lockfile
// the repair command must pass, an ordinary command must not, and a write must
// not. Before the fix every one of these was refused, which left the session
// unable to work, repair, or stop.
func TestPreToolUseAdmitsLockfileRepairWhileStale(t *testing.T) {
	repo := staleLockfileRepo(t)

	admitted := RunPreToolUse(repo, preToolUsePayload(t, "Bash", map[string]interface{}{"command": "reconc refresh ."}))
	if admitted.ExitCode != 0 {
		t.Fatalf("repair command must be admitted while the lockfile is stale, got exit=%d stderr=%s", admitted.ExitCode, admitted.Stderr)
	}

	blocked := RunPreToolUse(repo, preToolUsePayload(t, "Bash", map[string]interface{}{"command": "git status"}))
	if blocked.ExitCode == 0 {
		t.Fatal("an ordinary command must stay blocked while the lockfile is stale")
	}
	for _, phrase := range []string{"reconc refresh .", "this gate admits", "revert the policy source"} {
		if !strings.Contains(blocked.Stderr, phrase) {
			t.Fatalf("stale-lockfile block %q is missing %q", blocked.Stderr, phrase)
		}
	}

	write := RunPreToolUse(repo, preToolUsePayload(t, "Write", map[string]interface{}{"file_path": filepath.Join(repo, "notes.md"), "content": "x"}))
	if write.ExitCode == 0 {
		t.Fatal("a write must stay blocked while the lockfile is stale; policy cannot be enforced from an unreadable lockfile")
	}
	if !strings.Contains(write.Stderr, "reconc refresh .") {
		t.Fatalf("stale-lockfile write block must name the reachable repair, got %q", write.Stderr)
	}
}

// TestPreToolUseDoesNotAdmitRepairWhenLockfileIsFresh confirms the exemption is
// scoped to the failure it exists for: with a healthy lockfile the repair
// command goes through the ordinary policy path like any other command.
func TestPreToolUseDoesNotAdmitRepairWhenLockfileIsFresh(t *testing.T) {
	repo := setupPolicyRepo(t)
	result := RunPreToolUse(repo, preToolUsePayload(t, "Bash", map[string]interface{}{"command": "reconc refresh ."}))
	if result.ExitCode != 0 {
		t.Fatalf("a fresh lockfile must let the command through the normal path, got exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	if strings.Contains(result.Stderr, "this gate admits") {
		t.Fatalf("the stale-lockfile message must not appear with a fresh lockfile, got %q", result.Stderr)
	}
}

// TestIsLockfileRepairCommandAdmitsEveryDocumentedInvocationForm is the
// regression for the deadlock: a stale lockfile refused a path-qualified binary
// AND a bare `reconc refresh` identically, so matching the command word alone
// would not have escaped it.
func TestIsLockfileRepairCommandAdmitsEveryDocumentedInvocationForm(t *testing.T) {
	for _, command := range []string{
		"reconc refresh .",
		"reconc compile .",
		"dist/reconc-0.8.7-darwin-arm64 refresh .",
		"/usr/local/lib/reconc/reconc-0.8.7-darwin-arm64 refresh /srv/repository",
		"dist/reconc-1.0.0-linux-amd64 compile .",
		"reconc-0.8.7-windows-amd64.exe refresh .",
		"reconc --json refresh .",
	} {
		if !isLockfileRepairCommand(command) {
			t.Fatalf("%q must be admitted as a lockfile repair command", command)
		}
	}
}

// TestIsLockfileRepairCommandRejectsEverythingElse bounds the exemption. It must
// never become a general hole: not other reconc subcommands, not a compound
// command that smuggles work alongside a refresh, and not an arbitrary binary
// that merely starts with the product name.
func TestIsLockfileRepairCommandRejectsEverythingElse(t *testing.T) {
	for _, command := range []string{
		"reconc check .",
		"reconc status .",
		"reconc run on .",
		"git status",
		"reconc refresh . && rm -rf build",
		"rm -rf build && reconc refresh .",
		"reconc-evil refresh .",
		"reconcile refresh .",
		"reconc",
		"reconc --help",
		"$TOOL refresh .",
		"",
	} {
		if isLockfileRepairCommand(command) {
			t.Fatalf("%q must NOT be admitted as a lockfile repair command", command)
		}
	}
}

func TestIsReconcExecutableRequiresANumericVersionSegment(t *testing.T) {
	tests := []struct {
		token string
		want  bool
	}{
		{token: "reconc", want: true},
		{token: "/usr/local/bin/reconc", want: true},
		{token: "reconc-0.8.7-darwin-arm64", want: true},
		{token: "reconc-1-linux-amd64", want: true},
		{token: "reconc-evil", want: false},
		{token: "reconc-", want: false},
		{token: "reconc-beta-darwin-arm64", want: false},
		{token: "reconcile", want: false},
		{token: "notreconc", want: false},
	}
	for _, test := range tests {
		if got := isReconcExecutable(test.token); got != test.want {
			t.Fatalf("isReconcExecutable(%q) = %t, want %t", test.token, got, test.want)
		}
	}
}

// TestIsLockfileErrorSeparatesContractFromViolation pins the distinction the
// routes depend on: "policy cannot be read" must never be confused with
// "policy says no", or the exemption would leak into real violations.
func TestIsLockfileErrorSeparatesContractFromViolation(t *testing.T) {
	if !isLockfileError(&rerrors.LockfileError{Message: "compiled lockfile rules do not match the current policy sources"}) {
		t.Fatal("a LockfileError must be recognised")
	}
	wrapped := stderrors.Join(stderrors.New("context"), &rerrors.LockfileError{Message: "stale"})
	if !isLockfileError(wrapped) {
		t.Fatal("a wrapped LockfileError must be recognised")
	}
	if isLockfileError(stderrors.New("policy check found blocking violations")) {
		t.Fatal("an ordinary error must not be treated as a lockfile contract failure")
	}
	if isLockfileError(nil) {
		t.Fatal("nil must not be treated as a lockfile contract failure")
	}
}

// TestLockfileBlockMessageNamesAReachableEscape guards the property whose
// absence caused the deadlock: the message prescribed `reconc refresh .` while
// the same gate refused that exact command.
func TestLockfileBlockMessageNamesAReachableEscape(t *testing.T) {
	err := &rerrors.LockfileError{Message: "compiled lockfile rules do not match the current policy sources"}
	for _, surface := range []string{"pre", "stop"} {
		message := lockfileBlockMessage(surface, err)
		if !strings.Contains(message, "reconc hook ("+surface+")") {
			t.Fatalf("message must name its surface, got %q", message)
		}
		for _, phrase := range []string{
			"reconc refresh .",
			"this gate admits",
			"revert the policy source",
		} {
			if !strings.Contains(message, phrase) {
				t.Fatalf("message %q is missing %q", message, phrase)
			}
		}
		// The escape it names must be one it actually admits.
		if !isLockfileRepairCommand("reconc refresh .") {
			t.Fatal("the command named in the block message must be admitted by the gate")
		}
	}
}
