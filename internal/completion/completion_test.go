package completion

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/commandmeta"
)

func TestGenerateBashContainsSubcommands(t *testing.T) {
	var buf bytes.Buffer
	if err := GenerateBash(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"_reconc()", "complete -F _reconc reconc", "compile", "exec", "bootstrap", "audit", "hook"} {
		if !strings.Contains(out, want) {
			t.Errorf("bash completion missing %q", want)
		}
	}
}

func TestExecCompletionFlags(t *testing.T) {
	command, ok := commandmeta.Lookup("exec")
	if !ok {
		t.Fatal("exec completion command is not registered")
	}
	for _, want := range []string{"--staged", "--shell"} {
		if !containsFlag(command.Flags, want) {
			t.Fatalf("exec completion missing %q: %v", want, command.Flags)
		}
	}
}

func containsFlag(values []commandmeta.Flag, want string) bool {
	for _, value := range values {
		if value.Name == want {
			return true
		}
	}
	return false
}

func TestGenerateCompletionIncludesHookLifecycle(t *testing.T) {
	for name, generate := range map[string]func(io.Writer) error{
		"bash": GenerateBash,
		"zsh":  GenerateZsh,
		"fish": GenerateFish,
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := generate(&buf); err != nil {
				t.Fatal(err)
			}
			for _, command := range []string{"sync-scaffold", "uninstall"} {
				if !strings.Contains(buf.String(), command) {
					t.Fatalf("%s completion missing hook %s", name, command)
				}
			}
		})
	}
}

func TestGenerateCompletionIncludesTaskLifecycle(t *testing.T) {
	for name, generate := range map[string]func(io.Writer) error{
		"bash": GenerateBash,
		"zsh":  GenerateZsh,
		"fish": GenerateFish,
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := generate(&buf); err != nil {
				t.Fatal(err)
			}
			for _, command := range []string{"check-done", "new", "promote", "recover", "split"} {
				if !strings.Contains(buf.String(), command) {
					t.Fatalf("%s completion missing task %s", name, command)
				}
			}
			for _, flag := range []string{"no-next", "title", "id"} {
				needle := "--" + flag
				if name == "fish" {
					needle = "-l " + flag
				}
				if !strings.Contains(buf.String(), needle) {
					t.Fatalf("%s completion missing task flag %s", name, needle)
				}
			}
		})
	}
}

func TestGenerateCompletionIncludesRunControl(t *testing.T) {
	for name, generate := range map[string]func(io.Writer) error{
		"bash": GenerateBash,
		"zsh":  GenerateZsh,
		"fish": GenerateFish,
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := generate(&buf); err != nil {
				t.Fatal(err)
			}
			for _, command := range []string{"log", "off", "on", "reset", "status"} {
				if !strings.Contains(buf.String(), command) {
					t.Fatalf("%s completion missing run %s", name, command)
				}
			}
			for _, flag := range []string{"force", "verbose"} {
				needle := "--" + flag
				if name == "fish" {
					needle = "-l " + flag
				}
				if !strings.Contains(buf.String(), needle) {
					t.Fatalf("%s completion missing run flag %s", name, needle)
				}
			}
		})
	}
}

func TestGenerateCompletionIncludesBootstrapPhases(t *testing.T) {
	for name, generate := range map[string]func(io.Writer) error{
		"bash": GenerateBash,
		"zsh":  GenerateZsh,
		"fish": GenerateFish,
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := generate(&buf); err != nil {
				t.Fatal(err)
			}
			for _, command := range []string{"inspect", "plan", "apply", "verify", "profiles", "remove"} {
				if !strings.Contains(buf.String(), command) {
					t.Fatalf("%s completion missing bootstrap %s", name, command)
				}
			}
			for _, flag := range []string{"profile", "pack", "hook", "checksum", "replace-output", "accept-managed-blocks"} {
				needle := "--" + flag
				if name == "fish" {
					needle = "-l " + flag
				}
				if !strings.Contains(buf.String(), needle) {
					t.Fatalf("%s completion missing bootstrap flag %s", name, needle)
				}
			}
			for _, profile := range []string{"existing", "governed", "minimal"} {
				if !strings.Contains(buf.String(), profile) {
					t.Fatalf("%s completion missing bootstrap profile %s", name, profile)
				}
			}
		})
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
	for _, command := range commandmeta.All() {
		if !strings.Contains(out, `-a "`+command.Name+`"`) {
			t.Errorf("fish completion missing subcommand %q", command.Name)
		}
	}
}

