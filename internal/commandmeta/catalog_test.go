package commandmeta

import (
	"reflect"
	"strings"
	"testing"
)

func TestCatalogIsCompleteAndInternallyConsistent(t *testing.T) {
	commands := All()
	if len(commands) == 0 {
		t.Fatal("command catalog is empty")
	}
	knownCategories := map[Category]bool{}
	usedCategories := map[Category]bool{}
	for _, category := range Categories() {
		if category.ID == "" || category.Title == "" || knownCategories[category.ID] {
			t.Fatalf("invalid or duplicate category: %+v", category)
		}
		knownCategories[category.ID] = true
	}
	seen := map[string]bool{}
	for _, command := range commands {
		if command.Name == "" || seen[command.Name] {
			t.Fatalf("empty or duplicate command name %q", command.Name)
		}
		seen[command.Name] = true
		usedCategories[command.Category] = true
		if !knownCategories[command.Category] {
			t.Fatalf("command %q uses unknown category %q", command.Name, command.Category)
		}
		validateSurface(t, command.Name, command.Synopsis, command.Summary, command.Stability, command.OutputModes, command.Flags, command.Arguments)
		nestedNames := map[string]bool{}
		for _, nested := range command.Subcommands {
			if nested.Name == "" || nestedNames[nested.Name] {
				t.Fatalf("command %q has empty or duplicate nested name %q", command.Name, nested.Name)
			}
			nestedNames[nested.Name] = true
			validateSurface(t, command.Name+" "+nested.Name, nested.Synopsis, nested.Summary, nested.Stability, nested.OutputModes, nested.Flags, nested.Arguments)
		}
	}
	for category := range knownCategories {
		if !usedCategories[category] {
			t.Fatalf("category %q has no commands", category)
		}
	}
}

func validateSurface(t *testing.T, name, synopsis, summary string, stability Stability, outputs []OutputMode, flags []Flag, arguments []Argument) {
	t.Helper()
	if strings.TrimSpace(synopsis) == "" || strings.TrimSpace(summary) == "" {
		t.Fatalf("%s must have synopsis and summary", name)
	}
	if stability != StabilityStable && stability != StabilityInternal {
		t.Fatalf("%s has invalid stability %q", name, stability)
	}
	if len(outputs) == 0 {
		t.Fatalf("%s has no output modes", name)
	}
	seenOutputs := map[OutputMode]bool{}
	for _, output := range outputs {
		if !validOutputMode(output) || seenOutputs[output] {
			t.Fatalf("%s has invalid or duplicate output mode %q", name, output)
		}
		seenOutputs[output] = true
	}
	seenFlags := map[string]bool{}
	for _, flag := range flags {
		if !strings.HasPrefix(flag.Name, "-") || seenFlags[flag.Name] {
			t.Fatalf("%s has invalid or duplicate flag %q", name, flag.Name)
		}
		seenFlags[flag.Name] = true
		if len(flag.Values) != 0 && flag.Value == "" {
			t.Fatalf("%s flag %q has values without a value operand", name, flag.Name)
		}
		seenValues := map[string]bool{}
		for _, value := range flag.Values {
			if value == "" || seenValues[value] {
				t.Fatalf("%s flag %q has empty or duplicate value %q", name, flag.Name, value)
			}
			seenValues[value] = true
		}
	}
	for _, argument := range arguments {
		if argument.Name == "" {
			t.Fatalf("%s has an unnamed argument", name)
		}
		seenValues := map[string]bool{}
		for _, value := range argument.Values {
			if value == "" || seenValues[value] {
				t.Fatalf("%s argument %q has empty or duplicate value %q", name, argument.Name, value)
			}
			seenValues[value] = true
		}
	}
}

func validOutputMode(mode OutputMode) bool {
	switch mode {
	case OutputText, OutputJSON, OutputYAML, OutputMarkdown, OutputJSONL, OutputScript, OutputRoff, OutputSARIF, OutputJUnit, OutputGitHub, OutputFile, OutputMCP:
		return true
	default:
		return false
	}
}

func TestMCPGatewayCatalogDeclaresProtocolOnlyOutput(t *testing.T) {
	command, ok := Lookup("mcp")
	if !ok || !reflect.DeepEqual(command.OutputModes, []OutputMode{OutputMCP}) ||
		len(command.Subcommands) != 1 || command.Subcommands[0].Name != "gateway" ||
		!reflect.DeepEqual(command.Subcommands[0].OutputModes, []OutputMode{OutputMCP}) {
		t.Fatalf("MCP gateway output contract = %#v, present=%t", command, ok)
	}
}

func TestCatalogReturnsDefensiveCopies(t *testing.T) {
	first := All()
	first[0].Name = "mutated"
	first[0].Flags[0].Values = append(first[0].Flags[0].Values, "mutated")
	second := All()
	if second[0].Name == "mutated" || reflect.DeepEqual(first, second) {
		t.Fatal("All returned mutable catalog storage")
	}

	command, ok := Lookup("bootstrap")
	if !ok {
		t.Fatal("bootstrap missing")
	}
	command.Subcommands[0].Name = "mutated"
	again, _ := Lookup("bootstrap")
	if again.Subcommands[0].Name == "mutated" {
		t.Fatal("Lookup returned mutable catalog storage")
	}
}

func TestPublicCatalogFiltersInternalSurfacesRecursively(t *testing.T) {
	commands := Public()
	for _, command := range commands {
		if command.Stability != StabilityStable {
			t.Fatalf("public catalog exposed internal command %q", command.Name)
		}
		for _, nested := range command.Subcommands {
			if nested.Stability != StabilityStable {
				t.Fatalf("public catalog exposed internal command %q", command.Name+" "+nested.Name)
			}
			for _, leaf := range nested.Subcommands {
				if leaf.Stability != StabilityStable {
					t.Fatalf("public catalog exposed internal command %q", command.Name+" "+nested.Name+" "+leaf.Name)
				}
			}
		}
	}
	hook, ok := publicCommand(commands, "hook")
	if !ok {
		t.Fatal("public catalog omitted hook")
	}
	for _, nested := range hook.Subcommands {
		if nested.Name == "runtime" || nested.Name == "grok-pre-tool-guard" {
			t.Fatalf("public catalog exposed internal hook route %q", nested.Name)
		}
	}
	hook.Subcommands[0].Name = "mutated"
	again, _ := publicCommand(Public(), "hook")
	if again.Subcommands[0].Name == "mutated" {
		t.Fatal("Public returned mutable catalog storage")
	}
}

func TestSortedNamesExposeDeterministicCatalogViews(t *testing.T) {
	all := SortedNames()
	public := PublicSortedNames()
	if len(all) == 0 || len(public) == 0 || !reflect.DeepEqual(all, public) {
		t.Fatalf("top-level catalog views drifted: all=%v public=%v", all, public)
	}
	for index := 1; index < len(all); index++ {
		if all[index-1] >= all[index] {
			t.Fatalf("catalog names are not strictly sorted: %v", all)
		}
	}
}

func publicCommand(commands []Command, name string) (Command, bool) {
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	return Command{}, false
}

func TestSuggestUsesBoundedDeterministicDistance(t *testing.T) {
	for input, want := range map[string]string{
		"chekc":          "check",
		"bootstra":       "bootstrap",
		"totally-remote": "",
		"":               "",
	} {
		if got := Suggest(input); got != want {
			t.Fatalf("Suggest(%q) = %q, want %q", input, got, want)
		}
	}
}
