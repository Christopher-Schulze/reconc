# reconc -- Command Reference

Reference for the complete command surface. See `reconc help <subcommand>` or
`reconc <subcommand> --help` for the exact flag details emitted by the
installed binary. Nested help works the same way, for example
`reconc help task recover`.

<!-- BEGIN RECONC GENERATED COMMAND REFERENCE -->
## Canonical command catalog

Generated from `internal/commandmeta`; run `make reference-docs` after changing the public CLI contract.

| Command path | Canonical synopsis | Summary | Outputs |
|---|---|---|---|
| `reconc status` | `reconc status [repo] [--json] [--output PATH]` | one-line policy health summary | text, json, file |
| `reconc check` | `reconc check [repo] [evidence flags] [--format text\|json\|terse\|sarif\|junit]` | evaluate runtime evidence against compiled policy | text, json, sarif, junit, file |
| `reconc next` | `reconc next [repo] [evidence flags]` | show the next remediation | text, json, file |
| `reconc done` | `reconc done [repo] [--require-clean-git] [--json]` | evidence-complete task-finish gate | text, json |
| `reconc proof` | `reconc proof [repo] [--format json\|markdown] [--output PATH] \| reconc proof verify FILE [--repo REPO] [--json]` | export or strictly verify a portable completion proof bundle | text, json, markdown, file |
| `reconc proof verify` | `reconc proof verify FILE [--repo REPO] [--json]` | strictly verify a received proof offline; unsigned self-digest proves integrity, not identity; Exit 0 valid, 2 blocking or mismatch, 1 invalid | text, json |
| `reconc bootstrap` | `reconc bootstrap <subcommand>` | inspect, plan, apply, verify, or remove repository onboarding | text, json, file |
| `reconc bootstrap profiles` | `reconc bootstrap profiles [--json]` | list explicit bootstrap profiles | text, json |
| `reconc bootstrap inspect` | `reconc bootstrap inspect [repo] [--json]` | inspect repository bootstrap inputs without mutation | text, json |
| `reconc bootstrap plan` | `reconc bootstrap plan [repo] --profile PROFILE [selection flags]` | build a deterministic bootstrap manifest | text, json, file |
| `reconc bootstrap apply` | `reconc bootstrap apply --plan PATH \| [repo] --profile PROFILE [selection flags]` | apply an exact plan or explicit selection transaction | text, json |
| `reconc bootstrap remove` | `reconc bootstrap remove --plan PATH [--json]` | reverse one receipt-owned bootstrap transaction | text, json |
| `reconc bootstrap verify` | `reconc bootstrap verify --plan PATH [--json]` | verify an applied bootstrap manifest read-only | text, json |
| `reconc repo` | `reconc repo sync <plan\|apply\|resolve\|verify\|recover>` | plan, apply, resolve, verify, or recover receipt-owned repository upgrades | text, json, file |
| `reconc repo sync` | `reconc repo sync <plan\|apply\|resolve\|verify\|recover>` | operate the receipt-owned repository upgrade transaction | text, json, file |
| `reconc repo sync plan` | `reconc repo sync plan [repo] [--output PATH [--replace-output]] [--json]` | build a deterministic read-only repository sync plan | text, json, file |
| `reconc repo sync apply` | `reconc repo sync apply --plan PATH --digest SHA256 [--json]` | apply one exact receipt-owned repository transaction | text, json |
| `reconc repo sync resolve` | `reconc repo sync resolve --plan PATH --digest SHA256 --path RELATIVE --strategy STRATEGY [binary flags] [--json]` | resolve one exact non-mutable sync action | text, json |
| `reconc repo sync verify` | `reconc repo sync verify [repo] [--json]` | verify the portable repository receipt and owned artifacts | text, json |
| `reconc repo sync recover` | `reconc repo sync recover [repo] [--json]` | finalize or roll back an interrupted repository sync | text, json |
| `reconc install-cli` | `reconc install-cli [--install-dir PATH] [--json]` | install the running build as the stable user CLI | text, json |
| `reconc update` | `reconc update [--channel stable\|preview \| --version VERSION] [--allow-downgrade] [--from-dir PATH] [--json]` | apply an ownership-safe global CLI update | text, json |
| `reconc uninstall` | `reconc uninstall [--purge-state] [--json]` | remove only the globally owned CLI installation | text, json |
| `reconc init` | `reconc init [repo] [--profile PROFILE] [selection flags]` | transactionally onboard a repository | text, json, file |
| `reconc adopt` | `reconc adopt [repo] [--yaml \| --json \| --apply]` | detect tooling and suggest rules | text, yaml, json |
| `reconc extract` | `reconc extract [repo] [--from PATH] [--yaml \| --json]` | scan instruction prose for rule hints | text, yaml, json |
| `reconc doctor` | `reconc doctor [repo] [--deep] [--json] [--output PATH] \| reconc doctor --global [--json] [--output PATH]` | inspect repository or global installation state | text, json, file |
| `reconc refresh` | `reconc refresh [repo] [--strict-conflicts] [--json] [--output PATH]` | explicitly refresh the policy lockfile | text, json, file |
| `reconc sources` | `reconc sources [repo] [--json]` | inspect effective policy-source provenance without source bodies | text, json |
| `reconc ci` | `reconc ci [repo] (--staged \| --base REF [--head REF]) [evidence flags] [--format text\|json\|sarif\|junit]` | evaluate Git-derived changes under policy | text, json, sarif, junit, file |
| `reconc impact` | `reconc impact [repo] (--candidate FILE \| --pack NAME) [--corpus FILE \| --fixture FILE] [evidence flags] [--delta-manifest FILE] [--format text\|json\|sarif\|junit\|github]` | compare an in-memory additive policy candidate over privacy-bounded replay evidence | text, json, sarif, junit, github, file |
| `reconc impact export` | `reconc impact export [repo] (--session \| evidence flags) [--complete CLASS] [--case-id ID] [--output PATH]` | export a deterministic privacy-bounded replay corpus | json, file |
| `reconc policy` | `reconc policy author [repo] (--candidate FILE \| --detected) [authoring flags]` | validate, explain, and explicitly adopt a repository policy fragment | text, json |
| `reconc policy author` | `reconc policy author [repo] (--candidate FILE \| --detected) [--target policies/NAME.yml] [--corpus FILE \| --fixture FILE] [evidence flags] [--apply] [--json]` | validate and explain a policy candidate, then adopt it only by explicit flag or terminal confirmation | text, json |
| `reconc exec` | `reconc exec [repo] [--staged] [--shell] -- COMMAND [ARG ...]` | execute and record real command evidence | text |
| `reconc assert` | `reconc assert <rule-id> [repo] [evidence flags]` | evaluate one rule by id | text, json |
| `reconc can` | `reconc can write <path> [repo] [--why] [--json]` | return an ultra-terse yes/no policy decision | text, json |
| `reconc diff` | `reconc diff <lockfile-a> <lockfile-b> [--json]` | compare two compiled lockfiles | text, json |
| `reconc explain` | `reconc explain [repo] [evidence flags] \| --report-file PATH` | render a check report as text or Markdown | text, markdown, json, file |
| `reconc fix` | `reconc fix [repo] [evidence flags]` | build a structured remediation plan | text, json, file |
| `reconc why` | `reconc why <rule-id\|action\|mcp> [repo] [--terse] [--json]` | print one compiled rule, the canonical action plan, or its MCP compatibility view | text, json |
| `reconc mcp` | `reconc mcp gateway [repo] [flags] -- COMMAND [ARG...]` | run an enforcing tools-only MCP stdio gateway | mcp |
| `reconc mcp gateway` | `reconc mcp gateway [repo] --server LABEL (--expect-lock-digest SHA256 \| --allow-repository-managed-policy) --principal LABEL [trusted-context flags] -- COMMAND [ARG...]` | enforce one operator-selected downstream MCP stdio server | mcp |
| `reconc preset` | `reconc preset <list\|show>` | list or show bundled and user presets | text, json, file |
| `reconc preset list` | `reconc preset list [--json] [--output PATH]` | list bundled and user presets | text, json, file |
| `reconc preset show` | `reconc preset show <name> [--json] [--output PATH]` | show one resolved preset | text, json, file |
| `reconc template` | `reconc template <list\|show>` | list or show bundled and user rule templates | text, json |
| `reconc template list` | `reconc template list [--json]` | list rule templates | text, json |
| `reconc template show` | `reconc template show <name> [--json]` | show one resolved rule template | text, json |
| `reconc hook` | `reconc hook <subcommand>` | manage, inspect, or execute agent runtime hooks | text, json, file |
| `reconc hook generate` | `reconc hook generate <kind> [--json] [--output PATH]` | print one hook artifact | text, json, file |
| `reconc hook install` | `reconc hook install <kind> [repo] [--force] [--json] [--output PATH]` | install generated hooks into a repository | text, json, file |
| `reconc hook uninstall` | `reconc hook uninstall <kind> [repo] [--json] [--output PATH]` | remove one Reconc-managed hook safely | text, json, file |
| `reconc hook status` | `reconc hook status [repo] [--json]` | inspect registered hook installation and liveness | text, json |
| `reconc hook verify` | `reconc hook verify [--host KIND [--surface SURFACE]] [--json]` | verify generated hook transports offline or prepare an explicit live probe | text, json |
| `reconc hook bridge` | `reconc hook bridge <runtime> <host-event> [repo]` | dispatch a declarative repository-owned custom runtime event | json |
| `reconc hook conform` | `reconc hook conform <manifest.json> <fixtures.json> [--json]` | verify a custom runtime adapter contract offline | text, json |
| `reconc hook sync-scaffold` | `reconc hook sync-scaffold <repo-root-scaffold> [--json]` | synchronize generated scaffold hook artifacts | text, json |
| `reconc hook claim` | `reconc hook claim <repo> <claim-name> [--session ID] [--json] [--output PATH]` | record one explicit session claim | text, json, file |
| `reconc hook evidence-status` | `reconc hook evidence-status [repo] [--json]` | inspect persistent evidence taint without mutation | text, json |
| `reconc hook evidence-resolve` | `reconc hook evidence-resolve <repo> --token TOKEN --reason TEXT [--json]` | resolve reviewed persistent evidence taint explicitly | text, json |
| `reconc agent-intro` | `reconc agent-intro [--section NAME \| --list-sections] [--json]` | print the embedded agent integration guide | text, json |
| `reconc audit` | `reconc audit <tail\|stats\|export\|verify>` | inspect, export, or cryptographically verify decision evidence | text, json, jsonl |
| `reconc audit tail` | `reconc audit tail [repo] [filters]` | tail filtered audit decisions | text, json |
| `reconc audit stats` | `reconc audit stats [repo] [--json]` | aggregate audit decision statistics | text, json |
| `reconc audit export` | `reconc audit export [repo]` | export raw audit JSONL | jsonl |
| `reconc audit verify` | `reconc audit verify [repo] [--json]` | verify every retained record and detached chain head | text, json |
| `reconc action` | `reconc action <evidence\|key\|log>` | initialize action state, inspect decisions, or produce technical control evidence | text, json, file |
| `reconc action evidence` | `reconc action evidence <export\|verify>` | export or verify local technical control-evidence mappings | text, json, file |
| `reconc action evidence export` | `reconc action evidence export [repo] --as-of RFC3339 [evidence flags] [--format json\|markdown] [--output PATH]` | export deterministic privacy-bounded technical control evidence | json, markdown, file |
| `reconc action evidence verify` | `reconc action evidence verify [repo] --as-of RFC3339 [evidence flags] [--json]` | verify current technical evidence and fail unless every selected mapping is covered | text, json |
| `reconc action key` | `reconc action key init` | initialize private operator-owned action state | text, json, file |
| `reconc action key init` | `reconc action key init [--reconc-home PATH] [--json]` | create the action identity key exactly once | text, json |
| `reconc action log` | `reconc action log <tail\|stats\|verify\|export>` | inspect, verify, or export the action decision ledger | text, json, file |
| `reconc action log tail` | `reconc action log tail [repo] [filters]` | tail bounded action ledger events | text, json |
| `reconc action log stats` | `reconc action log stats [repo] [filters]` | aggregate explicit action call lifecycles | text, json |
| `reconc action log verify` | `reconc action log verify [repo] [--json]` | verify retained action records, archives, and detached head | text, json |
| `reconc action log export` | `reconc action log export [repo] [filters] [--output PATH]` | export complete privacy-bounded Impact Lab action cases | json, file |
| `reconc run` | `reconc run <on\|off\|reset\|status\|log>` | operate durable repository run control | text, json |
| `reconc run on` | `reconc run on [repo] [--force] [--json]` | enable repository run control | text, json |
| `reconc run off` | `reconc run off [repo] [--json]` | disable repository run control | text, json |
| `reconc run reset` | `reconc run reset [repo] [--json]` | recover a clean disabled run state | text, json |
| `reconc run status` | `reconc run status [repo] [--verbose \| --json]` | inspect run and TASK state | text, json |
| `reconc run log` | `reconc run log [repo] [-n N] [--branch B] [--session S] [--follow] [--json]` | inspect or follow bounded run decisions | text, json |
| `reconc task` | `reconc task <subcommand>` | inspect or mutate the typed TASK lifecycle | text, json |
| `reconc task status` | `reconc task status [repo] [--json]` | print compact current TASK context | text, json |
| `reconc task validate` | `reconc task validate [repo] [--json]` | validate the typed TASK control plane | text, json |
| `reconc task check-done` | `reconc task check-done [repo] [--task ID] [--json]` | validate TASK completion evidence | text, json |
| `reconc task new` | `reconc task new [repo] --title TEXT [--id ID] [--json]` | create a grammar-correct queued TASK | text, json |
| `reconc task claim` | `reconc task claim <ID> [repo] [--json]` | activate one queued TASK | text, json |
| `reconc task block` | `reconc task block [repo] --reason TEXT [--next ID \| --no-next] [--json]` | block the current TASK | text, json |
| `reconc task resume` | `reconc task resume <ID> [repo] [--json]` | reactivate one blocked TASK | text, json |
| `reconc task split` | `reconc task split [repo] --children ID,ID [--json]` | split a parent into pre-created children | text, json |
| `reconc task promote` | `reconc task promote [repo] [--next ID] [--json]` | archive current TASK and activate the next | text, json |
| `reconc task archive` | `reconc task archive [repo] [--json]` | archive the terminal current TASK | text, json |
| `reconc task recover` | `reconc task recover [repo] [--json]` | recover an interrupted TASK transaction | text, json |
| `reconc prune` | `reconc prune [repo] [--dry-run] [--json]` | bound runtime state and owned temporary residue | text, json |
| `reconc session-briefing` | `reconc session-briefing [repo] [--json]` | print the versioned session and reentry delta | text, json |
| `reconc context` | `reconc context size [repo] [flags]` | check canonical session files against a token budget | text, json |
| `reconc context size` | `reconc context size [repo] [--limit N] [--files PATH,...] [--json]` | measure canonical session context | text, json |
| `reconc start` | `reconc start [repo] [--minimal \| --json]` | render canonical onboarding context without mutation | text, json |
| `reconc tui` | `reconc tui [repo] [--json] [--output PATH]` | render the terminal policy and completion dashboard | text, json, file |
| `reconc completion` | `reconc completion <bash\|zsh\|fish>` | emit a shell completion script | script |
| `reconc manpage` | `reconc manpage` | emit a groff man(1) page | roff |
| `reconc version` | `reconc version [--json]` | print the build version | text, json |

