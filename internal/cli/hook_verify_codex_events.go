package cli

import (
	"bytes"
	jsonv2 "encoding/json/v2"
	"fmt"
	"strings"
	"time"
)

type liveHookCodexStream struct {
	SessionSHA256 string
	Commands      []liveHookCodexCommand
	FileChanges   []liveHookCodexFileChange
}

type liveHookCodexFileChange struct {
	ID, Path, Kind, Status string
}

type liveHookCodexCommand struct {
	ID       string
	Command  string
	ExitCode int
}

type liveHookCodexEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Item     struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Command  string `json:"command"`
		ExitCode *int   `json:"exit_code"`
		Status   string `json:"status"`
		Changes  []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		} `json:"changes"`
	} `json:"item"`
}

// Only the runner's own bounded stdout is eligible input. Assistant messages
// never supply attempts or results. One ephemeral run must finish one turn.
func parseCodexNativeStream(body []byte) (liveHookCodexStream, error) {
	result := liveHookCodexStream{}
	if len(body) == 0 || len(body) > maxHookVerificationOutput || body[len(body)-1] != '\n' {
		return result, fmt.Errorf("native Codex stream is empty, truncated, or oversized")
	}
	started, completed := false, false
	pending := map[string]string{}
	seen := map[string]bool{}
	lines := bytes.Split(bytes.TrimSuffix(body, []byte{'\n'}), []byte{'\n'})
	if len(lines) > 512 {
		return result, fmt.Errorf("native Codex stream has too many events")
	}
	for _, line := range lines {
		var event liveHookCodexEvent
		if err := jsonv2.Unmarshal(line, &event); err != nil || event.Type == "" || completed {
			return liveHookCodexStream{}, fmt.Errorf("native Codex event is malformed or follows completion")
		}
		switch event.Type {
		case "thread.started":
			if result.SessionSHA256 != "" || started || event.ThreadID == "" || len(event.ThreadID) > 512 {
				return liveHookCodexStream{}, fmt.Errorf("native Codex session identity is missing or repeated")
			}
			result.SessionSHA256 = liveHookDigest([]byte(event.ThreadID))
		case "turn.started":
			if result.SessionSHA256 == "" || started {
				return liveHookCodexStream{}, fmt.Errorf("native Codex turn starts outside its session")
			}
			started = true
		case "turn.completed":
			if !started || len(pending) != 0 {
				return liveHookCodexStream{}, fmt.Errorf("native Codex turn has missing attempts or outcomes")
			}
			completed = true
		case "item.started", "item.completed":
			if result.SessionSHA256 == "" {
				return liveHookCodexStream{}, fmt.Errorf("native Codex item precedes session identity")
			}
			if event.Item.Type == "file_change" {
				if !started {
					return liveHookCodexStream{}, fmt.Errorf("native Codex file change precedes its turn")
				}
				if err := applyCodexNativeFileChange(event, pending, seen, &result); err != nil {
					return liveHookCodexStream{}, err
				}
				continue
			}
			if event.Item.Type == "agent_message" || event.Item.Type == "reasoning" || event.Item.Type == "error" {
				continue
			}
			if event.Item.Type != "command_execution" {
				return liveHookCodexStream{}, fmt.Errorf("native Codex run performed an unqualified item type")
			}
			if !started {
				return liveHookCodexStream{}, fmt.Errorf("native Codex command precedes its turn")
			}
			if err := applyCodexNativeCommand(event, pending, seen, &result); err != nil {
				return liveHookCodexStream{}, err
			}
		default:
			return liveHookCodexStream{}, fmt.Errorf("native Codex stream has an unsupported or failed event")
		}
	}
	if !completed {
		return liveHookCodexStream{}, fmt.Errorf("native Codex turn did not complete")
	}
	return result, nil
}

