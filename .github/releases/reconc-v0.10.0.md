# reconc v0.10.0

Reconc 0.10.0 adds DeepSeek Harness support, ships the portable agent skill,
and improves agent workflows, policy enforcement, and resource efficiency.

## Agent integrations

- Add native DeepSeek Harness hooks for session events, tool decisions, and
  completion, preserving native tools, delegation, persistent shells, and
  agentless host calls. All supported agents use the same policy and evidence
  rules.
- Update Codex, Devin, Cursor, and Oh My Pi integration contracts and regression
  coverage. Bound worker lifetime, retained state, and repeated diagnostics.
- Track upstream host contracts automatically to detect integration drift.

## Agent skill and CLI

- Ship an owned portable skill with progressive reference loading and compact,
  structured guidance for inspection, remediation, and completion.
- Coordinate binary and skill updates, offering skill installation when absent
  and updating managed installed skills through the update workflow.
- Align session briefings and next actions with current policy and evidence,
  preserving machine identifiers and explicit output truncation metadata.

## Correctness and efficiency

- Strengthen command-policy handling, composite write prevention, approval
  recovery, path identity, template provenance, and public proof redaction.
- Make shared runtime-plan loading cancellation-safe and pre-decision caching
  sensitive to the policy, path, and evidence identities it depends on.
- Reduce hot-path allocations and redundant inspection work, bound cache memory,
  and improve source-bound benchmark comparisons and resource measurements.
- Correct coverage-policy classification and distinguish measured coverage from
  configured thresholds.

## Delivery

The release workflow checks the selected tag, runs source and race-test gates,
verifies immutable schema publication and release trust, builds platform
artifacts, and verifies their manifests and checksums before publication.
Published artifacts include build-provenance attestations.

## Upgrade

Use the installation's existing owner. For direct installations:

```bash
reconc update
reconc doctor --global
```

After updating the binary, use the documented repository synchronization
workflow to refresh generated integrations in existing repositories.
