package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/audit"
	"reconc.dev/reconc/internal/completiongate"
	"reconc.dev/reconc/internal/contextsize"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
	"reconc.dev/reconc/internal/tasklifecycle"
)

const agentBriefingFormatVersion = "1"

// runSessionBriefing emits one bounded, versioned machine handshake for
// session entry and reentry. It combines policy, typed TASK, and repository-run
// state without starting Git, loading audit history, or mutating repository
// state. Project-specific reference material remains explicitly on demand.
func runSessionBriefing(args []string, stdout, stderr io.Writer) error {
	repo := "."
	repoSet := false
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc session-briefing [repo] [--json]")
			fmt.Fprintln(stdout, "Compact session-start delta: current TASK, policy blockers, evidence, and remediation.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc session-briefing: unknown flag %q", a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc session-briefing: expected at most one repo path"}
			}
			repo = a
			repoSet = true
		}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc session-briefing: " + err.Error()}
	}

	briefing := compactSessionBriefing(buildSessionBriefing(abs))

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(briefing)
	}

	// Text form stays delta-oriented. Archive history and aggregate audit
	// counters are deliberately excluded from the session hot path.
	fmt.Fprintf(stdout, "Session briefing for %s\n", briefing["repo_root"])
	if task, ok := briefing["task"].(tasklifecycle.Briefing); ok {
		if task.Current != nil {
			fmt.Fprintf(stdout, "  TASK:          %s %s -> %s\n", task.Current.ID, task.Current.Title, task.Current.Path)
			if task.Current.CurrentSubTask != "" {
				fmt.Fprintf(stdout, "  Sub-Task:      %s\n", task.Current.CurrentSubTask)
			}
		}
		for _, blocker := range task.Blockers {
			fmt.Fprintf(stdout, "  Blocker:       %s %s\n", blocker.ID, blocker.Reason)
		}
		if task.OmittedBlockers > 0 {
			fmt.Fprintf(stdout, "  Blocker:       +%d more\n", task.OmittedBlockers)
		}
	}
	if taskErr, ok := briefing["task_error"].(string); ok && taskErr != "" {
		fmt.Fprintf(stdout, "  TASK error:    %s\n", boundedBriefingText(taskErr))
	}
	if runStatus, ok := briefing["run"].(agentsession.RepositoryRunStatus); ok {
		fmt.Fprintf(stdout, "  Run:           %s\n", strings.TrimPrefix(formatRunStatus(runStatus), "run: "))
	} else if runErr, ok := briefing["run_error"].(string); ok && runErr != "" {
		fmt.Fprintf(stdout, "  Run error:     %s\n", boundedBriefingText(runErr))
	}
	fmt.Fprintf(stdout, "  Policy delta:  %s\n", briefing["policy_delta"])
	if blockers, ok := briefing["policy_blockers"].([]policyBriefingBlocker); ok {
		for _, blocker := range blockers {
			fmt.Fprintf(stdout, "  Gate:          [%s] %s\n", blocker.ID, blocker.Action)
		}
	}
	if omitted, ok := briefing["omitted_policy_blockers"].(int); ok && omitted > 0 {
		fmt.Fprintf(stdout, "  Gate:          +%d more\n", omitted)
	}
	if evidence, ok := briefing["required_evidence"].([]string); ok && len(evidence) > 0 {
		fmt.Fprintf(stdout, "  Evidence:      %s\n", strings.Join(evidence, "; "))
	}
	if task, ok := briefing["task"].(tasklifecycle.Briefing); ok && task.OmittedEvidence > 0 {
		fmt.Fprintf(stdout, "  Evidence:      +%d more\n", task.OmittedEvidence)
	}
	if reportPath, ok := briefing["report_path"].(string); ok && reportPath != "" {
		fmt.Fprintf(stdout, "  Report:        %s\n", reportPath)
	}
	if remediation, ok := briefing["remediation"].(string); ok && remediation != "" {
		fmt.Fprintf(stdout, "  Next:          %s\n", remediation)
	}
	return nil
}

