# TASK 245: Harden hook ownership and agent process boundaries

## Why

Generated hook ownership, shell launch, user configuration, child environment,
and continuation accounting currently contain small but real trust-boundary
gaps. Marker substrings can claim foreign artifacts, login shells can emit
stdout before strict OMP JSON, ZCode user disablement is overwritten, inherited
Grok steering can survive a duplicate environment entry, and changing reason
text can evade the no-progress cap.

## Acceptance

- Every generated artifact has one exact ownership signature at a defined file
  or object location. Mentioning marker text elsewhere never grants ownership.
- Existing foreign artifacts remain untouched without explicit force, while
  all currently generated Reconc artifacts remain detectable and upgradeable.
- OMP and other direct runtime launchers use a non-login shell or direct exec
  path that cannot source interactive/login profiles before decision JSON.
- An explicit user `hooks.enabled: false` remains false while Reconc merges only
  its owned ZCode event entries; status reports disabled rather than rewriting
  user intent.
- Grok children receive exactly one `RECONC_GROK_STEER=0` entry regardless of
  parent environment ordering and platform case rules.
- The no-progress attempt cap is bound to material session progress, not mutable
  diagnostic wording. Changing reason text without material events cannot reset
  the cap.
- Script documentation explicitly states that repo-vouched policy scripts are
  trusted code, that environment filtering is secret minimization rather than
  a sandbox, and why `HOME` is retained or removed.
- Hook install/merge/status tests, real shell-output fixtures, environment tests,
  continuation tests, and platform contract tests pass.

## Sub-Tasks

- [ ] Define exact ownership signatures for every affected hook artifact
- [ ] Replace substring ownership checks and add foreign-marker adversarial tests
- [ ] Remove login-shell dependence from strict decision launch paths
- [ ] Preserve explicit ZCode disablement while merging owned event entries
- [ ] Replace inherited steering variables before spawning Grok
- [ ] Key the no-progress cap to material progress across reason changes
- [ ] Clarify the trusted policy-script and `HOME` environment boundary
- [ ] Run hook generation, merge, shell, session, race, and full gates

## Notes

- External findings: F-8, F-17, F-22, F-24, F-61, and F-63.
- OMP and Pi source templates remain separate because their abort and host
  result contracts differ. This task should share helpers only where semantics
  are identical, not merely where generated text looks similar.

## Deviations

None.