<!-- END RECONC GENERATED COMMAND REFERENCE -->

## Daily path

Install a portable build explicitly with `PATH/TO/reconc install-cli`, or invoke
`PATH/TO/reconc init .` and let init perform the same installation.
Init fails before repository writes unless the exact running build is
directly callable as bare `reconc`. Then use the same four-command daily loop
taught by the README and agent skill:

```bash
reconc session-briefing . --json
reconc check . --write path/to/file
reconc next .
reconc done .
```

Keep the installed CLI current with the single global lifecycle command:

```bash
reconc update
```

Everything below is the full automation and diagnostic surface.

## Exit codes

- `0` pass / warn / informational success
- `1` runtime or input error
- `2` at least one blocking policy violation

## Environment

Runtime:

- `RECONC_HOME` (default `~/.reconc`) -- user config, presets, templates
- `NO_COLOR` -- disable ANSI styling even when stdout is a terminal; redirected
  and `TERM=dumb` output is always plain
- `COLUMNS` -- terminal width for `reconc tui` when it is an integer from 20
  through 1000
- `CI` (`1`, `true`, `on`, or `yes`) and provider markers
  `GITHUB_ACTIONS`, `GITLAB_CI`, `CIRCLECI`, `TRAVIS`, `JENKINS_URL`,
  `BUILDKITE`, `DRONE`, `APPVEYOR`, `TEAMCITY_VERSION`, and
  `BITBUCKET_BUILD_NUMBER` -- allow explicit `--auto-claim` to assert
  `ci-green`
- `RECONC_AUDIT=1` -- enable the opt-in append-only audit log
- `RECONC_AUDIT_VERBOSE=1` -- store full command strings in audit records
  instead of the redacted first token (may capture secrets in arguments)
- `RECONC_CLAUDE_STATE_DIR` -- override the global session-state root
- `CLAUDE_CONFIG_DIR` (default `~/.claude`) -- absolute Claude configuration
  root used to recognize the current project's persistent-memory state
- `RECONC_SCHEMA_BASE_URL` -- enterprise override resolved through the typed
  per-artifact registry at `/schemas/<artifact>/v<schema-version>`; without an
  override, current contracts use their release-pinned v1, v2, v3, v4, or v6
  identities. Registered legacy aliases remain input-only. Runtime validation
  never fetches schema URLs
- `RECONC_STOP_FINGERPRINT_UNTRACKED` (`normal` default, `all`, `no`) --
  untracked-file mode for the Stop fingerprint's git status snapshot. `normal`
  content-binds each untracked directory recursively under bounded entry and
  byte limits; `all` asks Git to enumerate every untracked path; `no` excludes
  untracked paths from this fingerprint
- `RECONC_GROK_STEER=0` -- disable optional Grok TUI leader steering over the
  Unix socket or Windows named pipe; PreToolUse remains enforced and native
  Stop remains available only when the installed Grok guide advertises it
  (steering also honours `GROK_LEADER_SOCKET`)
- `GROK_HOME` (default `~/.grok`) -- Grok installation/state root used for the
  installed hook guide and leader-socket discovery
- `KIMI_CODE_HOME` (default `~/.kimi-code`) -- Kimi Code's user-global
  configuration root. Reconc reads or changes it only for explicit Kimi hook
  lifecycle and status operations; tests must always override it
- `PI_CODING_AGENT_DIR` (default `~/.pi/agent`) -- Pi's agent state root used
  read-only by hook status to evaluate `trust.json` and
  `defaultProjectTrust`; Reconc never changes those files
- `GROK_LEADER_SOCKET` -- authoritative Grok leader socket or named-pipe
  endpoint for optional TUI steering
- `XAI_API_KEY` -- optional credential used only by the hidden Grok ACP
  compatibility driver when the installed server offers `xai.api_key`;
  prefer `grok login`; Reconc does not persist or print the value
- `SOURCE_DATE_EPOCH` -- reproducible timestamp source for generated manpages
  and release artifacts; invalid values fail instead of falling back

Debugging:

- `RECONC_HOOK_TIMING=1` -- print per-stage hook-runtime timings to stderr
- `RECONC_HOOK_TIMING_THRESHOLD_MS` -- only print timings above this bound
- `RECONC_AUDIT_NO_CACHE=1` -- bypass the audit stats cache

Installers and `reconc install-cli`:

- `RECONC_INSTALL_DIR` (default `~/.local/bin` on POSIX and
  `%LOCALAPPDATA%\Programs\Reconc\bin` for `install.ps1`) -- install target
- `RECONC_RELEASE_BASE` -- release download mirror
- `RECONC_ATTESTATION_TOOL` -- alternate `gh`-compatible verifier used only by
  `reconc update` and its offline fixtures. Shipped native installers always
  require `gh` and the fixed `Christopher-Schulze/reconc` identity
- `RECONC_INSTALL_MANAGER`, `RECONC_INSTALL_CHANNEL`,
  `RECONC_INSTALL_ARTIFACT`, `RECONC_INSTALL_RELEASE_TAG`, and
  `RECONC_INSTALL_PROVENANCE` -- installer-to-binary transaction metadata;
  reserved for shipped native installers, not user configuration.
  `install-cli` rejects unsupported ownership claims through this surface

Variables prefixed `RECONC_HOOK_` other than the timing pair (for example
`RECONC_HOOK_REPO_RESOLVED`, `RECONC_HOOK_RUNTIME`) are internal wrapper
plumbing, not user configuration.

---

## Action Plane commands

`reconc why action` is implemented in `v0.9.7` and
is documented under Explain and remediate.

### `reconc action key init [--reconc-home PATH] [--json]`

Create the private operator-owned action identity key exactly once. The selected
home defaults through the normal Reconc home contract; an explicit gateway
deployment should pass the same private `--reconc-home` path to this command and
the gateway. Reconc creates missing private directories, publishes the key with
private permissions, and returns its non-secret key ID. If a key already exists,
the command fails without replacing or changing it. It never rotates a key,
returns budget capacity, or mutates repository files.

### `reconc action log tail [repo] [-n N] [filters] [--json]`

Verify the complete retained action-ledger chain, then return the last `N`
matching typed events. `N` defaults to 20 and must be from 1 through 1,000.
Filters are exact and combine: `--call`, `--run`, `--session`, `--principal`,
`--tool MODE:VALUE`, `--event`, `--decision`, and RFC3339 `--since`; tool mode
is `declaration_id`, `exact_name`, or `keyed_name`. A missing ledger is a
valid empty report and creates no state. Corrupt, unsafe, or unverifiable
retained evidence fails instead of returning a partial tail. Explicitly
incomplete lifecycles remain visible with their completeness flags.

### `reconc action log stats [repo] [filters] [--json]`

Verify the retained chain and aggregate explicit call lifecycles selected by
the same exact filters. The report separates evaluated, approved, dispatched,
downstream succeeded/failed/unknown, delivered/withheld/suppressed, terminal,
and incomplete calls, and groups them by run, session, principal, and tool.
Run and session groups use their keyed identities. Missing events never become
inferred success, and inactivity or MCP connection closure never becomes an
invented run or session terminal event. A malformed lifecycle timestamp is an
explicit reconstruction error; it never becomes a zero timestamp or disables
ordering checks.

### `reconc action log verify [repo] [--json]`

Verify record digests and sequence, archive order and continuity, detached
head, retained range, dropped-history boundary, and event completeness. A
missing ledger returns the canonical empty verification report without creating
state. If a durable interrupted transaction exists, verification first rolls
back its prepared append or completes its already-published detached head.
`events_evaluated` and `calls_evaluated` state whether completeness analysis
ran; `events_complete` and `calls_complete` state its result. An invalid chain
never reports unevaluated evidence as complete.

### `reconc action log export [repo] [filters] [--output PATH]`

Build a deterministic `reconc.action-ledger-impact-export/v1` wrapper around
verified synthetic minimized Impact Lab action cases. A call is exported only
when its retained lifecycle, policy/lock identity, tool identity, evidence, and
minimized replay reproduce the exact decision. Every omission has an explicit
reason. The report always states that it is not replay-complete and lists the
raw dimensions that cannot be reconstructed; it never expands keyed digests or
exports raw arguments, results, credentials, headers, environment values, or
metadata. Only declaration IDs and explicitly disclosure-safe exact tool names
can produce a synthetic case; keyed names and unsafe exact names remain explicit
omissions. `--output` publishes a new private `0600` file atomically and refuses
an existing path.

