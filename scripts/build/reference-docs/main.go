// Command reference-docs projects canonical registries into marked Markdown sections.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/commandmeta"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/schema"
)

const (
	maxDocumentBytes = 4 << 20
	maxSectionBytes  = 1 << 20
	maxCommands      = 512
	maxHookSurfaces  = 256
	maxContracts     = 128

	commandBegin = "<!-- BEGIN RECONC GENERATED COMMAND REFERENCE -->"
	commandEnd   = "<!-- END RECONC GENERATED COMMAND REFERENCE -->"
	hookBegin    = "<!-- BEGIN RECONC GENERATED HOOK REFERENCE -->"
	hookEnd      = "<!-- END RECONC GENERATED HOOK REFERENCE -->"
	schemaBegin  = "<!-- BEGIN RECONC GENERATED SCHEMA REFERENCE -->"
	schemaEnd    = "<!-- END RECONC GENERATED SCHEMA REFERENCE -->"
)

type markedSection struct {
	begin string
	end   string
	body  []byte
}

type documentProjection struct {
	path     string
	sections []markedSection
}

type commandRow struct {
	path     string
	synopsis string
	summary  string
	outputs  string
}

func main() {
	if err := mainRun(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "reference-docs:", err)
		os.Exit(1)
	}
}

func mainRun(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("reference-docs", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", ".", "repository root")
	check := flags.Bool("check", false, "fail if generated sections are stale")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: reference-docs [--root PATH] [--check]")
	}
	return run(*root, *check, stdout)
}

func run(root string, check bool, stdout io.Writer) error {
	documents, err := projections()
	if err != nil {
		return err
	}
	var stale []string
	for _, document := range documents {
		changed, err := projectDocument(root, document, check)
		if err != nil {
			return err
		}
		if changed {
			stale = append(stale, document.path)
		}
	}
	if check && len(stale) > 0 {
		return fmt.Errorf("generated reference drift in %s; run make reference-docs", strings.Join(stale, ", "))
	}
	if len(stale) == 0 {
		_, err = fmt.Fprintln(stdout, "reference docs are current")
	} else {
		_, err = fmt.Fprintf(stdout, "updated generated references in %s\n", strings.Join(stale, ", "))
	}
	return err
}

func projections() ([]documentProjection, error) {
	commands, err := renderCommandReference()
	if err != nil {
		return nil, err
	}
	hookReference, err := renderHookReference()
	if err != nil {
		return nil, err
	}
	schemaReference, err := renderSchemaReference()
	if err != nil {
		return nil, err
	}
	return []documentProjection{
		{path: "docs/commands.md", sections: []markedSection{{commandBegin, commandEnd, commands}}},
		{path: "docs/architecture.md", sections: []markedSection{
			{hookBegin, hookEnd, hookReference},
			{schemaBegin, schemaEnd, schemaReference},
		}},
	}, nil
}

func projectDocument(root string, projection documentProjection, check bool) (bool, error) {
	path := filepath.Join(root, filepath.FromSlash(projection.path))
	body, info, err := boundedio.ReadRegularFileSnapshot(path, maxDocumentBytes)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", projection.path, err)
	}
	next := body
	for _, section := range projection.sections {
		next, err = replaceMarkedSection(next, section)
		if err != nil {
			return false, fmt.Errorf("project %s: %w", projection.path, err)
		}
	}
	if bytes.Equal(body, next) {
		return false, nil
	}
	if check {
		return true, nil
	}
	changed, err := atomicfile.WriteIfChanged(path, next, info.Mode().Perm())
	if err != nil {
		return false, fmt.Errorf("publish %s: %w", projection.path, err)
	}
	return changed, nil
}

func replaceMarkedSection(document []byte, section markedSection) ([]byte, error) {
	if len(section.body) > maxSectionBytes {
		return nil, fmt.Errorf("section %q exceeds %d bytes", section.begin, maxSectionBytes)
	}
	begin := []byte(section.begin)
	end := []byte(section.end)
	if bytes.Count(document, begin) != 1 || bytes.Count(document, end) != 1 {
		return nil, fmt.Errorf("markers %q and %q must each appear exactly once", section.begin, section.end)
	}
	start := bytes.Index(document, begin)
	finish := bytes.Index(document, end)
	if start >= finish {
		return nil, fmt.Errorf("marker %q must precede %q", section.begin, section.end)
	}
	finish += len(end)
	next := make([]byte, 0, len(document)+len(section.body))
	next = append(next, document[:start]...)
	next = append(next, begin...)
	next = append(next, '\n')
	next = append(next, section.body...)
	next = append(next, end...)
	next = append(next, document[finish:]...)
	if len(next) > maxDocumentBytes {
		return nil, fmt.Errorf("projected document exceeds %d bytes", maxDocumentBytes)
	}
	return next, nil
}

func renderCommandReference() ([]byte, error) {
	rows, err := publicCommandRows()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.WriteString("## Canonical command catalog\n\n")
	output.WriteString("Generated from `internal/commandmeta`; run `make reference-docs` after changing the public CLI contract.\n\n")
	output.WriteString("| Command path | Canonical synopsis | Summary | Outputs |\n")
	output.WriteString("|---|---|---|---|\n")
	for _, row := range rows {
		fmt.Fprintf(&output, "| `%s` | `%s` | %s | %s |\n",
			markdownText(row.path), markdownText(row.synopsis), markdownText(row.summary), markdownText(row.outputs))
	}
	output.WriteByte('\n')
	return boundedSection("command", output.Bytes())
}

