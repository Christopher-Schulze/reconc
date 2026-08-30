package cireport

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

type contractFixture struct {
	FormatVersion string `json:"format_version"`
	SARIF         struct {
		Schema  string            `json:"schema"`
		Version string            `json:"version"`
		Levels  map[string]string `json:"levels"`
	} `json:"sarif"`
	JUnit struct {
		Root               string `json:"root"`
		BlockingElement    string `json:"blocking_element"`
		OperationalElement string `json:"operational_element"`
		NonBlockingElement string `json:"non_blocking_element"`
	} `json:"junit"`
}

func TestReportsMatchPinnedConsumerContracts(t *testing.T) {
	fixture := loadContractFixture(t)
	model := contractModel()
	sarifBody, err := Render(FormatSARIF, model)
	if err != nil {
		t.Fatal(err)
	}
	assertSARIFContract(t, sarifBody, fixture)
	junitBody, err := Render(FormatJUnit, model)
	if err != nil {
		t.Fatal(err)
	}
	assertJUnitContract(t, junitBody, fixture)
	for _, body := range [][]byte{sarifBody, junitBody} {
		for _, forbidden := range []string{"/private/host", "<script>", "\x1b"} {
			if bytes.Contains(body, []byte(forbidden)) {
				t.Fatalf("report contains unsafe text %q:\n%s", forbidden, body)
			}
		}
	}
}

func TestRenderIsDeterministicBoundedAndURISafe(t *testing.T) {
	model := contractModel()
	for _, format := range []Format{FormatSARIF, FormatJUnit} {
		first, err := Render(format, model)
		if err != nil {
			t.Fatal(err)
		}
		second, err := Render(format, model)
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("%s determinism = %v", format, err)
		}
		if len(first) > MaxBytes {
			t.Fatalf("%s bytes = %d", format, len(first))
		}
	}
	sarifBody, err := Render(FormatSARIF, model)
	if err != nil {
		t.Fatal(err)
	}
	for _, uri := range []string{"src/a%20b.go", "src/%23workflow.go", "src/new%0Aline.go"} {
		if !bytes.Contains(sarifBody, []byte(uri)) {
			t.Errorf("SARIF omitted escaped URI %q:\n%s", uri, sarifBody)
		}
	}
	if !bytes.Contains(sarifBody, []byte(`"omitted_matched_paths": 1`)) {
		t.Fatalf("SARIF omitted bounded path disclosure:\n%s", sarifBody)
	}
	if bytes.Contains(sarifBody, []byte(`"startLine"`)) {
		t.Fatal("SARIF invented a source line")
	}
}

func TestOperationalReportsPreserveExitAndRedactHostPaths(t *testing.T) {
	fixture := loadContractFixture(t)
	repo := filepath.Join(t.TempDir(), "private repo")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("open " + filepath.Join(repo, "policies", "rules.yml") +
		": malformed; home=" + filepath.Join(home, "private", "cache") +
		"; neighbor=" + repo + "-cache/file; cache=/private/other/cache; windows=C:\\private\\cache; url=https://example.test/a")
	model := Operational("ci", "test", repo, Candidate{}, 2, cause)
	for _, expected := range []string{"<repo>", "<home>", "<path>", "https://example.test/a"} {
		if !strings.Contains(model.OperationalError, expected) {
			t.Errorf("operational error omitted %q: %s", expected, model.OperationalError)
		}
	}
	for _, format := range []Format{FormatSARIF, FormatJUnit, FormatGitHub} {
		body, err := Render(format, model)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte(repo)) || bytes.Contains(body, []byte(home)) || bytes.Contains(body, []byte(repo+"-cache")) ||
			bytes.Contains(body, []byte("/private/other")) ||
			bytes.Contains(body, []byte(`C:\private`)) || !strings.Contains(strings.ToLower(string(body)), "operational") {
			t.Fatalf("%s operational report leaked or omitted detail:\n%s", format, body)
		}
	}
	junitBody, err := Render(FormatJUnit, model)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(junitBody, []byte("<"+fixture.JUnit.OperationalElement)) {
		t.Fatalf("JUnit omitted operational element:\n%s", junitBody)
	}
	sarifBody, err := Render(FormatSARIF, model)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(sarifBody, []byte(`"exitCode": 2`)) {
		t.Fatalf("SARIF lost exit code:\n%s", sarifBody)
	}
}