type policyBriefingBlocker struct {
	ID     string `json:"id"`
	Action string `json:"action"`
}

func compactSessionBriefing(full map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{
		"format_version": full["format_version"],
		"repo_root":      full["repo_root"],
		"policy_delta":   full["lockfile_status"],
	}
	if nextAction, exists := full["next_action"]; exists && nextAction != nil {
		out["remediation"] = nextAction
	}
	for _, key := range []string{"task", "task_error", "run", "run_error", "policy_blockers", "omitted_policy_blockers", "required_evidence", "report_path"} {
		if value, exists := full[key]; exists && value != nil {
			out[key] = value
		}
	}
	if conflicts, ok := full["conflicts"].(int); ok && conflicts > 0 {
		out["policy_delta"] = fmt.Sprintf("%v; %d conflict(s)", full["lockfile_status"], conflicts)
	}
	return out
}

// buildSessionBriefing collects the facts a session-start agent needs
// in one decode. Returns a map so text + JSON output render from the
// same source.
func buildSessionBriefing(repoRoot string) map[string]interface{} {
	out := map[string]interface{}{
		"format_version":  agentBriefingFormatVersion,
		"repo_root":       repoRoot,
		"lockfile_status": "unknown",
	}

	discovery, err := ingest.DiscoverPolicyRepo(repoRoot)
	if err != nil {
		out["lockfile_status"] = "discovery error: " + err.Error()
		out["next_action"] = "Fix the discovery error (is this a real directory?)"
		addTaskBriefing(out, repoRoot)
		addRunBriefing(out, repoRoot)
		return out
	}
	if !discovery.Discovered {
		out["lockfile_status"] = "no reconc config found"
		out["next_action"] = "run `reconc init " + repoRoot + "` to scaffold a starting config"
		addTaskBriefing(out, repoRoot)
		addRunBriefing(out, repoRoot)
		return out
	}
	out["repo_root"] = discovery.RepoRoot
	if validation, err := validatePolicyReadOnly(discovery.RepoRoot); err != nil {
		out["lockfile_status"] = "source error: " + err.Error()
		out["next_action"] = "fix policy sources, then run `reconc refresh " + discovery.RepoRoot + "`"
	} else {
		out["source_count"] = validation.sourceCount
		out["conflicts"] = validation.conflicts
		if err := runtime.ValidatePolicyLockfile(discovery.RepoRoot); err != nil {
			out["lockfile_status"] = err.Error()
			out["next_action"] = "run `reconc refresh " + discovery.RepoRoot + "`"
		} else if payload, err := readLockfileSummary(discovery.RepoRoot); err != nil {
			out["lockfile_status"] = "lockfile unreadable: " + err.Error()
			out["next_action"] = "run `reconc refresh " + discovery.RepoRoot + "`"
		} else {
			out["lockfile_status"] = "fresh"
			out["rule_count"] = int(jsonNumberAsIntDefault(payload["rule_count"], 0))
			out["source_count"] = int(jsonNumberAsIntDefault(payload["source_count"], 0))
			lockPath := filepath.Join(discovery.RepoRoot, ingest.LockfilePath)
			if lockInfo, err := os.Stat(lockPath); err == nil {
				out["lockfile_modified"] = lockInfo.ModTime().UTC().Format(time.RFC3339)
			}
		}
	}

	// Suggest a next action if one is obvious.
	if cnt, ok := out["conflicts"].(int); ok && cnt > 0 {
		out["next_action"] = "address " + itoaCLI(cnt) + " rule conflict(s), then run `reconc refresh " + discovery.RepoRoot + "`"
	}
	addTaskBriefing(out, discovery.RepoRoot)
	addRunBriefing(out, discovery.RepoRoot)
	addActivePolicyBriefing(out, discovery.RepoRoot)
	return out
}

