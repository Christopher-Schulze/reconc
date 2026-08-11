package main

import "testing"

func TestHistoricalFindingExceptionIsExact(t *testing.T) {
	for _, exception := range historicalFindingExceptions {
		path := "history/blob/" + exception.BlobID
		differentRule := "content/access-token"
		if exception.Rule == differentRule {
			differentRule = "content/private-key"
		}
		findings := []auditFinding{
			{Path: path, Rule: exception.Rule, Line: exception.Line, Detail: "synthetic fixture"},
			{Path: path, Rule: exception.Rule, Line: exception.Line + 100000, Detail: "different line"},
			{Path: path, Rule: differentRule, Line: exception.Line, Detail: "different rule"},
		}
		filtered := filterHistoricalFindingExceptions(findings)
		if len(filtered) != 2 || filtered[0].Line != exception.Line+100000 || filtered[1].Rule != differentRule {
			t.Fatalf("historical exception was not exact: %#v", filtered)
		}
	}
}
