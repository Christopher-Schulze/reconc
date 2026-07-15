package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reconc.dev/reconc/internal/completion"
	"reconc.dev/reconc/internal/manpage"
)

// runManpage emits a groff man(1) page for reconc to stdout.
// Content is generated from the same Subcommands table the shell
// completion uses; the page's date + version header reflect the
// current build.
//
// Install on a typical system:
//
//	reconc manpage | sudo tee /usr/local/share/man/man1/reconc.1
//	sudo mandb  # or `man -w reconc` to verify
func runManpage(args []string, version string, stdout io.Writer) error {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc manpage")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Emit a groff man(1) page on stdout.")
			fmt.Fprintln(stdout, "Install with:")
			fmt.Fprintln(stdout, "  reconc manpage | sudo tee /usr/local/share/man/man1/reconc.1")
			return nil
		}
		if len(a) > 0 && a[0] == '-' {
			return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc manpage: unknown flag %q", a)}
		}
	}
	return manpage.Render(stdout, version)
}

// runVersion prints the build version. Supports --json for agents.
// Also invoked via the `--version` / `-V` shortcuts so the two paths
// share one implementation.
func runVersion(args []string, version string, stdout io.Writer) error {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: reconc version [--json]")
			return nil
		}
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]string{
			"version":    version,
			"binary":     "reconc",
			"go_runtime": runtimeVersion(),
		})
	}
	fmt.Fprintf(stdout, "reconc %s\n", version)
	return nil
}

// runtimeVersion wraps runtime.Version for dependency-injection in
// tests. Defaults to the actual Go runtime version at build time.
var runtimeVersion = defaultRuntimeVersion

// runCompletion implements `reconc completion <shell>`.
//
// Emits a ready-to-source shell completion script on stdout for
// bash / zsh / fish. Script content is generated from the canonical
// Subcommands table so adding a subcommand requires only one table
// update to keep completion in sync.
func runCompletion(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &CLIError{ExitCode: 1, Message: "reconc completion: missing shell (bash | zsh | fish)"}
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, "Usage: reconc completion <bash|zsh|fish>")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Emits a shell completion script to stdout.")
			fmt.Fprintln(stdout, "Install:")
			fmt.Fprintln(stdout, "  bash:  reconc completion bash > /usr/local/etc/bash_completion.d/reconc")
			fmt.Fprintln(stdout, "  zsh:   reconc completion zsh  > /usr/local/share/zsh/site-functions/_reconc")
			fmt.Fprintln(stdout, "  fish:  reconc completion fish > ~/.config/fish/completions/reconc.fish")
			return nil
		}
	}
	shell := args[0]
	switch shell {
	case "bash":
		return completion.GenerateBash(stdout)
	case "zsh":
		return completion.GenerateZsh(stdout)
	case "fish":
		return completion.GenerateFish(stdout)
	default:
		return &CLIError{ExitCode: 1, Message: fmt.Sprintf("reconc completion: unknown shell %q (expected bash | zsh | fish)", shell)}
	}
}

// defaultRuntimeVersion is the Go runtime version this binary was
// compiled with. Exported indirection so tests can stub it.
func defaultRuntimeVersion() string {
	return goRuntimeVersion
}
