package agentsession

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/shellcommand"
)

// OperatorClaimPrefix reserves a workflow claim for local administration.
// Recognized tool invocations are refused and the CLI requires terminal
// confirmation. This convention does not authenticate a human or prevent a
// process with the same privileges from changing state or automating a PTY.
const OperatorClaimPrefix = "operator:"

const (
	hookPauseFormatVersion = "hook-pause-v1"
	// MaxHookPause bounds one operator pause; enforcement always returns.
	MaxHookPause        = 24 * time.Hour
	minHookPause        = time.Minute
	maxHookPauseBytes   = 64 * 1024
	maxHookPauseHistory = 32
	maxHookPauseReason  = 512
	hookPauseFileName   = "hook-pause.json"
)

type hookPauseRecord struct {
	Reason    string `json:"reason"`
	PausedAt  string `json:"paused_at"`
	ExpiresAt string `json:"expires_at"`
	ResumedAt string `json:"resumed_at,omitempty"`
}

type hookPauseFile struct {
	FormatVersion string            `json:"format_version"`
	RepoRoot      string            `json:"repo_root"`
	History       []hookPauseRecord `json:"history"`
}

// HookPauseStatus reports the newest operator pause of one repository.
type HookPauseStatus struct {
	Active    bool   `json:"active"`
	Reason    string `json:"reason,omitempty"`
	PausedAt  string `json:"paused_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	ResumedAt string `json:"resumed_at,omitempty"`
}

func hookPausePath(repoRoot string) string {
	return filepath.Join(projectDir(repoRoot), hookPauseFileName)
}

// PauseHooks lets blocking hook events pass for a bounded time. Evidence keeps
// being recorded, the operator-only command guard keeps holding, and the pause
// ends on its own at the expiry.
func PauseHooks(repoRoot string, duration time.Duration, reason string, now time.Time) (HookPauseStatus, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return HookPauseStatus{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > maxHookPauseReason {
		return HookPauseStatus{}, fmt.Errorf("pause reason must contain 1..%d bytes", maxHookPauseReason)
	}
	if duration < minHookPause || duration > MaxHookPause {
		return HookPauseStatus{}, fmt.Errorf("pause duration must be between %s and %s", minHookPause, MaxHookPause)
	}
	// The operator's pause is authoritative: a damaged record must never keep
	// the valve shut, so it is replaced instead of blocking the pause.
	file, err := loadHookPauseFile(root)
	if err != nil {
		file = hookPauseFile{FormatVersion: hookPauseFormatVersion, RepoRoot: root}
	}
	now = now.UTC()
	if current, active := newestActivePause(file, now); active {
		current.ResumedAt = now.Format(time.RFC3339Nano)
		file.History[len(file.History)-1] = current
	}
	record := hookPauseRecord{
		Reason:    reason,
		PausedAt:  now.Format(time.RFC3339Nano),
		ExpiresAt: now.Add(duration).Format(time.RFC3339Nano),
	}
	file.History = append(file.History, record)
	if len(file.History) > maxHookPauseHistory {
		file.History = append([]hookPauseRecord(nil), file.History[len(file.History)-maxHookPauseHistory:]...)
	}
	if err := writeHookPauseFile(root, file); err != nil {
		return HookPauseStatus{}, err
	}
	return pauseStatus(record, now), nil
}

// ResumeHooks ends the active pause before its expiry.
func ResumeHooks(repoRoot string, now time.Time) (HookPauseStatus, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return HookPauseStatus{}, err
	}
	file, err := loadHookPauseFile(root)
	if err != nil {
		return HookPauseStatus{}, err
	}
	now = now.UTC()
	current, active := newestActivePause(file, now)
	if !active {
		return HookPauseStatus{}, errors.New("no active hook pause")
	}
	current.ResumedAt = now.Format(time.RFC3339Nano)
	file.History[len(file.History)-1] = current
	if err := writeHookPauseFile(root, file); err != nil {
		return HookPauseStatus{}, err
	}
	return pauseStatus(current, now), nil
}

// ReadHookPauseStatus reports the newest pause without mutation.
func ReadHookPauseStatus(repoRoot string, now time.Time) (HookPauseStatus, error) {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		return HookPauseStatus{}, err
	}
	file, err := loadHookPauseFile(root)
	if err != nil {
		return HookPauseStatus{}, err
	}
	if len(file.History) == 0 {
		return HookPauseStatus{}, nil
	}
	return pauseStatus(file.History[len(file.History)-1], now.UTC()), nil
}

// activeHookPause reads the pause on the hook hot path. Any unreadable or
// foreign record means no pause: a damaged valve keeps enforcement on.
func activeHookPause(root string, now time.Time) (hookPauseRecord, bool) {
	file, err := readHookPauseForHook(root)
	if err != nil {
		return hookPauseRecord{}, false
	}
	return newestActivePause(file, now.UTC())
}

// readHookPauseForHook is the hook hot path's pause reader seam.
var readHookPauseForHook = loadHookPauseFile

func newestActivePause(file hookPauseFile, now time.Time) (hookPauseRecord, bool) {
	if len(file.History) == 0 {
		return hookPauseRecord{}, false
	}
	record := file.History[len(file.History)-1]
	return record, pauseStatus(record, now).Active
}

func pauseStatus(record hookPauseRecord, now time.Time) HookPauseStatus {
	status := HookPauseStatus{
		Reason: record.Reason, PausedAt: record.PausedAt,
		ExpiresAt: record.ExpiresAt, ResumedAt: record.ResumedAt,
	}
	expires, err := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	status.Active = err == nil && record.ResumedAt == "" && now.Before(expires)
	return status
}

func loadHookPauseFile(root string) (hookPauseFile, error) {
	body, err := boundedio.ReadRegularFile(hookPausePath(root), maxHookPauseBytes)
	if errors.Is(err, os.ErrNotExist) {
		return hookPauseFile{FormatVersion: hookPauseFormatVersion, RepoRoot: root}, nil
	}
	if err != nil {
		return hookPauseFile{}, fmt.Errorf("read hook pause: %w", err)
	}
	var file hookPauseFile
	if err := json.Unmarshal(body, &file); err != nil {
		return hookPauseFile{}, fmt.Errorf("hook pause is not valid JSON: %w", err)
	}
	if file.FormatVersion != hookPauseFormatVersion || file.RepoRoot != root || len(file.History) > maxHookPauseHistory {
		return hookPauseFile{}, errors.New("hook pause identity is invalid")
	}
	return file, nil
}

func writeHookPauseFile(root string, file hookPauseFile) error {
	file.FormatVersion = hookPauseFormatVersion
	file.RepoRoot = root
	body, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal hook pause: %w", err)
	}
	body = append(body, '\n')
	path := hookPausePath(root)
	if err := ensurePrivateStateDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("mkdir hook pause dir: %w", err)
	}
	if _, err := atomicfile.WritePrivateIfChanged(path, body, 0o600); err != nil {
		return fmt.Errorf("write hook pause: %w", err)
	}
	return nil
}

func (record hookPauseRecord) notice() string {
	return fmt.Sprintf("reconc hooks are paused by the operator until %s (%s); enforcement resumes at expiry or with `reconc hook resume`.", record.ExpiresAt, record.Reason)
}

// pausedPreToolDecision short-circuits a pre-tool gate while an operator pause
// is active. It runs before the decision cache so a cached block cannot outlive
// the pause decision. A pause lifts policy and evidence gates only: the
// built-in destructive Git guard and the operator-only command guard still
// hold, because administration never needs an agent to destroy work or to
// govern itself.
func pausedPreToolDecision(root string, payload *HookPayload) (Result, bool) {
	pause, paused := activeHookPause(root, time.Now())
	if !paused {
		return Result{}, false
	}
	if payload != nil && payload.IsCommandTool() {
		if reason := forbiddenShellCommandReasonInRepo(root, payload.Command()); reason != "" {
			return Result{ExitCode: 2, Stderr: reason}, true
		}
		if reason := operatorOnlyCommandReason(payload.Command()); reason != "" {
			return Result{ExitCode: 2, Stderr: reason}, true
		}
	}
	return Result{ExitCode: 0, Stderr: pause.notice()}, true
}

// operatorOnlyHookSubcommands change who governs the session. An agent tool
// call may never issue them; the operator runs them from a terminal.
var operatorOnlyHookSubcommands = map[string]bool{
	"pause":            true,
	"resume":           true,
	"evidence-resolve": true,
}

// operatorOnlyCommandReason refuses agent tool calls that would lift their own
// restraint: pausing or resuming hooks, resolving an evidence taint, or
// recording an operator claim. Unanalyzable commands are left to the shell
// guard, which already refuses them.
func operatorOnlyCommandReason(command string) string {
	invocations, incomplete := shellcommand.InvocationsWithReason(command, maxShellGuardDepth)
	if incomplete != shellcommand.IncompleteNone {
		return ""
	}
	for _, invocation := range invocations {
		words := invocation.Words
		if len(words) < 3 || !isReconcExecutable(words[0]) {
			continue
		}
		command, commandIndex := nextOperand(words, 1)
		if command != "hook" {
			continue
		}
		subcommand, subIndex := nextOperand(words, commandIndex+1)
		if operatorOnlyHookSubcommands[subcommand] {
			return operatorOnlyMessage("reconc hook " + subcommand)
		}
		if subcommand != "claim" {
			continue
		}
		for index := subIndex + 1; index < len(words); index++ {
			if index < len(invocation.DynamicWords) && invocation.DynamicWords[index] {
				return operatorOnlyMessage("reconc hook claim with a dynamic claim name")
			}
			if strings.HasPrefix(strings.TrimSpace(words[index]), OperatorClaimPrefix) {
				return operatorOnlyMessage("reconc hook claim " + strings.TrimSpace(words[index]))
			}
		}
	}
	return ""
}

func operatorOnlyMessage(what string) string {
	return fmt.Sprintf("reconc blocked an operator-only command (%s). Recognized tool invocations of pause, resume, evidence recovery, and `%s*` claims are refused. Ask the operator to confirm this action locally at an interactive terminal. This workflow does not authenticate a human or isolate processes with the same privileges.", what, OperatorClaimPrefix)
}

func nextOperand(words []string, start int) (string, int) {
	for index := start; index < len(words); index++ {
		if !strings.HasPrefix(words[index], "-") {
			return words[index], index
		}
	}
	return "", len(words)
}

// selfAdministrationCommands are the read-only Reconc diagnostics a gate admits
// even while it refuses all other work, so a blocked session can always explain
// why it is blocked and which operator action recovers it.
var selfAdministrationCommands = map[string]bool{
	"status": true, "next": true, "why": true, "explain": true, "doctor": true,
	"sources": true, "session-briefing": true, "agent-intro": true, "help": true,
	"version": true, "hook status": true, "hook evidence-status": true, "hook pause-status": true,
	"run status": true, "run log": true, "task status": true,
}

var selfAdministrationValueFlags = map[string]bool{
	"--section": true, "--format": true, "-n": true, "--branch": true, "--session": true,
	"--read": true, "--write": true, "--command": true, "--command-success": true,
	"--command-failure": true, "--claim": true,
}

var selfAdministrationBoolFlags = map[string]bool{
	"--json": true, "--deep": true, "--global": true, "--terse": true, "--verbose": true,
	"--list-sections": true, "-h": true, "--help": true,
}

// isSelfAdministrationCommand reports whether every executable position is a
// read-only Reconc diagnostic from a vouched binary, with no file-writing
// redirection and no file-writing or long-running flag.
func isSelfAdministrationCommand(repoRoot, command string) bool {
	if writes, complete := shellcommand.HasFileWriteRedirect(command); writes || !complete {
		return false
	}
	invocations, incomplete := shellcommand.InvocationsWithReason(command, maxShellGuardDepth)
	if incomplete != shellcommand.IncompleteNone || len(invocations) == 0 {
		return false
	}
	for _, invocation := range invocations {
		if !isSelfAdministrationInvocation(repoRoot, invocation) {
			return false
		}
	}
	return true
}

func isSelfAdministrationInvocation(repoRoot string, invocation shellcommand.Invocation) bool {
	words := invocation.Words
	if len(words) < 2 || !isReconcExecutable(words[0]) || !isVouchedReconcBinary(repoRoot, words[0]) {
		return false
	}
	for _, dynamic := range invocation.DynamicWords {
		if dynamic {
			return false
		}
	}
	var path []string
	for index := 1; index < len(words); index++ {
		word := words[index]
		if !strings.HasPrefix(word, "-") {
			if len(path) < 2 {
				path = append(path, word)
			}
			continue
		}
		name, _, joined := strings.Cut(word, "=")
		switch {
		case selfAdministrationBoolFlags[name] && !joined:
		case selfAdministrationValueFlags[name]:
			if !joined {
				index++
			}
		default:
			return false
		}
	}
	if len(path) == 0 {
		return false
	}
	if selfAdministrationCommands[path[0]] {
		return true
	}
	return len(path) == 2 && selfAdministrationCommands[path[0]+" "+path[1]]
}
