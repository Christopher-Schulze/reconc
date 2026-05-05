// Package tui renders a lightweight terminal dashboard for reconc.
//
// It intentionally avoids a framework dependency. The goal is one terminal
// view of repo, sources, rules, lockfile freshness, audit, and active session
// state.
package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/audit"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/parser"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

// SourceSummary is one source row in the terminal dashboard.
type SourceSummary struct {
	Kind  policy.SourceKind `json:"kind"`
	Path  string            `json:"path"`
	Block string            `json:"block,omitempty"`
}

// RuleSummary is one rule row in the terminal dashboard.
type RuleSummary struct {
	ID     string      `json:"id"`
	Kind   policy.Kind `json:"kind"`
	Mode   policy.Mode `json:"mode"`
	Scope  string      `json:"scope,omitempty"`
	Source string      `json:"source,omitempty"`
}

// View is the complete tui snapshot. It is JSON-serializable so tests
// and automation can assert on the same data rendered for humans.
type View struct {
	RepoRoot        string              `json:"repo_root"`
	Discovered      bool                `json:"discovered"`
	LockfileStatus  string              `json:"lockfile_status"`
	DefaultMode     policy.Mode         `json:"default_mode,omitempty"`
	RuleCount       int                 `json:"rule_count"`
	SourceCount     int                 `json:"source_count"`
	Sources         []SourceSummary     `json:"sources"`
	Rules           []RuleSummary       `json:"rules"`
	Conflicts       []compiler.Conflict `json:"conflicts"`
	AuditTotal      int                 `json:"audit_total"`
	AuditBlocking   int                 `json:"audit_blocking"`
	ActiveSessionID string              `json:"active_session_id,omitempty"`
	NextAction      string              `json:"next_action,omitempty"`
	Errors          []string            `json:"errors,omitempty"`
}

// Build creates a read-only terminal dashboard snapshot for repo.
func Build(repo string) (*View, error) {
	discovery, err := ingest.DiscoverPolicyRepo(repo)
	if err != nil {
		return nil, err
	}
	view := &View{
		RepoRoot:       discovery.RepoRoot,
		Discovered:     discovery.Discovered,
		LockfileStatus: "unknown",
		Sources:        []SourceSummary{},
		Rules:          []RuleSummary{},
		Conflicts:      []compiler.Conflict{},
	}
	if !discovery.Discovered {
		view.LockfileStatus = "not discovered"
		view.Errors = append(view.Errors, discovery.Warnings...)
		view.NextAction = "run `reconc setup " + repo + "`"
		return view, nil
	}

	bundle, err := ingest.LoadPolicySources(discovery.RepoRoot)
	if err != nil {
		view.LockfileStatus = "source error"
		view.Errors = append(view.Errors, err.Error())
		view.NextAction = "fix policy sources, then run `reconc compile .`"
		return view, nil
	}
	view.SourceCount = len(bundle.Sources)
	for _, src := range bundle.Sources {
		view.Sources = append(view.Sources, SourceSummary{
			Kind:  src.Kind,
			Path:  src.Path,
			Block: src.BlockID,
		})
	}

	parsed, err := parser.ParseRuleDocuments(bundle)
	if err != nil {
		view.LockfileStatus = "parse error"
		view.Errors = append(view.Errors, err.Error())
		view.NextAction = "fix rule validation, then run `reconc compile .`"
		return view, nil
	}
	view.DefaultMode = parsed.DefaultMode
	view.RuleCount = len(parsed.Rules)
	for _, rule := range parsed.Rules {
		view.Rules = append(view.Rules, RuleSummary{
			ID:     rule.ID,
			Kind:   rule.Kind,
			Mode:   effectiveMode(rule.Mode, parsed.DefaultMode),
			Scope:  rule.ScopeID,
			Source: rule.SourcePath,
		})
	}
	view.Conflicts = compiler.DetectConflicts(parsed.Rules)
	if view.Conflicts == nil {
		view.Conflicts = []compiler.Conflict{}
	}

	view.LockfileStatus = lockfileStatus(discovery.RepoRoot, compiler.ComputeSourceDigest(bundle))
	if view.LockfileStatus != "fresh" {
		view.NextAction = "run `reconc compile .`"
	}

	if stats, err := audit.Stats(discovery.RepoRoot); err == nil {
		view.AuditTotal = stats.TotalEntries
		view.AuditBlocking = stats.BlockingFires
	}
	if sessionID, err := agentsession.ResolveActiveSessionID(discovery.RepoRoot); err == nil {
		view.ActiveSessionID = sessionID
	}
	if len(view.Conflicts) > 0 && view.NextAction == "" {
		view.NextAction = "inspect static conflicts with `reconc doctor . --deep`"
	}
	return view, nil
}

