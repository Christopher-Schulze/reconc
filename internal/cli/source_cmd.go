package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reconc.dev/reconc/internal/adopt"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/extractor"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/lockdiff"
	"strings"
	"time"
)

// runExtract implements `reconc extract [repo] [--from PATH] [--yaml|--json]` (W20).
//
// Heuristic scan of AGENTS.md / CLAUDE.md prose for concrete rule
// hints (deny-write phrases, generated-file declarations, "run X
// before committing" patterns, secret mentions, CI gating). Emits
// suggestions in the same format as `reconc adopt` so the two
// commands feed the same downstream apply path.
//
// Deterministic. Pure heuristic. No LLM. False negatives by design:
// when in doubt, skip.
func runExtract(args []string, stdout, stderr io.Writer) error {
	repo := "."
	from := ""
	yamlOut := false
	jsonOut := false
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--yaml":
			yamlOut = true
		case "--from":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc extract: --from requires a path"}
			}
			from = args[i+1]
			i++
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc extract [repo] [--from PATH] [--yaml|--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Heuristic scan of AGENTS.md / CLAUDE.md prose for rule hints.")
			fmt.Fprintln(stdout, "Defaults to AGENTS.md; use --from to pick a specific file.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc extract: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}
	if yamlOut && jsonOut {
		return &CLIError{ExitCode: 1, Message: "reconc extract: --yaml and --json are mutually exclusive"}
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc extract: " + err.Error()}
	}

	var contents []byte
	var sourcePath string
	if from != "" {
		sourcePath = from
		contents, err = os.ReadFile(filepath.Join(abs, from))
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc extract: read " + from + ": " + err.Error()}
		}
	} else {
		// Try AGENTS.md then CLAUDE.md.
		for _, candidate := range []string{"AGENTS.md", "CLAUDE.md"} {
			path := filepath.Join(abs, candidate)
			if b, rerr := os.ReadFile(path); rerr == nil {
				contents = b
				sourcePath = candidate
				break
			}
		}
		if sourcePath == "" {
			return &CLIError{ExitCode: 1, Message: "reconc extract: no AGENTS.md or CLAUDE.md found (use --from PATH)"}
		}
	}

	suggestions := extractor.Extract(string(contents))

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]interface{}{
			"repo_root":   abs,
			"source":      sourcePath,
			"suggestions": suggestions,
		})
	}
	if yamlOut {
		fmt.Fprint(stdout, adopt.RenderYAML(adopt.Report{
			RepoRoot:    abs,
			Suggestions: suggestions,
		}))
		return nil
	}

	if len(suggestions) == 0 {
		fmt.Fprintf(stdout, "reconc extract: no rule hints detected in %s\n", sourcePath)
		return nil
	}
	fmt.Fprintf(stdout, "Extracted %d rule hint(s) from %s:\n\n", len(suggestions), sourcePath)
	for i, s := range suggestions {
		fmt.Fprintf(stdout, "%d. %s (%s)\n     %s\n", i+1, s.ID, s.Kind, s.Reason)
		if len(s.Paths) > 0 {
			fmt.Fprintf(stdout, "     -> paths: %s\n", strings.Join(s.Paths, ", "))
		}
		if len(s.Commands) > 0 {
			fmt.Fprintf(stdout, "     -> commands: %s\n", strings.Join(s.Commands, ", "))
		}
		if len(s.Claims) > 0 {
			fmt.Fprintf(stdout, "     -> claims: %s\n", strings.Join(s.Claims, ", "))
		}
		if len(s.Evidence) > 0 {
			fmt.Fprintf(stdout, "     cite:   %s\n", s.Evidence[0])
		}
	}
	fmt.Fprintln(stdout, "\nNext:")
	fmt.Fprintln(stdout, "  - Review each suggestion against the source.")
	fmt.Fprintf(stdout, "  - Preview YAML:   reconc extract %s --yaml\n", abs)
	fmt.Fprintf(stdout, "  - JSON for agent: reconc extract %s --json\n", abs)
	return nil
}

// runDiff implements `reconc diff <lockA> <lockB> [--json]` (W5).
//
// Structural JSON-level comparison of two lockfiles. Matches rules by
// id and reports added / removed / changed, plus default-mode drift
// and source-digest shift. Intended for PR reviews: "what did this
// commit change in the compiled policy?"
func runDiff(args []string, stdout, stderr io.Writer) error {
	jsonOut := false
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc diff <lockfile-a> <lockfile-b> [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Compare two compiled lockfiles. Reports added / removed / changed")
			fmt.Fprintln(stdout, "rules, default-mode drift, source-digest shift.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc diff: unknown flag %q", a)}
			}
			positional = append(positional, a)
		}
	}
	if len(positional) != 2 {
		return &CLIError{ExitCode: 1, Message: "reconc diff: usage: reconc diff <lockfile-a> <lockfile-b>"}
	}
	report, err := lockdiff.Diff(positional[0], positional[1])
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc diff: " + err.Error()}
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Fprintf(stdout, "Diff %s -> %s\n", report.PathA, report.PathB)
	if report.IsEmpty() && !report.DefaultModeDiff && report.DigestA == report.DigestB {
		fmt.Fprintln(stdout, "No changes.")
		return nil
	}
	if report.DefaultModeDiff {
		fmt.Fprintf(stdout, "  default_mode: %s -> %s\n", report.DefaultModeA, report.DefaultModeB)
	}
	if report.DigestA != report.DigestB {
		fmt.Fprintf(stdout, "  source_digest: %s -> %s\n", short12(report.DigestA), short12(report.DigestB))
	}
	if len(report.Added) > 0 {
		fmt.Fprintf(stdout, "\nAdded (%d):\n", len(report.Added))
		for _, r := range report.Added {
			fmt.Fprintf(stdout, "  + %s (%s, %s)\n", r.ID, r.Kind, r.Mode)
		}
	}
	if len(report.Removed) > 0 {
		fmt.Fprintf(stdout, "\nRemoved (%d):\n", len(report.Removed))
		for _, r := range report.Removed {
			fmt.Fprintf(stdout, "  - %s (%s, %s)\n", r.ID, r.Kind, r.Mode)
		}
	}
	if len(report.Changed) > 0 {
		fmt.Fprintf(stdout, "\nChanged (%d):\n", len(report.Changed))
		for _, c := range report.Changed {
			fmt.Fprintf(stdout, "  ~ %s (%s) -- %s\n", c.ID, c.Kind, strings.Join(c.FieldsChanged, ", "))
		}
	}
	if report.Unchanged > 0 {
		fmt.Fprintf(stdout, "\nUnchanged: %d rules\n", report.Unchanged)
	}
	return nil
}

