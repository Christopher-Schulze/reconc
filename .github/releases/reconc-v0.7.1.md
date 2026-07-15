# reconc v0.7.1

`v0.7.1` is a cross-platform correctness and CI-efficiency patch for the
`v0.7.x` release line.

## Fixes

- Bootstrap transactions now retain an open identity handle for every parent
  directory they create. Rollback therefore refuses an externally replaced
  directory even when Linux immediately reuses its former inode number.
- Windows CI no longer mistakes CRLF checkout normalization for Go formatting
  drift. The platform-independent formatting and module-tidy checks now run
  once on Linux, while tests, vet, Staticcheck, and race tests remain active on
  Linux, macOS, and Windows.

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