The source also contains the enforcing Go MCP gateway plus its trusted-context,
cumulative-budget, approval, inspection, and action-ledger owners. These
controls apply only to calls routed through `reconc mcp gateway`; they do not
intercept a direct downstream or native framework tool call.

The gateway invokes the deterministic action-inspection core before dispatch,
inspects progress and downstream results, and returns a bounded safe envelope
instead of a withheld result. Confusable-text matching uses Unicode
compatibility normalization plus one fixed, reviewable skeleton table for the
cross-script characters required by protected vocabulary; compatibility forms
such as fullwidth letters and listed small-capital variants cannot bypass the
same finding.

The same internal boundary implements canonical one-call approval requests,
Ed25519 approve or reject receipts, strict operator-owned authority registries,
single-use atomic consumption, budget coupling, expiry reconciliation, and
exact MCP `2026-07-28` input-required and MCP `2025-11-25` standard
form-elicitation transport mappings. It exposes no public approver: the
upstream MCP client must return a valid one-time signed receipt. An unsigned
client response or a signer under the agent's authority is not an independent
approval; missing elicitation support or a valid response fails closed.
Budget-state corruption during reservation or retry is returned as a typed
state error; it never masquerades as an empty candidate set or restored
capacity.

### `reconc mcp gateway [repo] --server LABEL (--expect-lock-digest SHA256 | --allow-repository-managed-policy) --principal LABEL [trusted-context flags] -- COMMAND [ARG...]`

Starts one local, tool-only stdio gateway around one operator-selected
downstream stdio MCP server. Exactly one policy-authority flag is required.
`--expect-lock-digest` pins startup and every call to one lock digest.
`--allow-repository-managed-policy` deliberately accepts refreshed repository
policy and therefore has lower provenance when the calling agent can modify
that repository. `--server` and `--principal` are mandatory;
`--role`, `--environment`, repeatable `--credential`, `--run`, `--session`,
paired `--approval-authorities` and `--approval-policy`,
`--server-working-dir`, repeatable `--inherit-env`, `--timeout`, and
`--reconc-home` are operator launch inputs. The command and exact argv begin
after `--` and never come from repository policy or tool arguments.

The gateway exposes only validated downstream tools, supports MCP `2026-07-28`
and `2025-11-25`, and binds each tool-contract generation, executable identity,
policy identity, trusted context, budget, approval, and ledger lifecycle before
dispatch. It validates original arguments without coercion, inspects progress
and results, withholds unsafe output, bounds protocol and child-process
resources, and owns process-tree shutdown. Stdout is MCP protocol only. Only
tools configured to launch through this gateway are enforced; native framework
tools and direct downstream configurations remain unenforced.

In-flight calls stop when either their MCP request context or the gateway
shutdown context is cancelled. Shutdown gives pending approvals a bounded
terminalization attempt before draining calls and reports failures from both
stages.

LangChain uses its official external MCP adapter, not Reconc-authored adapter
code. After `reconc action key init --reconc-home
/private/operator/reconc-home`, the exact operator-pinned stdio shape is:

```python
from datetime import timedelta

from langchain_mcp_adapters.client import MultiServerMCPClient

client = MultiServerMCPClient({
    "reconc": {
        "transport": "stdio",
        "command": "/absolute/path/to/reconc",
        "args": [
            "mcp", "gateway", "/absolute/path/to/repository",
            "--server", "downstream",
            "--expect-lock-digest", "<64-lowercase-hex-lock-digest>",
            "--principal", "langchain-operator",
            "--role", "automation",
            "--environment", "production",
            "--credential", "database-writer",
            "--run", "run-2026-08-12",
            "--session", "session-001",
            "--approval-authorities", "/private/operator/approval-authorities.json",
            "--approval-policy", "default",
            "--timeout", "60s",
            "--reconc-home", "/private/operator/reconc-home",
            "--",
            "/absolute/path/to/downstream-mcp-server",
            "--downstream-flag",
        ],
        "session_kwargs": {"read_timeout_seconds": timedelta(seconds=75)},
    }
})
```

The repository path and every trusted-context flag are before `--`; only the
downstream executable and its argv follow it. Replace the lock placeholder with
the reviewed current lock digest. The explicit lower-provenance alternative
replaces that flag/value pair with `--allow-repository-managed-policy`.
Exactly one authority mode is required. `--credential` is a safe label, never a
credential value. Approval configuration is an all-or-none pair and must remain
outside repository and agent authority.

The pinned external proof uses Reconc `0.9.7`, MCP Go SDK `v1.7.0`,
`langchain-mcp-adapters==0.3.2`, `langchain-core==1.5.4`, MCP Python SDK
`1.29.0`, Python `3.13.14`, legacy protocol `2025-11-25`, and Go fixture format
`1`. It completes an externally signed legacy form approval; the pure-Go suite
additionally proves current protocol `2026-07-28` input-required approval.
The adapter, Python runtime, package lifecycle, and client sessions belong to
the consumer and are not product or release dependencies. The proof invokes
tools directly, uses no model or service, and denies runtime network access.

Native LangChain tools, a client entry that launches the downstream server
directly, and all alternate routes are unenforced. Reconc does not parse
arbitrary Python configuration. `reconc status . --json` reports the gateway
scope as `explicit_routes_only`, external configuration as `not_inspected`, and
bypass routes as `unenforced`; the MCP row in `reconc doctor . --deep` states
the same limit. Neither diagnostic certifies an external configuration.

### `reconc action evidence export [repo] --as-of RFC3339 [--since RFC3339] [--until RFC3339] [evidence flags] [--format json|markdown] [--output PATH]`

Build a deterministic local `reconc.action-evidence/v1` report from the current
compiled policy, the verified retained Action Ledger, read-only action state,
cryptographically reverified approval receipts, and optional Impact Lab
corpora. `--as-of` is mandatory canonical UTC; `--since` defaults to the Unix
epoch and `--until` defaults to `--as-of`. The inclusive start and exclusive
end select complete call lifecycles by their accepted-request timestamp.
`--as-of` cannot precede the latest retained record, so a current snapshot
cannot be presented as historical evidence.

The four built-in reviewed mapping packs reference SOC 2, GDPR, the HIPAA
Security Rule, and the EU AI Act by control identifier and primary-source URL.
They contain original Reconc technical mappings, source edition/date, review
date, exact evidence selectors, and known gaps. This is technical evidence and
mapping only. Organizational control design and operation, legal assessment,
external assurance, and every determination outside the selected Reconc
boundary remain external responsibilities.

Repeat `--corpus FILE` for strict privacy-bounded Impact Lab evidence. Supply
`--approval-authorities PATH` to reverify stored signed approval decisions
against the exact operator-owned registry identity. Repeat `--map-pack FILE`
for custom `reconc.action-control-map/v1` packs and choose exactly one
authentication mode for the complete custom set: one matching
`--map-pack-digest SHA256` per pack, or one `--map-pack-signature FILE` per pack
plus `--map-pack-authorities PATH`. Custom text cannot set status or override an
evidence fact.

JSON is the default; Markdown renders the same report. `--output` atomically
creates a new file and refuses an existing path. The command makes no network
call and does not create, repair, or mutate action state. Missing state,
unavailable authority, failed integrity, stale mapping review, incomplete
history, unsupported host coverage, or absent scenario dimensions remains an
explicit downgrade rather than an inferred pass.

### `reconc action evidence verify [repo] --as-of RFC3339 [--since RFC3339] [--until RFC3339] [evidence flags] [--json]`

Build and verify the same current report, including its deterministic
self-identity and exact status derivation. Text is the default; `--json` emits
the full report. The command exits successfully only when every selected
mapping is `covered`; `partial`, `missing`, and `not_evaluated` emit the report
and then fail. It verifies technical evidence, not an organization, legal
obligation, external framework assessment, or third-party assurance result.

The exact Draft contract, failure behavior, limits, protocol versions, and
package owners are in
[RECONC-0008](rfcs/RECONC-0008-go-only-action-plane.md). Gateway and evidence
commands are registered in dispatch, command metadata, completion, and manpage
generation.

---

## Bootstrap & inspection

### `reconc install-cli [--install-dir PATH] [--json]`
Atomically installs the exact running executable as the stable `reconc` user
CLI. The default is `$RECONC_INSTALL_DIR`, then `~/.local/bin` on POSIX or
`%LOCALAPPDATA%\Programs\Reconc\bin` on Windows. The command verifies checksum,
executable mode, and the binary actually resolved by bare `reconc`; it exits
non-zero with an exact PATH remediation when another binary shadows the
install or the directory is not visible to the current shell. Once PATH
identity passes, it atomically publishes
`$RECONC_HOME/install/receipt.json` under the same cross-process lock. Direct
installers record verified release ownership; an explicit source build records
source ownership. No receipt is published for an off-PATH binary.

### `reconc update [--channel stable|preview | --version VERSION] [--allow-downgrade] [--from-dir PATH] [--json]`
Runs the complete ownership-aware update transaction under the global
installation lock. Stable is the default; preview and exact version selection
are explicit and mutually exclusive. An exact-version receipt records that one
installation choice but does not pin future updates: a later bare
`reconc update` selects stable. Leaving a recorded preview channel still
requires an explicit `--channel stable`. Direct updates verify release identity,
bounded bytes, `SHA256SUMS`, embedded version, target, source provenance,
mandatory GitHub build-provenance attestation, and an actual candidate
`--version` smoke test before atomic replacement and receipt publication. A
fixed-size streaming transaction verifies size and SHA-256 while copying;
private candidate and rollback files replace full-binary memory buffers. A
release with the same semantic version is current only when the installed
receipt's artifact SHA-256 matches the selected release asset; different bytes
at the same version still take the verified replacement path. Any publication
failure retains or restores the previous binary. A downgrade requires
`--allow-downgrade`.
`--from-dir` disables network access and requires a strict
`release-manifest.json`, `SHA256SUMS`, complete regular-file inventory, the
selected asset's `.sigstore.jsonl` bundle, and `trusted_root.jsonl`.
Source builds return the exact path-qualified rebuild and `install-cli`
guidance. The current user flow has no separate check/apply step:
`reconc update` performs the verified check and either applies the selected
update or succeeds without mutation when already current. Hidden `check` and
`apply` forms exist only as compatibility aliases for older automation.

### `reconc uninstall [--purge-state] [--json]`
Removes only a globally owned installation. Direct and source removals require
a valid receipt and exact binary checksum, serialize with update and install,
and remove the receipt-owned binary plus receipt without touching any
repository.
`--purge-state` additionally validates that no unknown installation state
exists before mutation. The private installation lock
and its directory remain permanently so concurrent and future operations keep
one lock inode; unknown entries fail closed and remain. Repository policies,
TASKs, docs, hooks, bootstrap receipts, and runtime evidence are never removed.

### `reconc init [repo] [--profile existing|minimal|governed|advanced] [--pack NAME] [--hook KIND | --no-hooks] [--accept-managed-blocks] [--json] [--output PATH]`
Canonical non-interactive repository onboarding. Init installs and verifies the
running global CLI, inspects the repository, selects a deterministic profile,
builds and applies one bootstrap plan, compiles policy when needed, writes a
durable plan, a private rollback receipt, and the portable
`.reconc/install.lock.json` ownership receipt, verifies every selected artifact
and hook, and emits exactly one next action. A repository with no
Reconc control artifact defaults to `minimal`; a valid receipt reuses its
recorded selection. Partial, mature, ambiguous, or already governed state
without a receipt performs no repository write until `--profile` is explicit.
`--hook` replaces detected hooks, while `--no-hooks` selects none. Existing
content is never overwritten. Drift creates hash-addressed candidates and
marker-only changes require explicit checksum-bound
`--accept-managed-blocks`. Pre-1.0 parser compatibility may accept historical
aliases, but they are intentionally absent from help, completion, and this
primary contract. `--output` mirrors the exact text or JSON result to a file.