// short12 returns the first 12 chars of a string (typically a hex
// digest) or the whole string if shorter. Keeps diff output tidy.
func short12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12] + "..."
}

// runWatch implements `reconc watch [repo] [--interval-ms N]` (W6).
//
// Poll-based source watcher: every --interval-ms (default 800) the
// watcher re-scans the policy sources and recompiles if any mtime
// shifted. Purposely poll-based rather than fsnotify-based so we
// don't add a new dep for a dev-convenience command.
//
// Runs forever; exit on Ctrl-C. First recompile happens on startup
// so the first output confirms the watcher is live.
func runWatch(args []string, stdout, stderr io.Writer) error {
	repo := "."
	intervalMS := 800
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--interval-ms":
			if i+1 >= len(args) {
				return &CLIError{ExitCode: 1, Message: "reconc watch: --interval-ms requires an integer"}
			}
			n, err := atoi(args[i+1])
			if err != nil || n < 100 {
				return &CLIError{ExitCode: 1, Message: "reconc watch: --interval-ms must be >= 100"}
			}
			intervalMS = n
			i++
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc watch [repo] [--interval-ms N]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Poll policy sources every N ms and recompile when any mtime changes.")
			fmt.Fprintln(stdout, "Exit with Ctrl-C. Default interval: 800 ms.")
			return nil
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc watch: unknown flag %q", a)}
			}
			repo = a
		}
		i++
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc watch: " + err.Error()}
	}
	discovery, derr := ingest.DiscoverPolicyRepo(abs)
	if derr != nil || !discovery.Discovered {
		return &CLIError{ExitCode: 1, Message: "reconc watch: no reconc config found under " + abs}
	}

	fmt.Fprintf(stdout, "reconc watch: watching %s (poll every %dms, Ctrl-C to exit)\n",
		discovery.RepoRoot, intervalMS)

	// Initial compile so the user gets immediate feedback.
	compileOnce(stdout, stderr, discovery.RepoRoot, "0.1.0-watch")

	lastSig := sourceMTimeSignature(discovery.RepoRoot)
	for {
		time.Sleep(time.Duration(intervalMS) * time.Millisecond)
		sig := sourceMTimeSignature(discovery.RepoRoot)
		if sig == lastSig {
			continue
		}
		lastSig = sig
		compileOnce(stdout, stderr, discovery.RepoRoot, "0.1.0-watch")
	}
}

// compileOnce runs the compiler and prints a tight 1-line status.
// Never returns an error upstream; watch is a best-effort loop.
func compileOnce(stdout, stderr io.Writer, repoRoot, version string) {
	start := time.Now()
	compiled, err := compiler.CompileRepoPolicy(repoRoot, version)
	dur := time.Since(start)
	ts := time.Now().UTC().Format("15:04:05")
	if err != nil {
		fmt.Fprintf(stdout, "[%s] compile failed (%s): %s\n", ts, dur.Round(time.Millisecond), err.Error())
		return
	}
	fmt.Fprintf(stdout, "[%s] compiled %d rules from %d sources in %s\n",
		ts, compiled.RuleCount, compiled.SourceCount, dur.Round(time.Millisecond))
	if len(compiled.Conflicts) > 0 {
		fmt.Fprintf(stdout, "          %d conflict(s): run `reconc refresh` for details\n", len(compiled.Conflicts))
	}
}

// sourceMTimeSignature builds a compact deterministic signature of
// every policy-source mtime under repoRoot. When this changes the
// watcher knows to recompile. Cheap: just stat calls on known paths.
func sourceMTimeSignature(repoRoot string) string {
	var b strings.Builder
	candidates := []string{
		"AGENTS.md", "CLAUDE.md", ".reconc.yml",
	}
	policyDir := filepath.Join(repoRoot, "policies")
	entries, _ := os.ReadDir(policyDir)
	for _, e := range entries {
		if !e.IsDir() {
			n := e.Name()
			if strings.HasSuffix(n, ".yml") || strings.HasSuffix(n, ".yaml") {
				candidates = append(candidates, filepath.ToSlash(filepath.Join("policies", n)))
			}
		}
	}
	for _, rel := range candidates {
		full := filepath.Join(repoRoot, rel)
		if info, err := os.Stat(full); err == nil {
			fmt.Fprintf(&b, "%s=%d;", rel, info.ModTime().UnixNano())
		}
	}
	return b.String()
}
