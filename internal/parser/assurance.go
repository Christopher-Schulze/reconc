package parser

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
	rerrors "reconc.dev/reconc/internal/errors"
	"reconc.dev/reconc/internal/policy"
)

var assuranceCommonFields = map[string]bool{
	"id": true, "type": true, "applicable_if": true,
}

var assuranceFieldsByKind = map[policy.AssuranceKind][]string{
	policy.AssuranceRepositoryLayout: {
		"allowed_root_entries", "required_root_entries", "forbidden_root_entries",
		"reserved_dirs", "allow_hidden_entries",
	},
	policy.AssuranceGeneratedReference: {"commands", "command_policy"},
	policy.AssuranceLanguageBoundary: {
		"scan_paths", "exclude_paths", "exemptions", "allowed_extensions",
	},
	policy.AssuranceDependencyPins: {
		"manifest_paths", "dependency_sections", "allowed_version_prefixes",
	},
	policy.AssurancePackageScripts: {
		"manifest_paths", "manifest_markers", "exclude_paths", "package_manager", "commands",
	},
	policy.AssuranceNetworkBoundary: {
		"scan_paths", "exclude_paths", "exemptions", "site_patterns",
		"guard_markers", "marker_window_lines",
	},
	policy.AssuranceProcessBoundary: {
		"scan_paths", "exclude_paths", "exemptions", "site_patterns",
		"guard_markers", "marker_window_lines",
	},
	policy.AssuranceSubstantiveProof: {"proof_file", "min_samples", "max_age_hours"},
	policy.AssuranceLiveVerification: {"commands", "command_policy"},
	policy.AssuranceGoConcurrency: {
		"scan_paths", "exclude_paths", "exemptions",
	},
	policy.AssuranceGoFormat: {
		"scan_paths", "exclude_paths", "exemptions",
	},
	policy.AssuranceSourceHygiene: {
		"scan_paths", "exclude_paths", "exemptions",
	},
}

// optionalAssuranceGateList decodes typed native gate configurations. A strict
// yaml decoder rejects unknown fields; the per-kind field allowlist rejects
// valid-but-irrelevant fields that could otherwise look enforced while doing
// nothing.
func optionalAssuranceGateList(item map[string]interface{}, key, ruleID string) ([]policy.AssuranceGate, error) {
	raw, ok := item[key]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, assuranceError(ruleID, key+" must be a list of gate mappings")
	}
	out := make([]policy.AssuranceGate, 0, len(list))
	seen := map[string]bool{}
	for index, entry := range list {
		mapping, ok := entry.(map[string]interface{})
		if !ok {
			return nil, assuranceError(ruleID, fmt.Sprintf("%s[%d] must be a mapping", key, index))
		}
		kindText, ok := mapping["type"].(string)
		kind := policy.AssuranceKind(strings.TrimSpace(kindText))
		if !ok || !kind.Valid() {
			return nil, assuranceError(ruleID, fmt.Sprintf("%s[%d].type is not a supported assurance kind: %q", key, index, kindText))
		}
		allowed := map[string]bool{}
		for name := range assuranceCommonFields {
			allowed[name] = true
		}
		for _, name := range assuranceFieldsByKind[kind] {
			allowed[name] = true
		}
		for name := range mapping {
			if !allowed[name] {
				return nil, assuranceError(ruleID, fmt.Sprintf("%s[%d] field %q is not valid for type %s", key, index, name, kind))
			}
		}
		data, err := yaml.Marshal(mapping)
		if err != nil {
			return nil, assuranceError(ruleID, fmt.Sprintf("%s[%d] cannot be encoded: %v", key, index, err))
		}
		var gate policy.AssuranceGate
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&gate); err != nil {
			return nil, assuranceError(ruleID, fmt.Sprintf("%s[%d] is invalid: %v", key, index, err))
		}
		if err := validateAssuranceGate(&gate); err != nil {
			return nil, assuranceError(ruleID, fmt.Sprintf("%s[%d]: %v", key, index, err))
		}
		if seen[gate.ID] {
			return nil, assuranceError(ruleID, fmt.Sprintf("duplicate assurance gate id %q", gate.ID))
		}
		seen[gate.ID] = true
		out = append(out, gate)
	}
	return out, nil
}

