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
	OutputSARIF    OutputMode = "sarif"
	OutputJUnit    OutputMode = "junit"
	OutputFile     OutputMode = "file"
)

type Flag struct {
	Name       string
	Value      string
	Values     []string
	Repeatable bool
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
	Subcommands []Subcommand
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

var hookKinds = []string{"antigravity", "claude-code", "codex", "cursor", "devin-cli", "git-pre-commit", "github-copilot", "grok", "kilo", "kimi-code", "omp", "opencode", "pi", "zcode"}
var bootstrapHookKinds = []string{"antigravity", "claude-code", "codex", "cursor", "devin-cli", "git-pre-commit", "github-copilot", "grok", "kilo", "omp", "opencode", "pi", "zcode"}
var bootstrapProfiles = []string{"advanced", "existing", "governed", "minimal"}

var commandCatalog = []Command{
	command("status", CategoryDaily, "reconc status [repo] [--json] [--output PATH]", "one-line policy health summary", flags(f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("check", CategoryDaily, "reconc check [repo] [evidence flags] [--format text|json|terse|sarif|junit]", "evaluate runtime evidence against compiled policy", append(evidenceFlags(true, true), f("--format", "FORMAT", "text", "json", "terse", "sarif", "junit")), nil, modes(OutputText, OutputJSON, OutputSARIF, OutputJUnit, OutputFile)),
	command("next", CategoryDaily, "reconc next [repo] [evidence flags]", "show the next remediation", flags(f("--read", "PATH"), f("--write", "PATH"), f("--command", "CMD"), f("--command-success", "CMD"), f("--command-failure", "CMD"), f("--claim", "NAME"), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("done", CategoryDaily, "reconc done [repo] [--require-clean-git] [--json]", "evidence-complete task-finish gate", flags(f("--require-clean-git", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("proof", CategoryDaily, "reconc proof [repo] [--format json|markdown] [--output PATH] | reconc proof verify FILE [--repo REPO] [--json]", "export or strictly verify a portable completion proof bundle", flags(f("--format", "FORMAT", "json", "markdown"), f("--output", "PATH")), []Subcommand{
		sub("verify", "reconc proof verify FILE [--repo REPO] [--json]", "strictly verify a received proof offline; unsigned self-digest proves integrity, not identity; Exit 0 valid, 2 blocking or mismatch, 1 invalid", flags(f("--repo", "REPO"), f("--json", "")), []Argument{{Name: "file"}}, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON, OutputMarkdown, OutputFile)),

	command("bootstrap", CategoryBootstrap, "reconc bootstrap <subcommand>", "inspect, plan, apply, verify, or remove repository onboarding", nil, []Subcommand{
		sub("profiles", "reconc bootstrap profiles [--json]", "list explicit bootstrap profiles", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("inspect", "reconc bootstrap inspect [repo] [--json]", "inspect repository bootstrap inputs without mutation", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("plan", "reconc bootstrap plan [repo] --profile PROFILE [selection flags]", "build a deterministic bootstrap manifest", bootstrapSelectionFlags(true), nil, modes(OutputText, OutputJSON, OutputFile)),
		sub("apply", "reconc bootstrap apply --plan PATH | [repo] --profile PROFILE [selection flags]", "apply an exact plan or explicit selection transaction", append(flags(f("--plan", "PATH")), bootstrapSelectionFlags(false)...), nil, modes(OutputText, OutputJSON)),
		sub("remove", "reconc bootstrap remove --plan PATH [--json]", "reverse one receipt-owned bootstrap transaction", flags(f("--plan", "PATH"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("verify", "reconc bootstrap verify --plan PATH [--json]", "verify an applied bootstrap manifest read-only", flags(f("--plan", "PATH"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON, OutputFile)),
	command("repo", CategoryBootstrap, "reconc repo sync <plan|apply|resolve|verify|recover>", "plan, apply, resolve, verify, or recover receipt-owned repository upgrades", nil, []Subcommand{
		subgroup("sync", "reconc repo sync <plan|apply|resolve|verify|recover>", "operate the receipt-owned repository upgrade transaction", []Subcommand{
			sub("plan", "reconc repo sync plan [repo] [--output PATH [--replace-output]] [--json]", "build a deterministic read-only repository sync plan", flags(f("--output", "PATH"), f("--replace-output", ""), f("--json", "")), nil, modes(OutputText, OutputJSON, OutputFile)),
			sub("apply", "reconc repo sync apply --plan PATH --digest SHA256 [--json]", "apply one exact receipt-owned repository transaction", flags(f("--plan", "PATH"), f("--digest", "SHA256"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
			sub("resolve", "reconc repo sync resolve --plan PATH --digest SHA256 --path RELATIVE --strategy STRATEGY [binary flags] [--json]", "resolve one exact non-mutable sync action", flags(f("--plan", "PATH"), f("--digest", "SHA256"), f("--path", "RELATIVE"), f("--strategy", "STRATEGY", "keep-current", "use-target", "use-binary"), f("--binary", "PATH"), f("--checksum", "SHA256"), f("--platform", "OS/ARCH"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
			sub("verify", "reconc repo sync verify [repo] [--json]", "verify the portable repository receipt and owned artifacts", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
			sub("recover", "reconc repo sync recover [repo] [--json]", "finalize or roll back an interrupted repository sync", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		}),
	}, modes(OutputText, OutputJSON, OutputFile)),
	command("install-cli", CategoryBootstrap, "reconc install-cli [--install-dir PATH] [--json]", "install the running build as the stable user CLI", flags(f("--install-dir", "PATH"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("update", CategoryBootstrap, "reconc update [--channel stable|preview | --version VERSION] [--allow-downgrade] [--from-dir PATH] [--json]", "apply an ownership-safe global CLI update", flags(f("--channel", "CHANNEL", "stable", "preview"), f("--version", "VERSION"), f("--allow-downgrade", ""), f("--from-dir", "PATH"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("uninstall", CategoryBootstrap, "reconc uninstall [--purge-state] [--json]", "remove only the globally owned CLI installation", flags(f("--purge-state", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("init", CategoryBootstrap, "reconc init [repo] [--profile PROFILE] [selection flags]", "transactionally onboard a repository", flags(f("--profile", "PROFILE", bootstrapProfiles...), repeat("--pack", "NAME"), repeatValues("--hook", "KIND", bootstrapHookKinds...), f("--no-hooks", ""), f("--accept-managed-blocks", ""), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("adopt", CategoryBootstrap, "reconc adopt [repo] [--yaml | --json | --apply]", "detect tooling and suggest rules", flags(f("--yaml", ""), f("--json", ""), f("--apply", "")), nil, modes(OutputText, OutputYAML, OutputJSON)),
	command("extract", CategoryBootstrap, "reconc extract [repo] [--from PATH] [--yaml | --json]", "scan instruction prose for rule hints", flags(f("--from", "PATH"), f("--yaml", ""), f("--json", "")), nil, modes(OutputText, OutputYAML, OutputJSON)),
	command("doctor", CategoryBootstrap, "reconc doctor [repo] [--deep] [--json] [--output PATH] | reconc doctor --global [--json] [--output PATH]", "inspect repository or global installation state", flags(f("--deep", ""), f("--global", ""), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("refresh", CategoryCompile, "reconc refresh [repo] [--strict-conflicts] [--json] [--output PATH]", "explicitly refresh the policy lockfile", flags(f("--json", ""), f("--strict-conflicts", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
	command("sources", CategoryCompile, "reconc sources [repo] [--json]", "inspect effective policy-source provenance without source bodies", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("ci", CategoryCompile, "reconc ci [repo] (--staged | --base REF [--head REF]) [evidence flags] [--format text|json|sarif|junit]", "evaluate Git-derived changes under policy", flags(f("--staged", ""), f("--base", "REF"), f("--head", "REF"), f("--read", "PATH"), f("--command", "CMD"), f("--command-success", "CMD"), f("--command-failure", "CMD"), f("--claim", "NAME"), f("--auto-claim", ""), f("--json", ""), f("--format", "FORMAT", "text", "json", "sarif", "junit"), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputSARIF, OutputJUnit, OutputFile)),
	command("impact", CategoryCompile, "reconc impact [repo] (--candidate FILE | --pack NAME) [--corpus FILE | --fixture FILE] [evidence flags]", "compare an in-memory additive policy candidate over privacy-bounded replay evidence", flags(f("--candidate", "FILE"), f("--pack", "NAME"), repeat("--corpus", "FILE"), repeat("--fixture", "FILE"), f("--case-id", "ID"), f("--read", "PATH"), f("--write", "PATH"), f("--command", "CMD"), f("--command-success", "CMD"), f("--command-failure", "CMD"), f("--claim", "NAME"), f("--json", ""), f("--output", "PATH")), []Subcommand{
		sub("export", "reconc impact export [repo] (--session | evidence flags) [--complete CLASS] [--case-id ID] [--output PATH]", "export a deterministic privacy-bounded replay corpus", flags(f("--session", ""), repeatValues("--complete", "CLASS", "all", "read", "write", "command", "command_outcome", "claim"), f("--case-id", "ID"), f("--read", "PATH"), f("--write", "PATH"), f("--command", "CMD"), f("--command-success", "CMD"), f("--command-failure", "CMD"), f("--claim", "NAME"), f("--output", "PATH")), nil, modes(OutputJSON, OutputFile)),
	}, modes(OutputText, OutputJSON, OutputFile)),
	command("exec", CategoryCompile, "reconc exec [repo] [--staged] [--shell] -- COMMAND [ARG ...]", "execute and record real command evidence", flags(f("--staged", ""), f("--shell", "")), nil, modes(OutputText)),
	command("assert", CategoryCompile, "reconc assert <rule-id> [repo] [evidence flags]", "evaluate one rule by id", flags(f("--var", "KEY=VALUE"), f("--read", "PATH"), f("--write", "PATH"), f("--command", "CMD"), f("--command-success", "CMD"), f("--command-failure", "CMD"), f("--claim", "NAME"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("can", CategoryCompile, "reconc can write <path> [repo] [--why] [--json]", "return an ultra-terse yes/no policy decision", flags(f("--why", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("diff", CategoryCompile, "reconc diff <lockfile-a> <lockfile-b> [--json]", "compare two compiled lockfiles", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),

	command("explain", CategoryExplain, "reconc explain [repo] [evidence flags] | --report-file PATH", "render a check report as text or Markdown", flags(f("--read", "PATH"), f("--write", "PATH"), f("--command", "CMD"), f("--claim", "NAME"), f("--format", "FORMAT", "text", "markdown"), f("--report-file", "PATH"), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputMarkdown, OutputJSON, OutputFile)),
	command("fix", CategoryExplain, "reconc fix [repo] [evidence flags]", "build a structured remediation plan", flags(f("--read", "PATH"), f("--write", "PATH"), f("--command", "CMD"), f("--command-success", "CMD"), f("--command-failure", "CMD"), f("--claim", "NAME"), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
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
		sub("verify", "reconc hook verify [--host KIND [--surface SURFACE]] [--json]", "verify generated hook transports offline or prepare an explicit live probe", flags(f("--host", "KIND", hookKinds...), f("--surface", "SURFACE"), f("--live", ""), f("--allow-authenticated", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("bridge", "reconc hook bridge <runtime> <host-event> [repo]", "dispatch a declarative repository-owned custom runtime event", nil, nil, modes(OutputJSON)),
		sub("conform", "reconc hook conform <manifest.json> <fixtures.json> [--json]", "verify a custom runtime adapter contract offline", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("sync-scaffold", "reconc hook sync-scaffold <repo-root-scaffold> [--json]", "synchronize generated scaffold hook artifacts", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		internalSub("runtime", "reconc hook runtime <event> <repo>", "dispatch one registry-owned runtime event"),
		internalSub("worker", "reconc hook worker", "serve versioned session-owned hook requests over stdio"),
		internalSub("kimi-runtime", "reconc hook kimi-runtime <event>", "dispatch one global Kimi Code runtime event"),
		internalSub("grok-pre-tool-guard", "reconc hook grok-pre-tool-guard <repo>", "run the internal fail-closed Grok pre-tool guard"),
		sub("claim", "reconc hook claim <repo> <claim-name> [--session ID] [--json] [--output PATH]", "record one explicit session claim", flags(f("--session", "ID"), f("--json", ""), f("--output", "PATH")), nil, modes(OutputText, OutputJSON, OutputFile)),
		sub("evidence-status", "reconc hook evidence-status [repo] [--json]", "inspect persistent evidence taint without mutation", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("evidence-resolve", "reconc hook evidence-resolve <repo> --token TOKEN --reason TEXT [--json]", "resolve reviewed persistent evidence taint explicitly", flags(f("--token", "TOKEN"), f("--reason", "TEXT"), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON, OutputFile)),

	command("agent-intro", CategoryMaintenance, "reconc agent-intro [--section NAME | --list-sections] [--json]", "print the embedded agent integration guide", flags(f("--section", "NAME"), f("--list-sections", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("audit", CategoryMaintenance, "reconc audit <tail|stats|export|verify>", "inspect, export, or cryptographically verify decision evidence", nil, []Subcommand{
		sub("tail", "reconc audit tail [repo] [filters]", "tail filtered audit decisions", flags(f("-n", "N"), f("--rule", "ID"), f("--since", "RFC3339"), f("--decision", "DECISION", "pass", "warn", "block"), f("--json", ""), f("--compact", "")), nil, modes(OutputText, OutputJSON)),
		sub("stats", "reconc audit stats [repo] [--json]", "aggregate audit decision statistics", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
		sub("export", "reconc audit export [repo]", "export raw audit JSONL", nil, nil, modes(OutputJSONL)),
		sub("verify", "reconc audit verify [repo] [--json]", "verify every retained record and detached chain head", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
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
	command("prune", CategoryMaintenance, "reconc prune [repo] [--dry-run] [--json]", "bound runtime state and owned temporary residue", flags(f("--dry-run", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("session-briefing", CategoryMaintenance, "reconc session-briefing [repo] [--json]", "print the versioned session and reentry delta", flags(f("--json", "")), nil, modes(OutputText, OutputJSON)),
	command("context", CategoryMaintenance, "reconc context size [repo] [flags]", "check canonical session files against a token budget", nil, []Subcommand{
		sub("size", "reconc context size [repo] [--limit N] [--files PATH,...] [--json]", "measure canonical session context", flags(f("--limit", "N"), f("--files", "PATH,..."), f("--json", "")), nil, modes(OutputText, OutputJSON)),
	}, modes(OutputText, OutputJSON)),
	command("start", CategoryMaintenance, "reconc start [repo] [--minimal | --json]", "render canonical onboarding context without mutation", flags(f("--minimal", ""), f("--json", "")), nil, modes(OutputText, OutputJSON)),
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

// Public returns the user-facing command catalog. Internal routes remain in
// All so dispatch and compatibility tests can validate them without leaking
// those implementation surfaces into help, completions, manpages, or docs.
func Public() []Command {
	out := make([]Command, 0, len(commandCatalog))
	for _, item := range commandCatalog {
		if item.Stability != StabilityStable {
			continue
		}
		command := cloneCommand(item)
		command.Subcommands = publicSubcommands(command.Subcommands)
		out = append(out, command)
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

func subgroup(name, synopsis, summary string, subcommands []Subcommand) Subcommand {
	return Subcommand{
		Name: name, Synopsis: synopsis, Summary: summary,
		Subcommands: subcommands, Stability: StabilityStable,
		OutputModes: modes(OutputText, OutputJSON, OutputFile),
	}
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
	values := flags(f("--profile", "PROFILE", bootstrapProfiles...), repeat("--pack", "NAME"), repeatValues("--hook", "KIND", bootstrapHookKinds...), f("--install-binary", ""), f("--binary", "PATH"), f("--checksum", "SHA256"), f("--platform", "OS/ARCH"))
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
		command.Subcommands[index] = cloneSubcommand(command.Subcommands[index])
	}
	return command
}

func cloneSubcommand(command Subcommand) Subcommand {
	command.Flags = cloneFlags(command.Flags)
	command.Arguments = cloneArguments(command.Arguments)
	command.OutputModes = append([]OutputMode(nil), command.OutputModes...)
	command.Subcommands = append([]Subcommand(nil), command.Subcommands...)
	for index := range command.Subcommands {
		command.Subcommands[index] = cloneSubcommand(command.Subcommands[index])
	}
	return command
}

func publicSubcommands(values []Subcommand) []Subcommand {
	out := make([]Subcommand, 0, len(values))
	for _, value := range values {
		if value.Stability != StabilityStable {
			continue
		}
		value.Subcommands = publicSubcommands(value.Subcommands)
		out = append(out, value)
	}
	return out
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

func PublicSortedNames() []string {
	commands := Public()
	values := make([]string, 0, len(commands))
	for _, command := range commands {
		values = append(values, command.Name)
	}
	sort.Strings(values)
	return values
}
