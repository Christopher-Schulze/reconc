package shellcommand

import (
	"slices"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func powerShellInvocationWords(t *testing.T, command string) ([][]string, IncompleteReason) {
	t.Helper()
	translated := PowerShellAnalysisCommand(command)
	invocations, reason := InvocationsWithReason(translated, 16)
	words := make([][]string, 0, len(invocations))
	for _, invocation := range invocations {
		words = append(words, invocation.Words)
	}
	return words, reason
}

func containsInvocation(invocations [][]string, want []string) bool {
	return slices.ContainsFunc(invocations, func(words []string) bool { return slices.Equal(words, want) })
}

func TestShellCommandWordsPreserveLiteralValues(t *testing.T) {
	for _, word := range []string{
		"!", "[[", "]]", "{", "}", "case", "coproc", "do", "done", "elif", "else", "esac", "fi", "for", "function", "if", "in", "select", "then", "time", "until", "while",
		"ordinary", "space in name", "quote' ; npm publish; '", "$(npm publish)", "line\nbreak", `back\slash`,
	} {
		t.Run(word, func(t *testing.T) {
			command := shellCommandWord(word) + " literal"
			state := newParserState()
			program, err := state.parse(command, "command-word")
			if err != nil || len(program.Stmts) != 1 {
				t.Fatalf("%q rendered as %q: parse=%v error=%v", word, command, program, err)
			}
			call, ok := program.Stmts[0].Cmd.(*syntax.CallExpr)
			if !ok || len(call.Args) != 2 {
				t.Fatalf("%q changed command structure: %q", word, command)
			}
			value, literal := staticWordParts(call.Args[0].Parts)
			if !literal || value != word {
				t.Fatalf("%q rendered as %q: decoded=%q literal=%t", word, command, value, literal)
			}
		})
	}
}

func TestPowerShellAnalysisEnumeratesLiteralInvocations(t *testing.T) {
	tests := []struct {
		name, command string
		want          []string
	}{
		{"plain", "npm publish", []string{"npm", "publish"}},
		{"Windows suffix", "npm.cmd publish", []string{"npm", "publish"}},
		{"call operator", "& npm publish", []string{"npm", "publish"}},
		{"quoted Windows path", `& 'C:\Program Files\nodejs\npm.cmd' publish`, []string{"C:/Program Files/nodejs/npm", "publish"}},
		{"Start-Process list", "Start-Process npm -ArgumentList publish", []string{"npm", "publish"}},
		{"Start-Process array", "Start-Process -FilePath npm.exe -ArgumentList 'publish','--tag','next' -Wait", []string{"npm", "publish", "--tag", "next"}},
		{"Start-Process alias and string", `saps npm "publish --dry-run"`, []string{"npm", "publish", "--dry-run"}},
		{"Start-Process prefix parameters", "Start-Process -Fi npm -Arg publish -NoNew", []string{"npm", "publish"}},
		{"script block", "Get-ChildItem | ForEach-Object { npm publish }", []string{"npm", "publish"}},
		{"condition and block", "if (Test-Path package.json) { npm publish }", []string{"npm", "publish"}},
		{"assignment", "$result = npm publish", []string{"npm", "publish"}},
		{"subexpression in string", `Write-Output "done: $(npm publish)"`, []string{"npm", "publish"}},
		{"grouped argument", "Write-Output (npm publish)", []string{"npm", "publish"}},
		{"function body", "function Ship { npm publish }; Ship", []string{"npm", "publish"}},
		{"chain", "npm test && npm publish", []string{"npm", "publish"}},
		{"redirections", "npm publish 2>&1 > out.log", []string{"npm", "publish"}},
		{"smart quotes", "npm \u201cpublish\u201d", []string{"npm", "publish"}},
		{"backtick escapes", "n`p`m publish", []string{"npm", "publish"}},
		{"line continuation", "npm `\n  publish", []string{"npm", "publish"}},
		{"nested PowerShell host", `pwsh -NoProfile -Command "npm publish"`, []string{"npm", "publish"}},
		{"nested cmd", "cmd.exe /c npm publish", []string{"npm", "publish"}},
		{"nested bash", "bash -c 'npm publish'", []string{"npm", "publish"}},
		{"comment", "npm publish # ship it", []string{"npm", "publish"}},
		{"redirect target subexpression", `"x" > $(npm publish)`, []string{"npm", "publish"}},
		{"module-qualified call", `& 'Microsoft.PowerShell.Core\ForEach-Object' { npm publish }`, []string{"npm", "publish"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocations, reason := powerShellInvocationWords(t, test.command)
			if reason != IncompleteNone || !containsInvocation(invocations, test.want) {
				t.Fatalf("%q -> %q: invocations=%q reason=%q, want %q", test.command, PowerShellAnalysisCommand(test.command), invocations, reason, test.want)
			}
		})
	}
}

func TestPowerShellAnalysisReportsDynamicExecution(t *testing.T) {
	for _, command := range []string{
		`iex "npm publish"`,
		`Invoke-Expression 'npm publish'`,
		`INVOKE-EXPRESSION 'npm publish'`,
		`& $tool publish`,
		`& (Get-Command npm) publish`,
		`& "$env:ProgramFiles\nodejs\npm.cmd" publish`,
		`. $script`,
		`Invoke-Command -ScriptBlock { npm publish }`,
		`icm { npm publish }`,
		`powershell -EncodedCommand bgBwAG0AIABwAHUAYgBsAGkAcwBoAA==`,
		`pwsh -enc bgBwAG0A`,
		`pwsh -c $command`,
		`pwsh -Command -`,
		`[Diagnostics.Process]::Start('npm', 'publish')`,
		`$block.Invoke()`,
		`(New-Object -ComObject WScript.Shell).Run('npm publish')`,
		`'x'.GetType().Assembly.CreateInstance('Runner')`,
		`Microsoft.PowerShell.Utility\Invoke-Expression 'npm publish'`,
		`Set-Alias ship npm; ship publish`,
		`Start-Process $exe publish`,
		`Start-Process npm -Bogus value`,
		`cmd /c "npm publish & echo"`,
		`npm 'publish`,
		`npm publish )`,
		`Get-Item x < input.txt`,
	} {
		t.Run(command, func(t *testing.T) {
			if _, reason := powerShellInvocationWords(t, command); reason != IncompleteDynamicCommand {
				t.Fatalf("%q -> %q: reason=%q, want %q", command, PowerShellAnalysisCommand(command), reason, IncompleteDynamicCommand)
			}
		})
	}
}

func TestPowerShellAnalysisKeepsOrdinaryCommandsComplete(t *testing.T) {
	for _, command := range []string{
		`Get-ChildItem -Recurse | Where-Object { $_.Length -gt 1kb } | Select-Object -First 5`,
		`$files = Get-ChildItem *.go; foreach ($file in $files) { Write-Output $file.Name }`,
		`git status --short`,
		`go test ./... 2>&1 | Tee-Object -FilePath test.log`,
		`Set-Location src; npm test`,
		`if ($LASTEXITCODE -ne 0) { exit 1 }`,
		`Write-Output @'
npm publish
'@`,
		`Get-Content .\package.json | ConvertFrom-Json | Select-Object -ExpandProperty version`,
		`Get-Process | select Name, Id | sort Name`,
		`"total: $($items.Count) at $5"`,
		``,
	} {
		t.Run(command, func(t *testing.T) {
			if _, reason := powerShellInvocationWords(t, command); reason != IncompleteNone {
				t.Fatalf("%q -> %q: reason=%q, want complete analysis", command, PowerShellAnalysisCommand(command), reason)
			}
		})
	}
	// Literal text is data, not a command.
	invocations, _ := powerShellInvocationWords(t, `Write-Output 'npm publish'`)
	if containsInvocation(invocations, []string{"npm", "publish"}) {
		t.Fatalf("a quoted argument became an invocation: %q", invocations)
	}
}

func TestBashAnalysisDescendsIntoWindowsShellHosts(t *testing.T) {
	for _, test := range []struct {
		command string
		reason  IncompleteReason
	}{
		{`pwsh -c "npm publish"`, IncompleteNone},
		{`/usr/local/bin/pwsh -NoLogo -Command 'Start-Process npm -ArgumentList publish'`, IncompleteNone},
		{`powershell.exe "npm publish"`, IncompleteNone},
		{`cmd.exe /c npm publish`, IncompleteNone},
		{`pwsh -EncodedCommand abc`, IncompleteDynamicCommand},
		{`pwsh -c "iex 'npm publish'"`, IncompleteDynamicCommand},
		{`pwsh -Unknown x -c "npm publish"`, IncompleteDynamicCommand},
		{`cmd /c "npm publish & whoami"`, IncompleteDynamicCommand},
	} {
		t.Run(test.command, func(t *testing.T) {
			invocations, reason := InvocationsWithReason(test.command, 16)
			if reason != test.reason {
				t.Fatalf("reason=%q, want %q", reason, test.reason)
			}
			if test.reason == IncompleteNone && !slices.ContainsFunc(invocations, func(invocation Invocation) bool {
				return strings.Join(invocation.Words, " ") == "npm publish"
			}) {
				t.Fatalf("nested invocation missing: %#v", invocations)
			}
		})
	}
	if _, reason := InvocationsWithReason(`pwsh -File ./build.ps1 -Release`, 16); reason != IncompleteNone {
		t.Fatalf("a script file run was reported incomplete: %q", reason)
	}
}

// The translation is always valid Bash, so analysis never reports PowerShell
// input as unparsable Bash and the fail-closed reason stays dynamic_command.
func FuzzPowerShellAnalysisProducesParsableBash(f *testing.F) {
	for _, seed := range []string{
		"npm publish", "& 'npm.cmd' publish", "iex $x", "Start-Process npm -ArgumentList 'a','b'",
		"if ($x) { npm publish } else { $y = (git status) }", `Write-Output "a $(b) $c"`,
		"@'\nx\n'@", "@\"\n$(npm publish)\n\"@", "a | b && c || d; e &", "f 2>&1 *> $null",
		"[x]::y('z')", "$a.b.c()", "cmd /c x", "pwsh -c 'x'", "x `\n y", "<# c #> x # d",
		"$x = @{ a = { npm publish }; b = 1 }", "foreach ($i in 1..3) { & $i }",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, command string) {
		translated := PowerShellAnalysisCommand(command)
		if _, reason := InvocationsWithReason(translated, 16); reason == IncompleteUnparsable {
			t.Fatalf("%q translated to unparsable Bash %q", command, translated)
		}
	})
}
