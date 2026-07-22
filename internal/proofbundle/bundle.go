// Package proofbundle renders portable, deterministic completion evidence.
package proofbundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/buildprovenance"
	"reconc.dev/reconc/internal/commandproof"
	"reconc.dev/reconc/internal/completiongate"
	"reconc.dev/reconc/internal/policyproof"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"reconc.dev/reconc/internal/schema"
)

const (
	FormatVersion = "1"
	MaxBytes      = 1 << 20
	maxItems      = 256
	maxTextBytes  = 4096
)

var (
	secretAssignment = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|auth[_-]?token|token|secret|password|passwd|authorization)\s*[:=]\s*[^\s,;]+`)
	bearerSecret     = regexp.MustCompile(`(?i)\bbearer\s+[^\s,;]+`)
	knownToken       = regexp.MustCompile(`\b(?:github_pat_|gh[pousr]_|sk-)[A-Za-z0-9_-]{8,}\b`)
	unixAbsolutePath = regexp.MustCompile(`(^|[\s(])/(?:[^\s:;,)]+)`)
	windowsPath      = regexp.MustCompile(`(?i)\b[A-Z]:\\[^\s:;,)]+`)
)

type Build struct {
	Version          string `json:"version"`
	ProvenanceFormat string `json:"provenance_format"`
	SourceDigest     string `json:"source_digest"`
	GOOS             string `json:"goos"`
	GOARCH           string `json:"goarch"`
}

type Task struct {
	Configured bool   `json:"configured"`
	ID         string `json:"id,omitempty"`
	State      string `json:"state"`
}

type Candidate struct {
	Fingerprint      string   `json:"fingerprint"`
	PolicyLockHash   string   `json:"policy_lock_hash"`
	GitAvailable     bool     `json:"git_available"`
	GitHead          string   `json:"git_head,omitempty"`
	GitIndexHash     string   `json:"git_index_hash,omitempty"`
	WorktreeHash     string   `json:"worktree_hash,omitempty"`
	WorktreeTrusted  bool     `json:"worktree_trusted"`
	DirtyPaths       []string `json:"dirty_paths"`
	PolicyReportHash string   `json:"policy_report_hash,omitempty"`
}

type Check struct {
	ID     string                `json:"id"`
	Status completiongate.Status `json:"status"`
	Detail string                `json:"detail"`
}

type CommandProof struct {
	Command        string `json:"command"`
	CommandHash    string `json:"command_hash"`
	ExecutionMode  string `json:"execution_mode"`
	Outcome        string `json:"outcome"`
	ExitCode       int    `json:"exit_code"`
	Head           string `json:"head"`
	IndexTree      string `json:"index_tree"`
	ReceiptDigest  string `json:"receipt_digest"`
	CandidateBound bool   `json:"candidate_bound"`
	Fresh          bool   `json:"fresh"`
}

type Violation struct {
	RuleID            string   `json:"rule_id"`
	Kind              string   `json:"kind"`
	Mode              string   `json:"mode"`
	Message           string   `json:"message"`
	RecommendedAction string   `json:"recommended_action,omitempty"`
	MatchedPaths      []string `json:"matched_paths"`
	RequiredPaths     []string `json:"required_paths"`
	RequiredCommands  []string `json:"required_commands"`
	RequiredClaims    []string `json:"required_claims"`
}

type Evidence struct {
	RequiredCommands []string       `json:"required_commands"`
	RequiredPaths    []string       `json:"required_paths"`
	RequiredClaims   []string       `json:"required_claims"`
	SatisfiedChecks  []string       `json:"satisfied_checks"`
	CommandProofs    []CommandProof `json:"command_proofs"`
}

type SupersededBlock struct {
	CandidateFingerprint string      `json:"candidate_fingerprint"`
	PolicyReportHash     string      `json:"policy_report_hash"`
	Violations           []Violation `json:"violations"`
}

// Bundle is the public proof contract. It intentionally excludes raw session
// events, environment data, absolute paths, and command arguments.
type Bundle struct {
	Schema           string            `json:"$schema"`
	FormatVersion    string            `json:"format_version"`
	OK               bool              `json:"ok"`
	Decision         string            `json:"decision"`
	RepoRoot         string            `json:"repo_root"`
	Build            Build             `json:"build"`
	Task             Task              `json:"task"`
	Candidate        Candidate         `json:"candidate"`
	Checks           []Check           `json:"checks"`
	Evidence         Evidence          `json:"evidence"`
	Violations       []Violation       `json:"violations"`
	SupersededBlocks []SupersededBlock `json:"superseded_blocks"`
	NextAction       string            `json:"next_action,omitempty"`
	CompletionDigest string            `json:"completion_digest"`
	Digest           string            `json:"digest"`
}

// Generate evaluates current state without persisting or changing repository
// data, then converts it to the portable public contract.
func Generate(repo, version string) (*Bundle, error) {
	report, err := completiongate.Evaluate(repo, completiongate.Options{})
	if err != nil {
		return nil, fmt.Errorf("evaluate completion evidence: %w", err)
	}
	root := filepath.Clean(report.RepoRoot)
	bundle := &Bundle{
		Schema: schema.ProofBundleURL, FormatVersion: FormatVersion,
		OK: report.OK, Decision: report.Decision, RepoRoot: ".",
		Build: buildIdentity(version), Task: taskIdentity(report),
		Candidate: candidateIdentity(root, report.Candidate),
		Checks:    []Check{}, Evidence: Evidence{RequiredCommands: []string{}, RequiredPaths: []string{}, RequiredClaims: []string{}, SatisfiedChecks: []string{}, CommandProofs: []CommandProof{}},
		Violations: []Violation{}, SupersededBlocks: []SupersededBlock{},
		NextAction: sanitizeText(root, report.NextAction), CompletionDigest: report.Digest,
	}
	for _, check := range report.Checks {
		bundle.Checks = append(bundle.Checks, Check{ID: sanitizeText(root, check.ID), Status: check.Status, Detail: sanitizeText(root, check.Detail)})
		if check.Status == completiongate.StatusPass {
			bundle.Evidence.SatisfiedChecks = append(bundle.Evidence.SatisfiedChecks, sanitizeText(root, check.ID))
		}
	}
	if report.PolicyReport != nil {
		bundle.Violations = violations(root, report.PolicyReport.Violations)
		bundle.Evidence.RequiredCommands = requiredValues(bundle.Violations, func(v Violation) []string { return v.RequiredCommands })
		bundle.Evidence.RequiredPaths = requiredValues(bundle.Violations, func(v Violation) []string { return v.RequiredPaths })
		bundle.Evidence.RequiredClaims = requiredValues(bundle.Violations, func(v Violation) []string { return v.RequiredClaims })
	}
	if report.Candidate.GitAvailable {
		before, captureErr := commandproof.CaptureCurrent(root)
		if captureErr != nil {
			return nil, fmt.Errorf("capture command-proof candidate: %w", captureErr)
		}
		if before.Head != candidateCommit(report.Candidate.GitHead) {
			return nil, errors.New("git HEAD changed after completion evaluation; retry")
		}
		proofs, loadErr := commandproof.LoadCurrentSuccesses(root, time.Now())
		if loadErr != nil {
			return nil, fmt.Errorf("load current command proofs: %w", loadErr)
		}
		after, captureErr := commandproof.CaptureCurrent(root)
		if captureErr != nil {
			return nil, fmt.Errorf("confirm command-proof candidate: %w", captureErr)
		}
		if after != before {
			return nil, errors.New("git HEAD or staged index changed while exporting proof; retry")
		}
		bundle.Evidence.CommandProofs = commandProofs(proofs, before, report.Candidate)
	}
	latest, found, loadErr := policyproof.LoadLatest(root)
	if loadErr != nil {
		return nil, fmt.Errorf("load latest policy decision: %w", loadErr)
	}
	if found && latest.CandidateFingerprint != report.Candidate.Fingerprint && latest.Report != nil && latest.Report.Decision == runtime.DecisionBlock {
		bundle.SupersededBlocks = append(bundle.SupersededBlocks, SupersededBlock{
			CandidateFingerprint: latest.CandidateFingerprint,
			PolicyReportHash:     latest.PolicyReportHash,
			Violations:           violations(root, latest.Report.Violations),
		})
	}
	stable, stateErr := agentsession.CaptureCompletionState(root)
	if stateErr != nil {
		return nil, fmt.Errorf("confirm completion candidate: %w", stateErr)
	}
	if stable.Fingerprint != report.Candidate.Fingerprint {
		return nil, errors.New("repository, policy, or active-session state changed while exporting proof; retry")
	}
	sortBundle(bundle)
	bundle.Digest = digest(bundle)
	body, err := MarshalJSON(bundle)
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBytes {
		return nil, fmt.Errorf("proof bundle exceeds %d bytes", MaxBytes)
	}
	return bundle, nil
}

func buildIdentity(version string) Build {
	result := Build{Version: strings.TrimSpace(version), ProvenanceFormat: buildprovenance.MarkerPrefix, SourceDigest: "unavailable", GOOS: "unavailable", GOARCH: "unavailable"}
	if provenance, err := buildprovenance.EmbeddedProvenance(); err == nil {
		result.Version = provenance.Version
		result.SourceDigest = provenance.SourceDigest
		result.GOOS = provenance.GOOS
		result.GOARCH = provenance.GOARCH
	}
	return result
}

func taskIdentity(report *completiongate.Report) Task {
	if report.TaskID != "" {
		return Task{Configured: true, ID: report.TaskID, State: "active"}
	}
	for _, check := range report.Checks {
		if strings.HasPrefix(check.ID, "task/") {
			state := "terminal"
			if check.ID == "task/lifecycle" {
				return Task{Configured: false, State: "absent"}
			}
			if check.Status == completiongate.StatusFail {
				state = "invalid"
			}
			return Task{Configured: true, State: state}
		}
	}
	return Task{Configured: false, State: "absent"}
}

func candidateIdentity(root string, value completiongate.CandidateBinding) Candidate {
	return Candidate{
		Fingerprint: value.Fingerprint, PolicyLockHash: value.PolicyLockHash,
		GitAvailable: value.GitAvailable, GitHead: sanitizeText(root, candidateCommit(value.GitHead)), GitIndexHash: sanitizeText(root, value.GitIndexHash),
		WorktreeHash: sanitizeText(root, value.WorktreeHash), WorktreeTrusted: value.WorktreeTrusted,
		DirtyPaths: sanitizePaths(root, value.DirtyPaths), PolicyReportHash: sanitizeText(root, value.PolicyReportHash),
	}
}

func violations(root string, values []runtime.Violation) []Violation {
	result := make([]Violation, 0, min(len(values), maxItems))
	for _, value := range values {
		if len(result) == maxItems {
			break
		}
		result = append(result, Violation{
			RuleID: sanitizeText(root, value.RuleID), Kind: string(value.Kind), Mode: string(value.Mode),
			Message: sanitizeText(root, value.Message), RecommendedAction: sanitizeText(root, value.RecommendedAction),
			MatchedPaths: sanitizePaths(root, value.MatchedPaths), RequiredPaths: sanitizePaths(root, value.RequiredPaths),
			RequiredCommands: sanitizeCommands(value.RequiredCommands), RequiredClaims: sanitizeValues(root, value.RequiredClaims),
		})
	}
	return result
}

func commandProofs(values []commandproof.Proof, snapshot commandproof.Snapshot, candidate completiongate.CandidateBinding) []CommandProof {
	result := make([]CommandProof, 0, min(len(values), maxItems))
	for _, value := range values {
		if len(result) == maxItems {
			break
		}
		normalized := strings.Join(strings.Fields(value.Command), " ")
		result = append(result, CommandProof{
			Command: summarizeCommand(normalized), CommandHash: hashString(normalized), ExecutionMode: sanitizeText("", value.ExecutionMode),
			Outcome: value.Outcome, ExitCode: value.ExitCode, Head: value.Head, IndexTree: value.IndexTree,
			ReceiptDigest:  value.Digest,
			CandidateBound: value.Head == snapshot.Head && value.IndexTree == snapshot.IndexTree && value.Head == candidateCommit(candidate.GitHead),
			Fresh:          true,
		})
	}
	return result
}

func candidateCommit(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "detached:") {
		return strings.TrimSpace(strings.TrimPrefix(value, "detached:"))
	}
	parts := strings.Split(value, "\n")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "missing" {
		return "unborn"
	}
	if strings.HasPrefix(value, "error:") {
		return sanitizeText("", value)
	}
	return last
}

func summarizeCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "<empty>"
	}
	name := filepath.Base(fields[0])
	if len(fields) == 1 {
		return sanitizeText("", name)
	}
	return sanitizeText("", name+" [arguments redacted]")
}

func requiredValues(values []Violation, pick func(Violation) []string) []string {
	var result []string
	for _, value := range values {
		result = append(result, pick(value)...)
	}
	return stableUnique(result)
}

func sanitizeCommands(values []string) []string {
	result := make([]string, 0, min(len(values), maxItems))
	for _, value := range values {
		result = append(result, summarizeCommand(value))
	}
	return stableUnique(result)
}

func sanitizePaths(root string, values []string) []string {
	result := make([]string, 0, min(len(values), maxItems))
	for _, value := range values {
		if len(result) == maxItems {
			break
		}
		value = strings.TrimSpace(value)
		if filepath.IsAbs(value) {
			relative, err := filepath.Rel(root, value)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				value = "<external>"
			} else {
				value = relative
			}
		} else {
			value = path.Clean(strings.ReplaceAll(value, "\\", "/"))
			if value == ".." || strings.HasPrefix(value, "../") {
				value = "<external>"
			}
		}
		result = append(result, filepath.ToSlash(sanitizeText(root, value)))
	}
	return stableUnique(result)
}

func sanitizeValues(root string, values []string) []string {
	result := make([]string, 0, min(len(values), maxItems))
	for _, value := range values {
		result = append(result, sanitizeText(root, value))
	}
	return stableUnique(result)
}

func sanitizeText(root, value string) string {
	value = strings.TrimSpace(value)
	if root != "" {
		value = strings.ReplaceAll(value, filepath.Clean(root), ".")
		if name := filepath.Base(filepath.Clean(root)); name != "." && name != string(filepath.Separator) {
			pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
			value = pattern.ReplaceAllString(value, "<repo>")
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		value = strings.ReplaceAll(value, filepath.Clean(home), "<home>")
	}
	if user := strings.TrimSpace(os.Getenv("USER")); len(user) >= 3 {
		pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(user) + `\b`)
		value = pattern.ReplaceAllString(value, "<user>")
	}
	value = secretAssignment.ReplaceAllString(value, "$1=<redacted>")
	value = bearerSecret.ReplaceAllString(value, "Bearer <redacted>")
	value = knownToken.ReplaceAllString(value, "<redacted>")
	value = unixAbsolutePath.ReplaceAllString(value, "$1<external>")
	value = windowsPath.ReplaceAllString(value, "<external>")
	if len(value) > maxTextBytes {
		value = value[:maxTextBytes] + "...[bounded]"
	}
	return value
}

func stableUnique(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, min(len(values), maxItems))
	for _, value := range values {
		if value == "" || seen[value] || len(result) == maxItems {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortBundle(bundle *Bundle) {
	bundle.Evidence.SatisfiedChecks = stableUnique(bundle.Evidence.SatisfiedChecks)
	sort.Slice(bundle.Checks, func(i, j int) bool { return bundle.Checks[i].ID < bundle.Checks[j].ID })
	sort.Slice(bundle.Violations, func(i, j int) bool { return bundle.Violations[i].RuleID < bundle.Violations[j].RuleID })
	sort.Slice(bundle.Evidence.CommandProofs, func(i, j int) bool {
		if bundle.Evidence.CommandProofs[i].CommandHash == bundle.Evidence.CommandProofs[j].CommandHash {
			return bundle.Evidence.CommandProofs[i].ReceiptDigest < bundle.Evidence.CommandProofs[j].ReceiptDigest
		}
		return bundle.Evidence.CommandProofs[i].CommandHash < bundle.Evidence.CommandProofs[j].CommandHash
	})
	sort.Slice(bundle.SupersededBlocks, func(i, j int) bool {
		return bundle.SupersededBlocks[i].CandidateFingerprint < bundle.SupersededBlocks[j].CandidateFingerprint
	})
	for index := range bundle.SupersededBlocks {
		sort.Slice(bundle.SupersededBlocks[index].Violations, func(i, j int) bool {
			return bundle.SupersededBlocks[index].Violations[i].RuleID < bundle.SupersededBlocks[index].Violations[j].RuleID
		})
	}
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func digest(bundle *Bundle) string {
	copyBundle := *bundle
	copyBundle.Digest = ""
	body, err := json.Marshal(copyBundle)
	if err != nil {
		return ""
	}
	return hashString(string(body))
}

// Verify checks the public contract and its self-digest.
func Verify(bundle *Bundle) error {
	if bundle == nil {
		return errors.New("proof bundle is nil")
	}
	if bundle.Schema != schema.ProofBundleURL || bundle.FormatVersion != FormatVersion || bundle.RepoRoot != "." {
		return errors.New("unsupported proof bundle contract")
	}
	if (bundle.Decision != "pass" && bundle.Decision != "block") || bundle.OK != (bundle.Decision == "pass") {
		return errors.New("proof bundle decision is inconsistent")
	}
	if strings.TrimSpace(bundle.Build.Version) == "" || !validDigest(bundle.Candidate.Fingerprint) || !validDigest(bundle.Candidate.PolicyLockHash) || !validDigest(bundle.CompletionDigest) {
		return errors.New("proof bundle identity is incomplete")
	}
	validTaskState := bundle.Task.State == "absent" || bundle.Task.State == "active" || bundle.Task.State == "terminal" || bundle.Task.State == "invalid"
	if !validTaskState || bundle.Task.Configured == (bundle.Task.State == "absent") {
		return errors.New("proof bundle TASK identity is inconsistent")
	}
	if bundle.Checks == nil || bundle.Violations == nil || bundle.SupersededBlocks == nil || bundle.Candidate.DirtyPaths == nil || bundle.Evidence.RequiredCommands == nil || bundle.Evidence.RequiredPaths == nil || bundle.Evidence.RequiredClaims == nil || bundle.Evidence.SatisfiedChecks == nil || bundle.Evidence.CommandProofs == nil {
		return errors.New("proof bundle contains a null collection")
	}
	for _, check := range bundle.Checks {
		if check.ID == "" || (check.Status != completiongate.StatusPass && check.Status != completiongate.StatusWarn && check.Status != completiongate.StatusFail) {
			return errors.New("proof bundle contains an invalid check")
		}
	}
	for _, proof := range bundle.Evidence.CommandProofs {
		if !proof.CandidateBound || !proof.Fresh || proof.Outcome != "success" || proof.ExitCode != 0 || !validDigest(proof.CommandHash) || !validDigest(proof.ReceiptDigest) {
			return errors.New("proof bundle contains an invalid command proof")
		}
	}
	if expected := digest(bundle); expected == "" || !equalDigest(expected, bundle.Digest) {
		return errors.New("proof bundle digest mismatch")
	}
	return nil
}

// MarshalJSON returns canonical indented JSON with one trailing newline.
func MarshalJSON(bundle *Bundle) ([]byte, error) {
	if err := Verify(bundle); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal proof bundle: %w", err)
	}
	body = append(body, '\n')
	if len(body) > MaxBytes {
		return nil, fmt.Errorf("proof bundle exceeds %d bytes", MaxBytes)
	}
	return body, nil
}

// RenderMarkdown renders the same typed fields as a standalone review report.
func RenderMarkdown(writer io.Writer, bundle *Bundle) error {
	if err := Verify(bundle); err != nil {
		return err
	}
	var output bytes.Buffer
	fmt.Fprintln(&output, "# Reconc Proof Bundle")
	fmt.Fprintf(&output, "\nSchema `%s`, format `%s`.  \nDecision: **%s**  \nBundle digest: `%s`\n", bundle.Schema, bundle.FormatVersion, strings.ToUpper(bundle.Decision), bundle.Digest)
	fmt.Fprintf(&output, "Completion report: `%s`.\n", bundle.CompletionDigest)
	fmt.Fprintf(&output, "\n## Build\n\nVersion `%s`, target `%s/%s`, source `%s`, provenance `%s`.\n", bundle.Build.Version, bundle.Build.GOOS, bundle.Build.GOARCH, bundle.Build.SourceDigest, bundle.Build.ProvenanceFormat)
	fmt.Fprintf(&output, "\n## TASK\n\nConfigured: `%t`; state: `%s`", bundle.Task.Configured, bundle.Task.State)
	if bundle.Task.ID != "" {
		fmt.Fprintf(&output, "; ID: `%s`", bundle.Task.ID)
	}
	fmt.Fprintln(&output, ".")
	fmt.Fprintln(&output, "\n## Candidate")
	fmt.Fprintf(&output, "\nFingerprint `%s`; policy `%s`; Git available `%t`; worktree trusted `%t`.\n", bundle.Candidate.Fingerprint, bundle.Candidate.PolicyLockHash, bundle.Candidate.GitAvailable, bundle.Candidate.WorktreeTrusted)
	for _, field := range []struct{ label, value string }{
		{"HEAD", bundle.Candidate.GitHead},
		{"Index", bundle.Candidate.GitIndexHash},
		{"Worktree", bundle.Candidate.WorktreeHash},
		{"Policy report", bundle.Candidate.PolicyReportHash},
	} {
		if field.value != "" {
			fmt.Fprintf(&output, "\n- %s: `%s`", field.label, field.value)
		}
	}
	fmt.Fprintln(&output)
	renderValues(&output, "Dirty paths", bundle.Candidate.DirtyPaths)
	fmt.Fprintln(&output, "\n## Checks\n\n| Check | Status | Detail |\n|---|---|---|")
	for _, check := range bundle.Checks {
		fmt.Fprintf(&output, "| `%s` | %s | %s |\n", markdown(check.ID), check.Status, markdown(check.Detail))
	}
	fmt.Fprintln(&output, "\n## Evidence")
	renderValues(&output, "Required commands", bundle.Evidence.RequiredCommands)
	renderValues(&output, "Required paths", bundle.Evidence.RequiredPaths)
	renderValues(&output, "Required claims", bundle.Evidence.RequiredClaims)
	renderValues(&output, "Satisfied checks", bundle.Evidence.SatisfiedChecks)
	if len(bundle.Evidence.CommandProofs) == 0 {
		fmt.Fprintln(&output, "\nNo current command proof receipts.")
	} else {
		fmt.Fprintln(&output, "\n| Command | Outcome | Candidate bound | Fresh | HEAD | Index tree | Receipt |\n|---|---|---:|---:|---|---|---|")
		for _, proof := range bundle.Evidence.CommandProofs {
			fmt.Fprintf(&output, "| `%s` (`%s`) | %s/%s (%d) | %t | %t | `%s` | `%s` | `%s` |\n", markdown(proof.Command), proof.CommandHash, proof.ExecutionMode, proof.Outcome, proof.ExitCode, proof.CandidateBound, proof.Fresh, proof.Head, proof.IndexTree, proof.ReceiptDigest)
		}
	}
	renderViolations(&output, "Violations", bundle.Violations)
	if len(bundle.SupersededBlocks) > 0 {
		fmt.Fprintln(&output, "\n## Superseded Blocks")
		for _, block := range bundle.SupersededBlocks {
			fmt.Fprintf(&output, "\nCandidate `%s`, policy report `%s`.\n", block.CandidateFingerprint, block.PolicyReportHash)
			renderViolations(&output, "Block details", block.Violations)
		}
	}
	if bundle.NextAction != "" {
		fmt.Fprintf(&output, "\n## Next Action\n\n%s\n", bundle.NextAction)
	}
	if output.Len() > MaxBytes {
		return fmt.Errorf("proof bundle Markdown exceeds %d bytes", MaxBytes)
	}
	_, err := writer.Write(output.Bytes())
	return err
}

func renderValues(writer io.Writer, label string, values []string) {
	fmt.Fprintf(writer, "\n%s:", label)
	if len(values) == 0 {
		fmt.Fprintln(writer, " none.")
		return
	}
	fmt.Fprintln(writer)
	for _, value := range values {
		fmt.Fprintf(writer, "- `%s`\n", markdown(value))
	}
}

func renderViolations(writer io.Writer, title string, values []Violation) {
	fmt.Fprintf(writer, "\n## %s\n", title)
	if len(values) == 0 {
		fmt.Fprintln(writer, "\nNone.")
		return
	}
	for _, value := range values {
		fmt.Fprintf(writer, "\n### `%s`\n\nKind `%s`, mode `%s`. %s\n", markdown(value.RuleID), value.Kind, value.Mode, markdown(value.Message))
		if value.RecommendedAction != "" {
			fmt.Fprintf(writer, "\nRemediation: %s\n", markdown(value.RecommendedAction))
		}
		renderValues(writer, "Matched paths", value.MatchedPaths)
		renderValues(writer, "Required paths", value.RequiredPaths)
		renderValues(writer, "Required commands", value.RequiredCommands)
		renderValues(writer, "Required claims", value.RequiredClaims)
	}
}

func markdown(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "`", "'")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func equalDigest(left, right string) bool {
	leftBytes, leftErr := hex.DecodeString(left)
	rightBytes, rightErr := hex.DecodeString(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
