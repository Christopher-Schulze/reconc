// Package completiongate composes policy, candidate, and typed TASK evidence
// into the single final-completion contract exposed by reconc done.
package completiongate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/policyproof"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"reconc.dev/reconc/internal/schema"
	"reconc.dev/reconc/internal/tasklifecycle"
)

const FormatVersion = "1"

const completionEvaluationAttempts = 2

const completionRetryExhaustedFormat = "repository, policy, or active-session state changed during completion evaluation after %d attempts; retry limit exhausted"

// RetryableStateDriftError identifies a completion attempt whose before and
// after snapshots no longer describe one coherent candidate.
type RetryableStateDriftError struct{}

func (e *RetryableStateDriftError) Error() string {
	return "repository, policy, or active-session state changed during completion evaluation; retry"
}

// RetryExhaustedError identifies completion evaluation that consumed every
// retry because candidate state kept drifting. It unwraps the last typed drift
// cause while preserving the stable operator-facing diagnostic.
type RetryExhaustedError struct {
	attempts int
	cause    error
}

func (e *RetryExhaustedError) Error() string {
	return fmt.Sprintf(completionRetryExhaustedFormat, e.attempts)
}

func (e *RetryExhaustedError) Unwrap() error {
	return e.cause
}

// Attempts returns the number of complete evaluations consumed.
func (e *RetryExhaustedError) Attempts() int {
	return e.attempts
}

type completionStateCapture func(string) (agentsession.CompletionStateSnapshot, error)

type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Options selects explicit policy choices without weakening the mandatory
// policy/TASK/state checks. WindowMinutes is retained only for CLI input
// compatibility and never manufactures a pass by waiting.
type Options struct {
	RequireCleanGit bool
	WindowMinutes   int
	PersistDecision bool
	DecisionEvent   string
}

// Check is one stable, machine-addressable completion invariant.
type Check struct {
	ID         string `json:"id"`
	Status     Status `json:"status"`
	Detail     string `json:"detail"`
	nextAction string
}

// CandidateBinding identifies exactly what the final gate evaluated.
type CandidateBinding struct {
	Fingerprint         string   `json:"fingerprint"`
	PolicyLockHash      string   `json:"policy_lock_hash"`
	GitAvailable        bool     `json:"git_available"`
	GitHead             string   `json:"git_head,omitempty"`
	GitIndexHash        string   `json:"git_index_hash,omitempty"`
	WorktreeHash        string   `json:"worktree_hash,omitempty"`
	WorktreeTrusted     bool     `json:"worktree_trusted"`
	DirtyPaths          []string `json:"dirty_paths"`
	SessionEvidenceHash string   `json:"session_evidence_hash,omitempty"`
	SessionReportHash   string   `json:"session_report_hash,omitempty"`
	PolicyReportHash    string   `json:"policy_report_hash,omitempty"`
}

// Report is the versioned final-completion result shared by CLI, TUI, and the
// later proof-bundle renderer. Digest covers the report with Digest omitted.
type Report struct {
	Schema        string               `json:"$schema"`
	FormatVersion string               `json:"format_version"`
	OK            bool                 `json:"ok"`
	Decision      string               `json:"decision"`
	RepoRoot      string               `json:"repo_root"`
	TaskID        string               `json:"task_id,omitempty"`
	Checks        []Check              `json:"checks"`
	NextAction    string               `json:"next_action,omitempty"`
	Candidate     CandidateBinding     `json:"candidate"`
	PolicyReport  *runtime.CheckReport `json:"policy_report,omitempty"`
	Digest        string               `json:"digest"`
}

type reportPayload struct {
	Schema        string               `json:"$schema"`
	FormatVersion string               `json:"format_version"`
	OK            bool                 `json:"ok"`
	Decision      string               `json:"decision"`
	RepoRoot      string               `json:"repo_root"`
	TaskID        string               `json:"task_id,omitempty"`
	Checks        []Check              `json:"checks"`
	NextAction    string               `json:"next_action,omitempty"`
	Candidate     CandidateBinding     `json:"candidate"`
	PolicyReport  *runtime.CheckReport `json:"policy_report,omitempty"`
}

