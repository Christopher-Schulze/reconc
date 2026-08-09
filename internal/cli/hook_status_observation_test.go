package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/hooks"
)

func TestHookStatusExposesOMPUserPythonObservationWithoutSource(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	payload := fmt.Sprintf(`{"hook_event_name":"user_python","session_id":"omp-status","cwd":%q,`+
		`"user_python_cwd":%q,"exclude_from_context":true,"code_bytes":321}`, repo, repo)
	stdout, stderr, code := runWithStdin(t, payload, "hook", "runtime", "omp-user-python", repo)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("OMP user_python runtime code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	stdout, stderr, code = runWithStdin(t, "", "hook", "status", repo, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("hook status code=%d stderr=%q", code, stderr)
	}
	var reports []hooks.PlatformStatus
	if err := json.Unmarshal([]byte(stdout), &reports); err != nil {
		t.Fatalf("decode hook status: %v\n%s", err, stdout)
	}
	var omp hooks.PlatformStatus
	for _, report := range reports {
		if report.Kind == hooks.KindOMP {
			omp = report
			break
		}
	}
	observation := omp.Observations["omp-user-python"]
	if observation.Count != 1 || observation.WorkingDirectory != "." || observation.CodeBytes != 321 ||
		!observation.ExcludeFromContext || observation.LastSeen == "" {
		t.Fatalf("OMP status observation = %+v; report=%+v", observation, omp)
	}
	if strings.Contains(stdout, "import ") || strings.Contains(stdout, `"code"`) {
		t.Fatalf("hook status leaked Python source: %s", stdout)
	}

	stdout, stderr, code = runWithStdin(t, "", "hook", "status", repo)
	if code != 0 || stderr != "" {
		t.Fatalf("text hook status code=%d stderr=%q", code, stderr)
	}
	want := "observation omp-user-python: count=1"
	if !strings.Contains(stdout, want) || !strings.Contains(stdout, "cwd=. code_bytes=321 exclude_from_context=true") {
		t.Fatalf("text hook status misses redacted observation %q:\n%s", want, stdout)
	}
}
