# TASK 246: Enforce installer and release trust atomically

## Why

The direct installers can continue after failed or unavailable provenance
verification, and the Unix installer can report success after `install-cli`
failed on a first installation. Release asset copy also separates its
no-clobber check from publication. These paths cross the highest trust boundary
in the product and must report one exact atomic outcome.

## Acceptance

- Stable and preview network installs verify build provenance by default before
  executing or publishing the candidate. Missing or failed verification stops
  with a precise remediation.
- If an explicit compatibility escape is retained, it is opt-in, loudly named,
  covered by release documentation, and recorded as unverified provenance; the
  default never silently downgrades.
- Unix and PowerShell installers apply identical provenance policy and bind
  repository, workflow identity, source tag, asset digest, and candidate bytes.
- Any non-zero `install-cli` result is a failed installer transaction on first
  install and upgrade, even if binary publication partially succeeded.
- Partial binary or receipt publication is rolled back or reported as an exact
  recoverable partial state; exit status always matches the report.
- Release asset publication uses a create-only or atomic no-replace operation.
  A concurrent destination cannot be overwritten between validation and copy.
- Make targets and release-trust allowlists contain only real, invoked surfaces;
  the absent `release-all` target and unused upload authorization are removed or
  implemented only if required by the existing release workflow.
- Installer failure-path tests, offline bundle tests, shell/PowerShell syntax,
  release-trust tests, deterministic asset tests, and full gates pass.

## Sub-Tasks

- [ ] Specify one default provenance policy for both installers
- [ ] Make attestation absence and verification failure explicit transaction outcomes
- [ ] Propagate `install-cli` failure on every install branch and prove rollback
- [ ] Publish copied release assets with atomic no-clobber semantics
- [ ] Reconcile Make phony targets and workflow action allowlists with actual use
- [ ] Add adversarial installer, concurrent-copy, and provenance downgrade tests
- [ ] Update installation, security, release, and operator documentation
- [ ] Run release-trust, installer, packaging, and full repository gates

## Notes

- External findings: F-18, F-19, F-31, and F-36. F-40 is mechanical cleanup
  performed under TASK 253 after this task defines the final action surface.
- F-33 is excluded because release notices already hard-fail module
  replacements even though the SBOM can represent them.
- F-35 is excluded because `manifest` is intentionally list-only and assembled
  by the dedicated release manifest stage.

## Deviations

None.
