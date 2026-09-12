package agentsession

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/runtime"
)

func TestPreDecisionEmptyRouteResamplesPathAndPolicyIdentity(t *testing.T) {
	for _, tool := range []string{"Write", "Bash"} {
		for _, mutation := range []string{"equal-metadata-content", "equal-content-replacement", "symlink-escape", "stale-policy"} {
			t.Run(tool+"/"+mutation, func(t *testing.T) {
				repo := setupStopBenchmarkRepo(t)
				payload := &HookPayload{
					SessionID: "empty-route", ToolUseID: "same-call", ToolName: tool,
					ToolInput: map[string]interface{}{"file_path": "src/a.go", "command": "echo safe"},
					Raw:       map[string]interface{}{"reconc_write_paths": []interface{}{"src/a.go"}},
				}
				evaluator, stopCache := runtime.NewEvaluator(), NewStopDecisionCache()
				before, ok := preDecisionInputsForPayloadWithEvaluatorAndStopCache(repo, payload, evaluator, stopCache)
				if !ok {
					t.Fatal("empty route did not produce an initial cache identity")
				}
				if err := writePreDecisionCacheForPayload(repo, payload, before.key, Result{decisionClass: preDecisionResultPass}); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(repo, "src/a.go")
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				switch mutation {
				case "equal-metadata-content":
					if err := os.WriteFile(path, []byte("package new\n"), info.Mode()); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
						t.Fatal(err)
					}
				case "equal-content-replacement", "symlink-escape":
					if err := os.Rename(path, path+".previous"); err != nil {
						t.Fatal(err)
					}
					if mutation == "symlink-escape" {
						if err := os.Symlink(filepath.Join(t.TempDir(), "outside.go"), path); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := os.WriteFile(path, []byte("package src\n"), info.Mode()); err != nil {
							t.Fatal(err)
						}
						if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
							t.Fatal(err)
						}
					}
				case "stale-policy":
					if err := os.WriteFile(filepath.Join(repo, "policies/rules.yml"), []byte("rules: []\n# changed source\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				after, cacheable := resamplePreDecisionInputsWithEvaluatorAndStopCache(repo, payload, before, evaluator, stopCache)
				if (mutation == "equal-metadata-content" || mutation == "equal-content-replacement") && !cacheable {
					t.Fatal("ordinary file mutation failed to produce a fresh cache identity")
				}
				if cacheable && before.identity.equal(after.identity) {
					t.Fatal("mutation retained the original empty-route identity")
				}
				if _, hit := readPreDecisionCacheForInputs(repo, payload, before); hit {
					t.Fatal("mutation served a stale empty-route cache entry")
				}
				if (mutation == "symlink-escape" || mutation == "stale-policy") && cacheable {
					t.Fatal("invalid path or stale source remained cacheable")
				}
			})
		}
	}
}
