package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reconc.dev/reconc/internal/adopt"
)

// runAdopt implements `reconc adopt [repo] [--yaml|--json|--apply]` (W15).
//
// Scans the repo for common tooling (package.json, pyproject.toml,
// Cargo.toml, go.mod, .github/workflows/, dist/, build/, generated/)
// and suggests matching reconc rules.
//
// Modes:
//
//	default:  human-readable text summary + next-steps hints
//	--yaml:   YAML snippet suitable for pasting into .reconc.yml rules:
//	--json:   machine-readable report (agent consumption)
//	--apply:  append suggestions to .reconc.yml (creates the file if absent)
//
// All suggested rules default to mode: warn so adoption doesn't
// immediately break workflows; the user can flip to block once green.
func runAdopt(args []string, stdout, stderr io.Writer) error {
	// --help short-circuit.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc adopt [repo] [--yaml|--json|--apply]")
			fmt.Fprintln(stdout, "Scan the repo for existing tooling and suggest reconc rules.")
			fmt.Fprintln(stdout, "All suggestions are warn-mode by default. Flip to block once green.")
			return nil
		}
	}

	repo := "."
	repoSet := false
	yamlOut := false
	jsonOut := false
	applyOut := false
	for _, a := range args {
		switch a {
		case "--yaml":
			yamlOut = true
		case "--json":
			jsonOut = true
		case "--apply":
			applyOut = true
		default:
			if len(a) > 0 && a[0] == '-' {
				return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc adopt: unknown flag %q", a)}
			}
			if repoSet {
				return &CLIError{ExitCode: 1, Message: "reconc adopt: expected at most one repo path"}
			}
			repo = a
			repoSet = true
		}
	}
	// Mutually exclusive output modes except that --apply can combine
	// with --json (useful for agents that want to know what was added).
	if yamlOut && jsonOut {
		return &CLIError{ExitCode: 1, Message: "reconc adopt: --yaml and --json are mutually exclusive"}
	}
	if yamlOut && applyOut {
		return &CLIError{ExitCode: 1, Message: "reconc adopt: --yaml and --apply are mutually exclusive"}
	}

	abs, err := filepath.Abs(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc adopt: " + err.Error()}
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return &CLIError{ExitCode: 1, Message: "reconc adopt: not a directory: " + abs}
	}

	report, err := adopt.Scan(abs)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc adopt: " + err.Error()}
	}

	if applyOut {
		added, err := adopt.Apply(abs, report)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc adopt --apply: " + err.Error()}
		}
		if jsonOut {
			payload := map[string]interface{}{
				"repo_root":        abs,
				"added":            added,
				"suggestions":      report.Suggestions,
				"pack_suggestions": report.PackSuggestions,
				"detected":         report.Detected,
				"config_path":      filepath.Join(abs, ".reconc.yml"),
			}
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}
		if len(added) == 0 {
			fmt.Fprintf(stdout, "reconc adopt --apply: no new rules (all %d suggestions already present or no conventions detected)\n", len(report.Suggestions))
			return nil
		}
		fmt.Fprintf(stdout, "reconc adopt --apply: added %d rule(s) to %s\n", len(added), filepath.Join(abs, ".reconc.yml"))
		for _, id := range added {
			fmt.Fprintf(stdout, "  - %s\n", id)
		}
		fmt.Fprintln(stdout, "\nNext: reconc refresh")
		return nil
	}

	if jsonOut {
		payload, err := adopt.ToJSON(report, true)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc adopt --json: " + err.Error()}
		}
		_, _ = stdout.Write(payload)
		_, _ = stdout.Write([]byte("\n"))
		return nil
	}

	if yamlOut {
		fmt.Fprint(stdout, adopt.RenderYAML(report))
		return nil
	}

	fmt.Fprint(stdout, adopt.RenderText(report))
	return nil
}
