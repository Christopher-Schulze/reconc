package runtime

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzDecodeRuntimeRulesNoPanic(f *testing.F) {
	for _, seed := range []string{
		`[]`,
		`[{"id":"r","kind":"deny_write","message":"m","paths":["x"]}]`,
		`[{"id":"r","kind":"future","message":"m"}]`,
		`[{"id":"r","kind":"deny_write","message":"m","extra":true}]`,
		`{"not":"a list"}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, encoded string) {
		var raw interface{}
		if json.Unmarshal([]byte(encoded), &raw) != nil {
			return
		}
		_, _ = decodeRuntimeRules(raw)
	})
}

func FuzzNormalizePathsStaysRepoRelative(f *testing.F) {
	for _, seed := range []string{
		"src/main.go",
		"./docs/../docs/spec.md",
		"generated//out.json",
		" ",
		"../outside.txt",
		"/tmp/outside.txt",
		"src/../../escape.go",
	} {
		f.Add(seed)
	}
	root := f.TempDir()

	f.Fuzz(func(t *testing.T, raw string) {
		got, err := normalizePaths([]string{raw}, root)
		if err != nil {
			return
		}
		for _, path := range got {
			if path == "" {
				t.Fatalf("normalized path must not be empty")
			}
			if filepath.IsAbs(path) {
				t.Fatalf("normalized path must be repo-relative, got %q", path)
			}
			if filepath.Separator == '\\' && strings.Contains(path, "\\") {
				t.Fatalf("normalized path must be POSIX, got %q", path)
			}
			for _, part := range strings.Split(path, "/") {
				if part == ".." {
					t.Fatalf("normalized path must not contain parent traversal, got %q", path)
				}
			}
		}
	})
}

func FuzzMatchPathNoPanic(f *testing.F) {
	seeds := [][2]string{
		{"src/**", "src/main.go"},
		{"generated-*/**", "generated-7/out.json"},
		{"**/*.gen.json", "pkg/a/file.gen.json"},
		{"[abc", "a"},
		{"", "src/main.go"},
	}
	for _, seed := range seeds {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, pattern, path string) {
		_, _ = MatchPath(pattern, path)
	})
}

func FuzzMatchTemplateNoPanic(f *testing.F) {
	seeds := [][2]string{
		{"src/{module}/**/file.go", "src/auth/file.go"},
		{"**/{leaf}.go", "internal/runtime/file.go"},
		{"{src,pkg}/{module}/file[0-9].go", "pkg/auth/file7.go"},
		{"tmp/{name}.txt", "tmp/*.txt"},
		{"{duplicate}/{duplicate}", "a/b"},
		{"src/{module}/file[.go", "src/auth/filex.go"},
	}
	for _, seed := range seeds {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, pattern, path string) {
		_, _, _ = MatchTemplate(pattern, path)
	})
}

func FuzzLoadExecutionInputsTextNoPanic(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"read_paths":["docs/spec.md"],"write_paths":["src/main.go"]}`,
		`{"events":[{"kind":"claim","claim":"ci-green"}]}`,
		`{"events":[{"kind":"command","command":"go test ./...","outcome":"success"}]}`,
		`{"events":"bad"}`,
		`{} {}`,
		`{} trailing`,
		`not-json`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, payload string) {
		inputs, err := LoadExecutionInputsText(payload, "fuzz")
		if err != nil {
			return
		}
		for _, commandResult := range inputs.CommandResults {
			if commandResult.Outcome != CommandOutcomeSuccess && commandResult.Outcome != CommandOutcomeFailure {
				t.Fatalf("invalid command outcome accepted: %q", commandResult.Outcome)
			}
		}
	})
}
