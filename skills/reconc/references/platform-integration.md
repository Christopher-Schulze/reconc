# Platform integration

## Choosing An Integration

Use CLI plus supported native hooks for coding agents with shell access. The
skill teaches that workflow and CI validates the selected Git candidate;
neither proves that a host intercepted a live tool call.

The existing `reconc mcp gateway` is optional: route a downstream MCP server
through it when its tool calls need policy, approval, budget, inspection, and
ledger controls. It does not expose every Reconc CLI command as an MCP tool.
Direct downstream connections and host-native tools bypass this gateway.
Use `reconc agent-intro --section integration-surfaces` for the compact role
map, then `reconc mcp gateway --help` when configuring an authorized route.

Make the complete `skills/reconc/` directory available through the host's
supported skill-discovery mechanism, preserving `references/`. A checked-in
skill is not automatically loaded by every host. `reconc agent-intro` works
without skill installation; copying a skill does not install or activate hooks.

## Platform Model

The typed registry owns native event coverage, fallback routes, failure and
timeout policy, output budgets, artifact paths, and activation probes:

| Platform | Artifact | Integration model |
|---|---|---|
| Claude Code | `.claude/settings.json` | Native session, tool, permission, Stop, cleanup, and compact-session recovery hooks |
| Codex | `.codex/hooks.json` | Native session start/end, tool, permission, evidence, Stop, advisory Interrupt, and compact-session context recovery hooks |
| GitHub Copilot | `.github/hooks/reconc.json` | Repository hooks for Copilot CLI and coding agent; host timeouts remain fail-open |
| Cursor | `.cursor/hooks.json` | Registry-driven Agent/Cmd+K, Tab, CLI, and eligible cloud routes; `surface_events`, workspace liveness, decisions, outcomes, and guarantees are event-specific |
| OpenCode | `.opencode/plugins/reconc.js` | Thin project plugin with strict shell exits and inferred bounded async idle continuation; decisions and state stay in Go |
| Devin CLI | `.devin/hooks.v1.json` | Native lifecycle plus post-compaction recovery |
| Antigravity CLI | `.agents/hooks.json` | Invocation, tool, evidence, and Stop adapters |
| Kilo Code | `.kilo/plugin/reconc.js` | Thin CLI/VS Code project plugin with strict shell exits and inferred bounded async idle continuation; disabled when `KILO_PURE` is set |
| Oh My Pi | `.omp/extensions/reconc.ts` | Typed project extension with blocking pre-tool and awaited main-session Stop; observational approval, outcome, compaction, and shutdown routes |
| DeepSeek Harness | `.dsh/reconc.mjs` and `.dsh/reconc.patch.yml` | Explicit profile overlay with blocking pre-tool decisions, passive results, and awaited bounded Stop continuation |
| Pi Coding Agent | `.pi/extensions/reconc.ts` | Trust-aware typed project extension with blocking tool/user-shell boundaries, observational results/lifecycle/compaction, and inferred bounded settled continuation |
| ZCode | `.zcode/config.json` | Native seven-event process hooks with blocking pre-tool, permission, and synchronous Stop routes |
| Grok Build | `.grok/hooks/reconc.json` | Native lifecycle and hard PreToolUse; project trust required; capability-probed native Stop or optional local leader fallback |
| Kimi Code CLI | `$KIMI_CODE_HOME/config.toml` | Explicit user-global 16-event hook block; repository discovery before action; exit-code-2 PreToolUse, prompt, and Stop control |

Run `reconc hook status . --json` before making enforcement claims.
`configured` proves a complete static artifact; `discoverable` means the named
host surface scans its path; `loaded` requires a current session/init route;
`observed` requires that exact route; `enforced` requires a disposable negative
probe that stopped the side effect; `inferred` is weaker host lifecycle;
`degraded` is missing or unproven required behavior; `unsupported` means no
sound host boundary. Never promote one state into another.

Codex local-function hooks and namespaced MCP hooks have disjoint matchers.
After installation, review the project and exact hook definitions in Codex
`/hooks`. Reconc's static `configured` state does not verify host trust or
managed-only filtering; use native host evidence before claiming loaded hooks.
MCP permission requests apply the same classified effect policy as the pre-hook.
Codex CLI 0.154.0 shell post-hooks contain output text without an authoritative
exit code; use `reconc exec --staged` for command-success evidence. Hosted tools
and opted-out transports remain outside native hook coverage.
Native child `agent_id` separates evidence from Codex's shared root session.
SubagentStop checks child policy with one remediation attempt, records an
unresolved repeated turn as uncertified, and never controls the parent's TASK
continuation. Child evidence survives turn stops; SessionEnd is root-only.

