package runtime

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/shellcommand"
)

func normalizePaths(paths []string, root string) ([]string, error) {
	resolvedRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repo filesystem identity: %w", err)
	}
	return normalizePathsWithResolvedRoot(paths, resolvedRoot)
}

// normalizePathsWithResolvedRoot is the per-path normalization core. Callers
// on the check hot path resolve the repo root identity once via
// pathidentity.ResolveExisting and reuse it for every path, instead of paying
// the symlink/reparse resolution cost once per evidence path.
func normalizePathsWithResolvedRoot(paths []string, resolvedRoot string) ([]string, error) {
	prospective := pathidentity.NewProspectiveResolver()
	return normalizePathsWithResolver(paths, resolvedRoot, prospective)
}

func normalizePathsWithResolver(paths []string, resolvedRoot string, prospective *pathidentity.ProspectiveResolver) ([]string, error) {
	out := []string{}
	if prospective == nil {
		prospective = pathidentity.NewProspectiveResolver()
	}
	for _, raw := range paths {
		posix, keep, err := normalizePathWithResolver(raw, resolvedRoot, prospective)
		if err != nil {
			return nil, err
		}
		if keep {
			out = append(out, posix)
		}
	}
	return out, nil
}

func normalizePathWithResolver(raw, resolvedRoot string, prospective *pathidentity.ProspectiveResolver) (string, bool, error) {
	candidate := raw
	if candidate == "" {
		return "", false, nil
	}
	// Convert only separators native to the current platform. On POSIX a
	// backslash is a legal filename byte and must not be conflated with '/'.
	candidate = filepath.ToSlash(candidate)
	var absPath string
	if path.IsAbs(candidate) || filepath.IsAbs(candidate) {
		absPath = candidate
	} else {
		absPath = filepath.Join(resolvedRoot, candidate)
	}
	cleaned := filepath.Clean(absPath)
	if prospective == nil {
		prospective = pathidentity.NewProspectiveResolver()
	}
	cleaned, err := prospective.Resolve(cleaned)
	if err != nil {
		return "", false, fmt.Errorf("resolve evidence path %q: %w", raw, err)
	}

	rel, err := filepath.Rel(resolvedRoot, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false, &rerrors.RepoBoundaryError{Path: raw, RepoRoot: resolvedRoot}
	}
	// Convert OS-native to POSIX
	posix := filepath.ToSlash(rel)
	if posix == "." {
		return "", false, nil
	}
	return posix, true, nil
}

func normalizeWriteEpochs(paths []string, epochs map[string]uint64, root string) (map[string]uint64, error) {
	resolvedRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repo filesystem identity: %w", err)
	}
	return normalizeWriteEpochsWithResolvedRoot(paths, epochs, resolvedRoot)
}

// normalizeWriteEpochsWithResolvedRoot maps raw write-path epoch keys onto
// their normalized repo-relative form. It reuses the caller-resolved repo
// root so the check hot path resolves the root identity once instead of once
// per write path.
func normalizeWriteEpochsWithResolvedRoot(paths []string, epochs map[string]uint64, resolvedRoot string) (map[string]uint64, error) {
	prospective := pathidentity.NewProspectiveResolver()
	return normalizeWriteEpochsWithResolver(paths, epochs, resolvedRoot, prospective)
}

func normalizeWriteEpochsWithResolver(paths []string, epochs map[string]uint64, resolvedRoot string, prospective *pathidentity.ProspectiveResolver) (map[string]uint64, error) {
	out := make(map[string]uint64, len(epochs))
	if prospective == nil {
		prospective = pathidentity.NewProspectiveResolver()
	}
	for _, raw := range paths {
		normalized, keep, err := normalizePathWithResolver(raw, resolvedRoot, prospective)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		epoch := epochs[raw]
		if epoch == 0 {
			epoch = epochs[filepath.ToSlash(raw)]
		}
		if epoch > out[normalized] {
			out[normalized] = epoch
		}
	}
	return out, nil
}

// RelativizeEpochKeys bridges the write-epoch key formats between agent
// sessions and git-derived paths: session hooks record epochs under the
// absolute tool payload path, while ci derives write paths repo-relative from
// git diff. Every absolute key under root gains a repo-relative slash alias
// (the original key is kept), so epoch lookups by either spelling hit the
// recorded value instead of silently reading zero and disabling the
// command-after-last-edit binding.
func RelativizeEpochKeys(root string, epochs map[string]uint64) map[string]uint64 {
	if len(epochs) == 0 {
		return epochs
	}
	resolvedRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return epochs
	}
	out := make(map[string]uint64, len(epochs)*2)
	for key, epoch := range epochs {
		if epoch > out[key] {
			out[key] = epoch
		}
		candidate := filepath.FromSlash(key)
		if !filepath.IsAbs(candidate) {
			continue
		}
		cleaned, resolveErr := pathidentity.ResolveProspective(filepath.Clean(candidate))
		if resolveErr != nil {
			continue
		}
		rel, relErr := filepath.Rel(resolvedRoot, cleaned)
		if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		relKey := filepath.ToSlash(rel)
		if epoch > out[relKey] {
			out[relKey] = epoch
		}
	}
	return out
}