### `reconc bootstrap inspect [repo] [--json]`
Read-only discovery of canonical repository root; detected Go, JavaScript,
TypeScript, npm, pnpm, Yarn, Bun, Python, Rust, Shell, C/C++, Java, PHP, C#,
Next.js, Svelte/SvelteKit, Zig, Elixir, and PowerShell stacks; evidence paths,
package managers, repository markers,
same-directory package-manager ambiguities, review-only pack suggestions;
detected or installed agent platforms with generated/installed/executable/
configured truth, existing control paths, and
platform-correct repo-local binary resolution.

### `reconc bootstrap profiles [--json]`
List the four explicit profiles. `minimal` selects policy, a managed AI
orientation block, and runtime ignores. `governed` adds the TASK control plane,
documentation, `start.md`, and the stable hook wrapper. Both default to the
`default` and `agent` packs. `existing` owns only selected hooks, the wrapper,
and an optional stable binary. It requires an already fresh compiled policy,
accepts no packs, and never owns existing control-plane files.
`advanced` adds the complete governed control plane and is the selection point
for the embedded public advanced harness pack.

### `reconc bootstrap plan [repo] --profile existing|minimal|governed|advanced [--pack NAME] [--hook KIND] [--install-binary | --binary PATH --checksum SHA256 [--platform OS/ARCH]] [--output PATH [--replace-output]] [--json]`
Build a deterministic, versioned manifest of desired hashes, modes, current
state, conflict candidates, compilation need, and blocking issues. Packs and
hooks are repeatable explicit selections; detected suggestions are never
applied automatically. The command is read-only unless `--output` is supplied.
Plan files are create-only and an exact repeat is reported as unchanged.
`--replace-output` is an explicit stale-plan recovery path: it replaces only a
strictly valid Reconc plan for the same canonical repository and refuses an
arbitrary or cross-repository file.

### `reconc bootstrap apply --plan PATH [--json]` / `reconc bootstrap apply [repo] --profile existing|minimal|governed|advanced [selection flags] [--json]`
Apply an exact reviewed plan or build the same plan from explicit selections.
Before publishing the plan, apply atomically installs the exact running build
as the stable user CLI and verifies that bare `reconc` resolves to it. A
missing or shadowed PATH entry fails before any repository write and prints the
exact activation remediation.
Repository targets are create-only. Exact files remain unchanged; any drift
creates hash-addressed candidate files and prevents all normal target installs.
Stale plans fail before publication and print the full copy-paste
`bootstrap plan ... --replace-output` command reconstructed from the saved
selection. A process crash may leave the exact hidden stage for one target.
The next apply, under the same repository lock, removes or adopts that residue
only when its reserved name, regular-file identity, complete digest, plan, and
target all match. Ambiguous residue is retained and reported with manual
inspection guidance. Failures roll back only transaction-owned
files whose identity and checksum still match. Status `drift` exits 1.
Successful reports contain one compact created/preserved/drifted/skipped and
installed/configured/live summary, a tamper-evident receipt path, and exactly
one primary next command. Human output uses TTY-only ANSI color for decisions,
rule IDs, and OK/WARN/FAIL tags; JSON and redirected output never contain ANSI.
All copy-paste shell commands render each argument for the current shell family
and preserve quotes, backslashes, dollars, command substitutions, whitespace,
newlines, and trailing separators as literal argv. Successful apply retains
the current private plan/receipt pair and the two newest fully validated
historical pairs; unknown, malformed, partial, current, or symlinked entries
are never cleanup authority.

### `reconc bootstrap remove --plan PATH [--json]`
Reverse one applied plan using the portable repository receipt as the maximum
ownership authority. Exact receipt-owned files and generated artifacts are
removed; marker-delimited blocks are stripped while every outside byte is
preserved. User-owned policy sources, documentation, TASKs, agent instructions
outside managed blocks, and unrelated files remain. A private transaction
receipt may remove its own exact lifecycle records but cannot expand portable
ownership. Drift produces hash-addressed removal candidates, blocks the normal
mutation set, and any partial failure rolls back exact applied mutations.

### `reconc repo sync plan [repo] [--output PATH [--replace-output]] [--json]`
Build a deterministic repository-upgrade plan from the portable receipt and
the immutable policy and harness packs embedded in the running binary. The plan
binds the canonical repository, current and target product identities, receipt
digest, optional Git snapshot, exact current/receipt/desired hashes, pack
digests, migrations, action states, candidates, blocking issues, and its own
SHA-256 digest. Current and target policy and harness pack identities are
separate fields, so a reviewer sees the real pack delta rather than only a
target.

Planning is repository-read-only unless `--output` is supplied. It takes no
compile lock, writes no policy lock, audit record, command proof, Git lock, or
Git object, and sanitizes ambient `GIT_*` variables. The staged-index tree is
computed through an ephemeral object database that can read the repository
object database only as an alternate. A second snapshot must match the first
or planning fails as raced. `--output` is create-only;
`--replace-output` replaces only a strict sync plan for the same canonical
repository. Text output omits unchanged action rows, reports their count, and
ends in exactly one command that saves, applies, or resolves the plan. JSON
contains every action and identity.

### `reconc repo sync apply --plan PATH --digest SHA256 [--json]`
Apply one reviewed sync plan only when `--digest` exactly matches the plan and
the running product, repository, Git state, receipt, files, managed blocks, and
embedded pack bytes still match every saved precondition. Apply serializes
with init, direct bootstrap apply, resolution, recovery, and removal; mutates
only `replace-owned`, `update-managed-block`, and `create-owned` actions; and
atomically advances the portable receipt. Receipt-owned generated policy is
compiled into exact bytes in memory, including when the old lock is missing or
not a registered migration input. Source and receipt preconditions still
decide whether publication is allowed.

Before the first target mutation, apply writes the bounded strict
`.reconc/repository-sync-transaction.json` journal with exact before-image
bytes, before and after hashes and modes, created-path state, plan/product
identity, and a self-digest. The complete result must pass receipt, pack,
binary, artifact, generated policy, and hook verification before the journal
is removed. A normal failure rolls back exact after-images immediately.
Process interruption leaves the journal for explicit recovery.
`user-drift`, `orphaned-legacy`, `incompatible`, and `manual-review` are
non-mutating blockers.

### `reconc repo sync resolve --plan PATH --digest SHA256 --path RELATIVE --strategy keep-current|use-target|use-binary [--binary PATH --checksum SHA256 --platform OS/ARCH] [--json]`
Resolve exactly one non-mutable action from the reviewed plan. Resolution
requires the exact saved digest, re-plans under the repository lock, and writes
the portable receipt plus any selected bytes through the same durable sync
journal.

- `keep-current` preserves the current bytes, records the path as user-owned,
  and releases the matching Reconc component. A hook resolution releases its
  hook and activation ownership; a harness resolution removes the matching
  harness-pack identity. An invalid generated policy lock cannot be retained.
- `use-target` publishes the exact in-memory target bytes already bound by the
  plan and adopts them into the receipt.
- `use-binary` is only for a receipt-owned binary that the current platform
  cannot produce. `--binary`, lowercase exact `--checksum`, and
  `--platform OS/ARCH` are mandatory together. Reconc verifies the bounded
  regular file and stable target name before publication.

The resolution advances ownership evidence, not the product version. Its next
action is always a fresh sync plan; the final full apply normalizes an approved
cross-platform binary back to normal binary ownership.

### `reconc repo sync recover [repo] [--json]`
Recover one interrupted sync or blocker-resolution journal under the shared
repository lock. No journal returns `clean`. An exact complete after-image is
verified and returns `finalized`; a complete before-image or an exact mixture
of before and after images is restored and returns `rolled-back`. Any malformed
journal, changed path, unexpected type, checksum, mode, repository identity, or
self-digest returns `refused`, leaves the journal and external bytes intact,
and exits 1.

Recovery is idempotent. It removes only exact transaction-created files and
restores only exact transaction after-images. Empty parent directories are
preserved because their identity cannot be proven across a process crash.
While a journal exists, init, bootstrap apply, sync plan/apply/resolve/verify,
and removal fail closed with this exact recovery command.

### `reconc repo sync verify [repo] [--json]`
Read-only verification of the strict portable receipt, product version,
receipt-owned file hashes and modes, marker-delimited managed bytes, generated
artifacts, policy-lock freshness, selected hook configuration, embedded policy
and harness pack identities, and receipt-owned binary identity. A same-platform
owned binary must match the exact running executable. A cross-platform owned
binary is checksum-verified against the receipt and reported with both target
and running platforms. It never repairs drift. A pending sync journal returns
one failed recovery check. Any failed check exits 1 and reports one exact next
action; text prints failures plus aggregate pass/fail counts, while JSON
retains every check.

### `reconc bootstrap verify --plan PATH [--json]`
Read-only verification of every selected artifact hash and mode, candidate
drift, policy-lock freshness, governed TASK structure, selected hook activation,
selected binary checksum/resolution, and the exact running user CLI resolved by
bare `reconc`. Any failed check exits 1.

### `reconc adopt [repo] [--yaml | --json | --apply]`
Detects common tooling (JavaScript, TypeScript, npm, pnpm, Yarn, Bun, Python,
Rust, Go, Shell, C/C++, Java, PHP, C#, Next.js, Svelte/SvelteKit, Zig, Elixir,
PowerShell, CI, generated dirs) and emits matching-rule suggestions. Node
commands are proposed only for non-empty scripts declared in `package.json`
when one package manager is evidenced at that boundary; ambiguity is rendered
for review and no manager is guessed. Stack evidence can
also produce review-only manifested policy-pack recommendations. `--apply`
appends individual rules to `.reconc.yml` idempotently and never changes
`extends`. The complete read-merge-write operation shares the canonical
repository transaction lock with init and sync, revalidates the source snapshot
immediately before atomic publication, and refuses concurrent drift. Emitted
YAML scalars are deterministic and round-trip exactly through yaml.v3.

### `reconc extract [repo] [--from PATH] [--yaml | --json]`
Regex-heuristic scan of AGENTS.md / CLAUDE.md prose for concrete rule
hints (don't-edit / generated / run-before-commit / secrets / ci-green
patterns). Emits suggestions in the same format as `adopt`.

### `reconc doctor [repo] [--deep] [--json] [--output PATH]`
Default mode inspects discovery state only. `--deep` adds nine
diagnostic checks: hook-runtime compatibility, native Grok hook trust/loading,
Grok leader steering protocol/extension compatibility, lockfile freshness,
the compiled MCP side-effect contract and redacted observation state,
audit-log size, preset/template reference resolution, session-claim age, and
static rule conflicts. Deep mode exits 1 when any check is `FAIL`, 0 when all
rows are `OK` or `WARN`. The MCP row always states that gateway enforcement is
limited to explicit `reconc mcp gateway` routes, external client configuration
is not inspected, and direct/native routes are unenforced.
Freshness, parsed rules, conflict detection, and preset/template references
share one bounded identity-checked source snapshot. A source-load failure stays
independently reportable: reference diagnosis falls back to its narrower
bounded raw-source inspection instead of fabricating a result or suppressing
the other rows.
For Grok runtime probes, process failure is the primary diagnosis; an
oversized captured output remains an additional bounded fact and cannot hide
the failed process.

### `reconc doctor --global [--json] [--output PATH]`
Read-only global installation diagnosis. Reports the running version, resolved
and target binary identities, ownership manager, channel, receipt validity,
checksum identity, PATH shadows, release provenance, evidence, and one exact
next action. The shadow scan walks PATH in resolution order and, on Windows,
every name PATHEXT makes executable, so a `reconc.bat` ahead of the installed
`reconc.exe` is reported rather than missed. Broken or unreadable entries are
reported as structured warnings without hiding a later resolvable candidate;
binary checksum/read failures are structured failures rather than a false
outdated result. The stable JSON contract is
`schemas/v1/global-diagnostic.schema.json`. When installation state exists,
diagnosis takes a validated shared lock on the persistent lock inode without
creating, repairing, chmodding, or rewriting state. When state is absent it
creates nothing. A concurrently appearing lock triggers receipt-generation
revalidation without executing the diagnostic callback twice. Status is `healthy`, `unowned`,
`stale`, `shadowed`, `ambiguous`, or `invalid`; all except `healthy` and a
single deterministic legacy `unowned` installation exit 1. `--global` cannot
be combined with `--deep` or a repository operand. Classification is
monotonic: PATH shadowing or ambiguity adds its check detail but never replaces
a more severe stale-receipt or invalid-ownership result and remediation.