func TestAllSubcommandsHaveHelp(t *testing.T) {
	for _, command := range commandmeta.All() {
		if command.Summary == "" {
			t.Errorf("subcommand %q has empty summary", command.Name)
		}
	}
}

func TestSubcommandNamesSortedForBash(t *testing.T) {
	names := commandmeta.SortedNames()
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("subcommand names must be sorted for bash completion; got %v", names)
		}
	}
}

func TestGeneratedCompletionsAreDeterministicAndCoverExactMetadataSurfaces(t *testing.T) {
	generators := []struct {
		name     string
		generate func(io.Writer) error
	}{
		{name: "bash", generate: GenerateBash},
		{name: "zsh", generate: GenerateZsh},
		{name: "fish", generate: GenerateFish},
	}
	outputs := map[string]string{}
	for _, generator := range generators {
		var first, second bytes.Buffer
		if err := generator.generate(&first); err != nil {
			t.Fatalf("generate %s: %v", generator.name, err)
		}
		if err := generator.generate(&second); err != nil {
			t.Fatalf("regenerate %s: %v", generator.name, err)
		}
		if first.String() != second.String() {
			t.Fatalf("%s completion is nondeterministic", generator.name)
		}
		outputs[generator.name] = first.String()
	}

	for _, command := range commandmeta.All() {
		assertBashFlags(t, outputs["bash"], command.Name, "", command.Flags)
		assertZshFlags(t, outputs["zsh"], command.Name, "", command.Flags)
		assertFishFlags(t, outputs["fish"], fishTestDirectCondition(command), command.Flags)
		for _, nested := range command.Subcommands {
			assertNestedCandidate(t, outputs, command, nested)
			fishCondition := fmt.Sprintf("__fish_seen_subcommand_from %s; and __fish_seen_subcommand_from %s", command.Name, nested.Name)
			if !strings.Contains(outputs["fish"], "-n "+strconv.Quote(fishCondition)+" ") && len(nested.Flags) != 0 {
				t.Errorf("fish completion omits nested surface %s %s", command.Name, nested.Name)
			}
			assertBashFlags(t, outputs["bash"], command.Name, nested.Name, nested.Flags)
			assertZshFlags(t, outputs["zsh"], command.Name, nested.Name, nested.Flags)
			assertFishFlags(t, outputs["fish"], fishCondition, nested.Flags)
			for _, leaf := range nested.Subcommands {
				assertLeafCompletion(t, outputs, command, nested, leaf)
			}
		}
	}
}

func assertLeafCompletion(
	t *testing.T,
	outputs map[string]string,
	command commandmeta.Command,
	nested commandmeta.Subcommand,
	leaf commandmeta.Subcommand,
) {
	t.Helper()
	parentKey := command.Name + ":" + nested.Name + ") values="
	for _, shell := range []string{"bash", "zsh"} {
		line := findGeneratedLine(outputs[shell], parentKey)
		if !strings.Contains(line, leaf.Name) {
			t.Errorf("%s completion omits %s %s %s", shell, command.Name, nested.Name, leaf.Name)
		}
	}
	surface := command.Name + ":" + nested.Name + ":" + leaf.Name + ") flags="
	for _, shell := range []string{"bash", "zsh"} {
		line := findGeneratedLine(outputs[shell], surface)
		for _, flag := range leaf.Flags {
			if !strings.Contains(line, flag.Name) {
				t.Errorf("%s completion omits %s for %s %s %s", shell, flag.Name, command.Name, nested.Name, leaf.Name)
			}
		}
	}
	fishCondition := fmt.Sprintf(
		"__fish_seen_subcommand_from %s; and __fish_seen_subcommand_from %s; and __fish_seen_subcommand_from %s",
		command.Name,
		nested.Name,
		leaf.Name,
	)
	assertFishFlags(t, outputs["fish"], fishCondition, leaf.Flags)
}