Cursor uses one project file, but desktop Agent, Cmd+K, Tab, interactive CLI,
print CLI, and cloud agents do not promise identical event delivery. Use the
same Reconc semantics when the same event fires and keep every unseen route
unproven. Shell `postToolUse` carries a JSON-stringified `tool_output` with an
exit code; Reconc requires a valid zero exit for command-success evidence.
`postToolUseFailure` is failure, and `afterShellExecution` is liveness only.
Cursor CLI discovery prefers `cursor-agent`; `agent` is accepted only
after version and help identity checks. `surface_events` lists eligible routes,
not proof that each route fired. On CLI `2026.09.10-fd3934a`, interactive
shell/file/failure and prompt/Stop routes fired; print shell/file/MCP/failure
routes fired, while prompt and Stop remain unproven. A generic write and its
`afterFileEdit` callback count once even when the specialized callback arrives
first without a tool ID. The captured print-mode deny was from an isolated
custom hook, not a Reconc live enforcement claim. Cursor CLI MCP pre events
carry a server locator and can be classified; post events omit both locator
and call ID, so Reconc treats them as observations without positive repository
evidence. `workspaceOpen` is
sessionless loading evidence only. `AskQuestion` and native
subagent denial were not exercised in the CLI probe; never claim Reconc gated
either action from static configuration or earlier host reports.

Kimi Code hook installation is always explicit and global:
`reconc hook install kimi-code`, without a repository path. It atomically
merges only Reconc's marker block and preserves unrelated TOML. Global
invocations silently no-op outside repositories with explicit Reconc
configuration. Kimi fails open on hook crashes, timeouts, and non-zero exits
other than 2, and its post-tool payload has no authoritative exit status.
Never claim live enforcement from static configuration or contract tests;
require exact `hook status` liveness.

OpenCode and Kilo accept shell success only from integer
`output.metadata.exit == 0`. Their `session.idle` continuation calls only
asynchronous `promptAsync`, is generation-deduplicated and capped, and remains
fail-open/inferred. A missing API, rejected request, or invalid response is not
delivered continuation.

Oh My Pi binds executed arguments from `tool_result` to the final
`tool_execution_end.isError` outcome by session and call ID. A pending background
`Bash` start creates no command-success evidence; only a settled successful
built-in `Bash` without a native exit code receives synthetic zero. A later
extension can replace tool input after Reconc's pre-gate because OMP gives all
pre-handlers the original input and the last replacement wins. Treat that as
an enforcement limit when other extensions are active. `tool_call` and `session_stop` fail closed on
deny, malformed decision, Reconc failure, or timeout. A host-aborted Stop yields
immediately without continuation. Observational routes fail open after bounded
diagnostics. Never infer live enforcement from the generated extension alone.

Pi loads project extensions only after trust. Reconc owns only
`.pi/extensions/reconc.ts` and never changes Pi trust. Require saved
canonical-path trust or `defaultProjectTrust: "always"` for static
`configured` status; `pi --approve` is one-run activation only. `tool_call` and
`user_bash` fail closed. `tool_result.isError` is authoritative, and only a
successful built-in `Bash` result receives synthetic exit code zero. Pi has no
native permission event, MCP discriminator, post-user-shell result,
synchronous Stop gate, or continuation acknowledgement.

DeepSeek Harness loads `.dsh/reconc.patch.yml` with the selected profile, for
example `npx --yes @deepseek-ai/dsh@0.1.5-rc.2 --profile headless --patch .dsh/reconc.patch.yml`.
`tools/pre-execute` returns native denials for policy blocks, invalid decisions,
worker errors, and timeouts. `agent/turn-stopping` awaits policy evaluation and
uses `agent.steer` for one remediation per turn, honoring cancellation and
reentry. Lifecycle failures produce bounded diagnostics. Native/PTC modes,
shells, terminals, delegation, and bridges use the same configured policy
without execution-mode blacklists. Subdirectory file paths are rebased; opaque
shell state and external children do not create assumed evidence or inherited
protection. Later plugins can replace execution input; Reconc does not lock
host-owned fields. Final transformed `tools/result` outcomes remain passive
observations. Use `reconc exec` for authoritative command proof.

ZCode snapshots `.zcode/config.json` at session start. Reconc merges only exact
managed process entries and preserves foreign settings, events, commands, and
an explicit user `hooks.enabled=false`; status reports that state as disabled.
Restart the ZCode session after install or uninstall. Hard `PreToolUse` blocks
use exit code 2, `PermissionRequest` denials use the native decision object,
and Stop uses native block JSON. Observation routes and host timeouts fail
open. Stop is limited by the host to three consecutive blocks. Static
configuration and offline fixtures are not live route proof.

MCP repository effects are opt-in exact mappings in `.reconc.yml`. Use
`reconc why mcp .` to inspect the compiled contract. Never treat an unknown
identity, malformed selector value, unknown outcome, or `external` effect as
repository evidence. Cursor can strictly deny unclassified calls through its
dedicated MCP pre-hook. OpenCode/Kilo generic hooks cannot identify
unconfigured MCP calls soundly; OMP, DSH, Pi, and ZCode have the same generic-tool identity limit.
Report strict unclassified deny as unavailable on those surfaces while exact
configured tool identities remain enforceable.

`installed`, `degraded`, `shadowed`, and `unsupported` require the status
detail to be handled or reported. Generic agents use explicit CLI checks.
Every platform keeps Git pre-commit as the hard repository backstop.
