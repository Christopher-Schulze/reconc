package main

import (
	"context"
	"os/exec"
	"slices"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestMacOSWorkflowRouting(t *testing.T) {
	root := publicSurfaceRoot(t)
	shared := "./.github/workflows/reconc-macos.yml"
	var dispatch struct {
		On struct {
			WorkflowDispatch struct {
				Inputs map[string]struct {
					Type    string `yaml:"type"`
					Default bool   `yaml:"default"`
				} `yaml:"inputs"`
			} `yaml:"workflow_dispatch"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal([]byte(readPublicSurfaceFile(t, root, ".github/workflows/reconc-ci.yml")), &dispatch); err != nil {
		t.Fatal(err)
	}
	input, exists := dispatch.On.WorkflowDispatch.Inputs["macos_checks"]
	if !exists || input.Type != "boolean" || input.Default {
		t.Fatal("manual macOS checks must be a default-false boolean input")
	}
	for _, test := range []struct {
		path string
		job  string
		when string
	}{
		{".github/workflows/reconc-ci.yml", "macos", "${{ github.event_name == 'workflow_dispatch' && inputs.macos_checks }}"},
	} {
		t.Run(test.job, func(t *testing.T) {
			var workflow githubWorkflow
			if err := yaml.Unmarshal([]byte(readPublicSurfaceFile(t, root, test.path)), &workflow); err != nil {
				t.Fatal(err)
			}
			job, ok := workflow.Jobs[test.job]
			if !ok || job.Uses != shared || job.If != test.when {
				t.Fatalf("%s must call the shared macOS checks with condition %q", test.path, test.when)
			}
		})
	}
	var release githubWorkflow
	if err := yaml.Unmarshal([]byte(readPublicSurfaceFile(t, root, ".github/workflows/reconc-release.yml")), &release); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(release.Jobs["release"].Needs, "gate-macos") {
		t.Fatal("release artifacts must not depend on macOS tests")
	}
	for _, job := range release.Jobs {
		if job.Uses == shared {
			t.Fatal("Release must not start macOS tests; select them explicitly in manual CI")
		}
	}
	var sharedWorkflow githubWorkflow
	body := readPublicSurfaceFile(t, root, ".github/workflows/reconc-macos.yml")
	if err := yaml.Unmarshal([]byte(body), &sharedWorkflow); err != nil {
		t.Fatal(err)
	}
	var triggers struct {
		On map[string]yaml.Node `yaml:"on"`
	}
	if err := yaml.Unmarshal([]byte(body), &triggers); err != nil {
		t.Fatal(err)
	}
	job := sharedWorkflow.Jobs["test"]
	if len(triggers.On) != 1 || triggers.On["workflow_call"].Kind == 0 ||
		len(sharedWorkflow.Jobs) != 1 || job.RunsOn != "macos-15" || job.TimeoutMinutes != 40 || job.If != "" {
		t.Fatal("shared macOS checks must run as a bounded, unconditional reusable job")
	}
	if len(sharedWorkflow.Permissions) != 1 || sharedWorkflow.Permissions["contents"] != "read" {
		t.Fatal("shared macOS checks must have only read permission")
	}
	checkout, tests, selfHost := false, false, false
	for _, step := range job.Steps {
		if step.ContinueOnError || step.If != "" {
			t.Fatal("shared macOS checks must not ignore or conditionally skip steps")
		}
		checkout = checkout || step.With["ref"] == "${{ github.sha }}"
		tests = tests || step.Run == `./scripts/tests/go-test-shard.sh 1 1 -p="$TEST_PARALLELISM" -vet=off -count=1`
		selfHost = selfHost || step.Run == "make self-host"
	}
	if !checkout || !tests || !selfHost {
		t.Fatal("shared macOS checks must test and self-host the caller's exact commit")
	}
}

func TestCIResultRequiresSelectedPlatforms(t *testing.T) {
	root := publicSurfaceRoot(t)
	var workflow githubWorkflow
	if err := yaml.Unmarshal([]byte(readPublicSurfaceFile(t, root, ".github/workflows/reconc-ci.yml")), &workflow); err != nil {
		t.Fatal(err)
	}
	job := workflow.Jobs["result"]
	if job.If != "${{ always() }}" || !equalStrings(job.Needs, []string{"checks", "linux", "macos"}) || len(job.Steps) != 1 {
		t.Fatal("CI result must run after all selected platform jobs, including failures")
	}
	step := job.Steps[0]
	for key, want := range map[string]string{
		"REQUIRED_RESULTS": "${{ needs.checks.result }} ${{ needs.linux.result }}",
		"MACOS_REQUIRED":   "${{ github.event_name == 'workflow_dispatch' && inputs.macos_checks }}",
		"MACOS_RESULT":     "${{ needs.macos.result }}",
	} {
		if step.Env[key] != want {
			t.Fatalf("CI result %s = %q, want %q", key, step.Env[key], want)
		}
	}
	for _, test := range []struct {
		name     string
		required string
		selected string
		macos    string
		pass     bool
	}{
		{"ordinary push", "success success", "false", "skipped", true},
		{"manual macOS success", "success success", "true", "success", true},
		{"boundary failed", "failure success", "false", "skipped", false},
		{"Linux failed", "success failure", "false", "skipped", false},
		{"Linux cancelled", "success cancelled", "false", "skipped", false},
		{"Linux skipped", "success skipped", "false", "skipped", false},
		{"manual macOS failed", "success success", "true", "failure", false},
		{"manual macOS cancelled", "success success", "true", "cancelled", false},
		{"manual macOS skipped", "success success", "true", "skipped", false},
		{"unexpected macOS failure", "success success", "false", "failure", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "bash", "-e", "-c", step.Run)
			command.Env = []string{"PATH=/usr/bin:/bin", "REQUIRED_RESULTS=" + test.required,
				"MACOS_REQUIRED=" + test.selected, "MACOS_RESULT=" + test.macos}
			output, err := command.CombinedOutput()
			if ctx.Err() != nil || (err == nil) != test.pass {
				t.Fatalf("CI result pass=%t, want %t: %v %s", err == nil, test.pass, err, output)
			}
		})
	}
}
