package assurance

import (
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestSourceHygieneFindsHighSignalShippedCodeDebt(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "src/main.go", "package src\n// TODO: implement\n_ = err\npanic(\"not implemented\")\n")
	writeAssuranceFile(t, root, "src/lib.rs", "/// placeholder: real body\ntodo!();\nunimplemented!();\n")
	writeAssuranceFile(t, root, "src/client.ts", "/** FIXME - finish */\nthrow new Error('not implemented');\n")
	gate := policy.AssuranceGate{ID: "hygiene", Type: policy.AssuranceSourceHygiene, ScanPaths: []string{"src/**"}}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths: []string{"src/main.go", "src/lib.rs", "src/client.ts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 8 {
		t.Fatalf("expected 8 high-signal findings, got %d: %+v", len(findings), findings)
	}
	joined := findingsText(findings)
	for _, expected := range []string{
		"implementation-debt marker TODO at src/main.go:2",
		"ignored Go error sentinel at src/main.go:3",
		"unimplemented Go panic sentinel at src/main.go:4",
		"unimplemented Rust macro sentinel",
		"unimplemented JavaScript/TypeScript throw sentinel",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("expected %q in findings: %s", expected, joined)
		}
	}
}

func TestSourceHygieneAvoidsExamplesAndHonorsReasonedExemptions(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "src/clean.go", "package src\nconst TODOList = \"// TODO: example\"\nconst panicExample = `panic(\"not implemented\")`\n// TODOList is a real symbol.\nvalue := 1 // panic(\"not implemented\")\n")
	writeAssuranceFile(t, root, "src/legacy.go", "package src\n// XXX: legacy protocol fixture\n")
	writeAssuranceFile(t, root, "src/clean_test.go", "package src\n// TODO: test fixture\n")
	writeAssuranceFile(t, root, "src/example.rs", "const EXAMPLE: &str = \"todo!(\";\n")
	writeAssuranceFile(t, root, "src/example.ts", "const example = \"throw new Error('not implemented')\";\n")
	gate := policy.AssuranceGate{
		ID: "hygiene", Type: policy.AssuranceSourceHygiene,
		ScanPaths: []string{"src/**"}, ExcludePaths: []string{"**/*_test.go"},
		Exemptions: []policy.AssuranceExemption{{Path: "src/legacy.go", Reason: "frozen protocol fixture"}},
	}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths: []string{"src/clean.go", "src/legacy.go", "src/clean_test.go", "src/example.rs", "src/example.ts"},
	})
	if err != nil || len(findings) != 0 {
		t.Fatalf("examples, token continuations, tests, and exemptions must not create friction: findings=%+v err=%v", findings, err)
	}
}

func TestSourceHygieneDoesNotTreatRustDereferenceAsBlockComment(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "src/lib.rs", "*pointer = todo!();\n")
	gate := policy.AssuranceGate{ID: "hygiene", Type: policy.AssuranceSourceHygiene, ScanPaths: []string{"src/**"}}

	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"src/lib.rs"}})
	if err != nil || len(findings) != 1 {
		t.Fatalf("Rust dereference line must retain sentinel coverage: findings=%+v err=%v", findings, err)
	}
}
