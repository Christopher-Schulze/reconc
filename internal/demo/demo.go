// Package demo runs Reconc's deterministic, isolated product journey.
package demo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/completiongate"
	"reconc.dev/reconc/internal/proofbundle"
	policyruntime "reconc.dev/reconc/internal/runtime"
)

const (
	FormatVersion   = "1"
	defaultTimeout  = 45 * time.Second
	demoSessionID   = "reconc-demo"
	demoTestCommand = "git diff --check"
)

// Options configures one isolated demo run.
type Options struct {
	Executable    string
	Version       string
	GitExecutable string
	TempRoot      string
	Keep          bool
	Timeout       time.Duration
}

// Step is one real command or filesystem action in the journey.
type Step struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Command    []string `json:"command"`
	ExitCode   int      `json:"exit_code"`
	Decision   string   `json:"decision"`
	DurationMS int64    `json:"duration_ms"`
	Stdout     string   `json:"stdout,omitempty"`
	Stderr     string   `json:"stderr,omitempty"`
}

// Artifact identifies one inspectable proof emitted by the real run.
type Artifact struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Result is the versioned machine-readable demo contract.
type Result struct {
	FormatVersion    string     `json:"format_version"`
	ReconcVersion    string     `json:"reconc_version"`
	Status           string     `json:"status"`
	WorkspacePath    string     `json:"workspace_path,omitempty"`
	RepositoryPath   string     `json:"repository_path,omitempty"`
	StatePath        string     `json:"state_path,omitempty"`
	Kept             bool       `json:"kept"`
	Cleaned          bool       `json:"cleaned"`
	DurationMS       int64      `json:"duration_ms"`
	Steps            []Step     `json:"steps"`
	Artifacts        []Artifact `json:"artifacts"`
	CompletionDigest string     `json:"completion_digest,omitempty"`
	ProofDigest      string     `json:"proof_digest,omitempty"`
	Error            string     `json:"error,omitempty"`
	Digest           string     `json:"digest"`
}

type journey struct {
	ctx        context.Context
	executable string
	git        string
	repo       string
	env        []string
	result     *Result
}

type commandRequest struct {
	id       string
	label    string
	execPath string
	display  []string
	args     []string
	dir      string
	stdin    string
	expected map[int]bool
}

// Run executes the complete journey and always returns a versioned result.
// A non-nil error means the proof did not reach the evidence-complete ending.
func Run(parent context.Context, options Options) (result *Result, runErr error) {
	started := time.Now()
	result = &Result{
		FormatVersion: FormatVersion,
		ReconcVersion: strings.TrimSpace(options.Version),
		Status:        "running",
		Kept:          options.Keep,
		Steps:         []Step{},
		Artifacts:     []Artifact{},
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	defer func() {
		if !options.Keep && result.WorkspacePath != "" {
			if err := os.RemoveAll(result.WorkspacePath); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("clean demo workspace: %w", err))
			} else {
				result.Cleaned = true
			}
		}
		result.DurationMS = elapsedMilliseconds(started)
		if runErr != nil {
			result.Status = "failed"
			result.Error = runErr.Error()
		} else {
			result.Status = "passed"
		}
		result.Digest = resultDigest(result)
	}()

	workspace, err := os.MkdirTemp(options.TempRoot, "reconc-demo-")
	if err != nil {
		return result, fmt.Errorf("create demo workspace: %w", err)
	}
	result.WorkspacePath = workspace
	result.RepositoryPath = filepath.Join(workspace, "repository")
	result.StatePath = filepath.Join(workspace, "state")
	if err := os.MkdirAll(result.RepositoryPath, 0o755); err != nil {
		return result, fmt.Errorf("create demo repository: %w", err)
	}

	executable, err := validateExecutable(options.Executable)
	if err != nil {
		return result, err
	}
	gitExecutable := strings.TrimSpace(options.GitExecutable)
	if gitExecutable == "" {
		gitExecutable, err = exec.LookPath("git")
		if err != nil {
			return result, fmt.Errorf("demo prerequisite Git is unavailable: %w", err)
		}
	}
	env := isolatedEnvironment(result.StatePath)
	runner := &journey{
		ctx: ctx, executable: executable, git: gitExecutable,
		repo: result.RepositoryPath,
		env:  env, result: result,
	}
	if err := runner.run(); err != nil {
		return result, err
	}
	return result, nil
}