func TestRedactHostPathsUsesCompleteBoundaries(t *testing.T) {
	replacements := []hostPathReplacement{
		{path: "/srv/über repo", token: "<repo>"},
		{path: "/srv/project", token: "<repo>"},
		{path: "/u", token: "<home>"},
		{path: `C:\Users\Al\repo`, token: "<repo>"},
	}
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "known roots with spaces unicode and punctuation",
			value: `open "/srv/über repo/policies/rules.yml"; home=/u/cache;`,
			want:  `open "<repo>"; home=<home>;`,
		},
		{
			name:  "prefix collisions become whole generic tokens",
			value: `/srv/projector/a /u2/cache`,
			want:  `<path> <path>`,
		},
		{
			name:  "windows drive separators and neighbors",
			value: `windows=(C:\Users\Al\repo\cache); neighbor=C:\Users\Al\repository\x lower=c:\users\al\repo\x`,
			want:  `windows=(<repo>); neighbor=<path> lower=<path>`,
		},
		{
			name:  "ordinary substrings and urls",
			value: `prefix/u/cache https://example.test/u/cache textC:\Users\Al\repo`,
			want:  `prefix/u/cache https://example.test/u/cache textC:\Users\Al\repo`,
		},
		{
			name:  "multiple generic paths",
			value: `one=/private/a,next two=D:/host/b; unc=\\server\share\c colon:/var/lib/x:detail`,
			want:  `one=<path>,next two=<path>; unc=<path> colon:<path>:detail`,
		},
		{
			name:  "unicode whitespace ends short-home token",
			value: "/u/cache\u00a0next",
			want:  "<home>\u00a0next",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := redactHostPaths(test.value, replacements); got != test.want {
				t.Fatalf("redactHostPaths() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFromCheckBoundsFindingCountWithoutChangingDecision(t *testing.T) {
	report := runtime.NewEmptyReport("/private/host", ".reconc/policy.lock.json", policy.ModeBlock, runtime.Empty())
	report.Violations = make([]runtime.Violation, maxFindings+1)
	for index := range report.Violations {
		report.Violations[index] = runtime.Violation{
			RuleID: "rule", Kind: policy.KindDenyWrite, Mode: policy.ModeBlock,
			Message: strings.Repeat("x", maxTextBytes), MatchedPaths: []string{"src/x.go"},
		}
	}
	report.Finalize()
	model := FromCheck("check", "test", Candidate{}, nil, &report)
	if model.Decision != "block" || len(model.Findings) != maxFindings || model.TruncatedFindings != 1 {
		t.Fatalf("bounded model = %+v", model)
	}
	if _, err := Render(FormatSARIF, model); err != nil {
		t.Fatal(err)
	}
}

func TestFromCheckNeverTruncatesLateBlockerBehindWarnings(t *testing.T) {
	report := runtime.NewEmptyReport("/private/host", ".reconc/policy.lock.json", policy.ModeBlock, runtime.Empty())
	report.Violations = make([]runtime.Violation, maxFindings+1)
	for index := range maxFindings {
		report.Violations[index] = runtime.Violation{
			RuleID: "warn-rule", Kind: policy.KindRequireRead, Mode: policy.ModeWarn,
			Message: "warning", MatchedPaths: []string{"src/warn.go"},
		}
	}
	report.Violations[maxFindings] = runtime.Violation{
		RuleID: "late-blocker", Kind: policy.KindDenyWrite, Mode: policy.ModeBlock,
		Message: "blocking violation", MatchedPaths: []string{"src/block.go"},
	}
	report.Finalize()

	model := FromCheck("ci", "test", Candidate{}, nil, &report)
	if model.Decision != "block" || len(model.Findings) != maxFindings || model.TruncatedFindings != 1 {
		t.Fatalf("bounded late-blocker model = %+v", model)
	}
	errorCount := 0
	for _, finding := range model.Findings {
		if finding.Level == "error" {
			errorCount++
		}
	}
	if errorCount != 1 {
		t.Fatalf("retained error findings = %d, want 1", errorCount)
	}

	sarifBody, err := Render(FormatSARIF, model)
	if err != nil {
		t.Fatal(err)
	}
	var sarif sarifLog
	if err := json.Unmarshal(sarifBody, &sarif); err != nil {
		t.Fatal(err)
	}
	if len(sarif.Runs) != 1 || sarif.Runs[0].Properties.TruncatedFindings != 1 ||
		len(sarif.Runs[0].Results) != maxFindings {
		t.Fatalf("SARIF truncation contract = %+v", sarif.Runs)
	}
	if level := sarifLevelForRule(sarif.Runs[0].Results, "late-blocker"); level != "error" {
		t.Fatalf("late blocker SARIF level = %q", level)
	}

	junitBody, err := Render(FormatJUnit, model)
	if err != nil {
		t.Fatal(err)
	}
	var junit junitSuites
	if err := xml.Unmarshal(junitBody, &junit); err != nil {
		t.Fatal(err)
	}
	if junit.Tests != maxFindings || junit.Failures != 1 || len(junit.Suites) != 1 ||
		junitPropertyValue(junit.Suites[0].Properties, "reconc.truncated_findings") != "1" {
		t.Fatalf("JUnit truncation contract = %+v", junit)
	}

	githubBody, err := Render(FormatGitHub, model)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(githubBody, []byte("1 finding(s) omitted by the bounded report limit.")) {
		t.Fatalf("GitHub report omitted truncation notice:\n%s", githubBody)
	}
}

func sarifLevelForRule(results []sarifResult, ruleID string) string {
	for _, result := range results {
		if result.RuleID == ruleID {
			return result.Level
		}
	}
	return ""
}

func junitPropertyValue(properties []junitProperty, name string) string {
	for _, property := range properties {
		if property.Name == name {
			return property.Value
		}
	}
	return ""
}

func contractModel() Model {
	report := runtime.NewEmptyReport("/private/host", ".reconc/policy.lock.json", policy.ModeBlock, runtime.Empty())
	report.Violations = []runtime.Violation{
		{RuleID: "warn-rule", Kind: policy.KindRequireRead, Mode: policy.ModeWarn, Message: "warn\n::warning", RecommendedAction: "read <script>", MatchedPaths: []string{"src/#workflow.go", "src/new\nline.go"}},
		{RuleID: "block-rule", Kind: policy.KindDenyWrite, Mode: policy.ModeBlock, Message: "block\x1b[31m", RecommendedAction: "remove", MatchedPaths: []string{"/private/host/secret", "src/a b.go"}, SourcePath: "policies/rules.yml"},
	}
	report.Finalize()
	return FromCheck("ci", "test", Candidate{
		Fingerprint: strings.Repeat("a", 64), PolicyLockHash: strings.Repeat("b", 64),
		WorktreeHash: strings.Repeat("c", 64), GitAvailable: true, WorktreeTrusted: true,
	}, &Git{Mode: "range", Base: "main", Head: "HEAD", WritePathCount: 3}, &report)
}

func loadContractFixture(t *testing.T) contractFixture {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "ci-native-contracts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture contractFixture
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func assertSARIFContract(t *testing.T, body []byte, fixture contractFixture) {
	t.Helper()
	var log sarifLog
	if err := json.Unmarshal(body, &log); err != nil {
		t.Fatal(err)
	}
	if log.Schema != fixture.SARIF.Schema || log.Version != fixture.SARIF.Version || len(log.Runs) != 1 {
		t.Fatalf("SARIF identity = %+v", log)
	}
	levels := map[string]string{}
	for _, result := range log.Runs[0].Results {
		levels[result.Properties.Mode] = result.Level
	}
	if levels["warn"] != fixture.SARIF.Levels["warn"] || levels["block"] != fixture.SARIF.Levels["block"] {
		t.Fatalf("SARIF levels = %v", levels)
	}
}

func assertJUnitContract(t *testing.T, body []byte, fixture contractFixture) {
	t.Helper()
	var root struct {
		XMLName  xml.Name `xml:"testsuites"`
		Tests    int      `xml:"tests,attr"`
		Failures int      `xml:"failures,attr"`
		Errors   int      `xml:"errors,attr"`
	}
	if err := xml.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	if root.XMLName.Local != fixture.JUnit.Root || root.Tests != 2 || root.Failures != 1 || root.Errors != 0 {
		t.Fatalf("JUnit counters = %+v", root)
	}
	for _, element := range []string{fixture.JUnit.BlockingElement, fixture.JUnit.NonBlockingElement} {
		if !bytes.Contains(body, []byte("<"+element)) {
			t.Errorf("JUnit omitted %s element:\n%s", element, body)
		}
	}
}
