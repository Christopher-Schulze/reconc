package cli

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNextArgValue(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		syntax    argValueSyntax
		want      string
		wantOK    bool
		wantIndex int
	}{
		{name: "ordinary value", args: []string{"--output", "report.json"}, syntax: argValueNoLeadingDash, want: "report.json", wantOK: true, wantIndex: 1},
		{name: "missing value", args: []string{"--output"}, syntax: argValueNoLeadingDash, wantIndex: 0},
		{name: "following option", args: []string{"--output", "--json"}, syntax: argValueNoLeadingDash, wantIndex: 0},
		{name: "unescaped leading dash", args: []string{"--command", "-version"}, syntax: argValueLeadingDashAfterSeparator, wantIndex: 0},
		{name: "escaped leading dash", args: []string{"--command", "--", "-version"}, syntax: argValueLeadingDashAfterSeparator, want: "-version", wantOK: true, wantIndex: 2},
		{name: "missing escaped value", args: []string{"--command", "--"}, syntax: argValueLeadingDashAfterSeparator, wantIndex: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := 0
			got, ok := nextArgValue(test.args, &index, test.args[0], test.syntax)
			if got != test.want || ok != test.wantOK || index != test.wantIndex {
				t.Fatalf("nextArgValue() = (%q, %t), index %d; want (%q, %t), index %d", got, ok, index, test.want, test.wantOK, test.wantIndex)
			}
		})
	}
}

func TestNextArgValueCallSiteContracts(t *testing.T) {
	tests := []struct {
		file                 string
		wantOrdinary         int
		wantLeadingDashValue int
	}{
		{file: "action_log_cmd.go", wantOrdinary: 1},
		{file: "bootstrap_cmd.go", wantOrdinary: 10},
		{file: "catalog_cmd.go", wantOrdinary: 2},
		{file: "check_options_cmd.go", wantOrdinary: 1, wantLeadingDashValue: 1},
		{file: "ci_options_cmd.go", wantOrdinary: 1, wantLeadingDashValue: 1},
		{file: "compile_cmd.go", wantOrdinary: 1},
		{file: "evaluate_cmd.go", wantOrdinary: 3, wantLeadingDashValue: 12},
		{file: "explain_cmd.go", wantOrdinary: 3, wantLeadingDashValue: 4},
		{file: "grok_cmd.go", wantOrdinary: 4},
		{file: "hook_claim_cmd.go", wantOrdinary: 1},
		{file: "hook_lifecycle_cmd.go", wantOrdinary: 3},
		{file: "impact_options_cmd.go", wantOrdinary: 2, wantLeadingDashValue: 1},
		{file: "inspect_cmd.go", wantOrdinary: 3},
		{file: "install_cli_cmd.go", wantOrdinary: 1},
		{file: "lifecycle_cmd.go", wantOrdinary: 3},
		{file: "mcp_gateway_cmd.go", wantOrdinary: 1},
		{file: "policy_author_options_cmd.go", wantOrdinary: 1},
		{file: "proof_cmd.go", wantOrdinary: 2},
		{file: "proof_verify_cmd.go", wantOrdinary: 1},
		{file: "repo_cmd.go", wantOrdinary: 4},
		{file: "scaffold_cmd.go", wantOrdinary: 5},
		{file: "workflow_cmd.go", wantOrdinary: 1},
	}
	ordinaryTotal := 0
	leadingDashTotal := 0
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			ordinary, leadingDash := nextArgValueCallSiteCounts(t, test.file)
			if ordinary != test.wantOrdinary || leadingDash != test.wantLeadingDashValue {
				t.Fatalf("contracts = ordinary %d, leading-dash %d; want ordinary %d, leading-dash %d", ordinary, leadingDash, test.wantOrdinary, test.wantLeadingDashValue)
			}
			ordinaryTotal += ordinary
			leadingDashTotal += leadingDash
		})
	}
	if ordinaryTotal != 54 || leadingDashTotal != 19 {
		t.Fatalf("contract totals = ordinary %d, leading-dash %d; want ordinary 54, leading-dash 19", ordinaryTotal, leadingDashTotal)
	}
}

