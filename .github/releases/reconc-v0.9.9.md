# reconc v0.9.9

Unreleased development version. Tag creation and release publication require
separate explicit authorization. These notes describe the implemented changes
in this development cycle and do not establish a published release.

## Template freshness

Named rule templates contribute their selected origin and exact content digest
to the compiled policy's source identity. Runtime freshness checks reject
changed or removed overrides, newly shadowed built-ins, malformed definitions,
and inconsistent dependency metadata before reusing a policy. Unused template
changes do not invalidate unrelated policies.

Template provenance excludes private installation paths and raw bodies.
Compilation bounds distinct template input to 64 MiB, retaining the existing
8 MiB per-template limit and sharing repeated references within one snapshot.

## Composite write prevention

Supported composite `deny_write` rules are enforced before PreToolUse and
PermissionRequest file mutations. Mixed `all_of` rules enforce their necessary
write checks before execution and retain completion checks for Stop. Mixed
`any_of` rules combining `deny_write` with other check kinds are rejected with
an authoring diagnostic instead of silently permitting a protected write.

## Compatibility

The format-6 policy-lock schema adds optional `template_dependencies` and targets
the future `reconc-v0.9.9/schemas/v6/policy-lock.schema.json` publication identity.
Previously published format-6 identities remain accepted inputs. Older locks
that reference named templates require `reconc refresh .`; older locks without
named templates retain their source-digest representation. Unchanged schema
contracts retain their published identities.
