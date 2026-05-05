package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIFormattingHelpers(t *testing.T) {
	t.Run("first-rule-path", func(t *testing.T) {
		cases := []struct {
			name   string
			target map[string]interface{}
			want   string
		}{
			{name: "paths", target: map[string]interface{}{"paths": []interface{}{"src/**"}}, want: "src/**"},
			{name: "before-paths", target: map[string]interface{}{"before_paths": []interface{}{"docs/**"}}, want: "docs/**"},
			{name: "when-paths", target: map[string]interface{}{"when_paths": []interface{}{"tests/**"}}, want: "tests/**"},
			{name: "required-files", target: map[string]interface{}{"required_files": []interface{}{map[string]interface{}{"path": "docs/report.md"}}}, want: "docs/report.md"},
			{name: "evidence", target: map[string]interface{}{"evidence": []interface{}{map[string]interface{}{"file": "docs/evidence.md"}}}, want: "docs/evidence.md"},
			{name: "missing", target: map[string]interface{}{}, want: ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := firstRulePath(tc.target); got != tc.want {
					t.Fatalf("firstRulePath(%#v) = %q, want %q", tc.target, got, tc.want)
				}
			})
		}
	})

	t.Run("short12", func(t *testing.T) {
		if got := short12("1234567890123456"); got != "123456789012..." {
			t.Fatalf("short12(long) = %q", got)
		}
		if got := short12("1234"); got != "1234" {
			t.Fatalf("short12(short) = %q", got)
		}
	})

	t.Run("itoaCLI", func(t *testing.T) {
		cases := map[int]string{
			0:   "0",
			17:  "17",
			-42: "-42",
		}
		for in, want := range cases {
			if got := itoaCLI(in); got != want {
				t.Fatalf("itoaCLI(%d) = %q, want %q", in, got, want)
			}
		}
	})
}

func TestGitHelpersOnCleanAndDirtyRepo(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)

	clean, detail := gitIsClean(repo)
	if !clean || detail != "" {
		t.Fatalf("expected clean git repo, got clean=%v detail=%q", clean, detail)
	}
	out, err := runGitPorcelain(repo)
	if err != nil {
		t.Fatalf("runGitPorcelain(clean): %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty porcelain output, got %q", out)
	}

	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean, detail = gitIsClean(repo)
	if clean {
		t.Fatal("expected dirty git repo after untracked file")
	}
	if !strings.Contains(detail, "1 unstaged/untracked change(s)") {
		t.Fatalf("unexpected dirty detail: %q", detail)
	}
	out, err = runGitPorcelain(repo)
	if err != nil {
		t.Fatalf("runGitPorcelain(dirty): %v", err)
	}
	if !strings.Contains(out, "scratch.txt") {
		t.Fatalf("expected dirty file in porcelain output, got %q", out)
	}
}

func TestWatchHelpersCompileOnceAndMTime(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: r1\n    kind: deny_write\n    paths: ['gen/**']\n    mode: warn\n    message: m\n")

	sig1 := sourceMTimeSignature(repo)
	if !strings.Contains(sig1, "AGENTS.md=") || !strings.Contains(sig1, "policies/rules.yml=") {
		t.Fatalf("expected source signature to include known policy files, got %q", sig1)
	}

	rulesPath := filepath.Join(repo, "policies", "rules.yml")
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(rulesPath, now, now); err != nil {
		t.Fatalf("chtimes rules.yml: %v", err)
	}
	sig2 := sourceMTimeSignature(repo)
	if sig1 == sig2 {
		t.Fatal("expected source signature to change after mtime update")
	}

	var stdout, stderr bytes.Buffer
	compileOnce(&stdout, &stderr, repo, "0.4.0-test")
	if !strings.Contains(stdout.String(), "compiled 1 rules from") {
		t.Fatalf("expected compileOnce success output, got %q", stdout.String())
	}

	stdout.Reset()
	compileOnce(&stdout, &stderr, t.TempDir(), "0.4.0-test")
	if !strings.Contains(stdout.String(), "compile failed") {
		t.Fatalf("expected compileOnce failure output, got %q", stdout.String())
	}
}

