package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	rerrors "reconc.dev/reconc/internal/errors"
)

// Event-payload constants.
const (
	EventKindRead    = "read"
	EventKindWrite   = "write"
	EventKindCommand = "command"
	EventKindClaim   = "claim"

	CommandOutcomeSuccess = "success"
	CommandOutcomeFailure = "failure"

	// ExplicitEvidenceEpoch marks command outcomes asserted directly by an
	// operator or CI invocation. They are current for the complete evaluation
	// snapshot, unlike session outcomes captured before later writes.
	ExplicitEvidenceEpoch uint64 = 1<<64 - 1
)

// CommandResult is one observed command-execution outcome.
type CommandResult struct {
	Command       string `json:"command"`
	Outcome       string `json:"outcome"` // "success" or "failure"
	EvidenceEpoch uint64 `json:"evidence_epoch,omitempty"`
}

// ExecutionInputs is the canonical, normalized runtime evidence:
// what the agent (or harness) READ, WROTE, RAN, ASSERTED, and how
// each command turned out.
//
// The fields are ordered so that policy.evaluator can read them with
// minimal allocation and so that JSON output stays compact.
type ExecutionInputs struct {
	ReadPaths      []string          `json:"read_paths"`
	WritePaths     []string          `json:"write_paths"`
	WriteEpochs    map[string]uint64 `json:"write_epochs,omitempty"`
	Commands       []string          `json:"commands"`
	Claims         []string          `json:"claims"`
	CommandResults []CommandResult   `json:"command_results"`
}

// Empty returns a zero-value ExecutionInputs with non-nil empty slices.
// Helpful for tests and as a starting point for builders.
func Empty() ExecutionInputs {
	return ExecutionInputs{
		ReadPaths:      []string{},
		WritePaths:     []string{},
		WriteEpochs:    map[string]uint64{},
		Commands:       []string{},
		Claims:         []string{},
		CommandResults: []CommandResult{},
	}
}

// MergedWith returns a new ExecutionInputs with the fields of e
// followed by other. Order is preserved (e first, then other) and no
// deduplication is performed - duplicates are the caller's
// responsibility. Used by the CLI to merge explicit
// --read/--write/--command/--claim flags with an events-file payload.
func (e ExecutionInputs) MergedWith(other ExecutionInputs) ExecutionInputs {
	return ExecutionInputs{
		ReadPaths:      appendCopy(e.ReadPaths, other.ReadPaths),
		WritePaths:     appendCopy(e.WritePaths, other.WritePaths),
		WriteEpochs:    mergeWriteEpochs(e.WriteEpochs, other.WriteEpochs),
		Commands:       appendCopy(e.Commands, other.Commands),
		Claims:         appendCopy(e.Claims, other.Claims),
		CommandResults: appendCommandResults(e.CommandResults, other.CommandResults),
	}
}

// LoadExecutionInputs validates and normalizes an evidence JSON
// payload into ExecutionInputs.
//
// Two payload shapes are supported and may be combined:
//
//   - bulk lists keyed by read_paths, write_paths, commands, claims,
//     command_results
//   - a list of typed events under "events" with kind discriminator
//     ("read" | "write" | "command" | "claim"); command events may
//     optionally carry an "outcome"
//
// Validation is strict: any malformed entry returns *EvidenceError
// with an indexed location pointing at the offending element.
func LoadExecutionInputs(payload map[string]interface{}) (ExecutionInputs, error) {
	if payload == nil {
		return Empty(), nil
	}

	reads, err := coerceStringList(payload["read_paths"], "read_paths")
	if err != nil {
		return Empty(), err
	}
	writes, err := coerceStringList(payload["write_paths"], "write_paths")
	if err != nil {
		return Empty(), err
	}
	writeEpochs, err := coerceWriteEpochs(payload["write_epochs"], "write_epochs")
	if err != nil {
		return Empty(), err
	}
	commands, err := coerceStringList(payload["commands"], "commands")
	if err != nil {
		return Empty(), err
	}
	claims, err := coerceStringList(payload["claims"], "claims")
	if err != nil {
		return Empty(), err
	}
	results, err := coerceCommandResultList(payload["command_results"], "command_results")
	if err != nil {
		return Empty(), err
	}

	bulk := ExecutionInputs{
		ReadPaths:      reads,
		WritePaths:     writes,
		WriteEpochs:    writeEpochs,
		Commands:       commands,
		Claims:         claims,
		CommandResults: results,
	}

	rawEvents, ok := payload["events"]
	if !ok || rawEvents == nil {
		return bulk, nil
	}
	eventsList, isList := rawEvents.([]interface{})
	if !isList {
		return Empty(), &rerrors.EvidenceError{Message: "'events' must be a JSON array"}
	}

	merged := bulk
	epoch := maxEvidenceEpoch(merged.WriteEpochs, merged.CommandResults)
	for i, ev := range eventsList {
		parsed, nextEpoch, err := parseEvent(ev, i, epoch)
		if err != nil {
			return Empty(), err
		}
		epoch = nextEpoch
		merged = merged.MergedWith(parsed)
	}
	return merged, nil
}