func nextArgValueCallSiteCounts(t *testing.T, filename string) (int, int) {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filename, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := 0
	leadingDash := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		function, ok := call.Fun.(*ast.Ident)
		if !ok || function.Name != "nextArgValue" {
			return true
		}
		if len(call.Args) != 4 {
			t.Errorf("nextArgValue call at %s has %d arguments; want 4", fileSet.Position(call.Pos()), len(call.Args))
			return true
		}
		syntax, ok := call.Args[3].(*ast.Ident)
		if !ok {
			t.Errorf("nextArgValue call in %s has a non-constant syntax", filename)
			return true
		}
		switch syntax.Name {
		case "argValueNoLeadingDash":
			ordinary++
		case "argValueLeadingDashAfterSeparator":
			leadingDash++
		default:
			t.Errorf("nextArgValue call in %s uses unknown syntax %s", filename, syntax.Name)
		}
		return true
	})
	return ordinary, leadingDash
}

func TestTeeToFilePublishesCompleteOutputAtomically(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "nested", "report.txt")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, []byte("previous output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	out, closeOutput, err := teeToFile(&stdout, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("complete output\n")); err != nil {
		t.Fatal(err)
	}
	if err := closeOutput(true); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "complete output\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	body, err := os.ReadFile(outputPath)
	if err != nil || string(body) != "complete output\n" {
		t.Fatalf("published output = %q err=%v", body, err)
	}
	after, err := os.Lstat(outputPath)
	if err != nil || runtime.GOOS != "windows" && os.SameFile(before, after) {
		t.Fatalf("output was not atomically replaced: before=%v after=%v err=%v", before, after, err)
	}
}

func TestTeeToFilePreservesExistingOutputWhenRenderingFails(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(outputPath, []byte("previous output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	out, closeOutput, err := teeToFile(&stdout, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("partial output")); err != nil {
		t.Fatal(err)
	}
	resultErr := errors.New("render failed")
	joinOutputCloseError(&resultErr, closeOutput)
	if resultErr == nil || resultErr.Error() != "render failed" {
		t.Fatalf("render result = %v", resultErr)
	}
	body, err := os.ReadFile(outputPath)
	if err != nil || string(body) != "previous output\n" {
		t.Fatalf("existing output = %q err=%v", body, err)
	}
}

func TestTeeToFilePreservesExistingOutputWhenStdoutFails(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(outputPath, []byte("previous output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, closeOutput, err := teeToFile(failingOutputWriter{}, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("unavailable output")); err == nil {
		t.Fatal("failing stdout unexpectedly accepted output")
	}
	if err := closeOutput(true); err == nil {
		t.Fatal("failed stdout unexpectedly published output")
	}
	body, err := os.ReadFile(outputPath)
	if err != nil || string(body) != "previous output\n" {
		t.Fatalf("existing output after stdout failure = %q err=%v", body, err)
	}
}

func TestTeeToFileRejectsSymlinkWithoutChangingReferent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires optional Windows privileges")
	}
	directory := t.TempDir()
	referent := filepath.Join(directory, "referent.txt")
	outputPath := filepath.Join(directory, "report.txt")
	if err := os.WriteFile(referent, []byte("protected output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(referent, outputPath); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	out, closeOutput, err := teeToFile(&stdout, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("replacement output\n")); err != nil {
		t.Fatal(err)
	}
	if err := closeOutput(true); err == nil {
		t.Fatal("symlink output unexpectedly published")
	}
	body, err := os.ReadFile(referent)
	if err != nil || string(body) != "protected output\n" {
		t.Fatalf("symlink referent = %q err=%v", body, err)
	}
	info, err := os.Lstat(outputPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("output symlink = %v err=%v", info, err)
	}
}

func TestTeeToFileRejectsDirectoryWithoutChangingIt(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "report")
	if err := os.Mkdir(outputPath, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	out, closeOutput, err := teeToFile(&stdout, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("replacement output\n")); err != nil {
		t.Fatal(err)
	}
	if err := closeOutput(true); err == nil {
		t.Fatal("directory output unexpectedly published")
	}
	info, err := os.Stat(outputPath)
	if err != nil || !info.IsDir() {
		t.Fatalf("output directory = %v err=%v", info, err)
	}
}
