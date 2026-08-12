#!/usr/bin/env bash

set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
python=${PYTHON:-python3}
reconc_version=0.9.6
go_mcp_sdk_version=v1.7.0
tmp=$(mktemp -d "${TMPDIR:-/tmp}/reconc-langchain.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

fail() {
  printf 'langchain-integration: %s\n' "$1" >&2
  exit 1
}

"$python" - <<'PY'
import importlib.metadata
import platform
import sys

expected = {
    "langchain-core": "1.5.4",
    "langchain-mcp-adapters": "0.3.2",
    "mcp": "1.29.0",
    "typing-extensions": "4.16.0",
}
actual = {name: importlib.metadata.version(name) for name in expected}
if actual != expected:
    raise SystemExit(f"external consumer versions drifted: {actual!r}")
if platform.python_version() != "3.13.14":
    raise SystemExit(f"Python 3.13.14 is required, got {sys.version.split()[0]}")

from mcp.types import LATEST_PROTOCOL_VERSION

if LATEST_PROTOCOL_VERSION != "2025-11-25":
    raise SystemExit(f"MCP Python protocol drifted: {LATEST_PROTOCOL_VERSION!r}")
PY

actual_go_mcp_sdk_version=$(go list -m -f '{{.Version}}' github.com/modelcontextprotocol/go-sdk)
[ "$actual_go_mcp_sdk_version" = "$go_mcp_sdk_version" ] ||
  fail "Go MCP SDK drifted: expected $go_mcp_sdk_version, got $actual_go_mcp_sdk_version"

mkdir -p "$tmp/bin" "$tmp/operator" "$tmp/repository" "$tmp/reconc-home"
chmod 700 "$tmp/operator" "$tmp/reconc-home"
go build -trimpath -o "$tmp/bin/reconc" ./cmd/reconc
go build -trimpath -o "$tmp/bin/langchain-fixture" ./scripts/tests/langchain-fixture
[ "$("$tmp/bin/reconc" --version)" = "reconc $reconc_version" ] ||
  fail "Reconc binary version drifted"

printf '# LangChain interoperability fixture\n' >"$tmp/repository/AGENTS.md"
cp "$root/scripts/tests/langchain-policy.yml" "$tmp/repository/.reconc.yml"
"$tmp/bin/reconc" refresh "$tmp/repository" --strict-conflicts >"$tmp/refresh.txt"
"$tmp/bin/reconc" action key init --reconc-home "$tmp/reconc-home" --json >"$tmp/key.json"

lock_digest=$("$python" - "$tmp/repository/.reconc/policy.lock.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    print(json.load(handle)["lock_digest"])
PY
)
[ "${#lock_digest}" -eq 64 ] || fail "compiled lock digest is invalid"

registry="$tmp/operator/approval-authorities.json"
"$python" - "$registry" <<'PY'
import base64
import json
import os
import sys

public_key = base64.urlsafe_b64encode(bytes([0x42]) * 32).rstrip(b"=").decode()
registry = {
    "authorities": [{
        "active_from": "2026-01-01T00:00:00Z",
        "id": "integration-authority",
        "public_key": public_key,
    }],
    "authority_policies": [{
        "authority_key_ids": ["integration-authority"],
        "id": "langchain-integration",
    }],
    "format_version": "1",
    "schema": "reconc.action-approval-authorities/v1",
}
body = json.dumps(registry, separators=(",", ":"), sort_keys=True).encode()
descriptor = os.open(sys.argv[1], os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
with os.fdopen(descriptor, "wb") as handle:
    handle.write(body)
PY

export RECONC_INTEROP_BINARY="$tmp/bin/reconc"
export RECONC_INTEROP_BYPASS_CANCELLATION="$tmp/bypass-cancellation.log"
export RECONC_INTEROP_BYPASS_EVENTS="$tmp/bypass-events.log"
export RECONC_INTEROP_CANCELLATION="$tmp/cancellation.log"
export RECONC_INTEROP_EVENTS="$tmp/events.log"
export RECONC_INTEROP_FIXTURE="$tmp/bin/langchain-fixture"
export RECONC_INTEROP_HOME="$tmp/reconc-home"
export RECONC_INTEROP_LOCK_DIGEST="$lock_digest"
export RECONC_INTEROP_REGISTRY="$registry"
export RECONC_INTEROP_REPOSITORY="$tmp/repository"

"$python" - <<'PY'
import asyncio
import os
import socket
from datetime import timedelta
from pathlib import Path

from langchain_core.messages import ToolMessage
from langchain_mcp_adapters.callbacks import Callbacks
from langchain_mcp_adapters.client import MultiServerMCPClient
from langchain_mcp_adapters.tools import load_mcp_tools

EXPECTED_SCHEMA = {
    "additionalProperties": False,
    "properties": {"value": {"type": "string"}},
    "required": ["value"],
    "type": "object",
}
EXPECTED_TOOLS = {
    "approval", "blocked", "budgeted", "echo", "slow", "tool-error", "warn", "withheld"
}


def connection() -> dict[str, object]:
    return {
        "transport": "stdio",
        "command": os.environ["RECONC_INTEROP_BINARY"],
        "args": [
            "mcp", "gateway", os.environ["RECONC_INTEROP_REPOSITORY"],
            "--server", "langchain-fixture",
            "--expect-lock-digest", os.environ["RECONC_INTEROP_LOCK_DIGEST"],
            "--principal", "langchain-consumer",
            "--role", "integration-test",
            "--environment", "ci",
            "--credential", "fixture-credential",
            "--run", "langchain-integration-run",
            "--session", "langchain-integration-session",
            "--approval-authorities", os.environ["RECONC_INTEROP_REGISTRY"],
            "--approval-policy", "langchain-integration",
            "--timeout", "5s",
            "--reconc-home", os.environ["RECONC_INTEROP_HOME"],
            "--", os.environ["RECONC_INTEROP_FIXTURE"],
            "--events", os.environ["RECONC_INTEROP_EVENTS"],
            "--cancellation", os.environ["RECONC_INTEROP_CANCELLATION"],
        ],
        "session_kwargs": {"read_timeout_seconds": timedelta(seconds=10)},
    }


def event_count(name: str) -> int:
    return event_count_at(Path(os.environ["RECONC_INTEROP_EVENTS"]), name)


def event_count_at(path: Path, name: str) -> int:
    if not path.exists():
        return 0
    return path.read_text(encoding="utf-8").splitlines().count(name)


def message_text(message: ToolMessage) -> str:
    if isinstance(message.content, str):
        return message.content
    parts = []
    for block in message.content:
        if isinstance(block, str):
            parts.append(block)
        elif isinstance(block, dict) and isinstance(block.get("text"), str):
            parts.append(block["text"])
    return "\n".join(parts)


async def invoke(tool, value: str, call_id: str) -> ToolMessage:
    result = await tool.ainvoke({
        "type": "tool_call", "name": tool.name,
        "args": {"value": value}, "id": call_id,
    })
    if not isinstance(result, ToolMessage):
        raise AssertionError(f"{tool.name} returned {type(result)!r}, not ToolMessage")
    return result


async def wait_for_file(path: Path) -> None:
    for _ in range(100):
        if path.exists():
            return
        await asyncio.sleep(0.02)
    raise AssertionError(f"timed out waiting for {path}")


async def wait_for_event_count(name: str, expected: int) -> None:
    for _ in range(100):
        if event_count(name) == expected:
            return
        await asyncio.sleep(0.02)
    raise AssertionError(
        f"timed out waiting for {expected} {name!r} event(s); got {event_count(name)}"
    )


async def main() -> None:
    progress = []

    async def on_progress(value, total, message, context) -> None:
        progress.append((value, total, message, context.server_name, context.tool_name))

    client = MultiServerMCPClient(
        {"reconc": connection()}, callbacks=Callbacks(on_progress=on_progress)
    )
    tools = await client.get_tools(server_name="reconc")
    if {tool.name for tool in tools} != EXPECTED_TOOLS:
        raise AssertionError(f"tool discovery drifted: {[tool.name for tool in tools]!r}")
    by_name = {tool.name: tool for tool in tools}
    if by_name["echo"].args_schema != EXPECTED_SCHEMA:
        raise AssertionError(f"input schema drifted: {by_name['echo'].args_schema!r}")
    metadata = by_name["echo"].metadata or {}
    if metadata.get("readOnlyHint") is not True or metadata.get("idempotentHint") is not True:
        raise AssertionError(f"tool annotations drifted: {metadata!r}")

    echo = await invoke(by_name["echo"], "alpha", "call-echo")
    if echo.status != "success" or "fixture:echo:alpha" not in message_text(echo):
        raise AssertionError(f"allowed call result drifted: {echo!r}")
    if echo.artifact != {"structured_content": {"echo": "alpha"}}:
        raise AssertionError(f"structured result drifted: {echo.artifact!r}")
    if progress != [
        (1.0, 2.0, "step 1", "reconc", "echo"),
        (2.0, 2.0, "step 2", "reconc", "echo"),
    ]:
        raise AssertionError(f"progress drifted: {progress!r}")

    before = event_count("blocked")
    blocked = await invoke(by_name["blocked"], "deny", "call-blocked")
    if blocked.status != "error" or "blocked" not in message_text(blocked).lower():
        raise AssertionError(f"policy block category drifted: {blocked!r}")
    if event_count("blocked") != before:
        raise AssertionError("blocked call reached the downstream fixture")

    approval = await invoke(by_name["approval"], "review", "call-approval")
    if approval.status != "error" or "(approval_required)" not in message_text(approval):
        raise AssertionError(f"approval-required category drifted: {approval!r}")
    if event_count("approval") != 0:
        raise AssertionError("unsupported legacy approval reached the downstream fixture")

    warned = await invoke(by_name["warn"], "warn", "call-warn")
    if warned.status != "success" or event_count("warn") != 1:
        raise AssertionError(f"warn flow drifted: {warned!r}")

    first_budget = await invoke(by_name["budgeted"], "first", "call-budget-1")
    second_budget = await invoke(by_name["budgeted"], "second", "call-budget-2")
    if first_budget.status != "success" or second_budget.status != "error":
        raise AssertionError(f"fresh-session budget flow drifted: {first_budget!r}, {second_budget!r}")
    if "budget" not in message_text(second_budget).lower() or event_count("budgeted") != 1:
        raise AssertionError("fresh sessions reset or bypassed the cumulative budget")

    tool_error = await invoke(by_name["tool-error"], "error", "call-tool-error")
    if tool_error.status != "error" or "downstream-tool-error" not in message_text(tool_error):
        raise AssertionError(f"downstream tool error drifted: {tool_error!r}")

    withheld = await invoke(by_name["withheld"], "secret", "call-withheld")
    withheld_text = message_text(withheld)
    if withheld.status != "error" or "withheld" not in withheld_text.lower():
        raise AssertionError(f"withheld result category drifted: {withheld!r}")
    if "sensitive-downstream-result" in withheld_text or event_count("withheld") != 1:
        raise AssertionError("withheld downstream content reached the LangChain boundary")

    async with client.session("reconc") as session:
        stateful_tools = {tool.name: tool for tool in await load_mcp_tools(
            session, callbacks=client.callbacks, server_name="reconc"
        )}
        stateful = await invoke(stateful_tools["echo"], "stateful", "call-stateful")
        if stateful.status != "success" or "fixture:echo:stateful" not in message_text(stateful):
            raise AssertionError(f"stateful session flow drifted: {stateful!r}")

    slow_task = asyncio.create_task(invoke(by_name["slow"], "cancel", "call-slow"))
    await wait_for_event_count("slow", 1)
    slow_task.cancel()
    try:
        await slow_task
    except asyncio.CancelledError:
        pass
    else:
        raise AssertionError("cancelled LangChain tool call unexpectedly completed")
    await wait_for_file(Path(os.environ["RECONC_INTEROP_CANCELLATION"]))
    if event_count("slow") != 1:
        raise AssertionError("cancelled call was dispatched more than once")

    invalid_client = MultiServerMCPClient({
        "missing": {"transport": "stdio", "command": "/reconc-missing-executable", "args": []}
    })
    try:
        await invalid_client.get_tools(server_name="missing")
    except BaseException as error:
        if isinstance(error, ToolMessage):
            raise AssertionError("transport failure was collapsed into a tool result")
    else:
        raise AssertionError("transport failure was accepted")

    direct_client = MultiServerMCPClient({
        "unenforced-direct": {
            "transport": "stdio",
            "command": os.environ["RECONC_INTEROP_FIXTURE"],
            "args": [
                "--events", os.environ["RECONC_INTEROP_BYPASS_EVENTS"],
                "--cancellation", os.environ["RECONC_INTEROP_BYPASS_CANCELLATION"],
            ],
        }
    })
    direct_tools = {
        tool.name: tool for tool in await direct_client.get_tools(
            server_name="unenforced-direct"
        )
    }
    bypassed = await invoke(direct_tools["blocked"], "direct", "call-direct-bypass")
    if bypassed.status != "success" or event_count_at(
        Path(os.environ["RECONC_INTEROP_BYPASS_EVENTS"]), "blocked"
    ) != 1:
        raise AssertionError("direct downstream configuration was incorrectly represented as enforced")


original_connect = socket.socket.connect


def deny_network(self, address):
    raise AssertionError(f"network access attempted: {address!r}")


socket.socket.connect = deny_network
try:
    asyncio.run(main())
finally:
    socket.socket.connect = original_connect
PY

"$tmp/bin/reconc" status "$tmp/repository" --json >"$tmp/status.json"
"$tmp/bin/reconc" doctor "$tmp/repository" --deep --json >"$tmp/doctor.json"
"$python" - "$tmp/status.json" "$tmp/doctor.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
with open(sys.argv[2], encoding="utf-8") as handle:
    doctor = json.load(handle)
expected = {
    "mcp_gateway_scope": "explicit_routes_only",
    "mcp_external_configuration": "not_inspected",
    "mcp_bypass_routes": "unenforced",
}
if any(status.get(key) != value for key, value in expected.items()):
    raise SystemExit(f"status gateway boundary drifted: {status!r}")
mcp_checks = [check for check in doctor.get("checks", []) if check.get("name") == "MCP side-effect policy"]
if len(mcp_checks) != 1:
    raise SystemExit(f"deep doctor MCP boundary missing: {doctor!r}")
detail = mcp_checks[0].get("detail", "")
for phrase in (
    "external client configuration is not inspected",
    "direct downstream configurations are unenforced",
):
    if phrase not in detail:
        raise SystemExit(f"deep doctor MCP boundary drifted: {detail!r}")
PY

RECONC_HOME="$tmp/reconc-home" "$tmp/bin/reconc" action log verify "$tmp/repository" --json >"$tmp/ledger-verify.json"
RECONC_HOME="$tmp/reconc-home" "$tmp/bin/reconc" action log stats "$tmp/repository" --json >"$tmp/ledger-stats.json"
"$python" - "$tmp/ledger-verify.json" "$tmp/ledger-stats.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    report = json.load(handle)
with open(sys.argv[2], encoding="utf-8") as handle:
    stats = json.load(handle)
expected_verification = {
    "integrity": "verified",
    "archive_continuity": "verified",
    "detached_head": "matched",
    "events_evaluated": True,
    "events_complete": False,
    "incomplete_events": 1,
    "calls_evaluated": True,
    "calls_complete": False,
    "incomplete_calls": 1,
}
if any(report.get(key) != value for key, value in expected_verification.items()):
    raise SystemExit(f"action ledger verification drifted: {report!r}")
expected_counts = {
    "calls": 10,
    "evaluated": 10,
    "allowed": 6,
    "warned": 1,
    "approval_required": 1,
    "blocked": 2,
    "not_dispatched": 3,
    "downstream_succeeded": 6,
    "downstream_unknown": 1,
    "delivered": 5,
    "withheld": 1,
    "terminal_complete": 10,
    "incomplete_terminal": 0,
    "evidence_complete": 9,
    "evidence_incomplete": 1,
}
counts = stats.get("counts", {})
if any(counts.get(key) != value for key, value in expected_counts.items()):
    raise SystemExit(f"action ledger lifecycle counts drifted: {counts!r}")
for dimension in ("by_run", "by_session", "by_principal"):
    groups = stats.get(dimension, [])
    if len(groups) != 1 or groups[0].get("counts", {}).get("calls") != 10:
        raise SystemExit(f"fresh sessions split {dimension}: {groups!r}")
if stats["by_principal"][0].get("identity") != "langchain-consumer":
    raise SystemExit(f"operator principal binding drifted: {stats['by_principal']!r}")
calls = {call["tool_identity"]["value"]: call for call in stats.get("calls", [])}
approval = calls.get("approval", {})
if approval.get("approval") != "unavailable" or not approval.get("terminal_complete"):
    raise SystemExit(f"legacy approval terminal state drifted: {approval!r}")
slow = calls.get("slow", {})
if slow.get("dispatch") != "unknown" or slow.get("evidence_complete") is not False or not slow.get("terminal_complete"):
    raise SystemExit(f"cancelled call lifecycle drifted: {slow!r}")
PY

printf 'langchain-integration: ok (Reconc %s, Go MCP SDK %s, LangChain MCP adapter 0.3.2, LangChain Core 1.5.4, MCP Python SDK 1.29.0, Python 3.13.14, protocol 2025-11-25, Go fixture 1)\n' \
  "$reconc_version" "$go_mcp_sdk_version"
