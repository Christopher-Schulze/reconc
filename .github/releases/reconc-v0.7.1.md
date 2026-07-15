# reconc v0.7.1

`v0.7.1` is a cross-platform correctness and CI-efficiency patch for the
`v0.7.x` release line.

## Fixes

- Bootstrap transactions now retain an open identity handle for every parent
  directory they create. Rollback therefore refuses an externally replaced
  directory even when Linux immediately reuses its former inode number.
- Windows CI no longer mistakes CRLF checkout normalization for Go formatting
  drift. Full runtime tests remain active on Linux and macOS; Windows now
  verifies native root-module package and test compilation, vet, binary
  construction, version output, and CLI startup without pretending POSIX
  hook-script, harness, and executable-bit semantics are native Windows
  behavior.

## Release Artifacts

- `reconc-0.7.1-darwin-amd64`
- `reconc-0.7.1-darwin-arm64`
- `reconc-0.7.1-linux-amd64`
- `reconc-0.7.1-linux-arm64`
- `reconc-0.7.1-windows-amd64.exe`
- Bash, Zsh, and Fish completions
- man page
- three public v1 JSON schemas
- `SHA256SUMS`
