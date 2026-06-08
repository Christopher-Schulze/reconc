package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type degenState struct {
	currentName string
	currentPath string
	state       string
	nextStep    string
	modeEnabled bool
	modeSession string
	modeActive  string
	modeReason  string
	modeNudges  int
}

type persistedDegenState struct {
	Enabled          bool   `json:"enabled"`
	SessionID        string `json:"session_id"`
	ActiveRunID      string `json:"active_run_id"`
	NoProgressNudges int    `json:"no_progress_nudges"`
	DisabledReason   string `json:"disabled_reason"`
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		exit("get cwd: %v", err)
	}
	state, err := readDegenState(root)
	if err != nil {
		exit("%v", err)
	}
	fmt.Printf("DEGENMODE_CONTINUE: %s | %s | blockers=none\n", state.currentName, state.nextStep)
	fmt.Printf("state=%s detail=%s\n", state.state, state.currentPath)
	if state.modeEnabled {
		session := state.modeSession
		if state.modeActive != "" {
			session = state.modeActive
		}
		if state.modeNudges > 0 {
			fmt.Printf("degenmode=enabled session=%s no_progress_nudges=%d\n", session, state.modeNudges)
			return
		}
		fmt.Printf("degenmode=enabled session=%s\n", session)
		return
	}
	if state.modeReason != "" {
		fmt.Printf("degenmode=disabled reason=%s\n", state.modeReason)
		return
	}
	fmt.Println("degenmode=disabled")
}

func readDegenState(root string) (degenState, error) {
	tasksPath := filepath.Join(root, "docs", "tasks.md")
	tasksBytes, err := os.ReadFile(tasksPath)
	if err != nil {
		return degenState{}, fmt.Errorf("read docs/tasks.md: %w", err)
	}
	currentName, currentTarget, err := parseCurrentTask(string(tasksBytes))
	if err != nil {
		return degenState{}, err
	}
	detailPath := filepath.Join(root, "docs", filepath.FromSlash(currentTarget))
	detailBytes, err := os.ReadFile(detailPath)
	if err != nil {
		return degenState{}, fmt.Errorf("read docs/%s: %w", currentTarget, err)
	}
	state := parseState(string(detailBytes))
	if state == "" {
		return degenState{}, fmt.Errorf("docs/%s missing State line", currentTarget)
	}
	nextStep := parseActiveSubTask(string(detailBytes))
	if nextStep == "" {
		nextStep = parseFirstOpenSubTask(string(detailBytes))
	}
	if nextStep == "" {
		return degenState{}, fmt.Errorf("docs/%s has no active/open sub-task", currentTarget)
	}
	mode := readPersistedDegenState(root)
	sessionID := mode.SessionID
	if mode.ActiveRunID != "" {
		sessionID = mode.ActiveRunID
	}
	return degenState{
		currentName: currentName,
		currentPath: filepath.ToSlash(filepath.Join("docs", currentTarget)),
		state:       state,
		nextStep:    nextStep,
		modeEnabled: mode.Enabled,
		modeSession: sessionID,
		modeActive:  mode.ActiveRunID,
		modeReason:  mode.DisabledReason,
		modeNudges:  mode.NoProgressNudges,
	}, nil
}

func readPersistedDegenState(root string) persistedDegenState {
	path := filepath.Join(root, ".reconc", "degenmode", "state.json")
	content, err := os.ReadFile(path)
	if err != nil {
		return persistedDegenState{}
	}
	var state persistedDegenState
	if err := json.Unmarshal(content, &state); err != nil {
		return persistedDegenState{DisabledReason: "invalid_state_json"}
	}
	return state
}

func parseCurrentTask(content string) (string, string, error) {
	currentRe := regexp.MustCompile(`(?m)^Current: (TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*) -> (tasks/TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*\.md)$`)
	match := currentRe.FindStringSubmatch(content)
	if match == nil {
		return "", "", fmt.Errorf("docs/tasks.md missing valid Current header for open task")
	}
	row := "- [ ] " + match[1] + " "
	if !strings.Contains(content, row) {
		return "", "", fmt.Errorf("current task %s is not an unchecked task row", match[1])
	}
	return match[1], match[2], nil
}

func parseState(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "State: ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "State: "))
		}
	}
	return ""
}

func parseActiveSubTask(content string) string {
	return parseSubTask(content, "- [~] ")
}

func parseFirstOpenSubTask(content string) string {
	return parseSubTask(content, "- [ ] ")
}

func parseSubTask(content string, prefix string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		}
	}
	return ""
}

func exit(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
