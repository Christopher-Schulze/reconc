package cireport

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
)

const sarifSchema = "https://json.schemastore.org/sarif-2.1.0.json"

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool              sarifTool         `json:"tool"`
	AutomationDetails sarifAutomation   `json:"automationDetails"`
	Invocations       []sarifInvocation `json:"invocations"`
	Results           []sarifResult     `json:"results"`
	Properties        sarifProperties   `json:"properties"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	Rules   []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string              `json:"id"`
	ShortDescription sarifMessage        `json:"shortDescription"`
	Properties       sarifRuleProperties `json:"properties"`
}

type sarifRuleProperties struct {
	Kind string `json:"kind"`
	Mode string `json:"mode"`
}

type sarifAutomation struct {
	ID string `json:"id"`
}

type sarifInvocation struct {
	ExecutionSuccessful bool `json:"executionSuccessful"`
	ExitCode            int  `json:"exitCode"`
}

type sarifResult struct {
	RuleID     string                `json:"ruleId"`
	Level      string                `json:"level"`
	Message    sarifMessage          `json:"message"`
	Locations  []sarifLocation       `json:"locations,omitempty"`
	Properties sarifResultProperties `json:"properties"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifResultProperties struct {
	Mode         string   `json:"mode"`
	Remediation  string   `json:"remediation,omitempty"`
	SourcePath   string   `json:"source_path,omitempty"`
	MatchedPaths []string `json:"matched_paths"`
	OmittedPaths int      `json:"omitted_matched_paths"`
}

type sarifProperties struct {
	FormatVersion     string    `json:"reconc_format_version"`
	Decision          string    `json:"decision"`
	Summary           string    `json:"summary"`
	Candidate         Candidate `json:"candidate"`
	Git               *Git      `json:"git,omitempty"`
	TruncatedFindings int       `json:"truncated_findings"`
}

func renderSARIF(model Model) ([]byte, error) {
	rules, results := sarifFindings(model)
	exitCode := decisionExitCode(model.Decision)
	successful := model.OperationalError == ""
	if !successful {
		exitCode = model.ExitCode
	}
	log := sarifLog{Schema: sarifSchema, Version: "2.1.0", Runs: []sarifRun{{
		Tool:              sarifTool{Driver: sarifDriver{Name: "Reconc", Version: model.ToolVersion, Rules: rules}},
		AutomationDetails: sarifAutomation{ID: "reconc/" + model.Command},
		Invocations:       []sarifInvocation{{ExecutionSuccessful: successful, ExitCode: exitCode}},
		Results:           results,
		Properties: sarifProperties{
			FormatVersion: model.FormatVersion, Decision: model.Decision, Summary: model.Summary,
			Candidate: model.Candidate, Git: model.Git, TruncatedFindings: model.TruncatedFindings,
		},
	}}}
	body, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal SARIF: %w", err)
	}
	return appendBoundedNewline(body)
}

func sarifFindings(model Model) ([]sarifRule, []sarifResult) {
	findings := model.Findings
	if model.OperationalError != "" {
		findings = []Finding{{
			RuleID: "reconc/operational-error", Kind: "operational", Mode: "error", Level: "error",
			Message: model.OperationalError, Paths: []string{},
		}}
	}
	rulesByID := map[string]sarifRule{}
	results := make([]sarifResult, 0, len(findings))
	for _, finding := range findings {
		ruleID := finding.RuleID
		if ruleID == "" {
			ruleID = "reconc/unknown-rule"
		}
		rulesByID[ruleID] = sarifRule{
			ID: ruleID, ShortDescription: sarifMessage{Text: finding.Message},
			Properties: sarifRuleProperties{Kind: finding.Kind, Mode: finding.Mode},
		}
		results = append(results, sarifResultFromFinding(ruleID, finding))
	}
	return sortedSARIFRules(rulesByID), results
}

func sarifResultFromFinding(ruleID string, finding Finding) sarifResult {
	locations := make([]sarifLocation, 0, len(finding.Paths))
	for _, matchedPath := range finding.Paths {
		uri := (&url.URL{Path: matchedPath}).EscapedPath()
		locations = append(locations, sarifLocation{PhysicalLocation: sarifPhysicalLocation{
			ArtifactLocation: sarifArtifactLocation{URI: uri},
		}})
	}
	return sarifResult{
		RuleID: ruleID, Level: finding.Level, Message: sarifMessage{Text: finding.Message}, Locations: locations,
		Properties: sarifResultProperties{
			Mode: finding.Mode, Remediation: finding.Remediation,
			SourcePath: finding.SourcePath, MatchedPaths: append([]string{}, finding.Paths...),
			OmittedPaths: finding.OmittedPaths,
		},
	}
}

func sortedSARIFRules(byID map[string]sarifRule) []sarifRule {
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rules := make([]sarifRule, 0, len(ids))
	for _, id := range ids {
		rules = append(rules, byID[id])
	}
	return rules
}
