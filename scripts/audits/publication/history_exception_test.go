package main

import "testing"

func TestHistoricalFindingExceptionIsExact(t *testing.T) {
	exception := historicalFindingExceptions[0]
	path := "history/blob/" + exception.BlobID
	findings := []auditFinding{
		{Path: path, Rule: exception.Rule, Line: exception.Line, Detail: "synthetic fixture"},
		{Path: path, Rule: exception.Rule, Line: exception.Line + 1, Detail: "different line"},
		{Path: path, Rule: "content/access-token", Line: exception.Line, Detail: "different rule"},
	}
	filtered := filterHistoricalFindingExceptions(findings)
	if len(filtered) != 2 || filtered[0].Line != exception.Line+1 || filtered[1].Rule != "content/access-token" {
		t.Fatalf("historical exception was not exact: %#v", filtered)
	}
}
