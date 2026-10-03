package assurance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"reconc.dev/reconc/internal/policy"
)

func TestCommandBackedAssuranceRequiresCausalFreshness(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	evidence := `{"samples":[10,11,12]}`
	writeAssuranceFile(t, root, "evidence.json", evidence)
	hash := sha256.Sum256([]byte(evidence))
	document := proofDocument{FormatVersion: "1", Proofs: []proofRecord{{
		ID: "measured", Subject: "latency", Command: "verify", Outcome: "pass",
		Aggregation: "mean", Comparator: "lte", Threshold: float64Pointer(20), Actual: float64Pointer(11), Samples: []float64{10, 11, 12},
		EvidencePath: "evidence.json", EvidenceSHA256: hex.EncodeToString(hash[:]), VerifiedAt: now.Format(time.RFC3339),
	}}}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	writeAssuranceFile(t, root, "proof.json", string(body))
	for _, kind := range []policy.AssuranceKind{policy.AssuranceLiveVerification, policy.AssuranceGeneratedReference, policy.AssuranceSubstantiveProof} {
		for _, test := range []struct {
			name           string
			write, command uint64
			want           int
		}{
			{"legacy-no-write", 0, 0, 0}, {"legacy-after-write", 2, 0, 1}, {"stale", 2, 1, 1}, {"equal", 2, 2, 0}, {"fresh", 2, 3, 0}, {"index-bound", 2, ^uint64(0), 0},
		} {
			t.Run(string(kind)+"/"+test.name, func(t *testing.T) {
				gate := policy.AssuranceGate{ID: "causal", Type: kind, Commands: []string{"verify"}, CommandPolicy: "all", ProofFile: "proof.json", MinSamples: 3, MaxAgeHours: 24}
				findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"main.go"}, WriteEpochs: map[string]uint64{"main.go": test.write}, SuccessfulCommandEvidence: []CommandEvidence{{Command: "verify", EvidenceEpoch: test.command}}, Now: now})
				if err != nil || len(findings) != test.want {
					t.Fatalf("findings=%+v, err=%v, want %d", findings, err, test.want)
				}
			})
		}
	}
}

func TestCausalFreshnessUsesIndependentModuleOwners(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		t.Run(map[bool]string{false: "nested-go", true: "rust-workspace"}[workspace], func(t *testing.T) {
			root := t.TempDir()
			manifest, file, command := "go.mod", "main.go", "go test ./..."
			if workspace {
				manifest, file, command = "Cargo.toml", "src/lib.rs", "cargo test --workspace"
				writeAssuranceFile(t, root, manifest, "[workspace]\nmembers = [\"a\", \"b\"]\n")
			}
			for _, module := range []string{"a", "b"} {
				body := "module example/" + module + "\n"
				if workspace {
					body = "[package]\nname = \"" + module + "\"\nversion = \"0.1.0\"\n"
				}
				writeAssuranceFile(t, root, module+"/"+manifest, body)
				writeAssuranceFile(t, root, module+"/"+file, "source\n")
			}
			inputs := Inputs{ChangedPaths: []string{"a/" + file, "b/" + file}, WriteEpochs: map[string]uint64{"a/" + file: 1, "b/" + file: 5}}
			for _, module := range []string{"a", "b"} {
				directory, epoch := module, uint64(1)
				if module == "b" {
					epoch = 4
				}
				if workspace {
					directory = "."
				}
				inputs.SuccessfulCommandEvidence = append(inputs.SuccessfulCommandEvidence, CommandEvidence{Command: command, WorkingDirectory: directory, EvidenceEpoch: epoch})
			}
			gate := policy.AssuranceGate{ID: "live", Type: policy.AssuranceLiveVerification, ApplicableIf: []string{manifest}, Commands: []string{command}, CommandPolicy: "all"}
			findings, err := Evaluate(root, []policy.AssuranceGate{gate}, inputs)
			if err != nil || len(findings) != 1 || findings[0].ModuleRoot != "b" {
				t.Fatalf("independent modules: %+v, %v", findings, err)
			}
			inputs.SuccessfulCommandEvidence[1].EvidenceEpoch = 5
			findings, err = Evaluate(root, []policy.AssuranceGate{gate}, inputs)
			if err != nil || len(findings) != 0 {
				t.Fatalf("fresh modules: %+v, %v", findings, err)
			}
		})
	}
}

func TestPackageScriptFreshnessUsesDeepestPackageOwner(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"package.json", "a/package.json", "a/nested/package.json"} {
		writeAssuranceFile(t, root, path, `{"packageManager":"bun@1.3.14","scripts":{"test":"bun test"}}`)
	}
	inputs := Inputs{ChangedPaths: []string{"main.ts", "a/main.ts", "a/nested/main.ts"}, WriteEpochs: map[string]uint64{"main.ts": 1, "a/main.ts": 2, "a/nested/main.ts": 5}, SuccessfulCommandEvidence: []CommandEvidence{
		{Command: "bun run test", EvidenceEpoch: 1}, {Command: "bun --cwd a run test", EvidenceEpoch: 2}, {Command: "bun --cwd a/nested run test", EvidenceEpoch: 4},
	}}
	gate := policy.AssuranceGate{ID: "scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"**/package.json"}, Commands: []string{"bun run test"}}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, inputs)
	if err != nil || len(findings) != 1 || len(findings[0].Paths) != 1 || findings[0].Paths[0] != "a/nested/package.json" {
		t.Fatalf("package ownership: %+v, %v", findings, err)
	}
	inputs.SuccessfulCommandEvidence = append(inputs.SuccessfulCommandEvidence, CommandEvidence{Command: "bun --cwd a/nested run test", EvidenceEpoch: 5})
	findings, err = Evaluate(root, []policy.AssuranceGate{gate}, inputs)
	if err != nil || len(findings) != 0 {
		t.Fatalf("newest duplicate evidence: %+v, %v", findings, err)
	}
}

func TestAssuranceIdentityIncludesCausalData(t *testing.T) {
	root := t.TempDir()
	gate := policy.AssuranceGate{ID: "live", Type: policy.AssuranceLiveVerification, Commands: []string{"verify"}, CommandPolicy: "all"}
	inputs := Inputs{ChangedPaths: []string{"main.go"}, WriteEpochs: map[string]uint64{"main.go": 1}, SuccessfulCommandEvidence: []CommandEvidence{{Command: "verify", EvidenceEpoch: 3}}}
	_, first, err := EvaluateWithInputIdentity(root, []policy.AssuranceGate{gate}, inputs)
	if err != nil {
		t.Fatal(err)
	}
	inputs.WriteEpochs["main.go"] = 2
	_, second, err := EvaluateWithInputIdentity(root, []policy.AssuranceGate{gate}, inputs)
	if err != nil || first == second {
		t.Fatalf("write identity unchanged: %v", err)
	}
	inputs.SuccessfulCommandEvidence[0].EvidenceEpoch = 4
	_, third, err := EvaluateWithInputIdentity(root, []policy.AssuranceGate{gate}, inputs)
	if err != nil || second == third {
		t.Fatalf("command identity unchanged: %v", err)
	}
}