func TestRunFixNextBranches(t *testing.T) {
	blockRepo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: generated output is locked\n")

	var stdout, stderr bytes.Buffer
	err := Run([]string{"fix", blockRepo, "--write", "gen/output.go", "--next"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected blocking fix --next exit 2, got err=%v code=%d", err, ExitCode(err))
	}
	if !strings.Contains(stdout.String(), "next: [blocking|") {
		t.Fatalf("expected compact next remediation output, got %q", stdout.String())
	}

	passRepo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: warn\n    message: generated output is locked\n")
	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"fix", passRepo, "--next", "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("fix --next --json pass repo: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON payload, got %v\n%s", err, stdout.String())
	}
	if payload["remediation_count"] != float64(0) {
		t.Fatalf("expected remediation_count 0, got %v", payload["remediation_count"])
	}
}

func TestRunNextAlias(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: generated output is locked\n")

	var stdout, stderr bytes.Buffer
	err := Run([]string{"next", repo, "--write", "gen/output.go"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected blocking next exit 2, got err=%v code=%d", err, ExitCode(err))
	}
	if !strings.Contains(stdout.String(), "next: [blocking|") {
		t.Fatalf("expected next alias to render compact remediation, got %q", stdout.String())
	}

	stdout.Reset()
	if err := Run([]string{"next", "--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("next --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc next") {
		t.Fatalf("expected next help, got %q", stdout.String())
	}
}

func TestRunFixTextAndFlagParsing(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: generated output is locked\n")

	var stdout, stderr bytes.Buffer
	err := Run([]string{
		"fix", repo,
		"--write", "gen/output.go",
		"--read", "docs/spec.md",
		"--command-success", "go test ./...",
		"--command-failure", "go vet ./...",
		"--claim", "ci-green",
	}, "0.4.0-test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected blocking fix exit 2, got err=%v code=%d", err, ExitCode(err))
	}
	if !strings.Contains(stdout.String(), "Fix plan:") || !strings.Contains(stdout.String(), "Remediations") {
		t.Fatalf("expected text fix plan, got %q", stdout.String())
	}
}

func TestRunFixHelpAndValueValidation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"fix", "--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("fix --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc fix") {
		t.Fatalf("expected fix help output, got %q", stdout.String())
	}

	cases := []struct {
		argv    []string
		wantSub string
	}{
		{argv: []string{"fix", "--read"}, wantSub: "--read requires a value"},
		{argv: []string{"fix", "--command-success"}, wantSub: "--command-success requires a value"},
		{argv: []string{"fix", "--output"}, wantSub: "--output requires a path"},
		{argv: []string{"fix", "--bogus"}, wantSub: "unknown flag"},
	}
	for _, tc := range cases {
		stdout.Reset()
		stderr.Reset()
		err := Run(tc.argv, "0.4.0-test", &stdout, &stderr)
		if err == nil {
			t.Fatalf("expected error for %v", tc.argv)
		}
		if !strings.Contains(err.Error(), tc.wantSub) {
			t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
		}
	}
}

func TestRunExplainReportFileMarkdownAndErrors(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: generated output is locked\n")

	var reportOut, stderr bytes.Buffer
	err := Run([]string{"check", repo, "--write", "gen/output.go", "--json"}, "0.4.0-test", &reportOut, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected blocking check to build report, got err=%v code=%d", err, ExitCode(err))
	}

	reportPath := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(reportPath, reportOut.Bytes(), 0o644); err != nil {
		t.Fatalf("write report file: %v", err)
	}

	var stdout bytes.Buffer
	if err := Run([]string{"explain", "--report-file", reportPath, "--format", "markdown"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("explain --report-file --format markdown: %v", err)
	}
	if !strings.Contains(stdout.String(), "# Policy Check Report") || !strings.Contains(stdout.String(), "deny-gen") {
		t.Fatalf("expected markdown report content, got %q", stdout.String())
	}

	badPath := filepath.Join(t.TempDir(), "bad-report.json")
	if err := os.WriteFile(badPath, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("write bad report: %v", err)
	}
	stdout.Reset()
	err = Run([]string{"explain", "--report-file", badPath}, "0.4.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected invalid report JSON to fail")
	}
	if !strings.Contains(err.Error(), "report file is not valid JSON") {
		t.Fatalf("unexpected explain error: %v", err)
	}
}

func TestRunExplainFreshTextAndFormatValidation(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: generated output is locked\n")

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"explain", repo, "--write", "gen/output.go"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("fresh explain text: %v", err)
	}
	if !strings.Contains(stdout.String(), "Decision:  block") {
		t.Fatalf("expected text explain output, got %q", stdout.String())
	}

	stdout.Reset()
	err := Run([]string{"explain", repo, "--format", "html"}, "0.4.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected invalid explain format to fail")
	}
	if !strings.Contains(err.Error(), "--format must be text or markdown") {
		t.Fatalf("unexpected explain format error: %v", err)
	}
}

