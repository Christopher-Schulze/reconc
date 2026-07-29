package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"reconc.dev/reconc/internal/ingest"
)

type sourceInspection struct {
	RepoRoot string                   `json:"repo_root"`
	Count    int                      `json:"count"`
	Sources  []sourceInspectionRecord `json:"sources"`
}

type sourceInspectionRecord struct {
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	ContentSHA256 string `json:"content_sha256"`
	BlockID       string `json:"block_id,omitempty"`
	LineStart     int    `json:"line_start,omitempty"`
}

func runSources(args []string, stdout io.Writer) error {
	repo := "."
	repoSeen := false
	jsonOut := false
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "-h" || arg == "--help":
			fmt.Fprintln(stdout, "Usage: reconc sources [repo] [--json]")
			fmt.Fprintln(stdout, "Inspect effective policy-source provenance and SHA-256 digests without source bodies.")
			return nil
		case strings.HasPrefix(arg, "-"):
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc sources: unknown flag %q", arg)}
		case repoSeen:
			return &CLIError{ExitCode: 1, Message: "reconc sources: expected at most one repo path"}
		default:
			repo = arg
			repoSeen = true
		}
	}
	bundle, err := ingest.LoadPolicySources(repo)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc sources: " + err.Error()}
	}
	report := sourceInspection{
		RepoRoot: bundle.RepoRoot,
		Count:    len(bundle.Sources),
		Sources:  make([]sourceInspectionRecord, 0, len(bundle.Sources)),
	}
	for _, source := range bundle.Sources {
		digest := sha256.Sum256([]byte(source.Content))
		report.Sources = append(report.Sources, sourceInspectionRecord{
			Kind:          string(source.Kind),
			Path:          source.Path,
			ContentSHA256: fmt.Sprintf("%x", digest[:]),
			BlockID:       source.BlockID,
			LineStart:     source.LineStart,
		})
	}
	if jsonOut {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc sources: encode report: " + err.Error()}
		}
		return nil
	}
	fmt.Fprintf(stdout, "Policy sources (%d) for %s:\n", report.Count, report.RepoRoot)
	for _, source := range report.Sources {
		location := source.Path
		if source.LineStart > 0 {
			location = fmt.Sprintf("%s:%d", location, source.LineStart)
		}
		fmt.Fprintf(stdout, "  %-16s %s  sha256=%s\n", source.Kind, location, source.ContentSHA256)
	}
	return nil
}