func addRunBriefing(out map[string]interface{}, repoRoot string) {
	status, err := agentsession.ReadRepositoryRunStatus(repoRoot)
	if err != nil {
		out["run_error"] = boundedBriefingText(err.Error())
		return
	}
	out["run"] = status
}

func addTaskBriefing(out map[string]interface{}, repoRoot string) {
	if board, taskErr := tasklifecycle.Inspect(repoRoot); taskErr != nil {
		out["task_error"] = taskErr.Error()
		if _, exists := out["next_action"]; !exists {
			out["next_action"] = "repair TASK state with `reconc task validate " + repoRoot + "`"
		}
	} else if board != nil {
		taskBriefing := tasklifecycle.BuildBriefing(board)
		taskRemediation := taskBriefing.Remediation
		missingEvidence := append([]string{}, taskBriefing.RequiredEvidence...)
		taskBriefing.Remediation = ""
		taskBriefing.RequiredEvidence = nil
		out["task"] = taskBriefing
		if len(missingEvidence) > 0 {
			out["required_evidence"] = missingEvidence
		}
		if _, exists := out["next_action"]; !exists {
			out["next_action"] = taskRemediation
		}
	}
}

func addActivePolicyBriefing(out map[string]interface{}, repoRoot string) {
	if status, _ := out["lockfile_status"].(string); status != "fresh" {
		return
	}
	sessionID, err := agentsession.ResolveActiveSessionID(repoRoot)
	if err != nil || sessionID == "" {
		return
	}
	state, err := agentsession.LoadSessionState(repoRoot, sessionID)
	if err != nil || state.ReportPath == "" {
		return
	}
	body, err := os.ReadFile(state.ReportPath)
	if err != nil || len(body) > 1<<20 {
		return
	}
	var report runtime.CheckReport
	if err := json.Unmarshal(body, &report); err != nil {
		return
	}
	if filepath.Clean(report.RepoRoot) != filepath.Clean(repoRoot) {
		return
	}
	blockers := make([]policyBriefingBlocker, 0, 3)
	omittedBlockers := 0
	evidence, _ := out["required_evidence"].([]string)
	for _, violation := range report.Violations {
		if violation.Mode != "block" && violation.Mode != "fix" {
			continue
		}
		action := strings.TrimSpace(violation.RecommendedAction)
		if action == "" {
			action = strings.TrimSpace(violation.Message)
		}
		action = boundedBriefingText(action)
		if len(blockers) < 3 {
			blockers = append(blockers, policyBriefingBlocker{ID: boundedBriefingText(violation.RuleID), Action: action})
		} else {
			omittedBlockers++
		}
		if action != "" && len(evidence) < 6 {
			evidence = append(evidence, action)
		}
	}
	if len(blockers) == 0 {
		return
	}
	out["policy_blockers"] = blockers
	if omittedBlockers > 0 {
		out["omitted_policy_blockers"] = omittedBlockers
	}
	out["required_evidence"] = cleanBriefingStrings(evidence)
	out["report_path"] = state.ReportPath
	out["next_action"] = "resolve the listed gate(s), then rerun their exact command; full details are in the saved report"
}

func cleanBriefingStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		value = boundedBriefingText(value)
		out = append(out, value)
	}
	return out
}

func boundedBriefingText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= 240 {
		return value
	}
	return string(runes[:239]) + "…"
}

// runContext implements `reconc context size [repo] [--limit N] [--json]` (W43).
//
// Guards the per-session token budget by reporting the canonical entrypoints
// plus the active TASK detail. Exits 1 when total approximate tokens exceed
// the budget so CI gates can block prompt growth.
//
// Subcommand design (instead of a flat `reconc context`) leaves room
// for future related commands (trim, audit-loaded-list, etc) without
// breaking the flag surface.
func runContext(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc context: missing subcommand (size)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage:")
			fmt.Fprintln(stdout, "  reconc context size [repo] [--limit N] [--files PATH[,PATH...]] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Check canonical session entrypoints plus the active TASK against a token budget.")
			fmt.Fprintln(stdout, "Default budget: 20000 approximate tokens (bytes / 4, rounded up per file).")
			fmt.Fprintln(stdout, "--files replaces the default list; paths must stay inside the repository.")
			fmt.Fprintln(stdout, "Exit 1 when budget is exceeded.")
			return nil
		}
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "size":
		return runContextSize(rest, stdout, stderr)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc context: unknown subcommand %q (expected 'size')", sub)}
	}
}