func TestRunWatchHelpAndValidation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"watch", "--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("watch --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc watch") {
		t.Fatalf("expected watch help output, got %q", stdout.String())
	}

	cases := []struct {
		argv    []string
		wantSub string
	}{
		{argv: []string{"watch", "--interval-ms"}, wantSub: "--interval-ms requires an integer"},
		{argv: []string{"watch", "--interval-ms", "50"}, wantSub: "--interval-ms must be >= 100"},
		{argv: []string{"watch", "--bogus"}, wantSub: "unknown flag"},
		{argv: []string{"watch", t.TempDir()}, wantSub: "no reconc config found"},
	}
	for _, tc := range cases {
		stdout.Reset()
		stderr.Reset()
		err := Run(tc.argv, "0.4.0-test", &stdout, &stderr)
		if err == nil {
			t.Fatalf("expected watch error for %v", tc.argv)
		}
		if !strings.Contains(err.Error(), tc.wantSub) {
			t.Fatalf("watch error %q does not contain %q", err.Error(), tc.wantSub)
		}
	}
}

func TestRunExplainHelpAndValueValidation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"explain", "--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("explain --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc explain") {
		t.Fatalf("expected explain help output, got %q", stdout.String())
	}

	cases := []struct {
		argv    []string
		wantSub string
	}{
		{argv: []string{"explain", "--report-file"}, wantSub: "--report-file requires a path"},
		{argv: []string{"explain", "--format"}, wantSub: "--format requires a value"},
		{argv: []string{"explain", "--read"}, wantSub: "--read requires a value"},
		{argv: []string{"explain", "--bogus"}, wantSub: "unknown flag"},
	}
	for _, tc := range cases {
		stdout.Reset()
		stderr.Reset()
		err := Run(tc.argv, "0.4.0-test", &stdout, &stderr)
		if err == nil {
			t.Fatalf("expected error for %v", tc.argv)
		}
		if !strings.Contains(err.Error(), tc.wantSub) {
			t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
		}
	}
}

func TestRunCIAutoClaimAndBaseHeadJSON(t *testing.T) {
	repo := makeCheckRepo(t,
		"rules:\n  - id: ci-claim\n    kind: require_claim\n    when_paths: ['src/**']\n    claims: ['ci-green']\n    mode: block\n    message: ci claim required\n")
	initGitRepo(t, repo)
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "initial")

	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "src/main.go")

	var stdout, stderr bytes.Buffer
	err := Run([]string{"ci", repo, "--staged"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected staged ci without claim to block, got err=%v code=%d", err, ExitCode(err))
	}

	t.Setenv("CI", "true")
	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"ci", repo, "--staged", "--auto-claim", "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("ci --staged --auto-claim --json: %v", err)
	}
	var stagedPayload map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &stagedPayload); err != nil {
		t.Fatalf("expected staged ci JSON, got %v\n%s", err, stdout.String())
	}
	if _, ok := stagedPayload["git"]; !ok {
		t.Fatalf("expected git metadata in staged ci JSON: %#v", stagedPayload)
	}

	gitRun(t, repo, "commit", "-m", "src change")
	if err := os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "src/main.go")
	gitRun(t, repo, "commit", "-m", "head change")

	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"ci", repo, "--base", "HEAD~1", "--head", "HEAD", "--json", "--claim", "ci-green"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("ci --base/--head --json: %v", err)
	}
	var rangePayload map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &rangePayload); err != nil {
		t.Fatalf("expected range ci JSON, got %v\n%s", err, stdout.String())
	}
	report, ok := rangePayload["report"].(map[string]interface{})
	if !ok || report["decision"] == nil {
		t.Fatalf("expected embedded report in ci JSON: %#v", rangePayload)
	}
}

func TestRunCITextWithCommandFlags(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	initGitRepo(t, repo)
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "initial")

	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "src/main.go")

	var stdout, stderr bytes.Buffer
	if err := Run([]string{
		"ci", repo, "--staged",
		"--read", "docs/spec.md",
		"--command-success", "go test ./...",
		"--command-failure", "go vet ./...",
		"--claim", "ci-green",
	}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("ci text path: %v", err)
	}
	if !strings.Contains(stdout.String(), "Git:") || !strings.Contains(stdout.String(), "Decision:  pass") {
		t.Fatalf("expected text ci output, got %q", stdout.String())
	}
}

