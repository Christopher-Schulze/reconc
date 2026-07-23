// Package commandmeta owns Reconc's dependency-neutral public CLI contract.
package commandmeta

import (
	"sort"
	"strings"
)

type Category string

const (
	CategoryDaily       Category = "daily"
	CategoryBootstrap   Category = "bootstrap-inspection"
	CategoryCompile     Category = "compile-evaluate"
	CategoryExplain     Category = "explain-remediate"
	CategoryWiring      Category = "packs-wiring"
	CategoryMaintenance Category = "workflow-maintenance"
	CategoryMeta        Category = "meta"
)

type Stability string

const (
	StabilityStable   Stability = "stable"
	StabilityInternal Stability = "internal"
)

type OutputMode string

const (
	OutputText     OutputMode = "text"
	OutputJSON     OutputMode = "json"
	OutputYAML     OutputMode = "yaml"
	OutputMarkdown OutputMode = "markdown"
	OutputJSONL    OutputMode = "jsonl"
	OutputScript   OutputMode = "script"
	OutputRoff     OutputMode = "roff"
	OutputFile     OutputMode = "file"
)

type Flag struct {
	Name          string
	Value         string
	Values        []string
	Repeatable    bool
	Compatibility bool
}

type Argument struct {
	Name   string
	Values []string
}

type Subcommand struct {
	Name        string
	Synopsis    string
	Summary     string
	Flags       []Flag
	Arguments   []Argument
	Stability   Stability
	OutputModes []OutputMode
}

type Command struct {
	Name        string
	Category    Category
	Synopsis    string
	Summary     string
	Flags       []Flag
	Arguments   []Argument
	Subcommands []Subcommand
	Stability   Stability
	OutputModes []OutputMode
}

type CategoryInfo struct {
	ID    Category
	Title string
}

var categoryCatalog = []CategoryInfo{
	{ID: CategoryDaily, Title: "Daily"},
	{ID: CategoryBootstrap, Title: "Bootstrap & inspection"},
	{ID: CategoryCompile, Title: "Compile & evaluate"},
	{ID: CategoryExplain, Title: "Explain & remediate"},
	{ID: CategoryWiring, Title: "Packs & wiring"},
	{ID: CategoryMaintenance, Title: "Workflow maintenance"},
	{ID: CategoryMeta, Title: "Meta"},
}

var hookKinds = []string{"antigravity", "claude-code", "codex", "cursor", "devin-cli", "git-pre-commit", "github-copilot", "grok", "kilo", "opencode"}
var bootstrapProfiles = []string{"existing", "governed", "minimal"}