// normalizeWhitespace collapses every run of whitespace (spaces,
// tabs, newlines) into a single space and trims leading/trailing
// whitespace. Used for command + claim matching so policy-side
// `"go test"` matches agent-reported `"go  test"` / `"go\ttest"`
// Empty / whitespace-only strings become empty.
func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// normalizeCommandSemantics applies idempotent semantic normalisations
// that reflect transparent wrapping by other tools so that
// require_command / require_command_success / forbid_command rules can
// be authored in their literal-intent form even when the recorded
// command was rewritten by a CLI proxy or anchored to an absolute path
// by the agent runtime.
//
// Two normalisations are layered on top of normalizeWhitespace:
//
//  1. RTK proxy prefix: the literal token "rtk " is stripped at the
//     start of the command and after every unquoted shell compound boundary
//     (&&, ||, ;, |, |&, &), with or without surrounding whitespace. The trailing space in the
//     match prevents false positives like a directory named /rtkfoo or
//     a literal command named rtk-tool.
//  2. Absolute repo path in cd: "cd <repoRoot>" becomes "cd ." and
//     "cd <repoRoot>/<sub>" becomes "cd <sub>", but only when the cd
//     argument is the repo root prefix at a segment boundary. A
//     command like `echo /<repoRoot>` is untouched because no `cd ` is
//     at segment start.
//
// repoRoot may be empty; in that case only the RTK prefix strip runs.
// The transformation is applied to BOTH sides of every command match
// so forbid-rule semantics stay exact (a literal `rm -rf /` still
// matches only `rm -rf /` and `rtk rm -rf /`, not `echo "rm -rf /"`).
//
// Applied repeatedly the function is idempotent: every pass after the
// first returns the same string.
func normalizeCommandSemantics(cmd, repoRoot string) string {
	cmd = normalizeShellWhitespace(cmd)
	if cmd == "" {
		return ""
	}
	repoRoot = strings.TrimRight(strings.TrimSpace(repoRoot), "/")
	segments := splitCommandSegments(cmd)
	for i := range segments {
		segments[i].body = normalizeSegmentBody(segments[i].body, repoRoot)
	}
	// Drop leading `cd .` segments left by agents that anchor commands with
	// an explicit cd into the repo root: `cd /abs/repo && X` is semantically
	// `X` when /abs/repo IS the repo root. Only `&&` and `;` joins are safe
	// to drop; `cd . || X` or `cd . | X` would change meaning and stay as-is.
	for len(segments) >= 2 && segments[0].body == "cd ." &&
		(segments[1].sep == " && " || segments[1].sep == " ; ") {
		segments = segments[1:]
		segments[0].sep = ""
	}
	var out strings.Builder
	for i, s := range segments {
		if i > 0 {
			out.WriteString(s.sep)
		}
		out.WriteString(s.body)
	}
	return normalizeShellWhitespace(out.String())
}

// commandSegment is one slice of a normalized command between shell
// compound boundaries. The first segment has sep == "".
type commandSegment struct {
	sep  string
	body string
}

// commandSegmentSeparators lists the shell compound boundaries that start a
// new command position. Shell does not require surrounding whitespace, so the
// scanner canonicalizes both `a&&b` and `a && b` without inspecting quoted
// literal data.
var commandSegmentSeparators = []string{"&&", "||", "|&", ";", "|", "&"}

// splitCommandSegments splits a whitespace-normalized command into
// segments at every shell compound boundary while preserving the
// separators so the command can be reconstructed verbatim.
func splitCommandSegments(cmd string) []commandSegment {
	segments := make([]commandSegment, 0, 4)
	start := 0
	nextSeparator := ""
	var quote byte
	escaped := false
	substitutionDepth := 0
	for index := 0; index < len(cmd); index++ {
		current := cmd[index]
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			continue
		}
		if current == '(' && (substitutionDepth > 0 || index > 0 && cmd[index-1] == '$') {
			substitutionDepth++
			continue
		}
		if current == ')' && substitutionDepth > 0 {
			substitutionDepth--
			continue
		}
		if substitutionDepth > 0 {
			continue
		}
		for _, separator := range commandSegmentSeparators {
			if !strings.HasPrefix(cmd[index:], separator) {
				continue
			}
			if separator == "&" && ((index > 0 && cmd[index-1] == '>') || (index+1 < len(cmd) && cmd[index+1] == '>')) {
				continue
			}
			segments = append(segments, commandSegment{sep: nextSeparator, body: cmd[start:index]})
			nextSeparator = " " + separator + " "
			index += len(separator) - 1
			start = index + 1
			break
		}
	}
	segments = append(segments, commandSegment{sep: nextSeparator, body: cmd[start:]})
	return segments
}