### `reconc status [repo] [--json] [--output PATH]`
One-line, read-only policy health summary. Missing, stale, malformed,
schema-drifted, migration-drifted, and non-portable current lockfiles surface as issues
with explicit `reconc refresh .` remediation. Useful as a session-start ping.
Text also names the external-MCP inspection limit. JSON exposes
`mcp_gateway_scope: "explicit_routes_only"`,
`mcp_external_configuration: "not_inspected"`, and
`mcp_bypass_routes: "unenforced"`; these are boundary facts, not inspection of
an arbitrary LangChain configuration.

### `reconc done [repo] [--require-clean-git] [--json]`
Evidence-complete task-finish gate. It binds current policy, the exact
HEAD/index/worktree candidate when Git is available, active-session evidence,
saved report integrity, current staged command proofs, and typed TASK completion
into a versioned, digested report. An unresolved explicit block for the same
candidate remains blocking until a later explicit non-blocking `check` or `ci`
decision clears it. Text mode prints every failed check and exactly one next
action; JSON emits the full completion report. Exit 0 = done, 2 = blocked,
1 = runtime/input error. `--require-clean-git` adds a clean-tree check.
Elapsed time never proves completion.

### `reconc proof [repo] [--format json|markdown] [--output PATH]`
Exports the current completion state as a deterministic, portable proof bundle.
The versioned contract binds build provenance, policy digest, candidate
fingerprint, HEAD/index/worktree identity, typed TASK state, completion checks,
current successful command receipts, required evidence, violations, exact
remediation, and any older unresolved block superseded by the current candidate.
JSON is the default; Markdown is rendered from the same verified typed data.
`--output` atomically mirrors the exact stdout bytes to a file. Absolute paths,
home/user identity, session IDs, prompts, transcripts, environment data, and raw
command arguments are excluded or redacted. Root redaction matches the
canonical absolute repository path only; it never globally replaces a common
repository basename such as `go` or `docs` inside evidence text. The public
`command_hash` is a
stable SHA-256 grouping key for the sanitized executable identity only; it
never hashes the full command or arguments and is not an offline
argument-guessing oracle. The command is read-only: it never
refreshes policy, runs missing tests, persists a decision, or converts missing
evidence into a pass. Exit 0 = pass bundle, 2 = blocked bundle emitted, 1 =
runtime/input/output error.

### `reconc proof verify FILE [--repo REPO] [--json]`
Strictly verify a received proof bundle offline. Reconc reads only a real
regular file up to the published 1 MiB limit and requires one valid UTF-8 JSON
object with no duplicate keys, unknown fields, missing required fields, null
required collections, or trailing values. It verifies the v1 schema and
format identity, bounded and canonical collections, decision/check
consistency, command-proof invariants, candidate and completion identities,
and the bundle self-digest. Object-key order and platform-independent JSON
whitespace do not affect validity.

`--repo REPO` additionally runs one fresh read-only completion evaluation and
compares the bundle's candidate fingerprint, policy lock, Git HEAD/index,
worktree trust and dirty paths, policy result, decision, and completion digest.
The report exposes only mismatch field names, not local values. Text is the
default; `--json` emits `reconc-proof-verification/v1`. Exit 0 = valid passing
bundle (and local match when requested), 2 = valid blocking bundle or local
candidate mismatch, 1 = malformed, unsupported, unsafe, or operational error.
The self-digest is unsigned: it proves bundle integrity, not author identity or
trusted publication provenance. Verification never uses a network service.

---

## Compile & evaluate

### `reconc refresh [repo] [--json] [--strict-conflicts] [--output PATH]`
Explicit policy refresh. Produces `.reconc/policy.lock.json` through the
deterministic compiler pipeline and is the only public remediation emitted by
read-only commands. Refresh captures its complete policy-source snapshot only
after acquiring the repository compile lock; concurrent refreshes cannot
publish an older pre-lock snapshot over newer policy. With
`--strict-conflicts`, exits 1 when any rule conflict
is detected. A `forbid_command` conflicts with `require_command` only when
their exact trigger scopes overlap and the forbid rule blocks every acceptable
required command; one blocked option among several valid alternatives remains
satisfiable.

### `reconc sources [repo] [--json]`
Read-only inspection of the effective policy source order. Reports each
source's kind, portable logical path, SHA-256 content digest, and inline block
location where present. It never emits source bodies, physical global-policy
paths, prompts, or other private source content. Invalid, escaping, symlinked,
or unreadable sources fail closed.

### `reconc check [repo] [--read PATH] [--write PATH] [--command CMD] [--command-success CMD] [--command-failure CMD] [--claim NAME] [--auto-claim] [--json] [--terse] [--format text|json|terse|sarif|junit] [--output PATH]`
The core policy evaluator. Exit 0 = pass/warn, 2 = block, 1 = error.
`--terse` emits ~50-token JSON optimised for hook-loop calls.
`--auto-claim` detects CI environment and auto-asserts `ci-green`.
Missing or stale lockfiles fail closed without writing and require
`reconc refresh .`.

### `reconc ci [repo] (--staged | --base REF [--head REF]) [--read PATH] [--command CMD] [--command-success CMD] [--command-failure CMD] [--claim NAME] [--auto-claim] [--json] [--format text|json|sarif|junit] [--output PATH]`
Git-aware check. Derives write paths from the working-tree index or a
`base..head` range instead of explicit `--write` flags. It inherits recorded
read paths, commands, and claims from the active agent session. In `--staged`
mode, successful-command rules accept only current `reconc exec --staged`
proofs bound to the exact HEAD and index; mutable active-session command
outcomes are not commit evidence. The CLI and its help reject explicit
`--command-success` and `--command-failure` flags with `--staged`; they remain
available for `--base`/`--head` CI ranges.
Missing or stale lockfiles fail closed without writing and require
`reconc refresh .`.

`check` and `ci` keep text as the default and retain `--json` and, for
`check`, `--terse` as compatibility aliases. `--format sarif` emits SARIF
2.1.0: observe findings are `note`, warn findings are `warning`, and block/fix
findings are `error`. Matched files use URI-escaped repository-relative
artifact locations with no fabricated line or column. `--format junit` emits
JUnit XML: observe/warn findings are successful cases with `system-out`,
block/fix findings are failures, and malformed policy, stale lock, Git, or
other operational evaluation failures are errors. A clean run emits one
successful policy-decision case.

Both machine reports include the decision, bounded remediation and matched
paths, candidate fingerprint/policy/worktree identity, dirty-path count, and
Git range metadata where applicable. They exclude absolute repository and home
paths, escape JSON/XML, URI, terminal-control, and workflow-command content,
cap text, paths, findings, and total output, and never invent source
coordinates. `--output` atomically writes the exact stdout bytes without human
prefixes. Defaults and exit codes remain unchanged; report generation performs
no network call.

Native consumer examples:

```bash
# GitHub Code Scanning: map RECONC_BASE_SHA to github.event.pull_request.base.sha.
reconc ci . --base "$RECONC_BASE_SHA" --head "$GITHUB_SHA" --format sarif --output reconc.sarif

# GitLab: declare reconc-junit.xml as artifacts:reports:junit.
reconc ci . --base "$CI_MERGE_REQUEST_DIFF_BASE_SHA" --head "$CI_COMMIT_SHA" --format junit --output reconc-junit.xml

# Jenkins, Azure Pipelines, or another JUnit consumer.
reconc ci . --base origin/main --head HEAD --format junit --output reconc-junit.xml
```

### `reconc impact [repo] (--candidate FILE | --pack NAME) [--corpus FILE | --fixture FILE] [evidence flags] [--delta-manifest FILE] [--format text|json|sarif|junit|github | --json] [--output PATH]`

Compile one additive candidate policy file or resolved preset in memory, then
compare the fresh current policy and candidate over explicit evidence fixtures
or imported replay corpora. Candidate policy may add repository rules and
`actions` tools, rules, and explicitly supplied defaults; duplicate tool or
rule ownership is still rejected by the production compiler. The command
writes no policy source, lockfile,
hook, session state, audit record, or TASK file, applies no suggestion, invokes
no model, and makes no network call. Candidate files are bounded regular UTF-8
files; their physical path is excluded from compiled provenance.

The deterministic report lists per-case decision and action changes, newly
blocking and warning rules, resolved violations, per-rule current/candidate
violation-match counts, rules unmatched in this corpus, and structural
evaluation-unit deltas. These units count rules, normalized evidence,
pattern-comparison opportunities, and external-rule boundaries; they are
stable comparison evidence, not wall-clock timing. An unmatched rule is never
reported as dead or safe.

Format 2 adds strict discriminated `repository`, `action_pre`, and
`action_post` cases. Every action case binds transport, server label and
fingerprint, tool and tool-contract digest, phase payload, trusted context with
provenance, principal, credential labels, evaluator state, completeness, and
an exact current expectation. The expectation requires decision, reason, tool
ID, ordered rule IDs, cache eligibility and reason, completeness, phase
outcome, and any failure code. An optional approval assertion also requires
the exact status, redacted identity, call-specific required-approval identity,
and any explicit pending, approved, rejected, expired, cancelled, unavailable,
malformed, or replayed transition. Snapshot and transition coverage are tracked
separately. The optional ledger assertion binds recording mode, the
phase-derived `pre_decision` or `result_inspection` event, required state,
tool-identity mode, and exact canonical selected-field declarations. Reconc
reports ledger-policy changes as a separate delta. Reconc executes both sides through the
production compiler, runtime plan, normalizer, and evaluator. A malformed,
oversized, stale, unsupported, incomplete, or expectation-mismatched case fails
instead of becoming a skipped scenario.

Action payloads are explicit synthetic, minimized fixtures, never captured live
arguments or complete downstream results. The exporter removes recognized
secret-shaped values, physical paths, oversized scalars, and unsafe metadata,
and replaces an over-limit payload with one canonical safe surrogate that
preserves the production `limit_exceeded` path without retaining source bytes.
Format 2 carries exact payload-free inspection evidence and uses the production
detector pack to redact recognized secret and PII shapes. It cannot infer that
an otherwise ordinary opaque value is confidential. Do not author scenarios
from live sensitive data.

Action comparison reports every decision change plus newly allowed, warned,
approval-required, and blocked changes and reason, rule-trace, cache,
phase-outcome, completeness, tool-identity, approval-state, and failure deltas.
`newly_allowed` means the decision became less restrictive, including
`block -> require_approval`, `block -> warn`, and `warn -> allow`;
`newly_blocked` means the candidate decision became an exact block. A phase
outcome change is reported independently and never reclassifies a warning or
approval requirement as a block. Any newly
allowed or newly blocked action exits 2 after rendering unless
`--delta-manifest` supplies an exact current review. Each manifest entry binds
case ID and identity, delta class, current and candidate outcomes, candidate
lock digest, rationale, and either canonical UTC expiry or permanent status.
Duplicate, wildcard, orphaned, partial, stale, expired, or digest-mismatched
review never passes the gate.

The manifest proves that an exact delta was acknowledged; it does not prove who
reviewed or authored the file. Reviewer identity and separation of duties must
come from repository governance such as protected reviews or signed commits.
Reconc never treats the manifest as a human-approval authority.

Compact text and full typed JSON render from the bounded impact report; JUnit,
SARIF, and GitHub render from its bounded 1,024-finding, 8 MiB CI projection.
Every format carries stable case IDs. Selected action values are removed before
replay and retained only as source, pointer, category, length, item count,
provenance, and an optional trusted identity. Raw credentials, headers, tokens,
physical paths, and full results are never report content. Format-1 corpora
migrate deterministically to repository cases; new output is always format 2.
A corpus is limited to 64 MiB and 10,000 cases, a full JSON report to 64 MiB,
and a delta manifest to 8 MiB.