func applyCodexNativeFileChange(event liveHookCodexEvent, pending map[string]string, seen map[string]bool, result *liveHookCodexStream) error {
	item := event.Item
	if item.ID == "" || len(item.ID) > 512 || len(item.Changes) == 0 || len(item.Changes) > 16 || len(result.FileChanges)+len(item.Changes) > 128 {
		return fmt.Errorf("native Codex file change is incomplete or oversized")
	}
	var identity strings.Builder
	identity.WriteString("file-change:")
	for _, change := range item.Changes {
		if change.Path == "" || len(change.Path) > 4096 || change.Kind != "add" && change.Kind != "update" && change.Kind != "delete" {
			return fmt.Errorf("native Codex file change has an invalid path or kind")
		}
		fmt.Fprintf(&identity, "%q:%q;", change.Path, change.Kind)
	}
	if event.Type == "item.started" {
		if seen[item.ID] || item.Status != "in_progress" {
			return fmt.Errorf("native Codex file change start is repeated or contradictory")
		}
		seen[item.ID], pending[item.ID] = true, identity.String()
		return nil
	}
	if item.Status != "completed" && item.Status != "failed" || seen[item.ID] && pending[item.ID] != identity.String() {
		return fmt.Errorf("native Codex file change outcome is repeated or contradicts its start")
	}
	seen[item.ID] = true
	delete(pending, item.ID)
	for _, change := range item.Changes {
		result.FileChanges = append(result.FileChanges, liveHookCodexFileChange{ID: item.ID, Path: change.Path, Kind: change.Kind, Status: item.Status})
	}
	return nil
}

func applyCodexNativeCommand(event liveHookCodexEvent, pending map[string]string, seen map[string]bool, result *liveHookCodexStream) error {
	item := event.Item
	if item.ID == "" || len(item.ID) > 512 || item.Command == "" || len(item.Command) > 64<<10 {
		return fmt.Errorf("native Codex command identity is unavailable or oversized")
	}
	if event.Type == "item.started" {
		if seen[item.ID] || item.ExitCode != nil || item.Status != "in_progress" || len(seen) >= 128 {
			return fmt.Errorf("native Codex command attempt is repeated or contradictory")
		}
		seen[item.ID], pending[item.ID] = true, "command:"+item.Command
		return nil
	}
	command, exists := pending[item.ID]
	if !exists || command != "command:"+item.Command || item.ExitCode == nil || *item.ExitCode < 0 || *item.ExitCode > 255 || item.Status != "completed" && item.Status != "failed" {
		return fmt.Errorf("native Codex command outcome lacks its matching attempt")
	}
	if item.Status == "completed" && *item.ExitCode != 0 || item.Status == "failed" && *item.ExitCode == 0 {
		return fmt.Errorf("native Codex command status contradicts its exit code")
	}
	delete(pending, item.ID)
	result.Commands = append(result.Commands, liveHookCodexCommand{ID: item.ID, Command: item.Command, ExitCode: *item.ExitCode})
	return nil
}

type liveHookNativeRejection struct {
	ObservedAt    time.Time
	CommandSHA256 string
}