func (runner *journey) run() error {
	versionStep, err := runner.command(commandRequest{
		id: "reconc-version", label: "Verify the running Reconc binary",
		execPath: runner.executable, display: []string{"reconc", "version", "--json"},
		args: []string{"version", "--json"}, expected: exitCodes(0),
	})
	if err != nil {
		return err
	}
	var versionOutput struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(versionStep.Stdout), &versionOutput); err != nil {
		return fmt.Errorf("decode running Reconc version: %w", err)
	}
	versionOutput.Version = strings.TrimSpace(versionOutput.Version)
	if versionOutput.Version == "" {
		return errors.New("running Reconc version is empty")
	}
	if expected := strings.TrimSpace(runner.result.ReconcVersion); expected != "" && expected != versionOutput.Version {
		return fmt.Errorf("running Reconc version %q does not match expected %q", versionOutput.Version, expected)
	}
	runner.result.ReconcVersion = versionOutput.Version
	if _, err := runner.command(commandRequest{
		id: "git-version", label: "Verify the declared Git prerequisite",
		execPath: runner.git, display: []string{"git", "--version"},
		args: []string{"--version"}, expected: exitCodes(0),
	}); err != nil {
		return fmt.Errorf("demo prerequisite Git failed: %w", err)
	}
	if err := runner.createFixture(); err != nil {
		return err
	}
	if err := runner.initializeGit(); err != nil {
		return err
	}
	if _, err := runner.reconcCommand("compile-policy", "Compile the deterministic policy", []string{"compile", runner.repo, "--json"}, "", exitCodes(0)); err != nil {
		return err
	}
	if _, err := runner.reconcCommand(
		"session-start", "Start isolated evidence collection",
		[]string{"hook", "runtime", "codex-session-start", runner.repo},
		`{"session_id":"`+demoSessionID+`","runtime":"codex"}`+"\n", exitCodes(0),
	); err != nil {
		return err
	}

	protected, err := runner.reconcCommand(
		"protected-action", "Attempt an out-of-scope write",
		[]string{"check", runner.repo, "--write", "protected/secret.txt", "--json"}, "", exitCodes(2),
	)
	if err != nil {
		return err
	}
	if err := validateBlockingReport(protected.Stdout, "demo-protected"); err != nil {
		return fmt.Errorf("protected-action proof: %w", err)
	}
	protected.Decision = "block"

	missingProof, err := runner.reconcCommand(
		"missing-proof", "Evaluate a source change without successful test proof",
		[]string{"check", runner.repo, "--write", "src/message.txt", "--command", demoTestCommand, "--json"}, "", exitCodes(2),
	)
	if err != nil {
		return err
	}
	if err := validateBlockingReport(missingProof.Stdout, "demo-test-proof"); err != nil {
		return fmt.Errorf("missing-proof decision: %w", err)
	}
	missingProof.Decision = "block"

	remediation, err := runner.reconcCommand(
		"remediation", "Ask Reconc for the exact next action",
		[]string{"next", runner.repo, "--json"}, "", exitCodes(2),
	)
	if err != nil {
		return err
	}
	if err := validateRemediation(remediation.Stdout); err != nil {
		return fmt.Errorf("remediation proof: %w", err)
	}
	remediation.Decision = "remediate"

	change, err := runner.action("apply-change", "Apply the intended source change", []string{"write", "src/message.txt"}, func() error {
		return writeFixtureFile(runner.repo, "src/message.txt", "Reconc demo change\n")
	})
	if err != nil {
		return err
	}
	change.Decision = "pass"
	writePayload := `{"session_id":"` + demoSessionID + `","runtime":"codex","tool_name":"Write","tool_input":{"file_path":"src/message.txt"},"tool_response":{"success":true}}` + "\n"
	if _, err := runner.reconcCommand(
		"record-change", "Record the real source write as session evidence",
		[]string{"hook", "runtime", "codex-post-tool-use", runner.repo}, writePayload, exitCodes(0),
	); err != nil {
		return err
	}

	verification, err := runner.reconcCommand(
		"real-test", "Execute the real repository test command",
		[]string{"exec", runner.repo, "--", "git", "diff", "--check"}, "", exitCodes(0),
	)
	if err != nil {
		return err
	}
	verification.Decision = "pass"

	corrected, err := runner.reconcCommand(
		"corrected-evaluation", "Evaluate the corrected current state with successful proof",
		[]string{"check", runner.repo, "--write", "src/message.txt", "--command-success", demoTestCommand, "--json"}, "", exitCodes(0),
	)
	if err != nil {
		return err
	}
	if err := validatePassingReport(corrected.Stdout); err != nil {
		return fmt.Errorf("corrected evaluation proof: %w", err)
	}
	corrected.Decision = "pass"

	completedTask, err := runner.action("complete-task-evidence", "Complete the typed TASK with exact test evidence", []string{"write", "docs/tasks/001-demo-journey.md"}, func() error {
		return writeFixtureFile(runner.repo, "docs/tasks/001-demo-journey.md", completedTaskDetail())
	})
	if err != nil {
		return err
	}
	completedTask.Decision = "pass"

	done, err := runner.reconcCommand(
		"done", "Run the evidence-complete final gate",
		[]string{"done", runner.repo, "--json"}, "", exitCodes(0),
	)
	if err != nil {
		return err
	}
	var completion completiongate.Report
	if err := json.Unmarshal([]byte(done.Stdout), &completion); err != nil {
		return fmt.Errorf("decode completion report: %w", err)
	}
	if !completion.OK || completion.Decision != "pass" {
		return fmt.Errorf("completion report did not prove done")
	}
	if err := completiongate.VerifyReport(&completion); err != nil {
		return fmt.Errorf("verify completion report: %w", err)
	}
	done.Decision = "done"
	runner.result.CompletionDigest = completion.Digest
	proofPath := filepath.Join(runner.result.WorkspacePath, "proof", "completion.json")
	if err := os.MkdirAll(filepath.Dir(proofPath), 0o755); err != nil {
		return fmt.Errorf("create demo proof directory: %w", err)
	}
	if _, err := atomicfile.WriteIfChanged(proofPath, []byte(done.Stdout), 0o600); err != nil {
		return fmt.Errorf("write completion proof: %w", err)
	}
	portablePath := filepath.Join(runner.result.WorkspacePath, "proof", "bundle.json")
	portable, err := runner.reconcCommand(
		"portable-proof", "Export the portable completion proof bundle",
		[]string{"proof", runner.repo, "--output", portablePath}, "", exitCodes(0),
	)
	if err != nil {
		return err
	}
	var bundle proofbundle.Bundle
	if err := json.Unmarshal([]byte(portable.Stdout), &bundle); err != nil {
		return fmt.Errorf("decode portable proof bundle: %w", err)
	}
	if err := proofbundle.Verify(&bundle); err != nil {
		return fmt.Errorf("verify portable proof bundle: %w", err)
	}
	if !bundle.OK || bundle.Decision != "pass" || bundle.Candidate.Fingerprint != completion.Candidate.Fingerprint {
		return errors.New("portable proof bundle does not match the passing completion candidate")
	}
	portable.Decision = "proof"
	runner.result.ProofDigest = bundle.Digest
	return runner.collectArtifacts(proofPath, portablePath)
}