Repository cases bind both evaluators to one non-Git filesystem observation.
Before replay, Reconc records stable identity, metadata, link target, and
regular-file content digest for at most 100,000 entries below the repository;
after replay it recaptures that inventory. Any identity, content, type, size,
timestamp, entry-set, or root drift aborts the comparison instead of becoming
a policy delta. `.git` internals are excluded because the compiled evaluators
do not use them as repository evidence.

Policies containing `require_script` are refused before replay because an
arbitrary repository script cannot be proven side-effect-free.

### `reconc impact export [repo] (--session | evidence flags) [--complete CLASS] [--case-id ID] [--output PATH]`

Export one strict replay corpus from explicit evidence and optionally the
active session's normalized read, write, command, outcome, and claim evidence.
The format stores no prompt, file body, command output, or raw session
identifier. Secret-like command arguments and claims are replaced with
`<redacted>`; affected event classes become incomplete. `--complete`
accepts `read`, `write`, `command`, `command_outcome`, `claim`, or
`all` and is a declaration about capture coverage, not an inferred claim.
Every corpus is bounded, deterministically ordered, self-identified, strict
about unknown/duplicate fields, and refused after mutation. `--output`
publishes the exact stdout JSON atomically.

`impact export` emits repository evidence cases only. Action corpora are
explicitly authored because a capture cannot infer trusted transport,
principal, credential, state, or expectation data. A full portable action-case
shape is committed at
`harness/template/audits/testdata/action-impact/corpus.json`.

### `reconc policy author [repo] (--candidate FILE | --detected) [--target policies/NAME.yml] [--corpus FILE | --fixture FILE] [evidence flags] [--apply] [--json]`

Run one guided validate, explain, and adopt workflow for a repository-owned
policy fragment. `--candidate` reads one bounded UTF-8 non-symlink file;
`--detected` converts the existing deterministic `adopt` rule recommendations
into a candidate. Detected pack recommendations remain visible for review but
are never inserted into a policy fragment or `extends` automatically.

Preview first validates the candidate against the embedded current
`policy-config` JSON Schema without network access, then runs the real source
loader, preset and template expansion, parser, semantic validation, conflict
detection, compiler, and canonical lock encoder. Text and
`reconc.policy-author/v1` JSON report schema success and compiler success
separately, plus effective packs, normalized rules, source provenance, rule
kind counts, warnings, conflicts, candidate SHA-256, and predicted source
identity. Candidate source bytes and the physical repository path are not
report fields. Any conflict makes the candidate not ready and prevents apply.

Supplying a corpus, fixture, or explicit evidence flag also runs the same
privacy-bounded Impact Lab comparison as `reconc impact`; it requires a fresh
current lockfile and preserves all replay, redaction, filesystem-snapshot, and
`require_script` refusal contracts. Without replay evidence, the command does
not invent an impact claim.

Validation and explanation never mutate the repository. `--apply` is the only
non-interactive mutation authority. In text mode on a real terminal, omitting
`--apply` prompts once with a default of no. Redirected input never prompts,
and `--json` never prompts even when attached to a terminal. Decline or EOF
leaves target and lock bytes and identities unchanged.

The target defaults to `policies/reconc-author.yml` and must be one direct
repository-owned `policies/*.yml` or `policies/*.yaml` file; absolute,
traversing, nested, linked, or reparse-resolved targets fail closed. Apply
re-prepares the exact candidate under the canonical repository transaction
lock, rejects source or candidate drift, atomically writes only that target,
runs the production compiler, requires the exact previewed lock bytes and a
fresh runtime validation, and restores its target and lock snapshots on any
post-publication failure. Existing `reconc adopt` and `reconc impact` commands
remain available for their narrower compatibility workflows.

### `reconc exec [repo] [--staged] [--shell] -- COMMAND [ARG ...]`
Execute a command from the repository root and record its real exit status in
the active Reconc session when one exists. `--staged` additionally requires no
tracked-unstaged or untracked paths, verifies that the command leaves HEAD,
the index, and the working tree unchanged, then atomically publishes a bounded
SHA-256 receipt outside the repository. `--shell` accepts one literal command
for platform-shell syntax; direct argv execution is the default. Failed
commands propagate their child exit code and never publish a proof. Staged
snapshot capture recognizes an actual Git index-lock file together with a
typed Git command failure, retries with capped backoff under one five-second
total deadline, and then fails explicitly; individual retries cannot each
consume the normal 30-second Git command timeout.

### `reconc assert <rule-id> [repo] [--var K=V] [--read PATH] [--write PATH] [--command CMD] [--command-success CMD] [--command-failure CMD] [--claim NAME] [--json]`
Evaluate exactly one rule, ignoring the rest of the lockfile. Useful
for single-rule workflows and template-variable rule tests.

### `reconc can write <path> [repo] [--why] [--json]`
Ultra-terse yes/no for one proposed repository write. Prints `yes` or
`no: <rule> <action>`. Exit 0 = yes, 2 = no, 1 = error.

### `reconc diff <lockfile-a> <lockfile-b> [--json]`
Strict structural comparison of two validated compiled lockfiles. Duplicate
rule identities, malformed envelopes, and invalid digests fail before diffing.
Reports added, removed, and changed rules, explicit rule-provenance moves,
source inventory additions/removals/moves, source ordering, and every changed
lockfile envelope field with its semantic, provenance, generated, or
unsupported classification. Default-mode and source-digest summaries remain
visible for compact review. Order-sensitive fields retain their order; only
explicitly order-insensitive rule fields are canonicalized as sets.

---

## Explain & remediate

### `reconc explain [repo] [--read PATH] [--write PATH] [--command CMD] [--claim NAME] [--format text|markdown] [--json] [--output PATH]` / `reconc explain --report-file PATH [output flags]`
Render the check report in human-readable form. Source can be fresh
inputs or a saved `CheckReport` JSON.

### `reconc fix [repo] [--read PATH] [--write PATH] [--command CMD] [--command-success CMD] [--command-failure CMD] [--claim NAME] [--json] [--output PATH]`
Structured remediation plan per violation, with per-kind steps,
suggested commands / claims, and files-to-inspect.

### `reconc next [repo] [--read PATH] [--write PATH] [--command CMD] [--command-success CMD] [--command-failure CMD] [--claim NAME] [--json] [--output PATH]`
With explicit evidence flags, runs a focused evaluation and emits only its
highest-priority remediation.
With only a repository path, loads the latest persisted blocking decision and
replays its top remediation when the repository/policy/session candidate is
still current. If it is stale, Reconc reconstructs the exact original
`reconc check` command including success/failure evidence flags instead of
claiming that no remediation is needed. When no persisted block exists, the
command succeeds with `No remediation needed.` or JSON
`{"state":"clear","remediation":null}`.

### `reconc why <rule-id|action|mcp> [repo] [--json] [--terse]`
Prints one full rule from the lockfile (kind, mode, message, paths,
provenance, DEPRECATED label if set). The reserved selector `action` prints the
canonical action format, exact defaults, provenance, legacy lowering origin,
selectors, decisions, failure/cache policy, and operand kind and size while
redacting every operand value. The reserved selector `mcp` prints the derived
MCP compatibility view. `--terse` emits only the compact rule or plan summary.
`--json` and `--terse` are mutually exclusive.

Use `reconc why action .` for the complete canonical plan and `reconc why mcp
.` for its legacy host-MCP subset. Neither output exposes operand values,
server locators, arguments, prompts, results, or command bodies.

---

## Packs & wiring

### `reconc preset list [--json] [--output PATH]` / `reconc preset show <name> [--json] [--output PATH]`
Built-in (`default`, `agent`, `docs-sync`, `release`, `strict`,
`go-assurance`, `bun-assurance`, `npm-assurance`, `pnpm-assurance`,
`yarn-assurance`, `typescript-assurance`, `python-assurance`, `rust-assurance`,
`shell-assurance`, `cpp-assurance`, `java-assurance`, `php-assurance`,
`csharp-assurance`, `nextjs-assurance`, `svelte-assurance`, `zig-assurance`,
`elixir-assurance`, `powershell-assurance`) + user
presets from `$RECONC_HOME/presets/*.yml`. User-authored presets override bundled
ones on name collision. JSON listing includes each validated manifest and its
declared capabilities when present.

### `reconc template list [--json]` / `reconc template show <name> [--json]`
Rule shape templates (`tests-follow-source`, `docs-follow-code`,
`no-generated-writes`, `ci-green-before-merge`, `authority-change-approval`,
`custom-gate-on-change`, `local-secret-state-read-only`, `verified-change`).
User overrides in `$RECONC_HOME/templates/*.yml`.
`RECONC_HOME` and user-home resolution failures are explicit. Existing
`presets` or `templates` roots must be real directories, never symlinks; Reconc
does not fall back to a CWD-relative state path.

### `reconc hook generate <git-pre-commit|claude-code|codex|github-copilot|cursor|opencode|devin-cli|antigravity|kilo|grok|omp|pi|zcode|kimi-code> [--json] [--output PATH]`
Emit the hook artefact content without writing to disk.

### `reconc hook install <git-pre-commit|claude-code|codex|github-copilot|cursor|opencode|devin-cli|antigravity|kilo|grok|omp|pi|zcode|kimi-code> [repo] [--force] [--json] [--output PATH]`
Write the hook into the repo. Git pre-commit uses Git's active hooks path
(`core.hooksPath`, otherwise `.git/hooks`), updates a Reconc-owned hook
idempotently, preserves inactive legacy hooks, requires `--force` for a foreign
active hook, and always refuses shared external targets. Claude Code and Codex
JSON configs are merged non-destructively. GitHub Copilot owns only
`.github/hooks/reconc.json`; a foreign file at that path is never overwritten,
including with `--force`. Cursor writes `.cursor/hooks.json`; OpenCode writes
`.opencode/plugins/reconc.js`; Devin merges `.devin/hooks.v1.json`;
Antigravity merges the top-level
`reconc` hook definition into `.agents/hooks.json`, preserving
non-reconc hook groups; and Kilo Code owns
`.kilo/plugin/reconc.js`. Grok Build owns the dedicated
`.grok/hooks/reconc.json` file. Oh My Pi owns only the dedicated
`.omp/extensions/reconc.ts` file. Pi owns only
`.pi/extensions/reconc.ts`. ZCode merges Reconc-owned process entries into
`.zcode/config.json` under `hooks.events` and enables that hook section while
preserving unrelated settings, hook events, and commands. These integrations
preserve every sibling project hook or extension file, and Pi installation
never changes the host trust store.
Every wrapper-dependent platform installs or verifies the exact executable
repo-local wrapper in the same operation. If the exact stable current-host
binary exists, installation also publishes the validated one-line
`tools/reconc/bin/hook-target` receipt. The wrapper executes that target on its
normal path without platform discovery, directory scans, version-glob
expansion, or `PATH` search. Missing, invalid, symlinked, and non-executable
targets enter the existing unambiguous recovery resolver. Transactional
bootstrap and repository sync own the receipt together with the wrapper and
binary; cross-platform plans omit it. Codex installation also manages its
`[features].hooks` activation: an explicit user-owned `false` requires
`--force`, and forced activation records the exact original line so uninstall
can restore it. Partial wrapper/target/activation outcomes are reported
explicitly with one recovery command. JSON always includes `success`; a partial
failure also includes the retained partial-state fields and `error` before the
command exits unsuccessfully. Merge-based installers revalidate the exact
source bytes, filesystem identity, mode, size, and modification time immediately
before publication, and refuse concurrent edits or replacements. Managed plugin/files refuse unrelated existing
content unless `--force` is passed. The dedicated GitHub Copilot, OMP, and Pi paths
never overwrite a foreign file, including with `--force`.
ZCode installs all seven native events: `SessionStart`, `UserPromptSubmit`,
`PreToolUse`, `PermissionRequest`, `PostToolUse`, `PostToolUseFailure`, and
`Stop`. It uses direct process arguments, native millisecond timeouts, exit 2
for fail-closed route errors, and the host's maximum of three consecutive Stop
blocks. Because ZCode snapshots hook configuration at session start, restart
the session after install or uninstall. All non-Git targets are resolved through operating-system filesystem identity
and must stay inside the selected repository. Unix symlinks, Windows reparse
points and 8.3 aliases are handled before containment. Forced malformed-config
backups are private, content-addressed, create-only, and durably synced before
publication.