// LoadExecutionInputsText parses JSON text into ExecutionInputs. The
// `source` label appears in error messages so users can tell stdin
// from a file.
func LoadExecutionInputsText(text, source string) (ExecutionInputs, error) {
	var payload map[string]interface{}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return Empty(), &rerrors.EvidenceError{
			Message: fmt.Sprintf("execution input payload from %s is not valid JSON", source),
			Cause:   err,
		}
	}
	var trailing interface{}
	if err := dec.Decode(&trailing); err != io.EOF {
		return Empty(), &rerrors.EvidenceError{
			Message: fmt.Sprintf("execution input payload from %s must contain exactly one JSON value", source),
			Cause:   err,
		}
	}
	return LoadExecutionInputs(payload)
}

// LoadExecutionInputsFile reads JSON from disk and validates it.
func LoadExecutionInputsFile(path string) (ExecutionInputs, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Empty(), fmt.Errorf("execution input payload file not found: %s", path)
		}
		return Empty(), &rerrors.EvidenceError{
			Message: "read execution input file " + path,
			Cause:   err,
		}
	}
	return LoadExecutionInputsText(string(data), path)
}

// --- helpers ---

func appendCopy(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

func appendCommandResults(a, b []CommandResult) []CommandResult {
	out := make([]CommandResult, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

func mergeWriteEpochs(a, b map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(a)+len(b))
	for path, epoch := range a {
		out[path] = epoch
	}
	for path, epoch := range b {
		if epoch > out[path] {
			out[path] = epoch
		}
	}
	return out
}

func maxEvidenceEpoch(writeEpochs map[string]uint64, results []CommandResult) uint64 {
	var maximum uint64
	for _, epoch := range writeEpochs {
		if epoch > maximum && epoch != ExplicitEvidenceEpoch {
			maximum = epoch
		}
	}
	for _, result := range results {
		if result.EvidenceEpoch > maximum && result.EvidenceEpoch != ExplicitEvidenceEpoch {
			maximum = result.EvidenceEpoch
		}
	}
	return maximum
}

func coerceWriteEpochs(value interface{}, field string) (map[string]uint64, error) {
	out := map[string]uint64{}
	if value == nil {
		return out, nil
	}
	mapping, ok := value.(map[string]interface{})
	if !ok {
		return nil, &rerrors.EvidenceError{Message: fmt.Sprintf("'%s' must be a JSON object", field)}
	}
	for path, raw := range mapping {
		path = strings.TrimSpace(path)
		if path == "" {
			return nil, &rerrors.EvidenceError{Message: fmt.Sprintf("'%s' contains an empty path", field)}
		}
		epoch, err := coerceEvidenceEpoch(raw, field+"."+path)
		if err != nil {
			return nil, err
		}
		out[path] = epoch
	}
	return out, nil
}

func coerceEvidenceEpoch(value interface{}, field string) (uint64, error) {
	if value == nil {
		return 0, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, &rerrors.EvidenceError{Message: fmt.Sprintf("'%s' must be a non-negative integer", field)}
	}
	epoch, err := strconv.ParseUint(number.String(), 10, 64)
	if err != nil {
		return 0, &rerrors.EvidenceError{Message: fmt.Sprintf("'%s' must be a non-negative integer", field)}
	}
	return epoch, nil
}

func coerceStringList(value interface{}, field string) ([]string, error) {
	if value == nil {
		return []string{}, nil
	}
	list, ok := value.([]interface{})
	if !ok {
		return nil, &rerrors.EvidenceError{
			Message: fmt.Sprintf("'%s' must be a JSON array of strings", field),
		}
	}
	out := make([]string, 0, len(list))
	for i, item := range list {
		str, isStr := item.(string)
		if !isStr || strings.TrimSpace(str) == "" {
			return nil, &rerrors.EvidenceError{
				Message: fmt.Sprintf("'%s[%d]' must be a non-empty string", field, i),
			}
		}
		out = append(out, strings.TrimSpace(str))
	}
	return out, nil
}

func coerceCommandResultList(value interface{}, field string) ([]CommandResult, error) {
	if value == nil {
		return []CommandResult{}, nil
	}
	list, ok := value.([]interface{})
	if !ok {
		return nil, &rerrors.EvidenceError{
			Message: fmt.Sprintf("'%s' must be a JSON array of command result objects", field),
		}
	}
	out := make([]CommandResult, 0, len(list))
	for i, item := range list {
		mapping, isMap := item.(map[string]interface{})
		if !isMap {
			return nil, &rerrors.EvidenceError{
				Message: fmt.Sprintf("'%s[%d]' must be a JSON object", field, i),
			}
		}
		ctx := fmt.Sprintf("%s[%d]", field, i)
		cmd, err := requireString(mapping["command"], "command", ctx)
		if err != nil {
			return nil, err
		}
		outcome, err := requireOutcome(mapping["outcome"], ctx+".outcome")
		if err != nil {
			return nil, err
		}
		epoch, err := coerceEvidenceEpoch(mapping["evidence_epoch"], ctx+".evidence_epoch")
		if err != nil {
			return nil, err
		}
		out = append(out, CommandResult{Command: cmd, Outcome: outcome, EvidenceEpoch: epoch})
	}
	return out, nil
}

func parseEvent(ev interface{}, index int, epoch uint64) (ExecutionInputs, uint64, error) {
	mapping, ok := ev.(map[string]interface{})
	if !ok {
		return Empty(), epoch, &rerrors.EvidenceError{
			Message: fmt.Sprintf("events[%d] must be a JSON object", index),
		}
	}
	kindRaw, ok := mapping["kind"]
	if !ok {
		return Empty(), epoch, &rerrors.EvidenceError{
			Message: fmt.Sprintf("events[%d] must contain a string 'kind'", index),
		}
	}
	kindStr, isStr := kindRaw.(string)
	if !isStr || strings.TrimSpace(kindStr) == "" {
		return Empty(), epoch, &rerrors.EvidenceError{
			Message: fmt.Sprintf("events[%d] must contain a string 'kind'", index),
		}
	}
	kind := strings.TrimSpace(kindStr)

	ctx := fmt.Sprintf("events[%d] kind '%s'", index, kind)
	switch kind {
	case EventKindRead:
		path, err := requireString(mapping["path"], "path", ctx)
		if err != nil {
			return Empty(), epoch, err
		}
		out := Empty()
		out.ReadPaths = []string{path}
		return out, epoch, nil
	case EventKindWrite:
		path, err := requireString(mapping["path"], "path", ctx)
		if err != nil {
			return Empty(), epoch, err
		}
		if epoch < ExplicitEvidenceEpoch-1 {
			epoch++
		}
		out := Empty()
		out.WritePaths = []string{path}
		out.WriteEpochs[path] = epoch
		return out, epoch, nil
	case EventKindCommand:
		cmd, err := requireString(mapping["command"], "command", ctx)
		if err != nil {
			return Empty(), epoch, err
		}
		out := Empty()
		out.Commands = []string{cmd}
		if outcomeRaw, present := mapping["outcome"]; present && outcomeRaw != nil {
			outcome, err := requireOutcome(outcomeRaw, ctx+" outcome")
			if err != nil {
				return Empty(), epoch, err
			}
			out.CommandResults = []CommandResult{{Command: cmd, Outcome: outcome, EvidenceEpoch: epoch}}
		}
		return out, epoch, nil
	case EventKindClaim:
		claim, err := requireString(mapping["claim"], "claim", ctx)
		if err != nil {
			return Empty(), epoch, err
		}
		out := Empty()
		out.Claims = []string{claim}
		return out, epoch, nil
	default:
		return Empty(), epoch, &rerrors.EvidenceError{
			Message: fmt.Sprintf("events[%d] kind '%s' is unsupported; expected one of: claim, command, read, write", index, kind),
		}
	}
}

func requireString(value interface{}, field, context string) (string, error) {
	if value == nil {
		return "", &rerrors.EvidenceError{
			Message: fmt.Sprintf("%s requires a string '%s'", context, field),
		}
	}
	str, ok := value.(string)
	if !ok || strings.TrimSpace(str) == "" {
		return "", &rerrors.EvidenceError{
			Message: fmt.Sprintf("%s requires a string '%s'", context, field),
		}
	}
	return strings.TrimSpace(str), nil
}

func requireOutcome(value interface{}, field string) (string, error) {
	if value == nil {
		return "", &rerrors.EvidenceError{
			Message: fmt.Sprintf("'%s' must be a non-empty string", field),
		}
	}
	str, ok := value.(string)
	if !ok || strings.TrimSpace(str) == "" {
		return "", &rerrors.EvidenceError{
			Message: fmt.Sprintf("'%s' must be a non-empty string", field),
		}
	}
	outcome := strings.TrimSpace(str)
	if outcome != CommandOutcomeSuccess && outcome != CommandOutcomeFailure {
		return "", &rerrors.EvidenceError{
			Message: fmt.Sprintf("'%s' must be one of: failure, success", field),
		}
	}
	return outcome, nil
}
