# TASK 009: Architecture and release trust

## Why

Release scripts can hide failures, the nested harness module is absent from
root CI, public artifacts lack a complete verification chain, and several core
files have accumulated too many responsibilities. These defects make green
output less trustworthy and future changes unnecessarily expensive.

## Acceptance

- Every release build and checksum failure terminates the release target.
- Installer downloads verify the published checksum before execution or installation.
- Root CI tests the product and nested harness module on supported operating systems with pinned tool versions.
- Public schemas and artifact references resolve to durable versioned locations.
- CLI, evaluator, hook generation, and session handling are split by responsibility without parallel implementations or behavior drift.
- Full formatting, tidy, test, race, vet, static analysis, release, install, and artifact-verification gates pass.

## Sub-Tasks

- [ ] Close release, checksum, installer, schema, and artifact trust gaps.
- [ ] Add nested-module and cross-platform CI coverage with pinned tools.
- [ ] Refactor complexity hotspots behind existing public behavior and tests.
- [ ] Remove drift and duplicated command, adapter, and evaluation paths.
- [ ] Prove release and install failure paths with negative tests.

## Notes

Approved areas: 1 Release fail-open; 2 Harness CI hole; 4 Public trust chain;
6 Complexity concentration.

The three pre-existing U1000 findings in the read-only and Stop paths were
removed when TASK 005 promoted staticcheck to a required completion gate.

## Deviations

None.
