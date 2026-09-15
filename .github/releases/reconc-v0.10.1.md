# reconc v0.10.1

Reconc 0.10.1 is a patch release that fixes disposable hook verification
workspaces leaking temporary data when sandboxed toolchains leave read-only
files behind.

## Fixes

- Reclaim hook verification workspaces that contain read-only directories, such
  as fresh Go module caches populated by isolated verification builds. Cleanup
  now restores writable directory permissions before removal instead of
  silently abandoning the workspace.
- Build disposable DSH verification workers with a writable module cache so the
  read-only tree is never created inside the sandbox.
- Preserve shared executable inodes during cleanup: only directories are made
  writable, because workspace binaries may hardlink the running executable.

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
