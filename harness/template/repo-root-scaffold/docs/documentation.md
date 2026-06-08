# Project Documentation

- [Workflow](#workflow)
- [Architecture](#architecture)
- [Setup](#setup)

## Architecture

Document the target repo architecture here after the bootstrap agent has identified the selected stack and layout.

## Setup

Document project setup, build, test and local operation commands here as they become real.

## Workflow

Reconc governance lives under `tools/reconc/`. The repo-local harness is installed at `tools/reconc/harness/project/` after bootstrap and is wired through `.reconc.yml`, `.codex/`, `.cursor/`, `.agents/`, `.claude/`, `.opencode/`, `AGENTS.md`, `start.md`, and `docs/tasks.md`.

The workflow uses canonical files and paths: `docs/spec.md` is the implementation blueprint, `docs/tasks.md` is the flat task logbook, active task details live in `docs/tasks/`, and completed task details move to `docs/tasks/done/`. Each active task declares `Spec Lines` under `## Scheduling`; the `spec-task-coverage` workflow audit verifies that open tasks collectively cover `docs/spec.md` line ranges so new spec content cannot remain ownerless.
