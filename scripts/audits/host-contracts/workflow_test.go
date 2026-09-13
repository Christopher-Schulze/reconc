package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMaintenanceWorkflowIsolation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".github/workflows/reconc-host-contracts.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On          map[string]yaml.Node `yaml:"on"`
		Permissions map[string]string    `yaml:"permissions"`
		Jobs        map[string]struct {
			Timeout int `yaml:"timeout-minutes"`
			Steps   []struct {
				ID              string            `yaml:"id"`
				Run             string            `yaml:"run"`
				Uses            string            `yaml:"uses"`
				If              string            `yaml:"if"`
				ContinueOnError bool              `yaml:"continue-on-error"`
				With            map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	if len(workflow.On) != 2 || workflow.On["schedule"].Kind == 0 || workflow.On["workflow_dispatch"].Kind == 0 {
		t.Fatal("source watch must remain scheduled/manual, separate from product CI")
	}
	if len(workflow.Permissions) != 1 || workflow.Permissions["contents"] != "read" {
		t.Fatal("maintenance gained write permissions")
	}
	job, ok := workflow.Jobs["sources"]
	if !ok || len(workflow.Jobs) != 1 || job.Timeout < 1 || job.Timeout > 8 {
		t.Fatal("maintenance job missing or unbounded")
	}
	compared, uploaded, surfaced := false, false, false
	for _, step := range job.Steps {
		if step.ID == "compare" {
			compared = step.ContinueOnError && step.Run == `"$RUNNER_TEMP/host-contracts" --check-upstream > host-contract-report.json`
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			uploaded = strings.Contains(step.If, "always()") && step.With["path"] == "host-contract-report.json"
		}
		if strings.Contains(step.If, "steps.compare.outcome == 'failure'") && strings.Contains(step.Run, "exit 1") {
			surfaced = true
		}
	}
	if !compared || !uploaded || !surfaced {
		t.Fatal("source drift must retain a report and visibly fail its own maintenance run")
	}
}