func runContextSize(args []string, stdout, stderr io.Writer) error {
	repo := "."
	repoSet := false
	jsonOut := false
	limit := contextsize.DefaultTokenBudget
	var files []string
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--limit":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc context size: --limit requires an integer"}
			}
			n, err := atoi(args[i+1])
			if err != nil || n <= 0 {
				return &CLIError{ExitCode: 1, Message: "reconc context size: --limit must be a positive integer"}
			}
			limit = n
			i++
		case "--files":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc context size: --files requires a comma-separated list"}
			}
			files = splitCommaList(args[i+1])
			i++
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc context size: unknown flag %q", a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc context size: expected at most one repo path"}
			}
			repo = a
			repoSet = true
		}
		i++
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc context size: " + err.Error()}
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return &CLIError{ExitCode: 1, Message: "reconc context size: not a directory: " + abs}
	}

	if len(files) == 0 {
		files = defaultContextFiles(abs)
	}
	report, err := contextsize.Scan(abs, files, limit)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc context size: " + err.Error()}
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc context size: json encode: " + err.Error()}
		}
		if report.OverBudget {
			return &CLIError{ExitCode: 1, Message: ""}
		}
		return nil
	}

	// Text: one line of summary, then a size table of files that exist.
	status := "OK"
	if report.OverBudget {
		status = "OVER BUDGET"
	}
	fmt.Fprintf(stdout, "Context size [%s]: %d / %d approx tokens (%d bytes total)\n",
		status, report.TotalApproxTokens, report.TokenBudget, report.TotalBytes)
	if report.Largest != "" {
		fmt.Fprintf(stdout, "Largest: %s\n", report.Largest)
	}
	fmt.Fprintln(stdout, "")
	fmt.Fprintf(stdout, "  %-32s %10s  %10s\n", "file", "bytes", "~tokens")
	for _, f := range report.Files {
		marker := "  "
		if !f.Exists {
			marker = "· " // unicode middle dot for absent entries
		}
		fmt.Fprintf(stdout, "%s%-32s %10d  %10d\n", marker, f.Path, f.SizeBytes, f.ApproxTokens)
	}
	if report.OverBudget {
		return &CLIError{ExitCode: 1, Message: ""}
	}
	return nil
}

func defaultContextFiles(repoRoot string) []string {
	files := append([]string(nil), contextsize.DefaultFiles...)
	board, err := tasklifecycle.Inspect(repoRoot)
	if err != nil || board == nil || board.Active == nil {
		return files
	}
	activePath := filepath.ToSlash(filepath.Clean(filepath.Join(
		filepath.Dir(filepath.FromSlash(board.Config.OverviewPath)),
		filepath.FromSlash(board.Active.Path),
	)))
	for _, path := range files {
		if path == activePath {
			return files
		}
	}
	return append(files, activePath)
}