`kimi-code` is the deliberate exception to the repository target model:
install it without a repo argument. Reconc merges one exact managed block with
all 16 documented Kimi Code events into
`$KIMI_CODE_HOME/config.toml` (default `~/.kimi-code/config.toml`) under a
cross-process lock, preserves unrelated TOML and file mode, refuses invalid or
drifted configuration, and backs up the exact prior file before a forced
managed-block replacement. Kimi is excluded from `init`, bootstrap hook
selection, and scaffold sync because its configuration is user-global. The
generated global commands discover the current repository and silently no-op
outside an explicit Reconc repository.

### `reconc hook uninstall <git-pre-commit|claude-code|codex|github-copilot|cursor|opencode|devin-cli|antigravity|kilo|grok|omp|pi|zcode|kimi-code> [repo] [--json] [--output PATH]`
Remove only generator-exact dedicated artifacts or canonical Reconc-owned JSON
entries while preserving unrelated hooks and configuration. Modified or
ambiguous Reconc-looking entries fail closed. Codex removes only its managed
activation block and restores a force-replaced explicit `hooks = false` line
byte-for-byte. The shared repo-local wrapper is deliberately preserved because
another platform may still depend on it. `kimi-code` accepts no repo argument
and removes only the exact current Reconc marker block from the global TOML;
modified managed content is never deleted.

### `reconc hook status [repo] [--json]`
Validate registered artifacts and activation requirements. States are
`absent`, `installed`, `configured`, `degraded`, `shadowed`, and `unsupported`.
The command checks malformed, incomplete, non-executable, or drifted managed
artifacts, the repo-local wrapper, Codex's enable flag, Git `core.hooksPath`,
Kilo Code pure mode, legacy Kilo Code plugin placement, Grok's native
project-hook artifact, OMP's generator-exact ExtensionAPI module, and Pi's
generator-exact extension plus project trust. Each platform generator runs once
per status inspection. The target bytes and mode come from one stable snapshot,
and the shared wrapper is inspected once for the complete multi-platform report
rather than reopened per platform. Pi is `configured` only when the
canonical root has a saved `true` trust entry or `defaultProjectTrust` is
`always`; Reconc never mutates either setting and reports `pi --approve` as the
one-run alternative. GitHub Copilot status verifies its generator-exact
repository hook and wrapper but reports live execution separately. Static Grok
status cannot prove folder trust; `doctor
--deep` additionally runs `grok inspect --json` when the artifact exists.
Each platform reports separate `generated`, `installed`, `executable`,
`configured`, and `live` booleans plus one exact remediation whenever static
configuration is incomplete. It also reports registry-derived
`surface_events` for documented per-surface route sets, complete-artifact
`expected_events`, and rate-limited `live_events`, `unseen_events`,
`last_seen`, and `last_event` runtime evidence separately from static
activation state. Source-free metadata for observational routes is exposed
under `observations` in JSON and as deterministic `observation` lines in text;
OMP `user_python` reports only count, latest timestamp, repository-relative
working directory, code byte size, and context-exclusion flag. `configured`
proves only that the host can discover a
complete static artifact. Codex accepts
`hooks = true` under `[features]` or the equivalent root dotted key
`features.hooks = true`, rejects root-level `hooks=true` and duplicate or
misplaced declarations, and has no
separate failed-tool route; failed Bash outcomes are inferred from
`PostToolUse`. OpenCode and Kilo Code preserve complete post-tool output,
deduplicate terminal tool errors from `message.part.updated`, and route user
prompts plus pre/post-compaction lifecycle. Their continuation is inferred from
`session.idle`, not a synchronous native Stop gate. OMP uses native awaited
`session_stop`, with at most eight continuation turns, and routes `tool_call`
and `user_bash` blocking separately from observational approval events. A shell
command the user types reaches the same decision as one the agent requests.
Python the user runs through OMP's own prefix cannot: the policy vocabulary
reads shell grammar, and Python source is not a command line. Because that code
can start a shell and so step around the `user_bash` decision, Reconc persists
a bounded, source-free observation in hook-liveness state: a saturating
execution count plus the latest timestamp, repository-relative working
directory, code byte size, and context-exclusion flag. It never stores the
code itself. Treat the user-shell gate on OMP as covering shell commands only. Reconc emits native Grok
`Stop` block JSON directly in the normal TUI without a leader, but treats it as
synchronously enforced only when the installed Grok hook guide advertises
blocking Stop decision control. Otherwise optional leader steering over the
Unix socket or Windows named pipe provides bounded same-session continuation.
Deep doctor reports native Stop capability separately from route
loading; its optional leader probe requires protocol
version 1 and a recognized `_x.ai/interject` response, not just a successful
register handshake. It also requires project-owned inspect metadata and exact
route command tokens; prefix collisions do not satisfy route coverage.
Pi maps `agent_settled` to fail-open asynchronous continuation, caps requests at
ten per session, and reports requested delivery without inventing an API
acknowledgement. Native `tool_call` and `user_bash` are blocking; result,
lifecycle, compaction, and shutdown routes are observational. Pi exposes no
native permission, MCP discriminator, post-user-shell result, or synchronous
Stop decision event.
ZCode status verifies the nested project configuration, `hooks.enabled`, all
seven exact managed process entries, the shared wrapper, and `sh` availability.
Hard pre-tool blocks use exit 2, permission denials use the native decision
object, and Stop uses native block JSON; malformed fail-closed requests use
exit 2. Host timeouts remain ZCode-owned fail-open boundaries. ZCode generic
tool payloads do not distinguish an unconfigured MCP call from a built-in or
custom tool.
OpenCode, Kilo Code, OMP, and Pi keep one session-owned Reconc stdio worker per
plugin repository. Requests are bounded format-1 JSON frames and retain the
registry route timeout and error policy. Startup, crash, or protocol failure
falls back to one-shot execution within the remaining route budget; cancellation
and timeout kill the child. Session shutdown or parent stdin closure prevents an
orphan. This is an internal transport optimization, not a daemon or public
network API. The worker owns an immutable typed policy-plan cache: unchanged
lock bytes reuse the decoded and indexed plan, while every request still
recomputes the bounded source-bundle identity. Lock drift rebuilds; source drift
fails closed until explicit refresh.
Claude Code, Codex, Cursor, OpenCode, Kilo, OMP, Pi, and ZCode rows additionally expose a redacted `mcp` object:
the configured unclassified mode, exact tool/fingerprint/effect mappings,
classified and unclassified observation counts, denials, failures,
strict-unavailable observations, and whether strict unclassified deny exists
on that surface. Locator strings, arguments, prompts, results, and command
bodies are never reported. Cursor can enforce strict unclassified deny through
its dedicated native MCP pre-hook, and Claude Code and Codex through the
`mcp__<server>__<tool>` namespace group Reconc installs on their tool events.
OpenCode, Kilo, OMP, Pi, and ZCode generic tool hooks cannot
distinguish an unconfigured MCP call from a built-in/custom tool, so status
reports that limitation without claiming enforcement.
Default text reports seen/expected counts and the last event without listing
every unseen route; the full unseen-event enumeration remains in `--json`.
Kimi Code status validates its global TOML block and bare `reconc` PATH
identity but keeps configuration separate from route liveness. Kimi blocks
only through host exit code 2 on `PreToolUse`, `UserPromptSubmit`, and `Stop`;
other non-zero exits, crashes, and host timeouts are Kimi-owned fail-open
boundaries.

### `reconc hook verify [--host KIND [--surface SURFACE]] [--json]`
Run the registry-owned offline hook verifier. The default covers every exact
host surface; `--host` limits it to one platform and optional `--surface`
limits that platform further. Reconc creates and removes an isolated temporary
Git repository, an isolated Reconc state root, isolated Kimi and Pi homes, and
a blocking synthetic policy. It then verifies artifact generation,
installation/configuration, the generated shell or Bun transport, a real
policy denial, the platform-native response adaptation, and route duration.
It invokes no host, model, account, cloud service, or caller repository.
Verification runs inside a dedicated child process with an explicit isolated
environment; parallel verification cannot mutate or restore the parent
process environment.
Native Windows discovers a POSIX `sh` transport from `PATH` (normally Git for
Windows) and uses platform-correct file URLs for generated Bun adapters; a
missing shell produces an explicit incomplete result.

Offline `configured`, `discoverable`, and `synthetic_enforced` facts refer only
to that disposable repository. `loaded`, `observed`, and `enforced` remain
false, and every expected live route remains in `unproven_events`. Bun is used
only when present to execute generated OpenCode, Kilo, OMP, and Pi adapters; a
missing Bun produces an explicit incomplete result rather than a pass. Text and
JSON are ordered by the registry matrix and never include the temporary path,
payload, tool arguments, prompt, output, or session identity.

### `reconc hook verify --live --host KIND --surface SURFACE --allow-authenticated [--json]`
Prepare one disposable operator-driven live probe. Both the exact surface and
`--allow-authenticated` approval are mandatory. Reconc installs the selected
artifact and a temporary capture shim, prints the disposable path and exact
exercise, and waits; it never launches or authenticates the host. The shim
preserves host stdout, stderr, and exit behavior while retaining only route
identity, sorted top-level field names, result class, exit code, and duration.
Raw payloads and outputs are never written. Missing host delivery, operator
EOF, partial route coverage, absent negative enforcement, unsupported direct
transports, unavailable tooling, and a missing executable for a locally
discoverable host surface remain incomplete or degraded. `host_available` is
emitted only where Reconc has an exact local executable-discovery contract;
UI- and cloud-only surfaces remain unclaimed instead of guessed.

`scripts/tests/host-integration-probe.sh` is a compatibility entrypoint that
normalizes historical Cursor surface names and delegates to this command. It
has no independent matrix or evidence logic.

### `reconc hook bridge <runtime> <host-event> [repo]`

Read one bounded host JSON object from stdin, select the exact declarative
manifest in `.reconc/runtimes/<runtime>.json`, normalize only its declared JSON
Pointers, and dispatch the neutral event through the existing session and
policy engine. The command emits one bounded, versioned neutral response.
The inner custom-runtime boundary accepts at most 8 MiB, 32 nesting levels,
65,536 object members, 65,536 array items, 13 declared mappings, and 2 MiB of
retained selected JSON. Go 1.27 `jsontext` rejects duplicate names, invalid
UTF-8 or numbers, malformed structure, and trailing data; one pointer trie
shares ancestor traversal and `SkipValue` discards unselected subtrees without
building a complete generic host tree.
Stale locks, unknown routes, malformed mappings, and fail-closed route errors
block; missing host guarantees return `unsupported` without claiming stronger
enforcement than the host provides.

### `reconc hook conform <manifest.json> <fixtures.json> [--json]`

Validate a custom-runtime manifest and its offline fixture suite without
executing adapter code or using the network. A passing suite must exercise
request normalization, response decisions, timeout and operational-failure
policies, liveness identity, and privacy-marker exclusion.

### `reconc hook sync-scaffold <repo-root-scaffold> [--json]`
Regenerate source-controlled hook artifacts inside a template
`repo-root-scaffold`: `.githooks/pre-commit`, `.codex/hooks.json`,
`.github/hooks/reconc.json`, `.cursor/hooks.json`, `.agents/hooks.json`,
`.claude/settings.json`, `.opencode/plugins/reconc.js`, `.devin/hooks.v1.json`,
`.kilo/plugin/reconc.js`, `.grok/hooks/reconc.json`,
`.omp/extensions/reconc.ts`, `.pi/extensions/reconc.ts`, and
`.zcode/config.json`. This keeps scaffolded repos on the
same generator truth as `reconc hook install`; do not copy these files
from a source-specific harness. Reconc preflights containment for every target
before the first write, preventing both parent-symlink escapes and partial
scaffold updates.

