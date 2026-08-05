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
	sarifBody, _ := Render(FormatSARIF, model)
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
	cause := errors.New("open " + filepath.Join(repo, "policies", "rules.yml") + ": malformed; cache=/private/other/cache; windows=C:\\private\\cache")
	model := Operational("ci", "test", repo, Candidate{}, 2, cause)
	for _, format := range []Format{FormatSARIF, FormatJUnit} {
		body, err := Render(format, model)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte(repo)) || bytes.Contains(body, []byte("/private/other")) ||
			bytes.Contains(body, []byte(`C:\private`)) || !bytes.Contains(body, []byte("operational")) {
			t.Fatalf("%s operational report leaked or omitted detail:\n%s", format, body)
		}
	}
	junitBody, _ := Render(FormatJUnit, model)
	if !bytes.Contains(junitBody, []byte("<"+fixture.JUnit.OperationalElement)) {
		t.Fatalf("JUnit omitted operational element:\n%s", junitBody)
	}
	sarifBody, _ := Render(FormatSARIF, model)
	if !bytes.Contains(sarifBody, []byte(`"exitCode": 2`)) {
		t.Fatalf("SARIF lost exit code:\n%s", sarifBody)
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