// runStart implements the read-only `reconc start [repo] [--json]` surface.
//
// Renders a canonical, self-contained onboarding / reentry markdown
// document an agent (or a human) can read at session start to know
// exactly where the repo is: compiled rules, recent enforcement
// activity, and where to look for more context. Essentially a
// "welcome + status" page that composes session-briefing + audit
// tail + links to agent-intro.
func runStart(args []string, stdout, stderr io.Writer) error {
	repo := "."
	repoSeen := false
	jsonOut := false
	minimal := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "--minimal":
			minimal = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc start [repo] [--json | --minimal]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Render canonical onboarding context to stdout without mutating the repository.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc start: unknown flag %q", a)}
			}
			if repoSeen {
				return &CLIError{ExitCode: 1, Message: "reconc start: expected at most one repo path"}
			}
			repo = a
			repoSeen = true
		}
	}

	if jsonOut && minimal {
		return &CLIError{ExitCode: 1, Message: "reconc start: --json and --minimal are mutually exclusive"}
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc start: " + err.Error()}
	}
	data, err := buildStartData(abs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc start: " + err.Error()}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}
	md := renderStartMarkdown(data)
	if minimal {
		md = renderStartMinimal(data)
	}
	_, _ = stdout.Write([]byte(md))
	return nil
}

// buildStartData gathers the facts start.md needs. Returns a map so
// text + JSON render from the same source of truth.
func buildStartData(repoRoot string) (map[string]interface{}, error) {
	briefing := buildSessionBriefing(repoRoot)
	briefing["generated_at"] = time.Now().UTC().Format(time.RFC3339)
	stats, err := audit.Stats(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("verify audit evidence: %w", err)
	}
	if stats.TotalEntries > 0 {
		briefing["audit_enabled"] = true
		briefing["audit_total"] = stats.TotalEntries
		briefing["audit_last_hour"] = stats.EntriesLastHour
		briefing["audit_blocking_events_24h"] = stats.BlockingEntriesLast24h
		briefing["audit_latest_decision"] = stats.LatestDecision
		briefing["audit_latest_blocking_count"] = stats.LatestBlockingCount
		if len(stats.TopRules) > 0 {
			briefing["audit_top_rule"] = stats.TopRules[0].RuleID
			briefing["audit_top_rule_count"] = stats.TopRules[0].Count
		}
	}

	// Recent audit entries: last 5 decisions if the log is enabled.
	recent, err := audit.Tail(repoRoot, audit.TailOptions{N: 5})
	if err != nil {
		return nil, fmt.Errorf("verify recent audit evidence: %w", err)
	}
	if len(recent) > 0 {
		briefing["audit_enabled"] = true
		lines := make([]string, 0, len(recent))
		for _, e := range recent {
			lines = append(lines, fmt.Sprintf("%s  %s  %s  %v",
				e.Timestamp, e.Event, e.Decision, e.RuleIDs))
		}
		briefing["recent_decisions"] = lines
	}
	return briefing, nil
}

