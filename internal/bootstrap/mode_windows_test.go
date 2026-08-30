//go:build windows

package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModeSatisfiesUsesWindowsWritableProxy(t *testing.T) {
	tests := []struct {
		name    string
		actual  os.FileMode
		desired uint32
		want    bool
	}{
		{name: "writable data", actual: 0o666, desired: 0o644, want: true},
		{name: "writable executable intent", actual: 0o666, desired: 0o755, want: true},
		{name: "read only", actual: 0o444, desired: 0o400, want: true},
		{name: "read only executable intent", actual: 0o444, desired: 0o500, want: true},
		{name: "unexpected read only", actual: 0o444, desired: 0o644, want: false},
		{name: "unexpected writable", actual: 0o666, desired: 0o400, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := modeSatisfies(test.actual, test.desired); got != test.want {
				t.Fatalf("modeSatisfies(%04o, %04o) = %t, want %t", test.actual.Perm(), test.desired, got, test.want)
			}
		})
	}
}

func TestBootstrapReportsAndPublishesWindowsReadOnlyModeDrift(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	request := Request{RepoRoot: repo, Profile: ProfileMinimal}
	plan, err := BuildPlan(request, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(plan, "test-version")
	if err != nil || report.Status != ApplyComplete {
		t.Fatalf("initial apply: report=%+v err=%v", report, err)
	}

	target := filepath.Join(repo, ".reconc.yml")
	if err := os.Chmod(target, 0o444); err != nil {
		t.Fatal(err)
	}
	driftPlan, err := BuildPlan(request, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	action := bootstrapActionForPath(t, driftPlan, ".reconc.yml")
	if action.State != ActionConflict {
		t.Fatalf("matching-content read-only target state = %s, want %s", action.State, ActionConflict)
	}

	report, err = Apply(driftPlan, "test-version")
	if err != nil || report.Status != ApplyDrift {
		t.Fatalf("drift apply: report=%+v err=%v", report, err)
	}
	candidate := filepath.Join(repo, filepath.FromSlash(action.CandidatePath))
	info, err := os.Stat(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !modeSatisfies(info.Mode(), action.Mode) {
		t.Fatalf("candidate mode %04o does not satisfy %04o", info.Mode().Perm(), action.Mode)
	}

	verification, err := Verify(driftPlan)
	if err != nil {
		t.Fatal(err)
	}
	if verification.Valid {
		t.Fatal("verification accepted unresolved target drift")
	}
	if check := bootstrapCheckForName(t, verification, "artifact:"+action.CandidatePath); check.Status != "PASS" {
		t.Fatalf("candidate verification = %+v", check)
	}
}

func bootstrapActionForPath(t *testing.T, plan *Plan, path string) Action {
	t.Helper()
	for _, action := range plan.Actions {
		if action.Path == path {
			return action
		}
	}
	t.Fatalf("plan has no action for %s", path)
	return Action{}
}

func bootstrapCheckForName(t *testing.T, verification *Verification, name string) Check {
	t.Helper()
	for _, check := range verification.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("verification has no check for %s", name)
	return Check{}
}
