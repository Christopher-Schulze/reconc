package agentsession

import (
	stderrors "errors"
	"os/exec"
	"path/filepath"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/shellcommand"
)

// managedReconcToolDir is the repository-relative tree bootstrap owns: the hook
// wrapper, its direct-target receipt, and the installed binaries.
const managedReconcToolDir = "tools/reconc"

// lockfileRepairSubcommands admit the canonical refresh command and the hidden
// compile compatibility command. User-facing remediation names only refresh.
var lockfileRepairSubcommands = map[string]bool{
	"refresh": true,
	"compile": true,
}

// The remediation the gate prints must be a command the gate admits. This is
// the exact invocation form named in that message.
const lockfileRepairHint = "run `reconc refresh .` as the only executable command, without piping it to another command or chaining unrelated work; this gate admits that repair even while the lockfile is stale, or revert the policy source to its committed state"

// isLockfileError reports whether the failure is the lockfile contract itself
// rather than a policy violation, so callers can distinguish "policy says no"
// from "policy cannot be read".
func isLockfileError(err error) bool {
	var lockErr *rerrors.LockfileError
	return stderrors.As(err, &lockErr)
}

// isLockfileRepairCommand reports whether every executable position in the
// command is a Reconc lockfile-repair invocation.
//
// It exists because a stale lockfile blocks every gated operation, including
// the refresh the block message prescribes. Without this the guard seals the
// session: the agent cannot repair, cannot revert, and cannot stop. Admitting
// the repair restores the invariant the gate protects instead of weakening it.
//
// Every invocation must qualify, so a compound command cannot smuggle
// unrelated work through on the back of a refresh. Analysis must also be
// complete: a command whose executables cannot all be identified never
// qualifies.
func isLockfileRepairCommand(repoRoot, command string) bool {
	invocations, incomplete := shellcommand.InvocationsWithReason(command, maxShellGuardDepth)
	if incomplete != shellcommand.IncompleteNone || len(invocations) == 0 {
		return false
	}
	for _, invocation := range invocations {
		if !isLockfileRepairInvocation(repoRoot, invocation.Words) {
			return false
		}
	}
	return true
}

func isLockfileRepairInvocation(repoRoot string, words []string) bool {
	if len(words) < 2 || !isReconcExecutable(words[0]) || !isVouchedReconcBinary(repoRoot, words[0]) {
		return false
	}
	for _, word := range words[1:] {
		if strings.HasPrefix(word, "-") {
			continue
		}
		return lockfileRepairSubcommands[word]
	}
	return false
}

// isReconcExecutable matches the Reconc CLI under every invocation form the
// project documents: a bare `reconc` from PATH, a path-qualified wrapper, and
// the versioned release artifact such as `reconc-0.9.0-darwin-arm64`. Matching
// the command word alone is not enough, which the reproduction proved when a
// bare `reconc refresh` was refused identically to a path-qualified binary.
func isReconcExecutable(token string) bool {
	base := filepath.Base(strings.TrimSpace(strings.Trim(token, `"'`)))
	if base == "reconc" {
		return true
	}
	if !strings.HasPrefix(base, "reconc-") {
		return false
	}
	// Versioned artifacts are reconc-<version>-<goos>-<goarch>[.exe]; require
	// the version segment so an arbitrary reconc-prefixed binary does not
	// inherit the exemption.
	rest := strings.TrimSuffix(strings.TrimPrefix(base, "reconc-"), ".exe")
	version, _, found := strings.Cut(rest, "-")
	if !found || version == "" {
		return false
	}
	for _, part := range strings.Split(version, ".") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// isVouchedReconcBinary decides whether the executable behind a repair
// invocation is a Reconc binary this repository vouches for.
//
// The name alone is not identity: an agent can write an executable called
// `reconc` into the repository it is being gated on and inherit the
// stale-lockfile exemption with it. Inside the repository only the managed
// `tools/reconc/` tree qualifies, which is the wrapper and the binaries
// bootstrap installs and tracks by digest. Outside the repository the name
// still decides, because that is where a normally installed CLI, a
// package-manager path, and a developer build all live, and refusing them
// would remove the only escape from a sealed session.
//
// Anything that cannot be resolved keeps the historical behaviour for the same
// reason: a repair that cannot be admitted leaves no reachable recovery.
func isVouchedReconcBinary(repoRoot, token string) bool {
	if repoRoot == "" {
		return true
	}
	executable, ok := resolveCommandExecutable(repoRoot, token)
	if !ok {
		return true
	}
	root, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return true
	}
	relative, err := filepath.Rel(root, executable)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return true
	}
	managed := filepath.FromSlash(managedReconcToolDir) + string(filepath.Separator)
	return strings.HasPrefix(relative, managed)
}

func resolveCommandExecutable(repoRoot, token string) (string, bool) {
	cleaned := strings.TrimSpace(strings.Trim(token, `"'`))
	if cleaned == "" {
		return "", false
	}
	candidate := filepath.FromSlash(cleaned)
	if !strings.ContainsRune(cleaned, '/') && !strings.ContainsRune(cleaned, filepath.Separator) {
		found, err := exec.LookPath(cleaned)
		if err != nil {
			return "", false
		}
		candidate = found
	} else if !filepath.IsAbs(candidate) {
		// A relative command runs against the repository the hook gates.
		candidate = filepath.Join(repoRoot, candidate)
	}
	resolved, err := pathidentity.ResolveExisting(candidate)
	if err != nil {
		return "", false
	}
	return resolved, true
}

// lockfileBlockMessage renders a stale-lockfile refusal that names an escape
// the gate actually admits. The pre-fix message prescribed `reconc refresh .`
// while refusing that exact command, which left no reachable recovery.
func lockfileBlockMessage(surface string, err error) string {
	return "reconc hook (" + surface + "): " + err.Error() + ". Policy cannot be enforced from a lockfile that no longer describes its sources, so gated work stays blocked; " + lockfileRepairHint + "."
}
