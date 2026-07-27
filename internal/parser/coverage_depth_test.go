package parser

import (
	"reflect"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestParseCheckAcceptsEverySupportedSubcheckKind(t *testing.T) {
	tests := []struct {
		name string
		item map[string]interface{}
		want policy.Check
	}{
		{
			name: "fresh file",
			item: map[string]interface{}{
				"kind": "require_fresh_file", "path": "report.json", "max_age_hours": int64(12), "optional": true,
			},
			want: policy.Check{Kind: policy.KindRequireFreshFile, Path: "report.json", MaxAgeHours: 12, Optional: true},
		},
		{
			name: "evidence",
			item: map[string]interface{}{
				"kind": "require_evidence", "file": "proof.md", "must_exist": true,
				"must_contain": []interface{}{"PASS", "checksum"}, "must_not_contain": "pending", "max_line_count": float64(50),
			},
			want: policy.Check{
				Kind: policy.KindRequireEvidence, File: "proof.md", MustExist: true,
				MustContain: []string{"PASS", "checksum"}, MustNotContain: "pending", MaxLineCount: 50,
			},
		},
		{
			name: "claim",
			item: map[string]interface{}{"kind": "require_claim", "claims": []interface{}{"ci-green"}},
			want: policy.Check{Kind: policy.KindRequireClaim, Claims: []string{"ci-green"}},
		},
		{
			name: "required command success",
			item: map[string]interface{}{
				"kind": "require_command_success", "commands": []interface{}{"go test ./..."}, "command_match": "prefix",
			},
			want: policy.Check{
				Kind: policy.KindRequireCommandSuccess, Commands: []string{"go test ./..."},
				CommandMatch: policy.CommandMatchPrefix,
			},
		},
		{
			name: "deny write",
			item: map[string]interface{}{"kind": "deny_write", "paths": []interface{}{"dist/**"}},
			want: policy.Check{Kind: policy.KindDenyWrite, Paths: []string{"dist/**"}},
		},
		{
			name: "script",
			item: map[string]interface{}{
				"kind": "require_script", "script": "scripts/check.sh", "args": []interface{}{"--strict"}, "timeout_sec": 15,
			},
			want: policy.Check{
				Kind: policy.KindRequireScript, Script: "scripts/check.sh", Args: []string{"--strict"}, TimeoutSec: 15,
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCheck(test.item, "rule-id", "checks", index)
			if err != nil {
				t.Fatalf("parseCheck: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseCheck() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseCheckRejectsEveryUnsafeOrIncompleteShape(t *testing.T) {
	tests := []struct {
		name string
		item map[string]interface{}
		want string
	}{
		{name: "missing kind", item: map[string]interface{}{}, want: ".kind' is required"},
		{name: "unknown kind", item: map[string]interface{}{"kind": "unknown"}, want: "not a recognized kind"},
		{name: "nested composite", item: map[string]interface{}{"kind": "all_of"}, want: "nested composite kinds are not supported"},
		{name: "non boolean optional", item: map[string]interface{}{"kind": "deny_write", "paths": []interface{}{"x"}, "optional": "yes"}, want: ".optional' must be a boolean"},
		{name: "fresh file missing path", item: map[string]interface{}{"kind": "require_fresh_file"}, want: ".path' is required"},
		{name: "fresh file negative age", item: map[string]interface{}{"kind": "require_fresh_file", "path": "x", "max_age_hours": -1}, want: ".max_age_hours' must be >= 0"},
		{name: "evidence no assertion", item: map[string]interface{}{"kind": "require_evidence", "file": "proof"}, want: "must specify at least one"},
		{name: "evidence negative lines", item: map[string]interface{}{"kind": "require_evidence", "file": "proof", "max_line_count": -1}, want: ".max_line_count' must be >= 0"},
		{name: "claim missing claims", item: map[string]interface{}{"kind": "require_claim"}, want: ".claims' is required"},
		{name: "command missing commands", item: map[string]interface{}{"kind": "forbid_command"}, want: ".commands' is required"},
		{name: "deny write missing paths", item: map[string]interface{}{"kind": "deny_write"}, want: ".paths' is required"},
		{name: "script absolute", item: map[string]interface{}{"kind": "require_script", "script": "/tmp/check.sh"}, want: "must be a repo-relative path"},
		{name: "script traversal", item: map[string]interface{}{"kind": "require_script", "script": "../check.sh"}, want: "must be a repo-relative path"},
		{name: "script negative timeout", item: map[string]interface{}{"kind": "require_script", "script": "check.sh", "timeout_sec": -1}, want: ".timeout_sec' must be >= 0"},
		{name: "unsupported valid kind", item: map[string]interface{}{"kind": "require_read"}, want: "not yet supported as a sub-check"},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseCheck(test.item, "rule-id", "checks", index)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseCheck() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestNestedPolicyListParsersRejectMalformedContainersAndEntries(t *testing.T) {
	if got, err := optionalCheckList(map[string]interface{}{}, "checks", "rule"); err != nil || got != nil {
		t.Fatalf("absent checks = (%#v, %v)", got, err)
	}
	if _, err := optionalCheckList(map[string]interface{}{"checks": "wrong"}, "checks", "rule"); err == nil ||
		!strings.Contains(err.Error(), "must be a list of check mappings") {
		t.Fatalf("non-list checks error = %v", err)
	}
	if _, err := optionalCheckList(map[string]interface{}{"checks": []interface{}{"wrong"}}, "checks", "rule"); err == nil ||
		!strings.Contains(err.Error(), "must be a YAML mapping") {
		t.Fatalf("non-mapping check error = %v", err)
	}
	checks, err := optionalCheckList(map[string]interface{}{
		"checks": []interface{}{map[string]interface{}{"kind": "deny_write", "paths": []interface{}{"dist/**"}}},
	}, "checks", "rule")
	if err != nil || len(checks) != 1 || checks[0].Kind != policy.KindDenyWrite {
		t.Fatalf("valid checks = (%#v, %v)", checks, err)
	}

	if _, err := optionalRequiredFileList(map[string]interface{}{"required_files": "wrong"}, "required_files", "rule"); err == nil {
		t.Fatal("non-list required_files must fail")
	}
	if _, err := optionalRequiredFileList(map[string]interface{}{"required_files": []interface{}{"wrong"}}, "required_files", "rule"); err == nil {
		t.Fatal("non-mapping required_files entry must fail")
	}
	required, err := optionalRequiredFileList(map[string]interface{}{
		"required_files": []interface{}{map[string]interface{}{
			"path": "proof.md", "max_age_hours": 24, "optional": true,
		}},
	}, "required_files", "rule")
	if err != nil || !reflect.DeepEqual(required, []policy.RequiredFile{{Path: "proof.md", MaxAgeHours: 24, Optional: true}}) {
		t.Fatalf("valid required_files = (%#v, %v)", required, err)
	}
	if _, err := optionalRequiredFileList(map[string]interface{}{
		"required_files": []interface{}{map[string]interface{}{"path": "proof.md", "max_age_hours": -1}},
	}, "required_files", "rule"); err == nil || !strings.Contains(err.Error(), "must be >= 0") {
		t.Fatalf("negative required file age error = %v", err)
	}

	if _, err := optionalEvidenceCheckList(map[string]interface{}{"evidence": "wrong"}, "evidence", "rule"); err == nil {
		t.Fatal("non-list evidence must fail")
	}
	if _, err := optionalEvidenceCheckList(map[string]interface{}{"evidence": []interface{}{"wrong"}}, "evidence", "rule"); err == nil {
		t.Fatal("non-mapping evidence entry must fail")
	}
	evidence, err := optionalEvidenceCheckList(map[string]interface{}{
		"evidence": []interface{}{map[string]interface{}{
			"file": "proof.md", "must_exist": true, "must_contain": []interface{}{"PASS"},
			"must_not_contain": "pending", "max_line_count": 50, "optional": true,
		}},
	}, "evidence", "rule")
	if err != nil || !reflect.DeepEqual(evidence, []policy.EvidenceCheck{{
		File: "proof.md", MustExist: true, MustContain: []string{"PASS"},
		MustNotContain: "pending", MaxLineCount: 50, Optional: true,
	}}) {
		t.Fatalf("valid evidence = (%#v, %v)", evidence, err)
	}
	if _, err := optionalEvidenceCheckList(map[string]interface{}{
		"evidence": []interface{}{map[string]interface{}{"file": "proof.md"}},
	}, "evidence", "rule"); err == nil || !strings.Contains(err.Error(), "must specify at least one") {
		t.Fatalf("assertion-free evidence error = %v", err)
	}
}

func TestNestedScalarHelpersAcceptOnlyDocumentedTypes(t *testing.T) {
	if got, err := requiredStringField(map[string]interface{}{"path": "  proof.md  "}, "path", "rule", "files", 0); err != nil || got != "proof.md" {
		t.Fatalf("requiredStringField() = (%q, %v)", got, err)
	}
	for _, item := range []map[string]interface{}{{}, {"path": 1}, {"path": "  "}} {
		if _, err := requiredStringField(item, "path", "rule", "files", 0); err == nil {
			t.Fatalf("requiredStringField(%#v) must fail", item)
		}
	}

	for _, test := range []struct {
		value interface{}
		want  int
		ok    bool
	}{
		{value: nil, want: 0, ok: true},
		{value: 3, want: 3, ok: true},
		{value: int64(4), want: 4, ok: true},
		{value: float64(5), want: 5, ok: true},
		{value: 5.5, ok: false},
		{value: "5", ok: false},
	} {
		item := map[string]interface{}{}
		if test.value != nil {
			item["value"] = test.value
		}
		got, err := optionalInt(item, "value", "rule", "checks", 0)
		if (err == nil) != test.ok || (test.ok && got != test.want) {
			t.Fatalf("optionalInt(%#v) = (%d, %v), want (%d, ok=%t)", test.value, got, err, test.want, test.ok)
		}
	}

	if got, err := optionalBool(map[string]interface{}{"value": true}, "value", "rule", "checks", 0); err != nil || !got {
		t.Fatalf("optionalBool(true) = (%t, %v)", got, err)
	}
	if _, err := optionalBool(map[string]interface{}{"value": "true"}, "value", "rule", "checks", 0); err == nil {
		t.Fatal("optionalBool(string) must fail")
	}
	if got, err := optionalString(map[string]interface{}{"value": "text"}, "value", "rule", "checks", 0); err != nil || got != "text" {
		t.Fatalf("optionalString(text) = (%q, %v)", got, err)
	}
	if _, err := optionalString(map[string]interface{}{"value": 1}, "value", "rule", "checks", 0); err == nil {
		t.Fatal("optionalString(number) must fail")
	}
	if _, err := optionalContainList(map[string]interface{}{"value": "wrong"}, "value", "rule", "checks", 0); err == nil {
		t.Fatal("optionalContainList(non-list) must fail")
	}
	if _, err := optionalContainList(map[string]interface{}{"value": []interface{}{""}}, "value", "rule", "checks", 0); err == nil {
		t.Fatal("optionalContainList(empty entry) must fail")
	}
}

func TestAssuranceGateValidationCoversEveryKindAndCrossFieldInvariant(t *testing.T) {
	tests := []struct {
		name   string
		gate   policy.AssuranceGate
		want   string
		assert func(*testing.T, policy.AssuranceGate)
	}{
		{name: "missing id", gate: policy.AssuranceGate{Type: policy.AssuranceGoFormat, ScanPaths: []string{"**/*.go"}}, want: "id"},
		{name: "duplicate values", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceGoFormat, ScanPaths: []string{"src", "src"}}, want: "duplicate"},
		{name: "escaping applicable path", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceGoFormat, ApplicableIf: []string{"../outside"}, ScanPaths: []string{"src"}}, want: "repo-relative"},
		{name: "invalid exemption", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceGoFormat, ScanPaths: []string{"src"}, Exemptions: []policy.AssuranceExemption{{Path: "src", Reason: " "}}}, want: "exemption"},
		{name: "empty layout", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceRepositoryLayout}, want: "at least one"},
		{name: "nested root entry", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceRepositoryLayout, AllowedRootEntries: []string{"docs/tasks"}}, want: "single"},
		{name: "layout conflict", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceRepositoryLayout, AllowedRootEntries: []string{"docs"}, ForbiddenRootEntries: []string{"docs"}}, want: "both forbidden"},
		{name: "required not allowed", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceRepositoryLayout, AllowedRootEntries: []string{"docs"}, RequiredRootEntries: []string{"scripts"}}, want: "must also be allowed"},
		{name: "layout valid", gate: policy.AssuranceGate{ID: " gate ", Type: policy.AssuranceRepositoryLayout, AllowedRootEntries: []string{"docs"}, RequiredRootEntries: []string{"docs"}}, assert: func(t *testing.T, gate policy.AssuranceGate) {
			if gate.ID != "gate" {
				t.Fatalf("normalized id = %q", gate.ID)
			}
		}},
		{name: "generated missing commands", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceGeneratedReference}, want: "requires commands"},
		{name: "generated policy", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceGeneratedReference, Commands: []string{"go generate"}, CommandPolicy: "none"}, want: "all or any"},
		{name: "generated default", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceGeneratedReference, Commands: []string{"go generate"}}, assert: func(t *testing.T, gate policy.AssuranceGate) {
			if gate.CommandPolicy != "all" {
				t.Fatalf("default command policy = %q", gate.CommandPolicy)
			}
		}},
		{name: "language missing extensions", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceLanguageBoundary, ScanPaths: []string{"src"}}, want: "requires scan_paths"},
		{name: "language invalid extension", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceLanguageBoundary, ScanPaths: []string{"src"}, AllowedExtensions: []string{"go"}}, want: "start with a dot"},
		{name: "language normalizes extension", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceLanguageBoundary, ScanPaths: []string{"src"}, AllowedExtensions: []string{".GO"}}, assert: func(t *testing.T, gate policy.AssuranceGate) {
			if gate.AllowedExtensions[0] != ".go" {
				t.Fatalf("extension = %q", gate.AllowedExtensions[0])
			}
		}},
		{name: "go format missing scan", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceGoFormat}, want: "requires scan_paths"},
		{name: "go concurrency valid", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"**/*.go"}}},
		{name: "source hygiene valid", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceSourceHygiene, ScanPaths: []string{"src"}}},
		{name: "dependency missing manifest", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceDependencyPins}, want: "requires manifest_paths"},
		{name: "dependency defaults sections", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceDependencyPins, ManifestPaths: []string{"package.json"}}, assert: func(t *testing.T, gate policy.AssuranceGate) {
			if strings.Join(gate.DependencySections, ",") != "dependencies,devDependencies" {
				t.Fatalf("dependency sections = %v", gate.DependencySections)
			}
		}},
		{name: "package scripts incomplete", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"package.json"}}, want: "requires manifest_paths"},
		{name: "package manager", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"package.json"}, Commands: []string{"test"}, PackageManager: "pip"}, want: "package_manager"},
		{name: "package command", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"package.json"}, Commands: []string{"bad command"}, PackageManager: "npm"}, want: "must use"},
		{name: "package valid", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"package.json"}, Commands: []string{"npm run test"}, PackageManager: " NPM "}, assert: func(t *testing.T, gate policy.AssuranceGate) {
			if gate.PackageManager != "npm" {
				t.Fatalf("package manager = %q", gate.PackageManager)
			}
		}},
		{name: "network incomplete", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceNetworkBoundary, ScanPaths: []string{"src"}}, want: "requires scan_paths"},
		{name: "network window", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceNetworkBoundary, ScanPaths: []string{"src"}, SitePatterns: []string{"Dial"}, GuardMarkers: []string{"allow"}, MarkerWindowLines: 201}, want: "between 1 and 200"},
		{name: "process defaults window", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceProcessBoundary, ScanPaths: []string{"src"}, SitePatterns: []string{"Command"}, GuardMarkers: []string{"allow"}}, assert: func(t *testing.T, gate policy.AssuranceGate) {
			if gate.MarkerWindowLines != 20 {
				t.Fatalf("marker window = %d", gate.MarkerWindowLines)
			}
		}},
		{name: "proof path", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceSubstantiveProof, ProofFile: "*.json"}, want: "proof_file"},
		{name: "proof samples", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceSubstantiveProof, ProofFile: "proof.json", MinSamples: 10001}, want: "min_samples"},
		{name: "proof age", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceSubstantiveProof, ProofFile: "proof.json", MaxAgeHours: 87601}, want: "max_age_hours"},
		{name: "proof defaults", gate: policy.AssuranceGate{ID: "gate", Type: policy.AssuranceSubstantiveProof, ProofFile: "proof.json"}, assert: func(t *testing.T, gate policy.AssuranceGate) {
			if gate.MinSamples != 3 || gate.MaxAgeHours != 24 {
				t.Fatalf("proof defaults = %d/%d", gate.MinSamples, gate.MaxAgeHours)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate := test.gate
			err := validateAssuranceGate(&gate)
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("error = %v, want substring %q", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.assert != nil {
				test.assert(t, gate)
			}
		})
	}
}
