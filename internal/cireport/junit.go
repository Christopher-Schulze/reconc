package cireport

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name       string          `xml:"name,attr"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Errors     int             `xml:"errors,attr"`
	Properties []junitProperty `xml:"properties>property"`
	Cases      []junitCase     `xml:"testcase"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCase struct {
	Name       string          `xml:"name,attr"`
	ClassName  string          `xml:"classname,attr"`
	Properties []junitProperty `xml:"properties>property"`
	Failure    *junitIssue     `xml:"failure,omitempty"`
	Error      *junitIssue     `xml:"error,omitempty"`
	SystemOut  string          `xml:"system-out,omitempty"`
}

type junitIssue struct {
	Type    string `xml:"type,attr"`
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

func renderJUnit(model Model) ([]byte, error) {
	suite := junitSuite{Name: "reconc/" + model.Command, Properties: junitModelProperties(model), Cases: []junitCase{}}
	if model.OperationalError != "" {
		suite.Cases = append(suite.Cases, junitOperationalCase(model))
	} else if len(model.Findings) == 0 {
		suite.Cases = append(suite.Cases, junitPassCase(model))
	} else {
		for _, finding := range model.Findings {
			suite.Cases = append(suite.Cases, junitFindingCase(model.Command, finding))
		}
	}
	for _, testCase := range suite.Cases {
		if testCase.Failure != nil {
			suite.Failures++
		}
		if testCase.Error != nil {
			suite.Errors++
		}
	}
	suite.Tests = len(suite.Cases)
	document := junitSuites{
		Name: "Reconc", Tests: suite.Tests, Failures: suite.Failures, Errors: suite.Errors,
		Suites: []junitSuite{suite},
	}
	body, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal JUnit: %w", err)
	}
	body = append([]byte(xml.Header), body...)
	return appendBoundedNewline(body)
}

func junitFindingCase(command string, finding Finding) junitCase {
	name := finding.RuleID
	if name == "" {
		name = "unknown-rule"
	}
	properties := []junitProperty{
		{Name: "kind", Value: finding.Kind}, {Name: "mode", Value: finding.Mode},
		{Name: "remediation", Value: finding.Remediation},
		{Name: "matched_paths", Value: strings.Join(finding.Paths, ",")},
		{Name: "omitted_matched_paths", Value: strconv.Itoa(finding.OmittedPaths)},
		{Name: "source_path", Value: finding.SourcePath},
	}
	testCase := junitCase{Name: name, ClassName: "reconc." + command, Properties: properties}
	detail := finding.Message
	if finding.Remediation != "" {
		detail += " Remediation: " + finding.Remediation
	}
	if finding.Level == "error" {
		testCase.Failure = &junitIssue{Type: "policy." + finding.Mode, Message: finding.Message, Body: detail}
	} else {
		testCase.SystemOut = strings.ToUpper(finding.Level) + ": " + detail
	}
	return testCase
}

func junitOperationalCase(model Model) junitCase {
	return junitCase{
		Name: "operational-error", ClassName: "reconc." + model.Command,
		Error: &junitIssue{Type: "reconc.operational", Message: model.OperationalError, Body: model.OperationalError},
	}
}

func junitPassCase(model Model) junitCase {
	return junitCase{Name: "policy-decision", ClassName: "reconc." + model.Command, SystemOut: model.Summary}
}

func junitModelProperties(model Model) []junitProperty {
	properties := []junitProperty{
		{Name: "reconc.format_version", Value: model.FormatVersion},
		{Name: "reconc.decision", Value: model.Decision},
		{Name: "reconc.candidate_fingerprint", Value: model.Candidate.Fingerprint},
		{Name: "reconc.policy_lock_hash", Value: model.Candidate.PolicyLockHash},
		{Name: "reconc.worktree_hash", Value: model.Candidate.WorktreeHash},
		{Name: "reconc.git_available", Value: strconv.FormatBool(model.Candidate.GitAvailable)},
		{Name: "reconc.worktree_trusted", Value: strconv.FormatBool(model.Candidate.WorktreeTrusted)},
		{Name: "reconc.dirty_path_count", Value: strconv.Itoa(model.Candidate.DirtyPathCount)},
		{Name: "reconc.truncated_findings", Value: strconv.Itoa(model.TruncatedFindings)},
	}
	if model.Git != nil {
		properties = append(properties,
			junitProperty{Name: "reconc.git_mode", Value: model.Git.Mode},
			junitProperty{Name: "reconc.git_base", Value: model.Git.Base},
			junitProperty{Name: "reconc.git_head", Value: model.Git.Head},
			junitProperty{Name: "reconc.git_write_path_count", Value: strconv.Itoa(model.Git.WritePathCount)},
		)
	}
	return properties
}