// Evaluate runs the complete non-destructive gate. A single retry is reserved
// for typed candidate drift; every attempt captures fresh state and builds a
// fresh report. PersistDecision writes only the tamper-evident latest policy
// receipt under RECONC_HOME; governed worktree content is never changed.
func Evaluate(repo string, options Options) (*Report, error) {
	return evaluateWithRetries(func() (*Report, error) {
		return evaluateOnce(repo, options)
	})
}

func evaluateWithRetries(attempt func() (*Report, error)) (*Report, error) {
	var lastDrift error
	for number := 1; number <= completionEvaluationAttempts; number++ {
		report, err := attempt()
		if err == nil {
			return report, nil
		}
		var drift *RetryableStateDriftError
		if !errors.As(err, &drift) {
			return nil, err
		}
		lastDrift = err
		if number == completionEvaluationAttempts {
			return nil, &RetryExhaustedError{attempts: number, cause: lastDrift}
		}
	}
	return nil, &RetryExhaustedError{attempts: completionEvaluationAttempts, cause: lastDrift}
}

func evaluateOnce(repo string, options Options) (*Report, error) {
	stateBefore, err := agentsession.CaptureCompletionState(repo)
	if err != nil {
		return nil, err
	}
	report := &Report{
		Schema: schema.Resolve(schema.CompletionReport), FormatVersion: FormatVersion,
		RepoRoot: stateBefore.RepoRoot, Checks: []Check{},
		Candidate: candidateFromState(stateBefore),
	}
	add := func(id string, status Status, detail, next string) {
		if status == StatusFail && strings.TrimSpace(next) == "" {
			next = "Resolve completion check `" + id + "`, then rerun `reconc done .`."
		}
		report.Checks = append(report.Checks, Check{ID: id, Status: status, Detail: detail, nextAction: next})
	}

	if stateBefore.EvidenceOverflow {
		detail := "bounded active-session evidence overflowed"
		if stateBefore.EvidenceOverflowReason != "" {
			detail += " at " + stateBefore.EvidenceOverflowReason
		}
		if stateBefore.EvidenceOverflowLimit != "" {
			detail += " due to " + stateBefore.EvidenceOverflowLimit
		}
		add("session/evidence-complete", StatusFail, detail, "Resolve the persisted evidence taint and reproduce the required evidence before rerunning `reconc done .`.")
	} else {
		add("session/evidence-complete", StatusPass, "active-session evidence is bounded", "")
	}
	if stateBefore.SessionReportTrusted {
		add("session/report-integrity", StatusPass, "saved session report is absent or hash-consistent", "")
	} else {
		add("session/report-integrity", StatusFail, "saved session report does not match its recorded hash", "Rerun the relevant policy check in the active session, then rerun `reconc done .`.")
	}
	switch {
	case !stateBefore.GitAvailable:
		add("git/status", StatusPass, "not a Git work tree; Git candidate binding is not applicable", "")
	case !stateBefore.GitStatusOK:
		add("git/status", StatusFail, "Git status could not be verified", "Restore Git status visibility, then rerun `reconc done .`.")
	case strings.HasPrefix(stateBefore.GitHead, "error:"):
		add("git/status", StatusFail, "Git HEAD could not be verified", "Restore Git HEAD visibility, then rerun `reconc done .`.")
	case stateBefore.GitIndexHash == "":
		add("git/status", StatusFail, "Git index identity could not be verified", "Restore Git index visibility, then rerun `reconc done .`.")
	case !stateBefore.WorktreeTrusted:
		add("git/status", StatusFail, "dirty worktree content could not be bound safely", "Restore readable regular-file or submodule state, then rerun `reconc done .`.")
	default:
		add("git/status", StatusPass, "Git HEAD, index, and worktree status are readable", "")
	}

	lockfileValid := true
	if err := runtime.ValidatePolicyLockfile(stateBefore.RepoRoot); err != nil {
		lockfileValid = false
		add("policy/lockfile", StatusFail, err.Error(), "Run `reconc refresh .`, then rerun the blocked policy check and `reconc done .`.")
	} else {
		add("policy/lockfile", StatusPass, "compiled policy lockfile is current", "")
	}

	unresolvedDecision := false
	latest, found, proofErr := policyproof.LoadLatest(stateBefore.RepoRoot)
	switch {
	case proofErr != nil:
		unresolvedDecision = true
		add("policy/latest-decision-integrity", StatusFail, proofErr.Error(), "Rerun `reconc check .` for the current candidate, then rerun `reconc done .`.")
	case found && latest.CandidateFingerprint == stateBefore.Fingerprint && latest.Report.Decision == runtime.DecisionBlock:
		unresolvedDecision = true
		for _, violation := range blockingViolations(latest.Report) {
			action := exactPolicyAction(violation)
			add("policy/unresolved/"+violation.RuleID, StatusFail, violation.Message, action)
		}
	case found && latest.CandidateFingerprint == stateBefore.Fingerprint:
		add("policy/latest-decision", StatusPass, "latest explicit policy decision is current and non-blocking", "")
	case found:
		add("policy/latest-decision", StatusPass, "older policy decision belongs to a different candidate and is superseded", "")
	default:
		add("policy/latest-decision", StatusPass, "no earlier explicit policy decision exists for this candidate", "")
	}

	var policyReport *runtime.CheckReport
	if lockfileValid {
		inputs, inputErr := completionInputs(stateBefore)
		if inputErr != nil {
			return nil, inputErr
		}
		policyReport, err = runtime.CheckRepoPolicy(stateBefore.RepoRoot, inputs)
		if err != nil {
			return nil, fmt.Errorf("evaluate current completion policy: %w", err)
		}
		report.PolicyReport = policyReport
		policyHash, err := hashJSON(policyReport)
		if err != nil {
			return nil, err
		}
		report.Candidate.PolicyReportHash = policyHash
		violations := blockingViolations(policyReport)
		if len(violations) == 0 {
			add("policy/current", StatusPass, "current candidate policy evaluation is non-blocking", "")
		} else {
			for _, violation := range violations {
				add("policy/current/"+violation.RuleID, StatusFail, violation.Message, exactPolicyAction(violation))
			}
		}
	}

	collectTaskChecks(report, stateBefore, add)
	if options.RequireCleanGit {
		switch {
		case !stateBefore.GitAvailable:
			add("git/clean", StatusPass, "not a Git work tree; clean-Git policy is not applicable", "")
		case !stateBefore.GitStatusOK:
			add("git/clean", StatusFail, "Git cleanliness could not be verified", "Restore Git status visibility, then rerun `reconc done . --require-clean-git`.")
		case len(stateBefore.DirtyPaths) == 0:
			add("git/clean", StatusPass, "Git working tree and index are clean", "")
		default:
			add("git/clean", StatusFail, fmt.Sprintf("%d dirty Git path(s) remain", len(stateBefore.DirtyPaths)), "Commit or intentionally remove the listed changes, then rerun `reconc done . --require-clean-git`.")
		}
	}
	if options.WindowMinutes > 0 {
		add("compat/window", StatusWarn, "--window is retained for compatibility but elapsed time never proves completion", "")
	}

	stateAfter, err := agentsession.CaptureCompletionState(stateBefore.RepoRoot)
	if err != nil {
		return nil, err
	}
	if stateAfter.Fingerprint != stateBefore.Fingerprint {
		return nil, &RetryableStateDriftError{}
	}
	if proofErr == nil {
		confirmed, confirmedFound, confirmErr := policyproof.LoadLatest(stateBefore.RepoRoot)
		switch {
		case confirmErr != nil:
			unresolvedDecision = true
			add("policy/decision-binding", StatusFail, confirmErr.Error(), "Rerun `reconc check .` for the current candidate, then rerun `reconc done .`.")
		case confirmedFound != found || (found && confirmed.Digest != latest.Digest):
			unresolvedDecision = true
			add("policy/decision-binding", StatusFail, "latest policy decision changed during completion evaluation", "Rerun `reconc done .` against a stable current candidate.")
		default:
			add("policy/decision-binding", StatusPass, "latest policy decision remained stable during evaluation", "")
		}
	}
	add("state/binding", StatusPass, "HEAD, index, worktree, policy, session, and report identity remained stable", "")

	if err := finalize(report); err != nil {
		return nil, err
	}
	if options.PersistDecision && policyReport != nil {
		shouldStore := policyReport.Decision == runtime.DecisionBlock || (!unresolvedDecision && report.OK)
		if shouldStore {
			event := strings.TrimSpace(options.DecisionEvent)
			if event == "" {
				event = "done"
			}
			if err := persistDecisionAtStableCandidate(
				stateBefore.RepoRoot, event, stateBefore.Fingerprint, policyReport, agentsession.CaptureCompletionState,
			); err != nil {
				return nil, err
			}
		}
	}
	return report, nil
}