func TestRunCheckTerseAndCommandFailure(t *testing.T) {
	repo := makeCheckRepo(t, "rules: []\n")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{
		"check", repo,
		"--write", "src/main.go",
		"--command-failure", "go vet ./...",
		"--terse",
	}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("check --terse with command-failure: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected terse JSON, got %v\n%s", err, stdout.String())
	}
	if payload["decision"] != "pass" {
		t.Fatalf("expected terse decision pass, got %#v", payload)
	}
}

func TestRunAuditCommandsOnRecordedEntries(t *testing.T) {
	t.Setenv("RECONC_AUDIT", "1")
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: generated output is locked\n")

	var stdout, stderr bytes.Buffer
	err := Run([]string{"check", repo, "--write", "gen/output.go"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected blocking check to record audit entry, got err=%v code=%d", err, ExitCode(err))
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"audit", "tail", repo, "--compact", "-n", "1"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("audit tail --compact: %v", err)
	}
	if !strings.Contains(stdout.String(), "check") || !strings.Contains(stdout.String(), "deny-gen") {
		t.Fatalf("expected compact audit tail line, got %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"audit", "stats", repo, "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("audit stats --json: %v", err)
	}
	var stats map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &stats); err != nil {
		t.Fatalf("expected audit stats JSON, got %v\n%s", err, stdout.String())
	}
	if total, ok := stats["total_entries"].(float64); !ok || total < 1 {
		t.Fatalf("expected total_entries >= 1, got %#v", stats)
	}
}

func TestRunAuditStatsTextAndFlagValidation(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := strings.Join([]string{
		`{"ts":"2026-04-14T00:00:00Z","event":"check","decision":"block","blocking_count":1,"rule_ids":["r1"]}`,
		`{"ts":"2026-04-14T01:00:00Z","event":"ci","decision":"pass","blocking_count":0,"rule_ids":["r1"]}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "audit.jsonl"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"audit", "stats", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("audit stats text: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Audit stats (2 entries") || !strings.Contains(out, "Top rules:") || !strings.Contains(out, "By decision:") {
		t.Fatalf("expected rich audit stats text, got %q", out)
	}

	stdout.Reset()
	err := Run([]string{"audit", "stats", repo, "--bogus"}, "0.4.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected audit stats unknown flag to fail")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unexpected audit stats error: %v", err)
	}
}

func TestRunAuditTailFiltersAndMutualExclusion(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := strings.Join([]string{
		`{"ts":"2026-04-14T00:00:00Z","event":"check","decision":"block","ok":false,"blocking_count":1,"rule_ids":["r1"],"write_paths":["src/a.go"]}`,
		`{"ts":"2026-04-14T01:00:00Z","event":"ci","decision":"pass","ok":true,"blocking_count":0,"rule_ids":["r2"],"write_paths":["src/b.go"]}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "audit.jsonl"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"audit", "tail", repo, "--rule", "r1", "--decision", "block", "--since", "2026-04-13T00:00:00Z"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("audit tail with filters: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "check") || strings.Contains(out, "r2") {
		t.Fatalf("expected filtered audit tail output, got %q", out)
	}

	stdout.Reset()
	err := Run([]string{"audit", "tail", repo, "--json", "--compact"}, "0.4.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected audit tail --json --compact to fail")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("unexpected audit tail mutual exclusion error: %v", err)
	}
}

func TestBuildStartDataAndRenderStartMarkdown(t *testing.T) {
	t.Setenv("RECONC_AUDIT", "1")
	repo := makeCheckRepo(t,
		"rules:\n  - id: deny-gen\n    kind: deny_write\n    paths: ['gen/**']\n    mode: block\n    message: generated output is locked\n")

	var stdout, stderr bytes.Buffer
	err := Run([]string{"check", repo, "--write", "gen/output.go"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected blocking check before start-data build, got err=%v code=%d", err, ExitCode(err))
	}

	data := buildStartData(repo)
	if data["generated_at"] == "" {
		t.Fatalf("expected generated_at in start data: %#v", data)
	}
	recent, ok := data["recent_decisions"].([]string)
	if !ok || len(recent) == 0 {
		t.Fatalf("expected recent decisions in start data: %#v", data)
	}

	md := renderStartMarkdown(data)
	if !strings.Contains(md, "# Session Start") || !strings.Contains(md, "## Recent activity") || !strings.Contains(md, "Last 5 decisions:") {
		t.Fatalf("expected rendered start markdown to include key sections, got %q", md)
	}
}

func TestRunVerifyTextWithWarningsAndGlobalPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RECONC_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "global-policy.yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := makeCheckRepo(t, "rules: []\n")
	initGitRepo(t, repo)

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"verify", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("verify text: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "global policy") || !strings.Contains(out, "git pre-commit hook") || !strings.Contains(out, "not installed") {
		t.Fatalf("expected verify warning-rich text output, got %q", out)
	}

	stdout.Reset()
	err := Run([]string{"verify", repo, "--bogus"}, "0.4.0-test", &stdout, &stderr)
	if err == nil {
		t.Fatal("expected verify unknown flag to fail")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unexpected verify error: %v", err)
	}
}

func TestRunBootstrapHintsAndAgentInstall(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())

	t.Run("hints-when-tooling-missing", func(t *testing.T) {
		repo := t.TempDir()
		var stdout, stderr bytes.Buffer
		if err := Run([]string{"bootstrap", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
			t.Fatalf("bootstrap missing-tooling repo: %v", err)
		}
		out := stdout.String()
		if !strings.Contains(out, "no .git/ found") || !strings.Contains(out, "Claude Code: create .claude/") || !strings.Contains(out, "Codex: create .codex/") {
			t.Fatalf("expected bootstrap hints, got %q", out)
		}
	})

	t.Run("agent-hooks-installed-when-dirs-present", func(t *testing.T) {
		repo := t.TempDir()
		initGitRepo(t, repo)
		if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(repo, ".codex"), 0o755); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if err := Run([]string{"bootstrap", repo, "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
			t.Fatalf("bootstrap json with agent dirs: %v", err)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
			t.Fatalf("expected bootstrap JSON, got %v\n%s", err, stdout.String())
		}
		steps, ok := payload["steps"].([]interface{})
		if !ok {
			t.Fatalf("expected bootstrap steps in payload: %#v", payload)
		}
		stepText := make([]string, 0, len(steps))
		for _, step := range steps {
			if s, ok := step.(string); ok {
				stepText = append(stepText, s)
			}
		}
		joined := strings.Join(stepText, "\n")
		if !strings.Contains(joined, "hook install claude-code") || !strings.Contains(joined, "hook install codex") {
			t.Fatalf("expected agent hook install steps, got %q", joined)
		}
	})
}

func TestRunHookGenerateAndPresetListText(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "generate", "codex"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("hook generate codex: %v", err)
	}
	if !strings.Contains(stdout.String(), "codex-pre-tool-use") {
		t.Fatalf("expected raw codex hook content, got %q", stdout.String())
	}

	stdout.Reset()
	if err := Run([]string{"preset", "list"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("preset list text: %v", err)
	}
	if !strings.Contains(stdout.String(), "Bundled and user presets:") || !strings.Contains(stdout.String(), "extends: [<name>, ...]") {
		t.Fatalf("expected preset list text output, got %q", stdout.String())
	}
}

func TestRunHookAndPresetValidationPaths(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if err := Run([]string{"hook", "--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("hook --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc hook generate") {
		t.Fatalf("expected hook help output, got %q", stdout.String())
	}

	stdout.Reset()
	err := Run([]string{"hook", "install"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "missing kind") {
		t.Fatalf("expected hook install missing-kind error, got %v", err)
	}

	stdout.Reset()
	err = Run([]string{"hook", "claim", "/tmp/repo", "ci-green", "--bogus"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("expected hook claim unknown-flag error, got %v", err)
	}

	stdout.Reset()
	if err := Run([]string{"preset", "--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("preset --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc preset list") {
		t.Fatalf("expected preset help output, got %q", stdout.String())
	}

	stdout.Reset()
	err = Run([]string{"preset", "bogus"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("expected preset unknown-subcommand error, got %v", err)
	}
}

func TestRunWhyComplexLockfileAndValidation(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".reconc"), 0o755); err != nil {
		t.Fatal(err)
	}
	lockfile := `{
  "rules": [
    {
      "id": "complex",
      "kind": "all_of",
      "message": "line one\nline two",
      "source_path": "policies/rules.yml",
      "source_block_id": "AGENTS.md:12",
      "required_files": [{"path": "docs/report.md", "max_age_hours": 24}],
      "evidence": [{"file": "docs/evidence.md"}],
      "checks": [{"kind": "require_evidence"}],
      "script": "scripts/check.sh"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(repo, ".reconc", "policy.lock.json"), []byte(lockfile), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"why", "complex", repo}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("why complex: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Mode:    (default)") || !strings.Contains(out, "required_files:") || !strings.Contains(out, "evidence:") || !strings.Contains(out, "checks (1 sub-checks):") || !strings.Contains(out, "Script:  scripts/check.sh") {
		t.Fatalf("expected rich why output, got %q", out)
	}

	stdout.Reset()
	err := Run([]string{"why", "complex", repo, "--json", "--terse"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected why mutually-exclusive error, got %v", err)
	}
}

func TestRunHookGenerateJSONAndPresetListValidation(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "generate", "codex", "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("hook generate codex --json: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("expected hook generate JSON, got %v\n%s", err, stdout.String())
	}
	if payload["kind"] != "codex" {
		t.Fatalf("unexpected hook generate payload: %#v", payload)
	}

	stdout.Reset()
	if err := Run([]string{"preset", "list", "--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("preset list --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc preset list") {
		t.Fatalf("expected preset list help output, got %q", stdout.String())
	}

	stdout.Reset()
	err := Run([]string{"preset", "list", "--bogus"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("expected preset list unknown-flag error, got %v", err)
	}
}

func TestRunHookValidationExtras(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Run([]string{"hook", "generate"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "missing kind") {
		t.Fatalf("expected hook generate missing-kind error, got %v", err)
	}

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := Run([]string{"hook", "install", "git-pre-commit", repo, "--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("hook install --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc hook install") {
		t.Fatalf("expected hook install help output, got %q", stdout.String())
	}

	stdout.Reset()
	err = Run([]string{"hook", "install", "git-pre-commit", repo, "--bogus"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("expected hook install unknown-flag error, got %v", err)
	}

	stdout.Reset()
	err = Run([]string{"hook", "claim", repo, "ci-green", "--session"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--session requires a value") {
		t.Fatalf("expected hook claim session-value error, got %v", err)
	}

	stdout.Reset()
	if err := Run([]string{"hook", "runtime", "--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("hook runtime --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc hook runtime") {
		t.Fatalf("expected hook runtime help output, got %q", stdout.String())
	}

	t.Setenv("RECONC_HOME", t.TempDir())
	repo2 := t.TempDir()
	stdout.Reset()
	if err := Run([]string{"hook", "claim", repo2, "ci-green", "--session", "session-1"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("hook claim text path: %v", err)
	}
	if !strings.Contains(stdout.String(), "claim 'ci-green' recorded for session session-1") {
		t.Fatalf("expected hook claim text output, got %q", stdout.String())
	}

	stdout.Reset()
	if err := Run([]string{"hook", "claim", "--help"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("hook claim --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: reconc hook claim") {
		t.Fatalf("expected hook claim help output, got %q", stdout.String())
	}

	stdout.Reset()
	err = Run([]string{"hook", "claim", repo2}, "0.4.0-test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "usage: reconc hook claim") {
		t.Fatalf("expected hook claim usage error, got %v", err)
	}

	stdout.Reset()
	err = Run([]string{"hook", "claim", repo2, "ci-green", "extra"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "too many positional arguments") {
		t.Fatalf("expected hook claim too-many-positionals error, got %v", err)
	}

	t.Setenv("RECONC_HOME", t.TempDir())
	repo3 := t.TempDir()
	stdout.Reset()
	err = Run([]string{"hook", "claim", repo3, "ci-green"}, "0.4.0-test", &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no active reconc session") {
		t.Fatalf("expected hook claim no-active-session error, got %v", err)
	}
}

func TestRunHookInstallAndPresetShowJSON(t *testing.T) {
	t.Setenv("RECONC_HOME", t.TempDir())
	repo := t.TempDir()
	initGitRepo(t, repo)

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"hook", "install", "git-pre-commit", repo, "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("hook install git-pre-commit --json: %v", err)
	}
	var install map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &install); err != nil {
		t.Fatalf("expected hook install JSON, got %v\n%s", err, stdout.String())
	}
	if install["kind"] != "git-pre-commit" {
		t.Fatalf("unexpected hook install payload: %#v", install)
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run([]string{"preset", "show", "default", "--json"}, "0.4.0-test", &stdout, &stderr); err != nil {
		t.Fatalf("preset show default --json: %v", err)
	}
	var preset map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &preset); err != nil {
		t.Fatalf("expected preset show JSON, got %v\n%s", err, stdout.String())
	}
	if preset["name"] != "default" || preset["content"] == "" {
		t.Fatalf("unexpected preset JSON payload: %#v", preset)
	}
}

func gitRun(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
