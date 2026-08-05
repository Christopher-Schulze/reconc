package assurance

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/policy"
)

var exactVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

func evaluateRepositoryLayout(root string, gate policy.AssuranceGate, state *evaluationState) ([]Finding, error) {
	entries, err := readDirectoryEntries(root, maxScannedFiles)
	if err != nil {
		return nil, fmt.Errorf("read repository root: %w", err)
	}
	allowed := stringSet(gate.AllowedRootEntries)
	forbidden := stringSet(gate.ForbiddenRootEntries)
	present := map[string]bool{}
	findings := []Finding{}
	for _, entry := range entries {
		name := entry.Name()
		present[name] = true
		if forbidden[name] {
			findings = append(findings, Finding{
				GateID: gate.ID, Paths: []string{name},
				Message:     "forbidden root entry exists: " + name,
				Remediation: "Move or remove " + name + " according to the configured repository layout.",
			})
			continue
		}
		if len(allowed) > 0 && !allowed[name] && !(gate.AllowHiddenEntries && strings.HasPrefix(name, ".")) {
			findings = append(findings, Finding{
				GateID: gate.ID, Paths: []string{name},
				Message:     "unexpected root entry: " + name,
				Remediation: "Move " + name + " under an allowed owner path or add it explicitly to allowed_root_entries.",
			})
		}
	}
	for _, name := range gate.RequiredRootEntries {
		if !present[name] {
			findings = append(findings, Finding{
				GateID: gate.ID, Paths: []string{name},
				Message:     "required root entry is missing: " + name,
				Remediation: "Create or restore the configured root entry " + name + ".",
			})
		}
	}
	for _, relative := range gate.ReservedDirs {
		resolved, err := state.resolve(root, relative)
		if err != nil {
			return nil, err
		}
		if !resolved.exists {
			continue
		}
		if !resolved.mode.IsDir() {
			findings = append(findings, Finding{GateID: gate.ID, Paths: []string{relative}, Message: "reserved directory path is not a directory: " + relative, Remediation: "Restore " + relative + " as a directory or remove it from reserved_dirs."})
			continue
		}
		hasContent, err := directoryHasContent(resolved.full, state.budget)
		if err != nil {
			return nil, err
		}
		if !hasContent {
			findings = append(findings, Finding{GateID: gate.ID, Paths: []string{relative}, Message: "reserved directory has no real content: " + relative, Remediation: "Remove the empty reserved directory until its first owned file exists."})
		}
	}
	return findings, nil
}

func evaluateCommands(gate policy.AssuranceGate, successful []string) []Finding {
	successSet := map[string]bool{}
	for _, command := range successful {
		successSet[normalizeCommand(command)] = true
	}
	missing := []string{}
	matched := 0
	for _, command := range gate.Commands {
		if successSet[normalizeCommand(command)] {
			matched++
			continue
		}
		missing = append(missing, command)
	}
	if (gate.CommandPolicy == "any" && matched > 0) || (gate.CommandPolicy != "any" && len(missing) == 0) {
		return nil
	}
	message := "required successful command evidence is missing: " + strings.Join(missing, ", ")
	if gate.CommandPolicy == "any" {
		message = "none of the configured commands has current successful evidence: " + strings.Join(gate.Commands, ", ")
	}
	return []Finding{{
		GateID: gate.ID, Message: message,
		Remediation: "Run the configured command successfully in the current session, then rerun the policy check.",
	}}
}

func evaluateLanguageBoundary(root string, gate policy.AssuranceGate, state *evaluationState) ([]Finding, error) {
	files, err := changedFiles(root, gate.ScanPaths, gate.ExcludePaths, gate.Exemptions, state)
	if err != nil {
		return nil, err
	}
	allowed := stringSet(gate.AllowedExtensions)
	findings := []Finding{}
	for _, file := range files {
		extension := strings.ToLower(filepath.Ext(file.relative))
		if allowed[extension] {
			continue
		}
		findings = append(findings, Finding{
			GateID: gate.ID, Paths: []string{file.relative},
			Message:     fmt.Sprintf("extension %q is not allowed in this language boundary: %s", extension, file.relative),
			Remediation: "Move the file to the correct language zone or add a narrowly documented exemption.",
		})
	}
	return findings, nil
}

func evaluateDependencyPins(root string, gate policy.AssuranceGate, state *evaluationState) ([]Finding, error) {
	files, err := changedFiles(root, gate.ManifestPaths, nil, nil, state)
	if err != nil {
		return nil, err
	}
	findings := []Finding{}
	for _, file := range files {
		document, err := state.jsonObject(file.full, false)
		if err != nil {
			return nil, fmt.Errorf("parse dependency manifest %s: %w", file.relative, err)
		}
		for _, section := range gate.DependencySections {
			raw, ok := document[section]
			if !ok {
				continue
			}
			dependencies := map[string]string{}
			if err := json.Unmarshal(raw, &dependencies); err != nil {
				return nil, fmt.Errorf("parse %s section %s: %w", file.relative, section, err)
			}
			names := sortedMapKeys(dependencies)
			for _, name := range names {
				version := strings.TrimSpace(dependencies[name])
				if exactVersion.MatchString(version) || hasAllowedPrefix(version, gate.AllowedVersionPrefixes) {
					continue
				}
				findings = append(findings, Finding{
					GateID: gate.ID, Paths: []string{file.relative},
					Message:     fmt.Sprintf("%s %s/%s is not exactly pinned: %q", file.relative, section, name, version),
					Remediation: "Replace the range or moving reference with an exact version, or configure a justified protocol prefix.",
				})
			}
		}
	}
	return findings, nil
}

func evaluateGuardBoundary(root string, gate policy.AssuranceGate, state *evaluationState) ([]Finding, error) {
	files, err := changedFiles(root, gate.ScanPaths, gate.ExcludePaths, gate.Exemptions, state)
	if err != nil {
		return nil, err
	}
	findings := []Finding{}
	for _, file := range files {
		lines, err := state.lines(file.full)
		if err != nil {
			return nil, err
		}
		for index, line := range lines {
			if isCommentOnly(strings.TrimSpace(line)) {
				continue
			}
			for _, site := range gate.SitePatterns {
				if !strings.Contains(line, site) || markerNear(lines, index, gate.MarkerWindowLines, gate.GuardMarkers) {
					continue
				}
				findings = append(findings, Finding{
					GateID: gate.ID, Paths: []string{file.relative},
					Message:     fmt.Sprintf("%s site %q at %s:%d has no configured guard marker within %d lines", gate.Type, site, file.relative, index+1, gate.MarkerWindowLines),
					Remediation: "Route the operation through a configured guard near the call site or add a narrowly documented path exemption.",
				})
			}
		}
	}
	return findings, nil
}

func markerNear(lines []string, index, window int, markers []string) bool {
	start := index - window
	if start < 0 {
		start = 0
	}
	end := index + window + 1
	if end > len(lines) {
		end = len(lines)
	}
	for _, line := range lines[start:end] {
		trimmed := strings.TrimSpace(line)
		if isCommentOnly(trimmed) {
			continue
		}
		for _, marker := range markers {
			if strings.Contains(line, marker) {
				return true
			}
		}
	}
	return false
}

func isCommentOnly(line string) bool {
	return strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") ||
		strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") ||
		strings.HasPrefix(line, "<!--")
}

func hasAllowedPrefix(version string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(version, prefix) {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func sortedMapKeys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