func assertNestedCandidate(t *testing.T, outputs map[string]string, command commandmeta.Command, nested commandmeta.Subcommand) {
	t.Helper()
	bashLine := findGeneratedLine(outputs["bash"], command.Name+") values=")
	bashValues := ""
	if start := strings.Index(bashLine, `values="`); start >= 0 {
		remaining := bashLine[start+len(`values="`):]
		if end := strings.Index(remaining, `"`); end >= 0 {
			bashValues = remaining[:end]
		}
	}
	if !containsString(strings.Fields(bashValues), nested.Name) {
		t.Errorf("bash completion omits %s %s", command.Name, nested.Name)
	}
	zshLine := findGeneratedLine(outputs["zsh"], command.Name+") values=(")
	if !strings.Contains(zshLine, strconv.Quote(nested.Name+":"+nested.Summary)) {
		t.Errorf("zsh completion omits %s %s", command.Name, nested.Name)
	}
	parentCondition := fishTestDirectCondition(command)
	wantFish := "-n " + strconv.Quote(parentCondition) + " -a " + strconv.Quote(nested.Name)
	if !strings.Contains(outputs["fish"], wantFish) {
		t.Errorf("fish completion omits %s %s", command.Name, nested.Name)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func findGeneratedLine(output, marker string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return "="
}

func TestGeneratedCompletionsPassInstalledShellSyntaxChecks(t *testing.T) {
	checks := []struct {
		name     string
		binary   string
		args     []string
		generate func(io.Writer) error
	}{
		{name: "bash", binary: "bash", args: []string{"-n"}, generate: GenerateBash},
		{name: "zsh", binary: "zsh", args: []string{"-n"}, generate: GenerateZsh},
		{name: "fish", binary: "fish", args: []string{"-n"}, generate: GenerateFish},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			path, err := exec.LookPath(check.binary)
			if err != nil {
				t.Skipf("%s is not installed", check.binary)
			}
			var script bytes.Buffer
			if err := check.generate(&script); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(path, check.args...)
			command.Stdin = strings.NewReader(script.String())
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s syntax check: %v\n%s", check.name, err, output)
			}
		})
	}
}

func assertBashFlags(t *testing.T, output, command, nested string, flags []commandmeta.Flag) {
	t.Helper()
	if len(flags) == 0 {
		return
	}
	key := command + ") flags="
	if nested != "" {
		key = command + ":" + nested + ") flags="
	}
	want := key + strconv.Quote(strings.Join(flagNames(flags), " "))
	if !strings.Contains(output, want) {
		t.Errorf("bash completion flags drift for %s %s: missing %q", command, nested, want)
	}
}

func assertZshFlags(t *testing.T, output, command, nested string, flags []commandmeta.Flag) {
	t.Helper()
	if len(flags) == 0 {
		return
	}
	key := command + ") flags=("
	if nested != "" {
		key = command + ":" + nested + ") flags=("
	}
	want := key + zshFlagArray(flags) + ")"
	if !strings.Contains(output, want) {
		t.Errorf("zsh completion flags drift for %s %s: missing %q", command, nested, want)
	}
}

func assertFishFlags(t *testing.T, output, condition string, flags []commandmeta.Flag) {
	t.Helper()
	for _, flag := range flags {
		option := "-l " + strings.TrimPrefix(flag.Name, "--")
		if strings.HasPrefix(flag.Name, "-") && !strings.HasPrefix(flag.Name, "--") {
			option = "-s " + strings.TrimPrefix(flag.Name, "-")
		}
		if !strings.Contains(output, "-n "+strconv.Quote(condition)+" "+option) {
			t.Errorf("fish completion omits %s under %s", flag.Name, condition)
		}
	}
}

func fishTestDirectCondition(command commandmeta.Command) string {
	if len(command.Subcommands) == 0 {
		return "__fish_seen_subcommand_from " + command.Name
	}
	names := make([]string, 0, len(command.Subcommands))
	for _, nested := range command.Subcommands {
		names = append(names, nested.Name)
	}
	return fmt.Sprintf("__fish_seen_subcommand_from %s; and not __fish_seen_subcommand_from %s", command.Name, strings.Join(names, " "))
}
