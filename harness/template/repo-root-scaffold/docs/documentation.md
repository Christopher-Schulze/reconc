# Project Documentation

- [Workflow](#workflow)
- [Architecture](#architecture)
- [Setup](#setup)

## Architecture

Document the target repo architecture here after the bootstrap agent has identified the selected stack and layout.

## Setup

Document project setup, build, test and local operation commands here as they become real.

## Workflow

Reconc governance lives under `tools/reconc/`. The repo-local harness is installed at `tools/reconc/harness/project/` after bootstrap and is wired through `.reconc.yml`, `.githooks/`, `.codex/`, `.github/hooks/`, `.cursor/`, `.agents/`, `.claude/`, `.opencode/`, `.devin/`, `.kilo/`, `.grok/`, `AGENTS.md`, `start.md`, and `docs/tasks.md`.

The workflow uses canonical files and paths: `docs/spec.md` is the implementation blueprint, `docs/tasks.md` is the flat task logbook, active task details live in `docs/tasks/`, and completed task details move to `docs/tasks/done/`. Each active task declares `Spec Lines` and a one-to-one ordered `Spec Bindings` field under `## Scheduling`. A binding uses `docs/spec.md:Lx-Ly@sha256:<range-digest>@term1+term2`; its digest pins the exact normalized spec bytes and its two or more meaningful terms must occur in both the TASK claim surface and cited range. Use `none` for both fields when a TASK has no spec surface. The task-state audit rejects missing, malformed, duplicated, reordered, stale, out-of-range, or lexically unsupported bindings, while `spec-task-coverage` verifies that open tasks collectively cover `docs/spec.md` so new spec content cannot remain ownerless. This is a deterministic drift and gross-mismatch guard, not a substitute for the required human line-by-line parity review.