func persistDecisionAtStableCandidate(
	repo, event, fingerprint string,
	report *runtime.CheckReport,
	capture completionStateCapture,
) error {
	before, err := capture(repo)
	if err != nil {
		return fmt.Errorf("capture completion candidate before policy proof publication: %w", err)
	}
	if before.Fingerprint != fingerprint {
		return &RetryableStateDriftError{}
	}
	if err := policyproof.Store(repo, event, fingerprint, report); err != nil {
		return err
	}
	after, err := capture(repo)
	if err != nil {
		return fmt.Errorf("confirm completion candidate after policy proof publication: %w", err)
	}
	if after.Fingerprint != fingerprint {
		return &RetryableStateDriftError{}
	}
	return nil
}

// VerifyReport validates the self-digest of an already rendered completion
// result. Candidate freshness still requires a new Evaluate call.
func VerifyReport(report *Report) error {
	if report == nil {
		return errors.New("completion report is nil")
	}
	if !schema.AcceptsFormat(schema.CompletionReport, report.Schema, report.FormatVersion) {
		return errors.New("unsupported completion report schema or format version")
	}
	expected, err := reportDigest(report)
	if err != nil {
		return err
	}
	if expected == "" || !equalDigest(report.Digest, expected) {
		return errors.New("completion report digest mismatch")
	}
	return nil
}