### `reconc hook claim <repo> <claim-name> [--session ID] [--json] [--output PATH]`
Assert a workflow claim (e.g. `ci-green`). Written to the session
state consulted by later hook-runtime checks and `ci` calls. `--session`
selects an exact existing session instead of resolving the active pointer.

### `reconc hook evidence-status [repo] [--json]`
Read-only inspection of persistent project evidence taint. Reports the exact
overflow or chain-integrity cause, affected limit, active-session state, and
operator resolution token without clearing or certifying the abandoned
evidence window.

### `reconc hook evidence-resolve <repo> --token TOKEN --reason TEXT [--json]`
Explicitly abandon one reviewed tainted evidence window. The command requires
no active session, an exact current token, and a bounded operator reason. It
writes an immutable resolution receipt before clearing the live taint; a later
session must reproduce every required proof.

---

## Workflow maintenance

### `reconc agent-intro [--section NAME] [--list-sections] [--json]`
Prints the embedded reconc integration guide. Section lookup is
case-insensitive substring match. A selected section ends at the next heading
of equal or higher rank, so a leaf heading cannot absorb later parent or
sibling sections. Section listing remains top-level only.

### `reconc audit tail [repo] [-n N] [--rule ID] [--since RFC3339] [--decision pass|warn|block] [--json] [--compact]`
Tail the decision log only after verifying its complete retained SHA-256 chain,
contiguous sequence, archive order, and detached head. Filters combine.
`--compact` emits `<ts> <event> <decision> <rule_id>`.

### `reconc audit stats [repo] [--json]`
Aggregate summary: totals, latest decision and blocking count, last-hour
activity, blocking events in the last 24 hours, by-decision, by-event, and top
rules. Malformed, missing, reordered, truncated, or modified retained evidence
fails instead of producing partial statistics.

### `reconc audit export [repo]`
Raw JSONL dump on stdout for external tooling. Audit tail, stats, and export
verify and read the two bounded archives plus the live file in chronological
order.

### `reconc audit verify [repo] [--json]`
Verify every retained decision record and `.reconc/audit.head.json` without
mutation. The report includes retained record count, first and last sequence,
and final digest. The audit writer alone owns its 2 MiB live file plus two
archives; generic retention verifies this ring and never compacts or rewrites
chained evidence.

### `reconc run on [repo] [--force] [--json]` / `reconc run off [repo] [--json]`
AI-operated switch scoped to one repository, not the whole machine. It routes
continuation through all thirteen registered agent runtimes. Claude Code, Codex,
GitHub Copilot, Cursor, Devin CLI, Antigravity CLI, Kimi Code CLI, OMP, and ZCode expose
synchronous Stop gates; OpenCode and Kilo Code use inferred `session.idle`
adapters whose host boundary is best-effort and fail-open. Reconc emits exact
Grok Stop block JSON
without a leader; synchronous stock-TUI enforcement and its continuation bound
are accepted only when the installed Grok guide explicitly advertises the
contract. Passive Stop sessions can be steered through `_x.ai/interject` over
the Unix socket or Windows named pipe. Eligible leader Stops use strict
continuation before policy evaluation.
Pi uses a separate inferred `agent_settled` adapter whose asynchronous
`sendUserMessage` request is fail-open and has no host delivery
acknowledgement.
Only successfully delivered interjections consume the 32-attempt cap;
transport or protocol failures do not. The cap resets on material progress, a
changed block, or a clean Stop. Before enabling, `run on` validates live policy
sources, the compiled lockfile, and an executable typed TASK disposition. It
fails without mutating state and gives one exact remediation; `--force` is the
explicit exceptional override. Typed `continue` and `claim` states
continue: `Current: none` or an empty Active section still claims queued
executable work. Complete or absent state disables the switch after terminal
gates; blocked state reaches terminal Stop without silently disabling it, and
invalid state fails closed. An explicit interrupt or six repeated no-progress
continuations in the same session releases only that invocation; concurrent
sessions have independent counters and progress fingerprints. Strict Grok
Stops do not consume this six-event guard: their applicable safety bound is 32
successfully delivered leader interjections. Ordinary prompts, session
end, runtime changes, and application restarts never mutate the durable switch.
`off` is the only normal manual disable action. Both commands are idempotent
and log only actual switch transitions. The agent executes these
commands itself; it must not ask the user to operate Reconc.

### `reconc run reset [repo] [--json]`
Recovery-only replacement of `state.bin` with an identity-bound clean disabled
state. Use the exact command printed after corrupt, unsupported, or foreign-root
state errors. It preserves `decisions.jsonl`, archives, and every unrelated run
artifact; it never enables autonomy.

### `reconc run status [repo] [--verbose | --json]`
One-line or JSON snapshot of run mode plus typed TASK disposition:
`enabled`, `task_disposition`, current TASK/Sub-Task, open count, no-progress
state, blocker, and reason. Invalid TASK state is reported as disposition
`invalid`; Stop then fails closed with the validation error. The default line
and JSON schema remain stable. `--verbose` adds complete TASK, blocker, and
latest bounded-decision context.

### `reconc run log [repo] [-n N] [--branch B] [--session S] [--follow | -f] [--json]`
Render the bounded run decision ring: every continuation, material state
transition, policy block, no-progress release, explicit switch, and stop
reason. Continuation records contain bounded identifiers, branch, and counter
metadata, never prompt bodies. Disabled no-op events are not logged.
`--branch`/`--session` filter, `-n` keeps the last N, and `--follow` tails new
records until Ctrl-C.

### `reconc task <subcommand>`
Typed repository TASK control with two non-migrating profiles:
`sections-v1` for bounded Active/Queue/Blocked/Done sections and `logbook-v1`
for a `Current:` line plus detail `State:` fields. `Current: none` is the
explicit valid logbook state when no TASK is active. Configure the profile,
overview/detail paths, Done window, and required completion evidence under
`task_lifecycle` in `.reconc.yml`; `auto` succeeds only on an unambiguous exact
grammar match. Explicit configuration makes the overview mandatory.
`completion.require_committed: true` requires the terminal TASK control-plane
changes to be committed, reusing the terminal Stop snapshot without adding Git
work to executable TASK continuations.

- `task status [repo] [--json]`: current TASK, current Sub-Task, bounded blockers, missing configured evidence, exact next action
- `task validate [repo] [--json]`: full live-control-plane validation with stable issue IDs
- `task check-done [repo] [--task ID] [--json]`: fail closed on any unfinished Sub-Task or missing configured evidence
- `task new [repo] --title TEXT [--id ID] [--json]`: atomically add the next or requested collision-free queued row and grammar-correct detail
- `task claim <ID> [repo] [--json]`: activate one executable queued TASK
- `task block [repo] --reason TEXT [--next ID | --no-next] [--json]`: block current; auto-claim the next executable TASK by default or explicitly leave none active
- `task resume <ID> [repo] [--json]`: reactivate a blocked TASK when no TASK is active
- `task split [repo] --children ID,ID [--json]`: block the parent and activate the first pre-created, parent-linked child
- `task promote [repo] [--next ID] [--json]`: completion-check, archive, and activate the next executable TASK
- `task archive [repo] [--json]`: terminal archive for either profile with no queued successor
- `task recover [repo] [--json]`: integrity-check and roll back an interrupted transaction without overwriting external edits; no journal is a successful idempotent no-op reported as `recovered: false`

Mutations use `.reconc/locks/task-lifecycle.lock`, atomic publication,
no-clobber file moves, and `.reconc/task-transaction.json`. Every touched file
and move source has a byte-and-mode before-image. Publication revalidates all
sources and destinations before mutation and again at each operation; recovery
accepts only exact regular-file before, after, or linked-move states. Unknown
journal fields, trailing JSON values, symlinked or non-canonical paths, type
drift, content drift, and mode drift fail closed. Version 2 journals distinguish
prepared rollback from committed finalization and ownership-mark every parent
directory created by the transaction. Rollback removes only marker-proven empty
parents, deepest first; user-created or replaced directories remain untouched.
Legacy version 1 journals remain recoverable without claiming their unrecorded
parent directories. Done rows are required newest-first before visible-window
trimming. Lock, unlock, close, and cleanup failures remain explicit. Normal reads never open
unlinked archive history. Briefings cap blockers/evidence and free text;
transactions cap journals at 4 MiB.

### `reconc prune [repo] [--dry-run] [--json]`
Run the product retention core immediately. It bounds external session,
report, lock, staged command-proof, and product-wide project-root state; audit
and run-decision JSONL rings; generated workflow-audit binaries; abandoned
repo-local atomic/build temps; and owned `reconc-proof-*` temp trees.
`--dry-run` reports file candidates without deleting them. Owned proof temp
trees use a two-hour inactivity grace. Run-decision JSONL class output reports
`inspection=complete` for measured projections and `inspection=unknown` without
zero-looking projection values when inspection fails. JSON uses the additive
`inspection_status` field; older decoders may ignore it, and its absence in a
legacy report means unspecified status. The
global project-state contract keeps at most 256 recognized roots / 128 MiB /
30 days while preserving the current project, live sessions, unknown
directories, recently active lifecycle roots, and every recognized root with a
durable `action/` state boundary. Generic retention never deletes budget state
because doing so could silently return consumed capacity, and it preserves the
action ledger live file, archives, detached head, and active transaction because
removing them would silently break retained-chain truth.
SessionStart and SessionEnd invoke the same core through a
six-hour due check; Stop never prunes. Historical parser compatibility is not
part of the public command surface.

### `reconc session-briefing [repo] [--json]`
Compact delta-oriented session state: current TASK/Sub-Task, bounded blockers,
current policy delta, required evidence, durable repository-run status, saved
report path, and one exact next action. JSON includes `format_version` for
machine consumers. Aggregate audit history and Git are intentionally excluded
from this hot path. It is read-only; missing or stale lockfiles require
`reconc refresh .`.

### `reconc context size [repo] [--limit N] [--files PATH,PATH,...] [--json]`
Guards the auto-loaded session-file token budget (default 20000 tokens).
Without `--files`, it measures `AGENTS.md`, `CLAUDE.md`, `start.md`,
`docs/tasks.md`, and the active TASK detail when present. Custom paths replace
that default. Paths are normalized and deduplicated; lexical and symlink
escapes outside the repository fail closed. Non-empty files round up to at
least one approximate token. JSON includes `format_version`; exit 1 over limit
so CI gates can block budget-growing PRs.

### `reconc start [repo] [--json | --minimal]`
Renders canonical onboarding and reentry context to stdout without mutating
the repository. It combines session briefing and verified audit summary data.
`--minimal` emits a compact three-line summary; `--json` and `--minimal` are
mutually exclusive.

### `reconc tui [repo] [--json] [--output PATH]`
Dependency-free terminal dashboard for policy state. Shows discovery,
lockfile freshness, source list, rule list, audit summary, active
session id, the exact completion decision and blockers, conflicts, and the next
action. `--json` emits the same
snapshot as structured data. Bounded audit-log and active-session observation
failures appear in `errors`; they never masquerade as an empty audit or absent
session. It never refreshes policy implicitly.

### `reconc help [command [subcommand...]]`
Print root help or the exact canonical synopsis and summary for one command
path, including nested paths such as `reconc help task recover`. The equivalent
suffix form is `reconc task recover --help`. Unknown command paths fail instead
of silently falling back to broader help.

### `reconc completion <bash|zsh|fish>`
Emit a shell completion script. Install one-liners:

```bash
reconc completion bash > /usr/local/etc/bash_completion.d/reconc
reconc completion zsh  > /usr/local/share/zsh/site-functions/_reconc
reconc completion fish > ~/.config/fish/completions/reconc.fish
```

### `reconc manpage`
Emit the roff man page (section 1) on stdout, generated from the same
canonical command metadata as root help and shell completion. Install example:

```bash
reconc manpage > /usr/local/share/man/man1/reconc.1
```

### `reconc version [--json]`
Print the build version as text or JSON. Equivalent to top-level
`reconc --version`.