func effectiveMode(mode, defaultMode policy.Mode) policy.Mode {
	if mode.Valid() {
		return mode
	}
	return defaultMode
}

func lockfileStatus(repoRoot, liveDigest string) string {
	data, err := os.ReadFile(filepath.Join(repoRoot, ingest.LockfilePath))
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "unreadable"
	}
	var payload map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return "invalid"
	}
	if stored, _ := payload["source_digest"].(string); stored == liveDigest {
		return "fresh"
	}
	return "stale"
}

// RenderText renders View as a compact terminal dashboard.
func RenderText(view *View) string {
	var b strings.Builder
	fmt.Fprintf(&b, "reconc tui: %s\n", view.RepoRoot)
	fmt.Fprintf(&b, "  discovered: %t\n", view.Discovered)
	fmt.Fprintf(&b, "  lockfile:   %s\n", view.LockfileStatus)
	fmt.Fprintf(&b, "  rules:      %d\n", view.RuleCount)
	fmt.Fprintf(&b, "  sources:    %d\n", view.SourceCount)
	if view.DefaultMode != "" {
		fmt.Fprintf(&b, "  default:    %s\n", view.DefaultMode)
	}
	if view.ActiveSessionID != "" {
		fmt.Fprintf(&b, "  session:    %s\n", view.ActiveSessionID)
	}
	if view.AuditTotal > 0 {
		fmt.Fprintf(&b, "  audit:      %d entries, %d blocking\n", view.AuditTotal, view.AuditBlocking)
	} else {
		fmt.Fprintf(&b, "  audit:      no entries\n")
	}
	if len(view.Conflicts) > 0 {
		fmt.Fprintf(&b, "  conflicts:  %d\n", len(view.Conflicts))
	}
	if view.NextAction != "" {
		fmt.Fprintf(&b, "  next:       %s\n", view.NextAction)
	}
	if len(view.Errors) > 0 {
		fmt.Fprintf(&b, "\nErrors:\n")
		for _, err := range view.Errors {
			fmt.Fprintf(&b, "  - %s\n", err)
		}
	}

	fmt.Fprintf(&b, "\nSources:\n")
	if len(view.Sources) == 0 {
		fmt.Fprintf(&b, "  none\n")
	} else {
		for _, source := range view.Sources {
			label := source.Path
			if source.Block != "" {
				label += "#" + source.Block
			}
			fmt.Fprintf(&b, "  %-16s %s\n", source.Kind, label)
		}
	}

	fmt.Fprintf(&b, "\nRules:\n")
	if len(view.Rules) == 0 {
		fmt.Fprintf(&b, "  none\n")
	} else {
		limit := len(view.Rules)
		if limit > 30 {
			limit = 30
		}
		for _, rule := range view.Rules[:limit] {
			scope := rule.Scope
			if scope == "" {
				scope = "-"
			}
			fmt.Fprintf(&b, "  %-28s %-23s %-7s scope=%s\n", rule.ID, rule.Kind, rule.Mode, scope)
		}
		if len(view.Rules) > limit {
			fmt.Fprintf(&b, "  ... %d more rule(s)\n", len(view.Rules)-limit)
		}
	}

	if len(view.Conflicts) > 0 {
		fmt.Fprintf(&b, "\nConflicts:\n")
		for _, conflict := range view.Conflicts {
			fmt.Fprintf(&b, "  - [%s] %s\n", conflict.Kind, conflict.Description)
		}
	}

	fmt.Fprintf(&b, "\nGenerated: %s\n", time.Now().UTC().Format(time.RFC3339))
	return b.String()
}
