# reconc v0.7.3

`v0.7.3` makes the exact source identity directly inspectable in both release
SBOM formats without changing runtime behavior or the public CLI surface.

## SBOM Identity

- CycloneDX 1.6 now exposes the full tagged source commit as the root component
  property `reconc:source-commit`.
- SPDX 2.3 and CycloneDX tests independently require that exact commit identity,
  preventing a deterministic but opaque commit binding from passing release
  validation again.
- SBOM generation remains deterministic and stdlib-only. Checksums and GitHub
  build-provenance attestations still cover every manifest-listed artifact.

## Release Artifacts

- `reconc-0.7.3-darwin-amd64`
- `reconc-0.7.3-darwin-arm64`
- `reconc-0.7.3-linux-amd64`
- `reconc-0.7.3-linux-arm64`
- `reconc-0.7.3-windows-amd64.exe`
- deterministic SPDX 2.3 and CycloneDX 1.6 SBOMs
- Bash, Zsh, and Fish completions
- man page
- three public v1 JSON schemas
- `SHA256SUMS`