var commandCatalog = []Command{
	command("demo", CategoryDaily, "reconc demo [--keep] [--json]", "run the isolated real-policy product journey", flags(f("--keep", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("status", CategoryDaily, "reconc status [repo] [--json] [--output PATH]", "one-line policy health summary", flags(f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("check", CategoryDaily, "reconc check [repo] [evidence flags]", "evaluate runtime evidence against compiled policy", evidenceFlags(true, true), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("next", CategoryDaily, "reconc next [repo] [evidence flags]", "show the next remediation", flags(f("--read", "PATH"), f("--write", "PATH"), f("--command", "CMD"), f("--command-success", "CMD"), f("--command-failure", "CMD"), f("--claim", "NAME"), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("done", CategoryDaily, "reconc done [repo] [--require-clean-git] [--json]", "evidence-complete task-finish gate", flags(compat("--window", "N"), f("--require-clean-git", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("proof", CategoryDaily, "reconc proof [repo] [--format json|markdown] [--output PATH]", "export a portable completion proof bundle", flags(f("--format", "FORMAT", "json", "markdown"), f("--output", "PATH")), nil, modes(OutputJSON, OutputMarkdown, OutputFile)),

	command("bootstrap", CategoryBootstrap, "reconc bootstrap [repo] | <subcommand>", "inspect, plan, apply, verify, or remove repository onboarding", flags(repeat("--preset", "NAME"), f("--skip-git-hook", ""), f("--skip-agent-hooks", ""), f("--accept-managed-blocks", ""), f("--json", "")), []Subcommand{
		sub("profiles", "reconc bootstrap profiles [--json]", "list explicit bootstrap profiles", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("inspect", "reconc bootstrap inspect [repo] [--json]", "inspect repository bootstrap inputs without mutation", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("plan", "reconc bootstrap plan [repo] --profile PROFILE [selection flags]", "build a deterministic bootstrap manifest", bootstrapSelectionFlags(true), nil, modes(OutputText, OutputJSON, OutputFile)),
		sub("apply", "reconc bootstrap apply --plan PATH | [repo] --profile PROFILE [selection flags]", "apply an exact plan or explicit selection transaction", append(flags(f("--plan", "PATH")), bootstrapSelectionFlags(false)...), nil, modes(OutputText, OutputJSON)),
		sub("remove", "reconc bootstrap remove --plan PATH [--json]", "reverse one receipt-owned bootstrap transaction", flags(f("--plan", "PATH"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("verify", "reconc bootstrap verify --plan PATH [--json]", "verify an applied bootstrap manifest read-only", flags(f("--plan", "PATH"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON, OutputFile)),
	command("init", CategoryBootstrap, "reconc init [repo] [--preset NAME] [--force] [--json] [--output PATH]", "scaffold policy and agent instructions", flags(repeat("--preset", "NAME"), f("--force", ""), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("adopt", CategoryBootstrap, "reconc adopt [repo] [--yaml | --json | --apply]", "detect tooling and suggest rules", flags(f("--yaml", ""), f("--json", ""), f("--apply", "")), nil, modes(OutputText, OutputYAML, OutputJSON)),
	command("extract", CategoryBootstrap, "reconc extract [repo] [--from PATH] [--yaml | --json]", "scan instruction prose for rule hints", flags(f("--from", "PATH"), f("--yaml", ""), f("--json", "")), nil, modes(OutputText, OutputYAML, OutputJSON)),
	command("doctor", CategoryBootstrap, "reconc doctor [repo] [--deep] [--json] [--output PATH]", "inspect discovery and validation state", flags(f("--deep", ""), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("verify", CategoryBootstrap, "reconc verify [repo] [--json]", "run the end-to-end installation health check", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),

	command("compile", CategoryCompile, "reconc compile [repo] [--strict-conflicts] [--json] [--output PATH]", "compile the policy lockfile", flags(f("--json", ""), f("--strict-conflicts", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("refresh", CategoryCompile, "reconc refresh [repo] [--strict-conflicts] [--json] [--output PATH]", "explicitly refresh the policy lockfile", flags(f("--json", ""), f("--strict-conflicts", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("ci", CategoryCompile, "reconc ci [repo] (--staged | --base REF [--head REF]) [evidence flags]", "evaluate Git-derived changes under policy", flags(f("--staged", ""), f("--base", "REF"), f("--head", "REF"), f("--read", "PATH"), f("--command", "CMD"), f("--command-success", "CMD"), f("--command-failure", "CMD"), f("--claim", "NAME"), f("--auto-claim", ""), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("exec", CategoryCompile, "reconc exec [repo] [--staged] [--shell] -- COMMAND [ARG ...]", "execute and record real command evidence", flags(f("--staged", ""), f("--shell", "")), nil, modes(OutputText)),
	command("assert", CategoryCompile, "reconc assert <rule-id> [repo] [evidence flags]", "evaluate one rule by id", flags(f("--var", "KEY=VALUE"), f("--read", "PATH"), f("--write", "PATH"), f("--command", "CMD"), f("--command-success", "CMD"), f("--command-failure", "CMD"), f("--claim", "NAME"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("can", CategoryCompile, "reconc can write <path> [repo] [--why] [--json]", "return an ultra-terse yes/no policy decision", flags(f("--why", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("diff", CategoryCompile, "reconc diff <lockfile-a> <lockfile-b> [--json]", "compare two compiled lockfiles", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("watch", CategoryCompile, "reconc watch [repo] [--interval-ms N]", "watch policy sources and recompile", flags(f("--interval-ms", "N")), nil, modes(OutputText)),

	command("explain", CategoryExplain, "reconc explain [repo] [evidence flags] | --report-file PATH", "render a check report as text or Markdown", flags(f("--read", "PATH"), f("--write", "PATH"), f("--command", "CMD"), f("--claim", "NAME"), f("--format", "FORMAT", "text", "markdown"), f("--report-file", "PATH"), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputMarkdown, OutputJSON, OutputFile)),
	command("fix", CategoryExplain, "reconc fix [repo] [evidence flags] [--next]", "build a structured remediation plan", flags(f("--read", "PATH"), f("--write", "PATH"), f("--command", "CMD"), f("--command-success", "CMD"), f("--command-failure", "CMD"), f("--claim", "NAME"), f("--json", ""), f("--next", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("why", CategoryExplain, "reconc why <rule-id|mcp> [repo] [--terse] [--json]", "print one compiled rule or the MCP side-effect contract", flags(f("--json", ""), f("--terse", "")), nil, modes(OutputText, OutputJSON)),

	command("preset", CategoryWiring, "reconc preset <list|show>", "list or show bundled and user presets", nil, []Subcommand{
		sub("list", "reconc preset list [--json] [--output PATH]", "list bundled and user presets", flags(f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
		sub("show", "reconc preset show <name> [--json] [--output PATH]", "show one resolved preset", flags(f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	}, modes(OutputText, OutputJSON, OutputFile)),
	command("template", CategoryWiring, "reconc template <list|show>", "list or show bundled and user rule templates", nil, []Subcommand{
		sub("list", "reconc template list [--json]", "list rule templates", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("show", "reconc template show <name> [--json]", "show one resolved rule template", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON)),
	command("hook", CategoryWiring, "reconc hook <subcommand>", "manage, inspect, or execute agent runtime hooks", nil, []Subcommand{
		subArgs("generate", "reconc hook generate <kind> [--json] [--output PATH]", "print one hook artifact", flags(f("--json", ""), f("--output", "PATH")), []Argument{{Name: "kind", Values: hookKinds}}, modes(OutputText, OutputJSON, OutputFile)),
		subArgs("install", "reconc hook install <kind> [repo] [--force] [--json] [--output PATH]", "install generated hooks into a repository", flags(f("--force", ""), f("--json", ""), f("--output", "PATH")), []Argument{{Name: "kind", Values: hookKinds}}, modes(OutputText, OutputJSON, OutputFile)),
		subArgs("uninstall", "reconc hook uninstall <kind> [repo] [--json] [--output PATH]", "remove one Reconc-managed hook safely", flags(f("--json", ""), f("--output", "PATH")), []Argument{{Name: "kind", Values: hookKinds}}, modes(OutputText, OutputJSON, OutputFile)),
		sub("status", "reconc hook status [repo] [--json]", "inspect registered hook installation and liveness", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("sync-scaffold", "reconc hook sync-scaffold <repo-root-scaffold> [--json]", "synchronize generated scaffold hook artifacts", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		internalSub("runtime", "reconc hook runtime <event> <repo>", "dispatch one registry-owned runtime event"),
		internalSub("grok-pre-tool-guard", "reconc hook grok-pre-tool-guard <repo>", "run the internal fail-closed Grok pre-tool guard"),
		sub("claim", "reconc hook claim <repo> <claim-name> [--session ID] [--json] [--output PATH]", "record one explicit session claim", flags(f("--session", "ID"), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	}, modes(OutputText, OutputJSON, OutputFile)),
	command("grok", CategoryWiring, "reconc grok [repo] [flags] --prompt TEXT", "run the strict Grok ACP continuation driver", flags(f("--prompt", "TEXT"), f("--model", "ID"), f("--grok-binary", "PATH"), f("--max-continuations", "N")), nil, modes(OutputText)),

	command("changelog", CategoryMaintenance, "reconc changelog <rotate|list-archives>", "rotate or inspect changelog archives", nil, []Subcommand{
		sub("rotate", "reconc changelog rotate [repo] [--force] [--lines N] [--json]", "rotate older changelog sections", flags(f("--force", ""), f("--lines", "N"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("list-archives", "reconc changelog list-archives [repo] [--json]", "list changelog archives", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON)),
	command("agent-intro", CategoryMaintenance, "reconc agent-intro [--section NAME | --list-sections] [--json]", "print the embedded agent integration guide", flags(f("--section", "NAME"), f("--list-sections", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("audit", CategoryMaintenance, "reconc audit <tail|stats|export>", "inspect or export the enforcement decision log", nil, []Subcommand{
		sub("tail", "reconc audit tail [repo] [filters]", "tail filtered audit decisions", flags(f("-n", "N"), f("--rule", "ID"), f("--since", "RFC3339"), f("--decision", "DECISION", "pass", "warn", "block"), f("--json", ""), f("--compact", "")), nil, modes(OutputText, OutputJSON)),
		sub("stats", "reconc audit stats [repo] [--json]", "aggregate audit decision statistics", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("export", "reconc audit export [repo]", "export raw audit JSONL", nil, nil, modes(OutputJSONL)),
	}, modes(OutputText, OutputJSON, OutputJSONL)),
	command("run", CategoryMaintenance, "reconc run <on|off|reset|status|log>", "operate durable repository run control", nil, []Subcommand{
		sub("on", "reconc run on [repo] [--force] [--json]", "enable repository run control", flags(f("--force", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("off", "reconc run off [repo] [--json]", "disable repository run control", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("reset", "reconc run reset [repo] [--json]", "recover a clean disabled run state", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("status", "reconc run status [repo] [--verbose | --json]", "inspect run and TASK state", flags(f("--verbose", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("log", "reconc run log [repo] [-n N] [--branch B] [--session S] [--follow] [--json]", "inspect or follow bounded run decisions", flags(f("-n", "N"), f("--branch", "B"), f("--session", "S"), f("--follow", ""), f("-f", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON)),
	command("task", CategoryMaintenance, "reconc task <subcommand>", "inspect or mutate the typed TASK lifecycle", nil, []Subcommand{
		sub("status", "reconc task status [repo] [--json]", "print compact current TASK context", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("validate", "reconc task validate [repo] [--json]", "validate the typed TASK control plane", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("check-done", "reconc task check-done [repo] [--task ID] [--json]", "validate TASK completion evidence", flags(f("--task", "ID"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("new", "reconc task new [repo] --title TEXT [--id ID] [--json]", "create a grammar-correct queued TASK", flags(f("--title", "TEXT"), f("--id", "ID"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("claim", "reconc task claim <ID> [repo] [--json]", "activate one queued TASK", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("block", "reconc task block [repo] --reason TEXT [--next ID | --no-next] [--json]", "block the current TASK", flags(f("--reason", "TEXT"), f("--next", "ID"), f("--no-next", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("resume", "reconc task resume <ID> [repo] [--json]", "reactivate one blocked TASK", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("split", "reconc task split [repo] --children ID,ID [--json]", "split a parent into pre-created children", flags(f("--children", "ID,ID"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("promote", "reconc task promote [repo] [--next ID] [--json]", "archive current TASK and activate the next", flags(f("--next", "ID"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("archive", "reconc task archive [repo] [--json]", "archive the terminal current TASK", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("recover", "reconc task recover [repo] [--json]", "recover an interrupted TASK transaction", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON)),
	command("prune", CategoryMaintenance, "reconc prune [repo] [--dry-run] [--json]", "bound runtime state and owned temporary residue", flags(f("--dry-run", ""), f("--json", ""), compat("--force", "")), nil, modes(OutputText, OutputJSON)),
	command("session-briefing", CategoryMaintenance, "reconc session-briefing [repo] [--json]", "print the versioned session and reentry delta", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("context", CategoryMaintenance, "reconc context size [repo] [flags]", "check canonical session files against a token budget", nil, []Subcommand{
		sub("size", "reconc context size [repo] [--limit N] [--files PATH,...] [--json]", "measure canonical session context", flags(f("--limit", "N"), f("--files", "PATH,..."), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON)),
	command("start", CategoryMaintenance, "reconc start [repo] [--write PATH] [--force] [--minimal] [--json]", "render or write canonical onboarding context", flags(f("--write", "PATH"), f("--force", ""), f("--minimal", ""), f("--json", "")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("post-task-check", CategoryMaintenance, "reconc post-task-check [repo] [--require-clean-git] [--json]", "run the evidence-complete pre-done gate", flags(compat("--window", "N"), f("--require-clean-git", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("delta", CategoryMaintenance, "reconc delta [repo] [--since RFC3339] [--json]", "show audit and policy activity since a point in time", flags(f("--since", "RFC3339"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("spec", CategoryMaintenance, "reconc spec check [repo] [flags]", "check docs/spec.md presence and freshness", nil, []Subcommand{
		sub("check", "reconc spec check [repo] [--file PATH] [--max-age-days N] [--json]", "check specification freshness", flags(f("--file", "PATH"), f("--max-age-days", "N"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON)),
	command("coverage", CategoryMaintenance, "reconc coverage check [repo] [flags]", "check a coverage percentage against a minimum", nil, []Subcommand{
		sub("check", "reconc coverage check [repo] [--file PATH] [--min-pct N] [--json]", "check measured coverage", flags(f("--file", "PATH"), f("--min-pct", "N"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON)),
	command("tui", CategoryMaintenance, "reconc tui [repo] [--json] [--output PATH]", "render the terminal policy and completion dashboard", flags(f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),

	commandArgs("completion", CategoryMeta, "reconc completion <bash|zsh|fish>", "emit a shell completion script", nil, []Argument{{Name: "shell", Values: []string{"bash", "fish", "zsh"}}}, nil, modes(OutputScript)),
	command("manpage", CategoryMeta, "reconc manpage", "emit a groff man(1) page", nil, nil, modes(OutputRoff)),
	command("version", CategoryMeta, "reconc version [--json]", "print the build version", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
}

func Categories() []CategoryInfo {
	return append([]CategoryInfo(nil), categoryCatalog...)
}

func All() []Command {
	out := make([]Command, 0, len(commandCatalog))
	for _, item := range commandCatalog {
		out = append(out, cloneCommand(item))
	}
	return out
}

func Lookup(name string) (Command, bool) {
	for _, item := range commandCatalog {
		if item.Name == name {
			return cloneCommand(item), true
		}
	}
	return Command{}, false
}

func Suggest(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	best := ""
	bestDistance := 3
	for _, item := range commandCatalog {
		distance := editDistance(name, item.Name)
		if distance < bestDistance || distance == bestDistance && item.Name < best {
			best = item.Name
			bestDistance = distance
		}
	}
	if bestDistance > 2 {
		return ""
	}
	return best
}

func command(name string, category Category, synopsis, summary string, commandFlags []Flag, subcommands []Subcommand, outputModes []OutputMode) Command {
	return commandArgs(name, category, synopsis, summary, commandFlags, nil, subcommands, outputModes)
}

func commandArgs(name string, category Category, synopsis, summary string, commandFlags []Flag, arguments []Argument, subcommands []Subcommand, outputModes []OutputMode) Command {
	return Command{Name: name, Category: category, Synopsis: synopsis, Summary: summary, Flags: commandFlags, Arguments: arguments, Subcommands: subcommands, Stability: StabilityStable, OutputModes: outputModes}
}

func sub(name, synopsis, summary string, subcommandFlags []Flag, arguments []Argument, outputModes []OutputMode) Subcommand {
	return Subcommand{Name: name, Synopsis: synopsis, Summary: summary, Flags: subcommandFlags, Arguments: arguments, Stability: StabilityStable, OutputModes: outputModes}
}

func subArgs(name, synopsis, summary string, subcommandFlags []Flag, arguments []Argument, outputModes []OutputMode) Subcommand {
	return sub(name, synopsis, summary, subcommandFlags, arguments, outputModes)
}

func internalSub(name, synopsis, summary string) Subcommand {
	return Subcommand{Name: name, Synopsis: synopsis, Summary: summary, Stability: StabilityInternal, OutputModes: modes(OutputText)}
}

func f(name, value string, values ...string) Flag {
	return Flag{Name: name, Value: value, Values: append([]string(nil), values...)}
}

func repeat(name, value string) Flag {
	flag := f(name, value)
	flag.Repeatable = true
	return flag
}

func repeatValues(name, value string, values ...string) Flag {
	flag := f(name, value, values...)
	flag.Repeatable = true
	return flag
}

func compat(name, value string) Flag {
	flag := f(name, value)
	flag.Compatibility = true
	return flag
}

func flags(values ...Flag) []Flag {
	return values
}

func modes(values ...OutputMode) []OutputMode {
	return values
}

func evidenceFlags(autoClaim, output bool) []Flag {
	values := flags(f("--read", "PATH"), f("--write", "PATH"), f("--command", "CMD"), f("--command-success", "CMD"), f("--command-failure", "CMD"), f("--claim", "NAME"))
	if autoClaim {
		values = append(values, f("--auto-claim", ""))
	}
	values = append(values, f("--json", ""), f("--terse", ""))
	if output {
		values = append(values, f("--output", "PATH"))
	}
	return values
}

func bootstrapSelectionFlags(allowOutput bool) []Flag {
	values := flags(f("--profile", "PROFILE", bootstrapProfiles...), repeat("--pack", "NAME"), repeatValues("--hook", "KIND", hookKinds...), f("--install-binary", ""), f("--binary", "PATH"), f("--checksum", "SHA256"), f("--platform", "OS/ARCH"))
	if allowOutput {
		values = append(values, f("--output", "PATH"), f("--replace-output", ""))
	}
	return append(values, f("--json", ""))
}

func cloneCommand(command Command) Command {
	command.Flags = cloneFlags(command.Flags)
	command.Arguments = cloneArguments(command.Arguments)
	command.OutputModes = append([]OutputMode(nil), command.OutputModes...)
	command.Subcommands = append([]Subcommand(nil), command.Subcommands...)
	for index := range command.Subcommands {
		command.Subcommands[index].Flags = cloneFlags(command.Subcommands[index].Flags)
		command.Subcommands[index].Arguments = cloneArguments(command.Subcommands[index].Arguments)
		command.Subcommands[index].OutputModes = append([]OutputMode(nil), command.Subcommands[index].OutputModes...)
	}
	return command
}

func cloneFlags(values []Flag) []Flag {
	out := append([]Flag(nil), values...)
	for index := range out {
		out[index].Values = append([]string(nil), out[index].Values...)
	}
	return out
}

func cloneArguments(values []Argument) []Argument {
	out := append([]Argument(nil), values...)
	for index := range out {
		out[index].Values = append([]string(nil), out[index].Values...)
	}
	return out
}

func editDistance(left, right string) int {
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	previous := make([]int, len(rightRunes)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftRune := range leftRunes {
		current := make([]int, len(rightRunes)+1)
		current[0] = leftIndex + 1
		for rightIndex, rightRune := range rightRunes {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[rightIndex+1] = min(previous[rightIndex+1]+1, current[rightIndex]+1, previous[rightIndex]+cost)
		}
		previous = current
	}
	return previous[len(rightRunes)]
}

func SortedNames() []string {
	values := make([]string, 0, len(commandCatalog))
	for _, command := range commandCatalog {
		values = append(values, command.Name)
	}
	sort.Strings(values)
	return values
}
