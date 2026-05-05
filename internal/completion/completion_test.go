package completion

import (
	"bytes"
	"strings"
	"testing"
)

func TestGenerateBashContainsSubcommands(t *testing.T) {
	var buf bytes.Buffer
	if err := GenerateBash(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"_reconc()", "complete -F _reconc reconc", "compile", "bootstrap", "audit"} {
		if !strings.Contains(out, want) {
			t.Errorf("bash completion missing %q", want)
		}
	}
}

func TestGenerateBashFlagsPerSubcommand(t *testing.T) {
	var buf bytes.Buffer
	_ = GenerateBash(&buf)
	out := buf.String()
	// compile has --strict-conflicts; make sure it shows up.
	if !strings.Contains(out, "--strict-conflicts") {
		t.Errorf("bash completion missing compile flag --strict-conflicts")
	}
	// check has --auto-claim.
	if !strings.Contains(out, "--auto-claim") {
		t.Errorf("bash completion missing --auto-claim flag")
	}
}

func TestGenerateZshContainsCompdef(t *testing.T) {
	var buf bytes.Buffer
	if err := GenerateZsh(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"#compdef reconc", "_describe", "audit:tail"} {
		if !strings.Contains(out, want) {
			// zsh descriptions use "name:description" format
			if want == "audit:tail" {
				if !strings.Contains(out, "audit:tail") && !strings.Contains(out, `"audit:tail`) {
					t.Errorf("zsh completion missing %q", want)
				}
				continue
			}
			t.Errorf("zsh completion missing %q", want)
		}
	}
}

func TestGenerateFishContainsCompletions(t *testing.T) {
	var buf bytes.Buffer
	if err := GenerateFish(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `complete -c reconc`) {
		t.Error("fish completion missing core directive")
	}
	// Should include every subcommand.
	for _, s := range Subcommands {
		if !strings.Contains(out, `-a "`+s.Name+`"`) {
			t.Errorf("fish completion missing subcommand %q", s.Name)
		}
	}
}

func TestAllSubcommandsHaveHelp(t *testing.T) {
	for _, s := range Subcommands {
		if s.Help == "" {
			t.Errorf("subcommand %q has empty Help", s.Name)
		}
	}
}

func TestSubcommandNamesSortedForBash(t *testing.T) {
	names := subcommandNames()
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("subcommand names must be sorted for bash completion; got %v", names)
		}
	}
}
