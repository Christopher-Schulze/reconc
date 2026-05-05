// Package errors is the typed error hierarchy for reconc.
//
// We use concrete error types with Unwrap() so callers can inspect failures
// via errors.Is / errors.As.
//
// Every error type here wraps an optional Cause so underlying IO or parse
// errors remain inspectable without manual string splicing.
//
// Exit-code mapping (see internal/cli):
//
//	PolicySourceError, RuleValidationError, LockfileError,
//	EvidenceError, ReportError, PresetError, GitError -> exit 1
//	(runtime/input error)
//
//	Blocking policy violations are NOT errors in this hierarchy;
//	they are carried on CheckReport values and mapped to exit 2 by
//	the CLI layer.
package errors

import "fmt"

// PolicySourceError is raised for malformed policy source files
// (CLAUDE.md, AGENTS.md, .reconc.yml, policies/*.yml).
type PolicySourceError struct {
	Message string
	Cause   error
}

func (e *PolicySourceError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("policy source: %s: %v", e.Message, e.Cause)
	}
	return "policy source: " + e.Message
}
func (e *PolicySourceError) Unwrap() error { return e.Cause }

// RuleValidationError is raised when a rule document fails validation
// (missing kind, duplicate id, unknown kind, bad field shape).
type RuleValidationError struct {
	Message string
	Cause   error
}

func (e *RuleValidationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("rule validation: %s: %v", e.Message, e.Cause)
	}
	return "rule validation: " + e.Message
}
func (e *RuleValidationError) Unwrap() error { return e.Cause }

// LockfileError is raised when a compiled lockfile is malformed, stale,
// or schema-drifted.
type LockfileError struct {
	Message string
	Cause   error
}

func (e *LockfileError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("lockfile: %s: %v", e.Message, e.Cause)
	}
	return "lockfile: " + e.Message
}
func (e *LockfileError) Unwrap() error { return e.Cause }

// EvidenceError is raised when an execution input / event payload fails
// validation.
type EvidenceError struct {
	Message string
	Cause   error
}

func (e *EvidenceError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("evidence: %s: %v", e.Message, e.Cause)
	}
	return "evidence: " + e.Message
}
func (e *EvidenceError) Unwrap() error { return e.Cause }

// ReportError is raised when a saved check report or fix plan fails
// validation.
type ReportError struct {
	Message string
	Cause   error
}

func (e *ReportError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("report: %s: %v", e.Message, e.Cause)
	}
	return "report: " + e.Message
}
func (e *ReportError) Unwrap() error { return e.Cause }

// PresetError is raised when a bundled preset cannot be loaded or resolved.
type PresetError struct {
	Message string
	Cause   error
}

func (e *PresetError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("preset: %s: %v", e.Message, e.Cause)
	}
	return "preset: " + e.Message
}
func (e *PresetError) Unwrap() error { return e.Cause }

// PresetNotFoundError is raised when a requested preset is not available.
// Distinct type so callers can distinguish "malformed preset" from
// "preset does not exist".
type PresetNotFoundError struct {
	Name string
}

func (e *PresetNotFoundError) Error() string {
	return fmt.Sprintf("preset not found: %q", e.Name)
}

// RepoBoundaryError is raised when a runtime evidence path resolves
// outside the discovered repo root.
type RepoBoundaryError struct {
	Path     string
	RepoRoot string
}

func (e *RepoBoundaryError) Error() string {
	return fmt.Sprintf("path %q escapes repo root %q", e.Path, e.RepoRoot)
}

// GitError is raised when a git invocation fails or its flag
// combination is invalid.
type GitError struct {
	Message string
	Cause   error
}

func (e *GitError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("git: %s: %v", e.Message, e.Cause)
	}
	return "git: " + e.Message
}
func (e *GitError) Unwrap() error { return e.Cause }
