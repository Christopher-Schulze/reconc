package usercli

// verifiedInstallProvenance turns a provenance claim from the installer's
// environment into a recorded state. A GitHub claim is recorded only when the
// installed binary verifies against its release attestation here; without a
// verifier on PATH it keeps the checksum-verified state, and a failing
// verification refuses the receipt.
func verifiedInstallProvenance(claimed ProvenanceState, binaryPath, releaseTag string) (ProvenanceState, error) {
	return verifiedInstallProvenanceFor(claimed, binaryPath, releaseTag, "")
}