// renderStartMarkdown formats the start-data map as a human / agent
// readable markdown document. Deterministic output for the same data.
func renderStartMarkdown(d map[string]interface{}) string {
	var b strings.Builder
	b.WriteString("# Session Start\n\n")
	b.WriteString("_Auto-generated by `reconc start`. Safe to overwrite; re-run to refresh._\n\n")
	if ts, ok := d["generated_at"].(string); ok {
		b.WriteString("Generated: " + ts + "\n\n")
	}

	b.WriteString("## Repo state\n\n")
	if root, ok := d["repo_root"].(string); ok {
		b.WriteString("- **Root:** `" + root + "`\n")
	}
	if status, ok := d["lockfile_status"].(string); ok {
		b.WriteString("- **Lockfile:** " + status + "\n")
	}
	if rc, ok := d["rule_count"].(int); ok {
		sc, _ := d["source_count"].(int)
		b.WriteString(fmt.Sprintf("- **Rules:** %d active across %d source(s)\n", rc, sc))
	}
	if cnt, ok := d["conflicts"].(int); ok && cnt > 0 {
		b.WriteString(fmt.Sprintf("- **Conflicts:** %d (run `reconc refresh` to inspect)\n", cnt))
	} else {
		b.WriteString("- **Conflicts:** none\n")
	}
	if runStatus, ok := d["run"].(agentsession.RepositoryRunStatus); ok {
		b.WriteString("- **Run:** `" + strings.TrimPrefix(formatRunStatus(runStatus), "run: ") + "`\n")
	}

	b.WriteString("\n## Recent activity\n\n")
	if enabled, _ := d["audit_enabled"].(bool); enabled {
		total, _ := d["audit_total"].(int)
		hour, _ := d["audit_last_hour"].(int)
		blocking, _ := d["audit_blocking_events_24h"].(int)
		b.WriteString(fmt.Sprintf("- Audit log: %d entries (%d in the last hour, %d blocking events in the last 24h)\n",
			total, hour, blocking))
		if decision, ok := d["audit_latest_decision"].(string); ok && decision != "" {
			count, _ := d["audit_latest_blocking_count"].(int)
			b.WriteString(fmt.Sprintf("- Latest decision: `%s` (%d blocking violations)\n", decision, count))
		}
		if top, ok := d["audit_top_rule"].(string); ok && top != "" {
			cnt, _ := d["audit_top_rule_count"].(int)
			b.WriteString(fmt.Sprintf("- Top firing rule: `%s` (%d fires)\n", top, cnt))
		}
		if recent, ok := d["recent_decisions"].([]string); ok && len(recent) > 0 {
			b.WriteString("\nLast 5 decisions:\n")
			for _, line := range recent {
				b.WriteString("- " + line + "\n")
			}
		}
	} else {
		b.WriteString("- Audit log not enabled. Enable with `RECONC_AUDIT=1` to record decisions.\n")
	}

	b.WriteString("\n## Next action\n\n")
	if na, ok := d["next_action"].(string); ok && na != "" {
		b.WriteString(na + "\n")
	} else {
		b.WriteString("None outstanding.\n")
	}

	b.WriteString("\n## Agent orientation\n\n")
	b.WriteString("Run `reconc agent-intro` for the full command + rule-kind reference.\n")
	b.WriteString("Fast-path decisions: `reconc can write <path>` (exit 0/2).\n")
	b.WriteString("Full check: `reconc check . --write <path> --json`.\n")
	return b.String()
}

func renderStartMinimal(d map[string]interface{}) string {
	var b strings.Builder
	root := strOrEmpty(d["repo_root"])
	if root != "" {
		root = filepath.Base(root)
	}
	lockfile := strOrEmpty(d["lockfile_status"])
	if lockfile == "" {
		lockfile = "unknown"
	}
	ruleCount, _ := d["rule_count"].(int)
	sourceCount, _ := d["source_count"].(int)
	conflicts, _ := d["conflicts"].(int)
	runEnabled := false
	if runStatus, ok := d["run"].(agentsession.RepositoryRunStatus); ok {
		runEnabled = runStatus.Enabled
	}
	fmt.Fprintf(&b, "status: repo=%s lockfile=%s rules=%d sources=%d conflicts=%d run=%t\n",
		root, lockfile, ruleCount, sourceCount, conflicts, runEnabled)
	nextAction := "None outstanding."
	if value, ok := d["next_action"].(string); ok && value != "" {
		nextAction = value
	}
	fmt.Fprintf(&b, "next: %s\n", nextAction)
	b.WriteString("more: run `reconc start` for the full guide.\n")
	return b.String()
}

// runPostTaskCheck exposes the same evidence-complete contract as `done`, but
// retains exit 1 on a failed gate for compatibility with existing hook loops.
func runPostTaskCheck(args []string, stdout, stderr io.Writer) error {
	repo := "."
	jsonOut := false
	requireCleanGit := false
	windowMinutes := 0
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--require-clean-git":
			requireCleanGit = true
		case "--window":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc post-task-check: --window requires minutes"}
			}
			n, err := atoi(args[i+1])
			if err != nil || n <= 0 {
				return &CLIError{ExitCode: 1, Message: "reconc post-task-check: --window must be a positive integer"}
			}
			windowMinutes = n
			i++
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc post-task-check [repo] [--window N] [--require-clean-git] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Evidence-complete pre-done gate. --window is accepted for compatibility;")
			fmt.Fprintln(stdout, "elapsed time never clears a block. Exit 1 on any failed check.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc post-task-check: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	report, err := completiongate.Evaluate(repo, completiongate.Options{
		RequireCleanGit: requireCleanGit, WindowMinutes: windowMinutes,
		PersistDecision: true, DecisionEvent: "post-task-check",
	})
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc post-task-check: " + err.Error()}
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc post-task-check: encode report: " + err.Error()}
		}
		if !report.OK {
			return &CLIError{ExitCode: 1, Message: ""}
		}
		return nil
	}
	if !report.OK {
		renderCompletionBlock(stdout, report)
		return &CLIError{ExitCode: 1, Message: ""}
	}
	fmt.Fprintln(stdout, "All checks passed.")
	return nil
}