// normalizeShellWhitespace collapses only unquoted shell whitespace. Literal
// spaces inside single quotes, double quotes, backticks, or an escaped token
// are semantic data and must survive command normalization byte-for-byte.
func normalizeShellWhitespace(command string) string {
	command = shellcommand.StripLineContinuations(command)
	var normalized strings.Builder
	normalized.Grow(len(command))
	var quote byte
	escaped := false
	pendingSpace := false
	lastUnquotedSeparator := false
	flushSpace := func() {
		if pendingSpace && normalized.Len() > 0 {
			normalized.WriteByte(' ')
		}
		pendingSpace = false
	}
	for index := 0; index < len(command); index++ {
		current := command[index]
		if escaped {
			flushSpace()
			normalized.WriteByte(current)
			lastUnquotedSeparator = false
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			flushSpace()
			normalized.WriteByte(current)
			escaped = true
			continue
		}
		if quote != 0 {
			normalized.WriteByte(current)
			lastUnquotedSeparator = false
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			flushSpace()
			quote = current
			normalized.WriteByte(current)
			lastUnquotedSeparator = false
			continue
		}
		if current == '\r' || current == '\n' {
			flushSpace()
			if normalized.Len() > 0 && !lastUnquotedSeparator {
				normalized.WriteString(" ; ")
			}
			lastUnquotedSeparator = true
			if current == '\r' && index+1 < len(command) && command[index+1] == '\n' {
				index++
			}
			continue
		}
		if current == ' ' || current == '\t' {
			pendingSpace = true
			continue
		}
		flushSpace()
		normalized.WriteByte(current)
		lastUnquotedSeparator = current == ';' || current == '|' || current == '&'
	}
	return normalized.String()
}

// normalizeSegmentBody applies the two semantic normalisations to one
// command segment body (the text between compound boundaries).
//
// The cd-arg path is cleaned via path.Clean before comparison so that
// `cd /repo/`, `cd /repo/.`, `cd /repo//sub`, and `cd /repo/sub/..` all
// resolve to their canonical forms relative to repoRoot. Cleaning is
// only applied to unquoted absolute-style arguments to avoid touching
// `cd "/path with spaces"` and similar quoted forms.
func normalizeSegmentBody(body, repoRoot string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return body
	}
	// Every pass consumes at least the complete four-byte "rtk " token, so
	// the input-derived bound proves convergence even for adversarial stacks.
	maximumWrappers := len(body) / len("rtk ")
	for range maximumWrappers {
		if !strings.HasPrefix(body, "rtk ") {
			break
		}
		body = strings.TrimSpace(body[len("rtk "):])
	}
	if repoRoot != "" && strings.HasPrefix(body, "cd ") {
		arg := strings.TrimSpace(body[len("cd "):])
		// Skip quote-wrapped arguments — path.Clean would mishandle
		// embedded spaces in quoted shell tokens.
		if !strings.HasPrefix(arg, "\"") && !strings.HasPrefix(arg, "'") {
			cleaned := path.Clean(arg)
			cleanedRoot := path.Clean(repoRoot)
			if cleaned == cleanedRoot {
				body = "cd ."
			} else if strings.HasPrefix(cleaned, cleanedRoot+"/") {
				body = "cd " + strings.TrimPrefix(cleaned, cleanedRoot+"/")
			}
		}
	}
	return body
}

func normalizeCommands(commands []string) []string {
	out := make([]string, 0, len(commands))
	for _, c := range commands {
		norm := normalizeShellWhitespace(c)
		if norm != "" {
			out = append(out, norm)
		}
	}
	return out
}

func normalizeCommandResults(results []CommandResult) []CommandResult {
	out := make([]CommandResult, 0, len(results))
	for _, r := range results {
		c := normalizeShellWhitespace(r.Command)
		if c == "" {
			continue
		}
		out = append(out, CommandResult{Command: c, Outcome: r.Outcome, EvidenceEpoch: r.EvidenceEpoch})
	}
	return out
}

func dedupePreservingOrder(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// --- Per-rule evaluation ---
