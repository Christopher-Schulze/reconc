package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/adopt"
	"reconc.dev/reconc/internal/extractor"
	"reconc.dev/reconc/internal/lockdiff"
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
	repoSet := false
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
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc extract: expected at most one repo path"}
			}
			repo = a
			repoSet = true
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