// Codex's JSON item stream omits pre-hook rejections. Its router emits the
// rejected command on native stderr. Accept only that exact diagnostic framing;
// the runner must additionally bind the host, process stream, run, and call.
// A changed diagnostic format leaves the operation unproven.
func parseCodexNativeRejections(body []byte) ([]liveHookNativeRejection, error) {
	if len(body) > maxHookVerificationOutput {
		return nil, fmt.Errorf("native Codex diagnostics exceed their limit")
	}
	const prefix = "ERROR codex_core::tools::router: error=Command blocked by PreToolUse hook: "
	var stamp time.Time
	var entry strings.Builder
	rejections := []liveHookNativeRejection{}
	flush := func() error {
		if stamp.IsZero() {
			return nil
		}
		record, err := decodeCodexNativeRejection(stamp, entry.String())
		if err != nil || len(rejections) >= 128 {
			return fmt.Errorf("native Codex rejection is incomplete or outside its contract")
		}
		rejections = append(rejections, record)
		return nil
	}
	for _, line := range strings.SplitAfter(string(body), "\n") {
		date, remainder, found := strings.Cut(line, " ")
		at, err := time.Parse(time.RFC3339Nano, date)
		if found && err == nil {
			if err := flush(); err != nil {
				return nil, err
			}
			stamp, entry = time.Time{}, strings.Builder{}
			if strings.HasPrefix(strings.TrimLeft(remainder, " "), prefix) {
				stamp = at
			}
		}
		if !stamp.IsZero() {
			entry.WriteString(line)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return rejections, nil
}

func decodeCodexNativeRejection(stamp time.Time, entry string) (liveHookNativeRejection, error) {
	const marker = " Command: "
	index := strings.LastIndex(entry, marker)
	if index < 0 || strings.Count(entry, marker) != 1 || !strings.HasSuffix(entry, "\n") {
		return liveHookNativeRejection{}, fmt.Errorf("missing rejected command or terminator")
	}
	command := strings.TrimSuffix(entry[index+len(marker):], "\n")
	if command == "" {
		return liveHookNativeRejection{}, fmt.Errorf("missing rejected command")
	}
	return liveHookNativeRejection{ObservedAt: stamp, CommandSHA256: liveHookDigest([]byte(command))}, nil
}

// Correlation is necessary, but cannot establish stream ownership, executable
// identity, configuration stability, or filesystem effects. The native runner
// must verify those separately before marking any operation complete.
func correlateCodexNativeDenial(receipt *liveHookReceipt, records []liveHookProbeRecord, stream liveHookCodexStream, rejections []liveHookNativeRejection, command string, finished time.Time) (string, error) {
	if err := validateLiveHookCaptureBindings(records, receipt, finished); err != nil {
		return "", err
	}
	if !liveHookHex(stream.SessionSHA256, 32) || command == "" {
		return "", fmt.Errorf("native Codex denial has no native session or exact probe command")
	}
	digest := liveHookDigest([]byte(command))
	var match *liveHookProbeRecord
	for index := range records {
		record := &records[index]
		binding := record.Binding
		if record.Route != "codex-pre-tool-use" || binding.CommandSHA256 != digest {
			continue
		}
		if match != nil || binding.SessionSHA256 != stream.SessionSHA256 || binding.CallSHA256 == "" || binding.TurnSHA256 == "" || binding.ToolNameSHA256 == "" || binding.ToolInputSHA256 == "" || binding.PolicyDecision != "block" || binding.Decision != "deny" || record.ExitCode != 2 {
			return "", fmt.Errorf("native Codex denial has ambiguous, foreign, or unproven policy evidence")
		}
		match = record
	}
	if match == nil {
		return "", fmt.Errorf("native Codex denial lacks its captured pre-tool attempt")
	}
	if err := validateCodexDenialTurn(records, stream.SessionSHA256, match.Binding); err != nil {
		return "", err
	}
	count := 0
	for _, rejection := range rejections {
		if rejection.CommandSHA256 != digest {
			continue
		}
		count++
		decisionFinished := match.Binding.StartedAt.Add(time.Duration(match.DurationNanos))
		if rejection.ObservedAt.Before(decisionFinished) || rejection.ObservedAt.After(finished) {
			return "", fmt.Errorf("native Codex rejection falls outside its captured decision and run")
		}
	}
	if count != 1 {
		return "", fmt.Errorf("native Codex denial lacks one unique matching native rejection")
	}
	return match.Binding.CallSHA256, nil
}

func validateCodexDenialTurn(records []liveHookProbeRecord, session string, attempt *liveHookCaptureBinding) error {
	count := 0
	for _, record := range records {
		binding := record.Binding
		if binding.SessionSHA256 != session {
			return fmt.Errorf("native Codex capture contains an observation from another session")
		}
		if record.Route != "codex-user-prompt-submit" {
			continue
		}
		count++
		if binding.TurnSHA256 != attempt.TurnSHA256 || binding.StartedAt.After(attempt.StartedAt) {
			return fmt.Errorf("native Codex denial does not belong to the captured prompt turn")
		}
	}
	if count != 1 {
		return fmt.Errorf("native Codex denial lacks one unique captured prompt turn")
	}
	return nil
}