func completionInputs(state agentsession.CompletionStateSnapshot) (runtime.ExecutionInputs, error) {
	return runtime.ExecutionInputs{
		ReadPaths:      append([]string{}, state.Inputs.ReadPaths...),
		WritePaths:     append([]string{}, state.Inputs.WritePaths...),
		WriteEpochs:    cloneEpochs(state.Inputs.WriteEpochs),
		Commands:       append([]string{}, state.Inputs.Commands...),
		Claims:         append([]string{}, state.Inputs.Claims...),
		CommandResults: append([]runtime.CommandResult{}, state.Inputs.CommandResults...),
	}, nil
}

func collectTaskChecks(report *Report, state agentsession.CompletionStateSnapshot, add func(string, Status, string, string)) {
	board, err := tasklifecycle.Inspect(state.RepoRoot)
	if err != nil {
		var validation *tasklifecycle.ValidationError
		if errors.As(err, &validation) {
			for _, issue := range validation.Issues {
				add(issue.ID, StatusFail, issue.Message, issue.Remediation)
			}
			return
		}
		add("task/inspection", StatusFail, err.Error(), "Repair the TASK control plane, then rerun `reconc task validate .` and `reconc done .`.")
		return
	}
	if board == nil {
		add("task/lifecycle", StatusPass, "no typed TASK lifecycle is configured or present", "")
		return
	}
	if board.Active != nil {
		report.TaskID = board.Active.ID
		_, issues, checkErr := tasklifecycle.CheckCompletion(state.RepoRoot, board.Active.ID)
		if checkErr != nil {
			add("task/completion", StatusFail, checkErr.Error(), "Run `reconc task validate .`, repair the current TASK, then rerun `reconc done .`.")
		} else if len(issues) == 0 {
			add("task/completion", StatusPass, "active TASK "+board.Active.ID+" satisfies typed completion", "")
		} else {
			for _, issue := range issues {
				add(issue.ID, StatusFail, issue.Message, issue.Remediation)
			}
		}
	} else if len(board.Queue) > 0 {
		add("task/terminal", StatusFail, fmt.Sprintf("%d queued TASK(s) remain", len(board.Queue)), "Claim and complete the next queued TASK with `reconc task claim .`, then rerun `reconc done .`.")
	} else if len(board.Blocked) > 0 {
		add("task/terminal", StatusFail, fmt.Sprintf("%d blocked TASK(s) remain", len(board.Blocked)), "Resolve and resume the blocked TASK, then rerun `reconc done .`.")
	} else {
		add("task/terminal", StatusPass, "typed TASK lifecycle is terminal with no open TASK", "")
	}
	if !board.Config.Completion.RequireCommitted {
		return
	}
	if !state.GitAvailable || !state.GitStatusOK {
		add("task/committed", StatusFail, "TASK completion requires committed state but Git status is unavailable", "Restore Git status visibility and commit the TASK control plane, then rerun `reconc done .`.")
		return
	}
	dirty := tasklifecycle.DirtyCompletionPaths(board.Config, state.DirtyPaths)
	if len(dirty) == 0 {
		add("task/committed", StatusPass, "TASK control plane is committed", "")
		return
	}
	add("task/committed", StatusFail, "TASK control plane is not committed: "+strings.Join(dirty, ", "), "Commit the TASK overview/detail changes, then rerun `reconc done .`.")
}

