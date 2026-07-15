package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/templates"
)

// runTemplate implements `reconc template <list|show>` (W18).
//
// Templates are reusable rule shapes that users can reference by name
// in .reconc.yml via `template: NAME`. At parse time the template's
// fields merge in as defaults. Handy for the same rule pattern across
// many paths (tests-follow-source, docs-follow-code, etc.).
func runTemplate(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc template: missing subcommand (list | show)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage:")
			fmt.Fprintln(stdout, "  reconc template list [--json]")
			fmt.Fprintln(stdout, "  reconc template show <name> [--json]")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Templates live in $RECONC_HOME/templates/ (user) and the embedded")
			fmt.Fprintln(stdout, "builtin/ set. User templates override builtins on name collision.")
			return nil
		}
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return runTemplateList(rest, stdout, stderr)
	case "show":
		return runTemplateShow(rest, stdout, stderr)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc template: unknown subcommand %q (expected list or show)", sub)}
	}
}

func runTemplateList(args []string, stdout, stderr io.Writer) error {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc template list: unknown flag %q", a)}
		}
	}
	list, err := templates.List()
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc template list: " + err.Error()}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "No templates available.")
		return nil
	}
	fmt.Fprintf(stdout, "Templates (%d total):\n", len(list))
	for _, t := range list {
		fmt.Fprintf(stdout, "  %-30s [%s]  %s\n", t.Name, t.Source, t.Description)
	}
	return nil
}

func runTemplateShow(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc template show: missing <name> argument"}
	}
	name := args[0]
	jsonOut := false
	for _, a := range args[1:] {
		switch a {
		case "--json":
			jsonOut = true
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc template show: unknown flag %q", a)}
		}
	}
	tmpl, err := templates.Resolve(name)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc template show: " + err.Error()}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tmpl)
	}
	fmt.Fprintf(stdout, "Template: %s [%s]\n", tmpl.Name, tmpl.Source)
	fmt.Fprintf(stdout, "Source:   %s\n", tmpl.Path)
	fmt.Fprintf(stdout, "About:    %s\n", tmpl.Description)
	fmt.Fprintln(stdout, "Body:")
	for _, k := range sortedMapKeys(tmpl.Body) {
		if k == "description" {
			continue
		}
		fmt.Fprintf(stdout, "  %s: %v\n", k, tmpl.Body[k])
	}
	return nil
}

// runPreset implements `reconc preset <list|show> [name] [--json]`.
func runPreset(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc preset: missing subcommand (list | show)"}
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Fprintln(stdout, "Usage: reconc preset list [--json]")
		fmt.Fprintln(stdout, "       reconc preset show <name>")
		return nil
	case "list":
		return runPresetList(args[1:], stdout, stderr)
	case "show":
		return runPresetShow(args[1:], stdout, stderr)
	}
	return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc preset: unknown subcommand %q", args[0])}
}

func runPresetList(args []string, stdout, stderr io.Writer) error {
	jsonOut := false
	outputPath := ""
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc preset list: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc preset list [--json] [--output PATH]")
			return nil
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc preset list: unknown flag %q", a)}
		}
		i++
	}
	list, err := presets.List()
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc preset list: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc preset list: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()
	if jsonOut {
		payload := map[string]interface{}{
			"preset_count": len(list),
			"presets":      list,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
		return nil
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "No presets available.")
		return nil
	}
	fmt.Fprintln(out, "Bundled and user presets:")
	for _, p := range list {
		fmt.Fprintf(out, "  %s (%s)  %s\n", p.Name, p.Source, p.Path)
	}
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Extend any of these from .reconc.yml:")
	fmt.Fprintln(out, "  extends: [<name>, ...]")
	return nil
}

func runPresetShow(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc preset show: missing preset name"}
	}
	name := args[0]
	jsonOut := false
	outputPath := ""
	i := 1
	for i < len(args) {
		a := args[i]
		switch a {
		case "--json":
			jsonOut = true
		case "--output":
			val, ok := nextArgValue(args, &i, a)
			if !ok {
				return &CLIError{ExitCode: 1, Message: "reconc preset show: --output requires a path"}
			}
			outputPath = val
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc preset show <name> [--json] [--output PATH]")
			return nil
		default:
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc preset show: unknown flag %q", a)}
		}
		i++
	}
	content, err := presets.Load(name)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc preset show: " + err.Error()}
	}
	path, source, err := presets.Path(name)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc preset show: " + err.Error()}
	}
	out, closeOutput, err := teeToFile(stdout, outputPath)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc preset show: open output file: " + err.Error()}
	}
	defer func() { _ = closeOutput() }()
	if jsonOut {
		payload := map[string]interface{}{
			"name":    name,
			"path":    path,
			"source":  source,
			"content": content,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
		return nil
	}
	// Plain content to stdout so users can redirect into a file.
	fmt.Fprint(out, content)
	return nil
}