func runDone(args []string, stdout, stderr io.Writer) error {
	repo := "."
	repoSet := false
	jsonOut := false
	requireCleanGit := false
	windowMinutes := 0
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--require-clean-git":
			requireCleanGit = true
		case "--window":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc done: --window requires minutes"}
			}
			n, err := atoi(val)
			if err != nil || n <= 0 {
				return &CLIError{ExitCode: 1, Message: "reconc done: --window must be a positive integer"}
			}
			windowMinutes = n
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc done [repo] [--require-clean-git] [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Evidence-complete task-finish gate. Exit 0 = done, 2 = blocked, 1 = error.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc done: unknown flag %q", a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc done: expected at most one repo path"}
			}
			repo = a
			repoSet = true
		}
		i++
	}

	report, err := completiongate.Evaluate(repo, completiongate.Options{
		RequireCleanGit: requireCleanGit, WindowMinutes: windowMinutes,
		PersistDecision: true, DecisionEvent: "done",
	})
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc done: " + err.Error()}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc done: encode report: " + err.Error()}
		}
		if !report.OK {
			return &CLIError{ExitCode: 2, Message: ""}
		}
		return nil
	}
	if report.OK {
		fmt.Fprintln(stdout, "done")
		return nil
	}
	renderCompletionBlock(stdout, report)
	return &CLIError{ExitCode: 2, Message: ""}
}

func renderCompletionBlock(stdout io.Writer, report *completiongate.Report) {
	style := newTextStyler(stdout)
	fmt.Fprintf(stdout, "%s:\n", style.decision("blocked"))
	for _, check := range report.Checks {
		if check.Status == completiongate.StatusFail {
			fmt.Fprintf(stdout, "[%s] %s: %s\n", style.statusTag("FAIL", 4), style.ruleID(check.ID), check.Detail)
		}
	}
	fmt.Fprintf(stdout, "next: %s\n", report.NextAction)
}

// gitIsClean runs `git status --porcelain` and returns (clean, detail).
// Non-git repos return (true, "not a git repo") so the check is a
// no-op there, keeping the gate useful before a repository is initialized.
func gitIsClean(repoRoot string) (bool, string) {
	gitDir := filepath.Join(repoRoot, ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return true, "not a git repo"
	}
	out, err := runGitPorcelain(repoRoot)
	if err != nil {
		return false, "git status failed: " + err.Error()
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return true, ""
	}
	lines := strings.Count(trimmed, "\n") + 1
	return false, fmt.Sprintf("%d unstaged/untracked change(s)", lines)
}

// runGitPorcelain is factored out so tests can replace it if needed.
// Kept minimal: no env scrubbing (git porcelain is read-only) but we
// explicitly set Dir so CI environment doesn't leak.
func runGitPorcelain(repoRoot string) (string, error) {
	cmd := osExecCommand("git", "status", "--porcelain")
	cmd.Dir = repoRoot
	b, err := cmd.Output()
	return string(b), err
}

// osExecCommand is an indirection so the post-task-check test suite
// can stub git calls without actually invoking git binaries in CI.
// Defaults to exec.Command.
var osExecCommand = exec.Command
