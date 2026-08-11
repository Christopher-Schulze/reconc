package cireport

import (
	"fmt"
	"strings"
)

func renderGitHub(model Model) ([]byte, error) {
	var output strings.Builder
	fmt.Fprintln(&output, "## Reconc "+githubCell(model.Command))
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "Decision: **%s**. %s\n", githubCell(model.Decision), githubCell(model.Summary))
	if model.OperationalError != "" {
		fmt.Fprintf(&output, "\nOperational error: `%s`\n", githubCell(model.OperationalError))
	}
	if len(model.Findings) > 0 {
		fmt.Fprintln(&output, "\n| Case | Delta | Current | Candidate | Review |")
		fmt.Fprintln(&output, "|---|---|---|---|---|")
		for _, finding := range model.Findings {
			caseID := finding.CaseID
			if caseID == "" {
				caseID = finding.RuleID
			}
			review := "n/a"
			if finding.ReviewRequired {
				if finding.Reviewed {
					review = "reviewed"
				} else {
					review = "unreviewed"
				}
			}
			fmt.Fprintf(&output, "| `%s` | `%s` | `%s` | `%s` | %s |\n",
				githubCell(caseID), githubCell(finding.DeltaKind), githubCell(finding.Current),
				githubCell(finding.Candidate), review)
		}
	}
	if model.TruncatedFindings > 0 {
		fmt.Fprintf(&output, "\n%d finding(s) omitted by the bounded report limit.\n", model.TruncatedFindings)
	}
	return appendBoundedNewline([]byte(strings.TrimSuffix(output.String(), "\n")))
}

func githubCell(value string) string {
	value = cleanText(value)
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(value, "`", "'")
}