func validateAssuranceGate(gate *policy.AssuranceGate) error {
	gate.ID = strings.TrimSpace(gate.ID)
	if gate.ID == "" {
		return fmt.Errorf("id must be a non-empty string")
	}
	stringLists := []struct {
		name   string
		values *[]string
	}{
		{"applicable_if", &gate.ApplicableIf},
		{"scan_paths", &gate.ScanPaths},
		{"exclude_paths", &gate.ExcludePaths},
		{"allowed_root_entries", &gate.AllowedRootEntries},
		{"required_root_entries", &gate.RequiredRootEntries},
		{"forbidden_root_entries", &gate.ForbiddenRootEntries},
		{"reserved_dirs", &gate.ReservedDirs},
		{"commands", &gate.Commands},
		{"allowed_extensions", &gate.AllowedExtensions},
		{"manifest_paths", &gate.ManifestPaths},
		{"manifest_markers", &gate.ManifestMarkers},
		{"dependency_sections", &gate.DependencySections},
		{"allowed_version_prefixes", &gate.AllowedVersionPrefixes},
		{"site_patterns", &gate.SitePatterns},
		{"guard_markers", &gate.GuardMarkers},
	}
	for _, field := range stringLists {
		if err := normalizeAssuranceStrings(field.name, field.values); err != nil {
			return err
		}
	}
	pathPatterns := append(append(append(append([]string{}, gate.ApplicableIf...), gate.ScanPaths...), append(gate.ExcludePaths, gate.ManifestPaths...)...), gate.ManifestMarkers...)
	for _, value := range pathPatterns {
		if !isRepoRelativePath(value) {
			return fmt.Errorf("path pattern must stay repo-relative: %q", value)
		}
	}
	if err := validateGlobPatterns(pathPatterns, "assurance gate '"+gate.ID+"'"); err != nil {
		return err
	}
	for index := range gate.Exemptions {
		exemption := &gate.Exemptions[index]
		exemption.Path = strings.TrimSpace(exemption.Path)
		exemption.Reason = strings.TrimSpace(exemption.Reason)
		if !isRepoRelativePath(exemption.Path) || exemption.Reason == "" {
			return fmt.Errorf("every exemption requires a repo-relative path and non-empty reason")
		}
		if err := validateGlobPatterns([]string{exemption.Path}, "assurance gate '"+gate.ID+"' exemption"); err != nil {
			return err
		}
	}
	switch gate.Type {
	case policy.AssuranceRepositoryLayout:
		if len(gate.AllowedRootEntries)+len(gate.RequiredRootEntries)+len(gate.ForbiddenRootEntries)+len(gate.ReservedDirs) == 0 {
			return fmt.Errorf("repository_layout requires at least one root or reserved-dir contract")
		}
		for _, entry := range append(append(append([]string{}, gate.AllowedRootEntries...), gate.RequiredRootEntries...), gate.ForbiddenRootEntries...) {
			if strings.ContainsAny(entry, `/\`) {
				return fmt.Errorf("root entry must be a single non-empty name: %q", entry)
			}
		}
		for _, dir := range gate.ReservedDirs {
			if !isRepoRelativePath(dir) {
				return fmt.Errorf("reserved_dirs path must stay repo-relative: %q", dir)
			}
		}
		forbidden := stringSetForValidation(gate.ForbiddenRootEntries)
		allowed := stringSetForValidation(gate.AllowedRootEntries)
		for _, entry := range append(append([]string{}, gate.AllowedRootEntries...), gate.RequiredRootEntries...) {
			if forbidden[entry] {
				return fmt.Errorf("root entry %q cannot be both forbidden and allowed or required", entry)
			}
		}
		if len(allowed) > 0 {
			for _, entry := range gate.RequiredRootEntries {
				if !allowed[entry] && !(gate.AllowHiddenEntries && strings.HasPrefix(entry, ".")) {
					return fmt.Errorf("required root entry %q must also be allowed", entry)
				}
			}
		}
	case policy.AssuranceGeneratedReference, policy.AssuranceLiveVerification:
		if len(gate.Commands) == 0 {
			return fmt.Errorf("%s requires commands", gate.Type)
		}
		if gate.CommandPolicy == "" {
			gate.CommandPolicy = "all"
		}
		if gate.CommandPolicy != "all" && gate.CommandPolicy != "any" {
			return fmt.Errorf("command_policy must be all or any")
		}
	case policy.AssuranceLanguageBoundary:
		if len(gate.ScanPaths) == 0 || len(gate.AllowedExtensions) == 0 {
			return fmt.Errorf("language_boundary requires scan_paths and allowed_extensions")
		}
		for index, ext := range gate.AllowedExtensions {
			ext = strings.ToLower(ext)
			if !strings.HasPrefix(ext, ".") || strings.ContainsAny(ext, `/\`) {
				return fmt.Errorf("allowed extension must start with a dot: %q", ext)
			}
			gate.AllowedExtensions[index] = ext
		}
	case policy.AssuranceGoConcurrency, policy.AssuranceGoFormat, policy.AssuranceSourceHygiene:
		if len(gate.ScanPaths) == 0 {
			return fmt.Errorf("%s requires scan_paths", gate.Type)
		}
	case policy.AssuranceDependencyPins:
		if len(gate.ManifestPaths) == 0 {
			return fmt.Errorf("dependency_pins requires manifest_paths")
		}
		if len(gate.DependencySections) == 0 {
			gate.DependencySections = []string{"dependencies", "devDependencies"}
		}
	case policy.AssurancePackageScripts:
		if len(gate.ManifestPaths) == 0 || len(gate.Commands) == 0 {
			return fmt.Errorf("package_scripts requires manifest_paths and commands")
		}
		gate.PackageManager = strings.ToLower(strings.TrimSpace(gate.PackageManager))
		if gate.PackageManager != "" && gate.PackageManager != "bun" && gate.PackageManager != "npm" && gate.PackageManager != "pnpm" && gate.PackageManager != "yarn" {
			return fmt.Errorf("package_manager must be bun, npm, pnpm, yarn, or empty")
		}
		for _, command := range gate.Commands {
			if _, err := policy.ParsePackageScriptCommand(command); err != nil {
				return err
			}
		}
	case policy.AssuranceNetworkBoundary, policy.AssuranceProcessBoundary:
		if len(gate.ScanPaths) == 0 || len(gate.SitePatterns) == 0 || len(gate.GuardMarkers) == 0 {
			return fmt.Errorf("%s requires scan_paths, site_patterns, and guard_markers", gate.Type)
		}
		if gate.MarkerWindowLines == 0 {
			gate.MarkerWindowLines = 20
		}
		if gate.MarkerWindowLines < 1 || gate.MarkerWindowLines > 200 {
			return fmt.Errorf("marker_window_lines must be between 1 and 200")
		}
	case policy.AssuranceSubstantiveProof:
		gate.ProofFile = strings.TrimSpace(gate.ProofFile)
		if !isRepoRelativePath(gate.ProofFile) || strings.ContainsAny(gate.ProofFile, "*?[{") {
			return fmt.Errorf("substantive_proof requires a repo-relative proof_file")
		}
		if gate.MinSamples == 0 {
			gate.MinSamples = 3
		}
		if gate.MaxAgeHours == 0 {
			gate.MaxAgeHours = 24
		}
		if gate.MinSamples < 1 || gate.MinSamples > 10_000 {
			return fmt.Errorf("min_samples must be between 1 and 10000")
		}
		if gate.MaxAgeHours < 1 || gate.MaxAgeHours > 87_600 {
			return fmt.Errorf("max_age_hours must be between 1 and 87600")
		}
	}
	return nil
}

func normalizeAssuranceStrings(name string, values *[]string) error {
	seen := map[string]bool{}
	for index := range *values {
		value := strings.TrimSpace((*values)[index])
		if value == "" {
			return fmt.Errorf("%s cannot contain an empty value", name)
		}
		if seen[value] {
			return fmt.Errorf("%s cannot contain duplicate value %q", name, value)
		}
		seen[value] = true
		(*values)[index] = value
	}
	return nil
}

func stringSetForValidation(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func assuranceError(ruleID, message string) error {
	return &rerrors.RuleValidationError{Message: "rule '" + ruleID + "' assurance: " + message}
}