func candidateFromState(state agentsession.CompletionStateSnapshot) CandidateBinding {
	return CandidateBinding{
		Fingerprint: state.Fingerprint, PolicyLockHash: state.PolicyLockHash,
		GitAvailable: state.GitAvailable, GitHead: state.GitHead, GitIndexHash: state.GitIndexHash,
		WorktreeHash: state.WorktreeHash, WorktreeTrusted: state.WorktreeTrusted,
		DirtyPaths:          append([]string{}, state.DirtyPaths...),
		SessionEvidenceHash: state.SessionEvidenceHash, SessionReportHash: state.SessionReportHash,
	}
}

func blockingViolations(report *runtime.CheckReport) []runtime.Violation {
	if report == nil {
		return nil
	}
	out := make([]runtime.Violation, 0, report.BlockingViolationCount)
	for _, violation := range report.Violations {
		if violation.IsBlocking() {
			out = append(out, violation)
		}
	}
	return out
}

func exactPolicyAction(violation runtime.Violation) string {
	action := strings.TrimSpace(violation.RecommendedAction)
	if action == "" {
		action = "Resolve policy rule `" + violation.RuleID + "`, rerun the same policy check, then rerun `reconc done .`."
	}
	return action
}

func finalize(report *Report) error {
	report.OK = true
	report.NextAction = ""
	for _, check := range report.Checks {
		if check.Status != StatusFail {
			continue
		}
		report.OK = false
		if report.NextAction == "" {
			report.NextAction = check.nextAction
		}
	}
	if report.OK {
		report.Decision = "pass"
	} else {
		report.Decision = "block"
	}
	digest, err := reportDigest(report)
	if err != nil {
		return err
	}
	report.Digest = digest
	return nil
}

func reportDigest(report *Report) (string, error) {
	payload := reportPayload{
		Schema: report.Schema, FormatVersion: report.FormatVersion, OK: report.OK,
		Decision: report.Decision, RepoRoot: filepath.Clean(report.RepoRoot), TaskID: report.TaskID,
		Checks: report.Checks, NextAction: report.NextAction, Candidate: report.Candidate,
		PolicyReport: report.PolicyReport,
	}
	return hashJSON(payload)
}

func hashJSON(value interface{}) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal report payload: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func equalDigest(left, right string) bool {
	leftBytes, leftErr := hex.DecodeString(left)
	rightBytes, rightErr := hex.DecodeString(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func cloneEpochs(values map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(values))
	for path, epoch := range values {
		out[path] = epoch
	}
	return out
}