func (runner *journey) createFixture() error {
	files := map[string]string{
		"AGENTS.md":                      "# Reconc Demo\n\nThis disposable repository exists only for `reconc demo`.\n",
		".reconc.yml":                    demoPolicy(),
		"docs/tasks.md":                  "# TASK Control Plane\n\n## Active\n\n- [~] 001 Demo journey -> tasks/001-demo-journey.md\n\n## Queue\n\n## Blocked\n\n## Done\n",
		"docs/tasks/001-demo-journey.md": openTaskDetail(),
		"src/message.txt":                "Reconc demo baseline\n",
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	_, err := runner.action("create-fixture", "Create the isolated public fixture", []string{"create", "isolated", "fixture"}, func() error {
		for _, path := range paths {
			if err := writeFixtureFile(runner.repo, path, files[path]); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func (runner *journey) initializeGit() error {
	hooksDir := filepath.Join(runner.result.WorkspacePath, "empty-hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("create isolated Git hooks directory: %w", err)
	}
	commands := [][]string{
		{"init", "-q"},
		{"config", "user.name", "Reconc Demo"},
		{"config", "user.email", "demo@reconc.local"},
		{"config", "commit.gpgsign", "false"},
		{"config", "core.autocrlf", "false"},
		{"config", "core.fsmonitor", "false"},
		{"config", "core.hooksPath", hooksDir},
		{"add", "-A"},
		{"commit", "-q", "-m", "demo baseline"},
	}
	for index, args := range commands {
		if _, err := runner.command(commandRequest{
			id: fmt.Sprintf("git-setup-%02d", index+1), label: "Initialize the isolated Git baseline",
			execPath: runner.git, display: append([]string{"git"}, args...), args: args,
			dir: runner.repo, expected: exitCodes(0),
		}); err != nil {
			return fmt.Errorf("initialize demo Git repository: %w", err)
		}
	}
	return nil
}

func (runner *journey) reconcCommand(id, label string, args []string, stdin string, expected map[int]bool) (*Step, error) {
	return runner.command(commandRequest{
		id: id, label: label, execPath: runner.executable,
		display: append([]string{"reconc"}, args...), args: args,
		dir: runner.repo, stdin: stdin, expected: expected,
	})
}

func (runner *journey) command(request commandRequest) (*Step, error) {
	started := time.Now()
	command := exec.CommandContext(runner.ctx, request.execPath, request.args...)
	command.Dir = request.dir
	if command.Dir == "" {
		command.Dir = runner.repo
	}
	command.Env = runner.env
	if request.stdin != "" {
		command.Stdin = strings.NewReader(request.stdin)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exitCode := commandExitCode(err)
	step := Step{
		ID: request.id, Label: request.label, Command: append([]string(nil), request.display...),
		ExitCode: exitCode, DurationMS: elapsedMilliseconds(started),
		Stdout: stdout.String(), Stderr: stderr.String(),
	}
	if exitCode == 0 {
		step.Decision = "pass"
	} else {
		step.Decision = "error"
	}
	runner.result.Steps = append(runner.result.Steps, step)
	stored := &runner.result.Steps[len(runner.result.Steps)-1]
	if runner.ctx.Err() != nil {
		return stored, runner.ctx.Err()
	}
	if !request.expected[exitCode] {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return stored, fmt.Errorf("%s exited %d: %s", strings.Join(request.display, " "), exitCode, detail)
	}
	return stored, nil
}

func (runner *journey) action(id, label string, command []string, action func() error) (*Step, error) {
	started := time.Now()
	err := action()
	step := Step{
		ID: id, Label: label, Command: append([]string(nil), command...),
		ExitCode: 0, Decision: "pass", DurationMS: elapsedMilliseconds(started),
	}
	if err != nil {
		step.ExitCode = 1
		step.Decision = "error"
		step.Stderr = err.Error()
	}
	runner.result.Steps = append(runner.result.Steps, step)
	stored := &runner.result.Steps[len(runner.result.Steps)-1]
	if err != nil {
		return stored, fmt.Errorf("%s: %w", label, err)
	}
	return stored, nil
}

func (runner *journey) collectArtifacts(completionPath, portablePath string) error {
	items := []struct {
		kind string
		path string
	}{
		{kind: "policy-lock", path: filepath.Join(runner.repo, ".reconc", "policy.lock.json")},
		{kind: "task-detail", path: filepath.Join(runner.repo, "docs", "tasks", "001-demo-journey.md")},
		{kind: "completion-report", path: completionPath},
		{kind: "proof-bundle", path: portablePath},
	}
	for _, item := range items {
		digest, err := fileDigest(item.path)
		if err != nil {
			return err
		}
		runner.result.Artifacts = append(runner.result.Artifacts, Artifact{Kind: item.kind, Path: item.path, SHA256: digest})
	}
	return nil
}

// VerifyResult validates the self-digest of one already rendered demo result.
func VerifyResult(result *Result) error {
	if result == nil {
		return errors.New("demo result is nil")
	}
	if result.FormatVersion != FormatVersion {
		return fmt.Errorf("unsupported demo format_version %q", result.FormatVersion)
	}
	expected := resultDigest(result)
	if expected == "" || !strings.EqualFold(expected, result.Digest) {
		return errors.New("demo result digest mismatch")
	}
	return validateResultContract(result)
}

// RenderText prints a compact human summary derived only from verified steps.
func RenderText(writer io.Writer, result *Result) error {
	if err := VerifyResult(result); err != nil {
		return fmt.Errorf("verify demo result: %w", err)
	}
	fmt.Fprintln(writer, "Reconc demo: isolated real-policy journey")
	visible := map[string]bool{
		"protected-action": true, "missing-proof": true, "remediation": true,
		"real-test": true, "corrected-evaluation": true, "done": true,
		"portable-proof": true,
	}
	for _, step := range result.Steps {
		if visible[step.ID] {
			fmt.Fprintf(writer, "[%s] %s\n", strings.ToUpper(step.Decision), step.Label)
		}
	}
	if result.Status == "passed" {
		fmt.Fprintln(writer, "Result: evidence-complete proof verified")
	} else {
		fmt.Fprintf(writer, "Result: failed: %s\n", result.Error)
	}
	if result.Kept {
		fmt.Fprintf(writer, "Workspace: %s\n", result.WorkspacePath)
	} else if result.Cleaned {
		fmt.Fprintln(writer, "Workspace: cleaned")
	}
	return nil
}

func validateExecutable(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("demo Reconc executable path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve demo Reconc executable: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("inspect demo Reconc executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("demo Reconc executable is not a regular file: %s", abs)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("demo Reconc executable is not executable: %s", abs)
	}
	return abs, nil
}

func validateBlockingReport(body, ruleID string) error {
	var report policyruntime.CheckReport
	if err := json.Unmarshal([]byte(body), &report); err != nil {
		return err
	}
	if report.Decision != policyruntime.DecisionBlock {
		return fmt.Errorf("decision = %s, want block", report.Decision)
	}
	for _, violation := range report.Violations {
		if violation.RuleID == ruleID && violation.IsBlocking() {
			return nil
		}
	}
	return fmt.Errorf("blocking rule %s is absent", ruleID)
}

func validatePassingReport(body string) error {
	var report policyruntime.CheckReport
	if err := json.Unmarshal([]byte(body), &report); err != nil {
		return err
	}
	if !report.OK || report.Decision != policyruntime.DecisionPass || report.BlockingViolationCount != 0 {
		return fmt.Errorf("corrected decision = %s with %d blocking violations", report.Decision, report.BlockingViolationCount)
	}
	return nil
}

func validateRemediation(body string) error {
	var remediation policyruntime.Remediation
	if err := json.Unmarshal([]byte(body), &remediation); err != nil {
		return err
	}
	if remediation.RuleID != "demo-test-proof" {
		return fmt.Errorf("remediation rule = %q", remediation.RuleID)
	}
	if remediation.Priority != "blocking" || !remediation.Mode.IsBlocking() {
		return fmt.Errorf("remediation is not blocking: priority=%q mode=%q", remediation.Priority, remediation.Mode)
	}
	for _, command := range remediation.SuggestedCommands {
		if command == demoTestCommand {
			return nil
		}
	}
	return fmt.Errorf("remediation omitted %q", demoTestCommand)
}

func validateResultContract(result *Result) error {
	if strings.TrimSpace(result.ReconcVersion) == "" {
		return errors.New("demo result Reconc version is empty")
	}
	switch result.Status {
	case "failed":
		if strings.TrimSpace(result.Error) == "" {
			return errors.New("failed demo result has no error")
		}
		return nil
	case "passed":
	default:
		return fmt.Errorf("unsupported demo status %q", result.Status)
	}
	if result.CompletionDigest == "" {
		return errors.New("passed demo result has no completion digest")
	}
	if result.ProofDigest == "" {
		return errors.New("passed demo result has no portable proof digest")
	}
	if result.Kept == result.Cleaned {
		return fmt.Errorf("passed demo result has inconsistent keep/cleanup state: kept=%t cleaned=%t", result.Kept, result.Cleaned)
	}
	requiredSteps := map[string]string{
		"protected-action":     "block",
		"missing-proof":        "block",
		"remediation":          "remediate",
		"real-test":            "pass",
		"corrected-evaluation": "pass",
		"done":                 "done",
		"portable-proof":       "proof",
	}
	seenSteps := make(map[string]bool, len(result.Steps))
	for _, step := range result.Steps {
		if seenSteps[step.ID] {
			return fmt.Errorf("demo result repeats step %q", step.ID)
		}
		seenSteps[step.ID] = true
		if expected, ok := requiredSteps[step.ID]; ok {
			if step.Decision != expected {
				return fmt.Errorf("demo step %s decision = %q, want %q", step.ID, step.Decision, expected)
			}
			delete(requiredSteps, step.ID)
		}
	}
	if len(requiredSteps) != 0 {
		missing := make([]string, 0, len(requiredSteps))
		for id := range requiredSteps {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return fmt.Errorf("demo result is missing required steps: %s", strings.Join(missing, ", "))
	}
	requiredArtifacts := map[string]bool{
		"policy-lock":       true,
		"task-detail":       true,
		"completion-report": true,
		"proof-bundle":      true,
	}
	for _, artifact := range result.Artifacts {
		if !requiredArtifacts[artifact.Kind] {
			return fmt.Errorf("demo result has unexpected or repeated artifact %q", artifact.Kind)
		}
		if artifact.Path == "" || artifact.SHA256 == "" {
			return fmt.Errorf("demo artifact %q is incomplete", artifact.Kind)
		}
		delete(requiredArtifacts, artifact.Kind)
	}
	if len(requiredArtifacts) != 0 {
		return errors.New("demo result is missing required proof artifacts")
	}
	return nil
}

func demoPolicy() string {
	return `task_lifecycle:
  profile: sections-v1
  completion:
    required_evidence_fields:
      - Tests
rules:
  - id: demo-protected
    kind: deny_write
    paths:
      - protected/**
    mode: block
    message: Protected paths are outside the demo agent scope.
  - id: demo-test-proof
    kind: require_command_success
    when_paths:
      - src/**
    commands:
      - git diff --check
    mode: block
    message: The source change requires a successful real test command.
`
}

func openTaskDetail() string {
	return `# TASK 001: Demo journey

## Why

Prove Reconc's block-to-remediation-to-completion journey.

## Acceptance

- The real repository test passes and Reconc verifies completion.

## Evidence

- Tests:

## Sub-Tasks

- [~] Execute and prove the isolated journey.

## Notes

None.

## Deviations

None.
`
}

func completedTaskDetail() string {
	return `# TASK 001: Demo journey

## Why

Prove Reconc's block-to-remediation-to-completion journey.

## Acceptance

- The real repository test passes and Reconc verifies completion.

## Evidence

- Tests: reconc exec . -- git diff --check (exit 0)

## Sub-Tasks

- [x] Execute and prove the isolated journey.

## Notes

The corrected current-state policy evaluation passed.

## Deviations

None.
`
}

func writeFixtureFile(root, relative, body string) error {
	relative = filepath.Clean(filepath.FromSlash(relative))
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe demo fixture path %q", relative)
	}
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create fixture parent: %w", err)
	}
	if _, err := atomicfile.WriteIfChanged(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write fixture %s: %w", relative, err)
	}
	return nil
}

func isolatedEnvironment(stateRoot string) []string {
	overrides := map[string]string{
		"RECONC_HOME":         stateRoot,
		"RECONC_AUDIT":        "1",
		"NO_COLOR":            "1",
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_TERMINAL_PROMPT": "0",
	}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key = entry[:index]
		}
		if strings.HasPrefix(strings.ToUpper(key), "RECONC_") {
			continue
		}
		matched := false
		for override := range overrides {
			if strings.EqualFold(key, override) {
				matched = true
				break
			}
		}
		if !matched {
			env = append(env, entry)
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+overrides[key])
	}
	return env
}

func exitCodes(values ...int) map[int]bool {
	out := make(map[int]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func fileDigest(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hash demo artifact %s: %w", path, err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func resultDigest(result *Result) string {
	if result == nil {
		return ""
	}
	copyResult := *result
	copyResult.Digest = ""
	body, err := json.Marshal(copyResult)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func elapsedMilliseconds(start time.Time) int64 {
	elapsed := time.Since(start).Milliseconds()
	if elapsed < 1 {
		return 1
	}
	return elapsed
}