func publicCommandRows() ([]commandRow, error) {
	commands := commandmeta.Public()
	rows := make([]commandRow, 0, len(commands)*2)
	seen := make(map[string]bool, len(commands)*2)
	for _, command := range commands {
		path := "reconc " + command.Name
		if err := appendCommandRow(&rows, seen, path, command.Synopsis, command.Summary, command.OutputModes); err != nil {
			return nil, err
		}
		if err := appendSubcommandRows(&rows, seen, path, command.Subcommands); err != nil {
			return nil, err
		}
	}
	if len(rows) == 0 || len(rows) > maxCommands {
		return nil, fmt.Errorf("public command reference has %d rows, limit %d", len(rows), maxCommands)
	}
	return rows, nil
}

func appendSubcommandRows(rows *[]commandRow, seen map[string]bool, parent string, subcommands []commandmeta.Subcommand) error {
	for _, subcommand := range subcommands {
		path := parent + " " + subcommand.Name
		if err := appendCommandRow(rows, seen, path, subcommand.Synopsis, subcommand.Summary, subcommand.OutputModes); err != nil {
			return err
		}
		if err := appendSubcommandRows(rows, seen, path, subcommand.Subcommands); err != nil {
			return err
		}
	}
	return nil
}

func appendCommandRow(rows *[]commandRow, seen map[string]bool, path, synopsis, summary string, modes []commandmeta.OutputMode) error {
	if path == "" || synopsis == "" || summary == "" || seen[path] {
		return fmt.Errorf("invalid or duplicate public command path %q", path)
	}
	seen[path] = true
	outputs := make([]string, len(modes))
	for index, mode := range modes {
		outputs[index] = string(mode)
	}
	*rows = append(*rows, commandRow{path: path, synopsis: synopsis, summary: summary, outputs: strings.Join(outputs, ", ")})
	return nil
}

func renderHookReference() ([]byte, error) {
	surfaces := hooks.VerificationSurfaces()
	if len(surfaces) == 0 || len(surfaces) > maxHookSurfaces {
		return nil, fmt.Errorf("hook reference has %d surfaces, limit %d", len(surfaces), maxHookSurfaces)
	}
	var output bytes.Buffer
	output.WriteString("## Canonical hook verification matrix\n\n")
	output.WriteString("Generated from the hook registry. Capability describes registry evidence, not observed host liveness.\n\n")
	output.WriteString("| Host | Surface | Expected runtime events | Capability | Operator exercise |\n")
	output.WriteString("|---|---|---|---|---|\n")
	seen := make(map[string]bool, len(surfaces))
	for _, surface := range surfaces {
		key := surface.Kind + ":" + surface.Surface
		if seen[key] {
			return nil, fmt.Errorf("duplicate hook verification surface %q", key)
		}
		seen[key] = true
		capability := "documented"
		if surface.Inferred {
			capability = "includes inferred routes"
		}
		events := "-"
		if len(surface.ExpectedEvents) > 0 {
			events = strings.Join(surface.ExpectedEvents, ", ")
		}
		fmt.Fprintf(&output, "| `%s` | `%s` | %s | %s | %s |\n",
			markdownText(surface.Kind), markdownText(surface.Surface), markdownText(events),
			capability, markdownText(surface.Action))
	}
	output.WriteByte('\n')
	return boundedSection("hook", output.Bytes())
}

func renderSchemaReference() ([]byte, error) {
	if err := schema.ValidateRegistry(); err != nil {
		return nil, fmt.Errorf("validate schema registry: %w", err)
	}
	contracts := schema.Contracts()
	if len(contracts) == 0 || len(contracts) > maxContracts {
		return nil, fmt.Errorf("schema reference has %d contracts, limit %d", len(contracts), maxContracts)
	}
	var output bytes.Buffer
	output.WriteString("## Canonical schema contracts\n\n")
	output.WriteString("Generated from `internal/schema`. Canonical URLs are immutable publication identities; aliases remain input-only.\n\n")
	output.WriteString("| Artifact | Schema | Formats | State | Canonical URL | Local source |\n")
	output.WriteString("|---|---|---|---|---|---|\n")
	seen := make(map[string]bool, len(contracts))
	for _, contract := range contracts {
		key := string(contract.Artifact) + "/v" + contract.SchemaVersion
		if seen[key] {
			return nil, fmt.Errorf("duplicate schema contract %q", key)
		}
		seen[key] = true
		formats := "-"
		if len(contract.FormatVersions) > 0 {
			formats = strings.Join(contract.FormatVersions, ", ")
		}
		fmt.Fprintf(&output, "| `%s` | `v%s` | %s | `%s` | <%s> | `%s` |\n",
			markdownText(string(contract.Artifact)), markdownText(contract.SchemaVersion), markdownText(formats),
			markdownText(string(contract.State)), contract.DefaultURL, markdownText(contract.LocalPath))
	}
	output.WriteByte('\n')
	return boundedSection("schema", output.Bytes())
}

func boundedSection(name string, body []byte) ([]byte, error) {
	if len(body) == 0 || len(body) > maxSectionBytes {
		return nil, fmt.Errorf("%s reference is %d bytes, limit %d", name, len(body), maxSectionBytes)
	}
	return slices.Clone(body), nil
}

func markdownText(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}
