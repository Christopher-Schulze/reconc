package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const harnessBindingTestVersion = "0.9.0"

func TestAdvancedPlanConsumersRejectUnboundHarnessPackState(t *testing.T) {
	bootstrapTestHome(t)
	repo := t.TempDir()
	valid, err := BuildPlan(Request{RepoRoot: repo, Profile: ProfileAdvanced}, harnessBindingTestVersion)
	if err != nil {
		t.Fatal(err)
	}

	mutations := []struct {
		name   string
		mutate func(*Plan)
	}{
		{name: "pack name", mutate: func(plan *Plan) { plan.Selection.HarnessPacks[0].Name = "forged" }},
		{name: "pack version", mutate: func(plan *Plan) { plan.Selection.HarnessPacks[0].Version = "9.9.9" }},
		{name: "pack digest", mutate: func(plan *Plan) { plan.Selection.HarnessPacks[0].Digest = strings.Repeat("0", 64) }},
		{name: "incompatible product version", mutate: func(plan *Plan) { plan.ProductVersion = "1.0.0" }},
		{name: "invalid product version", mutate: func(plan *Plan) { plan.ProductVersion = "unavailable" }},
		{name: "artifact digest", mutate: func(plan *Plan) {
			for index := range plan.Actions {
				if strings.HasPrefix(plan.Actions[index].Component, "harness-pack:") {
					plan.Actions[index].DesiredSHA256 = strings.Repeat("0", 64)
					return
				}
			}
			t.Fatal("advanced plan has no harness pack artifact")
		}},
	}

	consumers := []struct {
		name string
		run  func(*testing.T, *Plan) error
	}{
		{name: "validate", run: func(_ *testing.T, plan *Plan) error { return ValidatePlan(plan) }},
		{name: "load", run: loadMutatedPlan},
		{name: "write", run: func(t *testing.T, plan *Plan) error {
			_, err := WritePlan(filepath.Join(t.TempDir(), "plan.json"), plan)
			return err
		}},
		{name: "replace", run: func(t *testing.T, plan *Plan) error {
			_, err := ReplacePlan(filepath.Join(t.TempDir(), "plan.json"), plan)
			return err
		}},
		{name: "verify", run: func(_ *testing.T, plan *Plan) error {
			_, err := Verify(plan)
			return err
		}},
		{name: "apply", run: func(_ *testing.T, plan *Plan) error {
			_, err := Apply(plan, plan.ProductVersion)
			return err
		}},
		{name: "remove", run: func(_ *testing.T, plan *Plan) error {
			_, err := Remove(plan)
			return err
		}},
		{name: "managed candidate acceptance", run: func(_ *testing.T, plan *Plan) error {
			_, err := AcceptManagedCandidates(plan, &Report{})
			return err
		}},
		{name: "repository receipt", run: func(_ *testing.T, plan *Plan) error {
			_, err := BuildRepositoryReceipt(plan, &InstallReceipt{}, 1, strings.Repeat("a", 64))
			return err
		}},
	}

	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			plan := cloneBootstrapPlan(t, valid)
			mutation.mutate(plan)
			plan.PlanDigest, err = computePlanDigest(plan)
			if err != nil {
				t.Fatal(err)
			}
			want := ValidatePlan(plan)
			if want == nil {
				t.Fatal("mutated plan passed canonical validation")
			}
			for _, consumer := range consumers {
				t.Run(consumer.name, func(t *testing.T) {
					got := consumer.run(t, plan)
					if got == nil || got.Error() != want.Error() {
						t.Fatalf("consumer error = %v, want %v", got, want)
					}
				})
			}
		})
	}
}

func loadMutatedPlan(t *testing.T, plan *Plan) error {
	t.Helper()
	body, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return err
	}
	_, err = LoadPlan(path)
	return err
}

func TestAdvancedPlanProductCompatibilityErrorIsExplicit(t *testing.T) {
	bootstrapTestHome(t)
	plan, err := BuildPlan(Request{RepoRoot: t.TempDir(), Profile: ProfileAdvanced}, harnessBindingTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	plan.ProductVersion = "1.0.0"
	plan.PlanDigest, err = computePlanDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	err = ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "supports Reconc >=0.9.0 and <1.0.0, not 1.0.0") {
		t.Fatalf("compatibility error = %v", err)
	}
}
