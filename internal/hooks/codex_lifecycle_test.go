package hooks

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestCodexGeneratedLifecycleSelectsRecoveryAndBoundsInterrupt(t *testing.T) {
	artifact, err := Generate(KindCodex)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(artifact.Content), &config); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"startup", "resume", "clear", "compact"} {
		matches := []string{}
		for _, group := range config.Hooks["SessionStart"] {
			pattern, err := regexp.Compile("^(?:" + group.Matcher + ")$")
			if err != nil {
				t.Fatal(err)
			}
			if pattern.MatchString(source) {
				for _, hook := range group.Hooks {
					matches = append(matches, hook.Command)
				}
			}
		}
		want := "codex-session-start"
		if source == "compact" {
			want = "codex-compaction-recovery"
		}
		if len(matches) != 1 || !strings.Contains(matches[0], want) {
			t.Fatalf("source %s routes to %v", source, matches)
		}
	}
	groups := config.Hooks["Interrupt"]
	if len(groups) != 1 || groups[0].Matcher != "" || len(groups[0].Hooks) != 1 || groups[0].Hooks[0].Timeout != 3 || !strings.Contains(groups[0].Hooks[0].Command, "codex-interrupt") {
		t.Fatalf("interrupt contract=%+v", groups)
	}
	route, ok := RuntimeEvent("codex-interrupt")
	if !ok || route.ErrorPolicy != FailureAllow || route.TimeoutPolicy != FailureAllow || route.TimeoutSeconds != 3 {
		t.Fatalf("interrupt could veto host cancellation: %+v", route)
	}
	groups = config.Hooks["SubagentStop"]
	if len(groups) != 1 || len(groups[0].Hooks) != 1 || groups[0].Hooks[0].Timeout != 30 {
		t.Fatalf("child policy check lacks its bounded deadline: %+v", groups)
	}
	route, ok = RuntimeEvent("codex-subagent-stop")
	if !ok || route.ErrorPolicy != FailureBlock || route.TimeoutPolicy != FailureAllow {
		t.Fatalf("child policy errors or host timeout have wrong semantics: %+v", route)
	}
}
