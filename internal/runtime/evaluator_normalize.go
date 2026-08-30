package runtime

import (
	"bytes"
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
	repoRoot = strings.TrimRight(strings.TrimSpace(repoRoot), "/")
	normalizer := commandSemanticNormalizer{
		output:   make([]byte, 0, len(cmd)),
		repoRoot: repoRoot,
		leading:  true,
	}
	if repoRoot != "" {
		normalizer.cleanedRoot = path.Clean(repoRoot)
	}
	return normalizer.normalize(cmd)
}

type commandSemanticNormalizer struct {
	output            []byte
	repoRoot          string
	cleanedRoot       string
	segmentStart      int
	quote             byte
	escaped           bool
	substitutionDepth int
	pendingSpace      bool
	lastSeparator     bool
	leading           bool
}

func (n *commandSemanticNormalizer) normalize(command string) string {
	for index := 0; index < len(command); index++ {
		current := command[index]
		if n.consumeQuotedOrEscaped(command, &index, current) {
			continue
		}
		if current == '\r' || current == '\n' {
			n.consumeNewline(command, &index, current)
			continue
		}
		if current == ' ' || current == '\t' {
			n.pendingSpace = true
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			n.flushSpace()
			n.quote = current
			n.output = append(n.output, current)
			n.lastSeparator = false
			continue
		}
		if n.consumeSubstitutionParen(current) {
			continue
		}
		if n.substitutionDepth == 0 {
			if separator, end := commandSeparatorAt(command, index, n.previousByte()); separator != "" {
				n.finishSegment(separator)
				index = end
				continue
			}
		}
		n.writeUnquoted(current)
	}
	n.finishSegment("")
	for len(n.output) > 0 && n.output[len(n.output)-1] == ' ' {
		n.output = n.output[:len(n.output)-1]
	}
	return string(n.output)
}

func (n *commandSemanticNormalizer) consumeQuotedOrEscaped(command string, index *int, current byte) bool {
	if n.escaped {
		n.flushSpace()
		n.output = append(n.output, current)
		n.lastSeparator = false
		n.escaped = false
		return true
	}
	if n.quote == '\'' {
		n.output = append(n.output, current)
		n.lastSeparator = false
		if current == '\'' {
			n.quote = 0
		}
		return true
	}
	if current == '\\' {
		if end, ok := lineContinuationEnd(command, *index); ok {
			*index = end
			return true
		}
		n.flushSpace()
		n.output = append(n.output, current)
		n.escaped = true
		return true
	}
	if n.quote != 0 {
		n.output = append(n.output, current)
		n.lastSeparator = false
		if current == n.quote {
			n.quote = 0
		}
		return true
	}
	return false
}

func (n *commandSemanticNormalizer) consumeNewline(command string, index *int, current byte) {
	if current == '\r' && *index+1 < len(command) && command[*index+1] == '\n' {
		*index = *index + 1
	}
	n.pendingSpace = false
	if n.lastSeparator || len(n.output) == 0 {
		return
	}
	if n.substitutionDepth > 0 {
		n.appendSeparator(";")
		n.lastSeparator = true
		return
	}
	n.finishSegment(";")
}

func (n *commandSemanticNormalizer) consumeSubstitutionParen(current byte) bool {
	if current != '(' && current != ')' {
		return false
	}
	n.flushSpace()
	if current == '(' && (n.substitutionDepth > 0 || n.previousByte() == '$') {
		n.substitutionDepth++
	} else if current == ')' && n.substitutionDepth > 0 {
		n.substitutionDepth--
	}
	n.output = append(n.output, current)
	n.lastSeparator = false
	return true
}

func (n *commandSemanticNormalizer) writeUnquoted(current byte) {
	n.flushSpace()
	n.output = append(n.output, current)
	n.lastSeparator = current == ';' || current == '|' || current == '&'
}

func (n *commandSemanticNormalizer) flushSpace() {
	if n.pendingSpace && len(n.output) > n.segmentStart && n.output[len(n.output)-1] != ' ' {
		n.output = append(n.output, ' ')
	}
	n.pendingSpace = false
}

func (n *commandSemanticNormalizer) previousByte() byte {
	if n.pendingSpace || len(n.output) == 0 {
		return 0
	}
	return n.output[len(n.output)-1]
}

func (n *commandSemanticNormalizer) finishSegment(separator string) {
	n.pendingSpace = false
	n.normalizeSegment()
	body := n.output[n.segmentStart:]
	if n.leading && bytes.Equal(body, []byte("cd .")) && (separator == "&&" || separator == ";") {
		n.output = n.output[:n.segmentStart]
	} else {
		n.leading = false
		n.appendSeparator(separator)
	}
	n.segmentStart = len(n.output)
	n.lastSeparator = separator != ""
}

func (n *commandSemanticNormalizer) normalizeSegment() {
	body := bytes.TrimSpace(n.output[n.segmentStart:])
	for bytes.HasPrefix(body, []byte("rtk ")) {
		body = bytes.TrimSpace(body[len("rtk "):])
	}
	n.output = n.output[:n.segmentStart]
	if n.repoRoot == "" || !bytes.HasPrefix(body, []byte("cd ")) {
		n.output = append(n.output, body...)
		return
	}
	argument := strings.TrimSpace(string(body[len("cd "):]))
	if strings.HasPrefix(argument, "\"") || strings.HasPrefix(argument, "'") {
		n.output = append(n.output, body...)
		return
	}
	cleaned := path.Clean(argument)
	switch {
	case cleaned == n.cleanedRoot:
		n.output = append(n.output, "cd ."...)
	case strings.HasPrefix(cleaned, n.cleanedRoot+"/"):
		n.output = append(n.output, "cd "...)
		n.output = append(n.output, strings.TrimPrefix(cleaned, n.cleanedRoot+"/")...)
	default:
		n.output = append(n.output, body...)
	}
}

func (n *commandSemanticNormalizer) appendSeparator(separator string) {
	if separator == "" {
		return
	}
	if len(n.output) > 0 && n.output[len(n.output)-1] != ' ' {
		n.output = append(n.output, ' ')
	}
	n.output = append(n.output, separator...)
	n.output = append(n.output, ' ')
}

func commandSeparatorAt(command string, index int, previous byte) (string, int) {
	current := command[index]
	next, nextIndex := nextCommandByte(command, index+1)
	switch current {
	case ';':
		return ";", index
	case '|':
		if next == '|' {
			return "||", nextIndex
		}
		if next == '&' {
			return "|&", nextIndex
		}
		return "|", index
	case '&':
		if next == '&' {
			return "&&", nextIndex
		}
		if previous == '>' || next == '>' {
			return "", index
		}
		return "&", index
	default:
		return "", index
	}
}

func nextCommandByte(command string, index int) (byte, int) {
	for index < len(command) {
		if end, ok := lineContinuationEnd(command, index); ok {
			index = end + 1
			continue
		}
		return command[index], index
	}
	return 0, index
}

func lineContinuationEnd(command string, index int) (int, bool) {
	if index+1 < len(command) && command[index] == '\\' && command[index+1] == '\n' {
		return index + 1, true
	}
	if index+2 < len(command) && command[index] == '\\' && command[index+1] == '\r' && command[index+2] == '\n' {
		return index + 2, true
	}
	return index, false
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
