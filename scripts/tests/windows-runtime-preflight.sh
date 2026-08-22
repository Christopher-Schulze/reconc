#!/usr/bin/env bash

set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_cmd=${GO:-go}
tests='TestWriteIfChangedReconcilesRepresentableReadOnlyDrift|TestPublicationCreatesExplicitPublicAndPrivateParents|TestSecureWindowsDescriptorPersistsProtectedDACL|TestValidateDirectoryEntryRejectsReplacement|TestOpenLockRepairsModeAndRejectsHardLinkAndSymlink|TestConcurrentFirstLockCreationPublishesOneValidIdentity|TestBootstrapTransactionHelpersPreserveCreateOnlyBoundaries|TestCreatedArtifactDescriptorReconcilesWindowsWritableMode|TestScanSparseHugeFileIsExplicitlyOverBudget|TestInspectPiProjectTrustStates|TestPowerShellRemediationRoundTripsInNativeShell|TestEvaluationPathStateRejectsRepositoryRootReplacement|TestSourceLoadContextRejectsConfigReplacement|TestPathDepthUsesNormalizedSlashForm|TestAppendPublishesPrivateAuditLayoutAndMigratesLegacyModes|TestInspectRetentionUsesPrivateAuditLayout|TestAppendTransactionRecoversCommitFailureWithoutRotation|TestAcquireCompileLockRejectsSymlinksWithoutChangingTarget|TestEnsurePrivateStateDirRepairsOwnedDirectory|TestActiveSessionFirstPublicationWaitsForProjectRetentionLock|TestActiveSessionConcurrentWritersShareOnePointerLock|TestProofBindsSuccessToCurrentStagedIndex|TestStoreLoadAndTamperDetection|TestReceiptWriterSerializesConcurrentPublication|TestHomeRespectsEnvVar'
packages=(
  ./internal/atomicfile
  ./internal/audit
  ./internal/bootstrap
  ./internal/compiler
  ./internal/contextsize
  ./internal/commandproof
  ./internal/hooks
  ./internal/ingest
  ./internal/jsonl
  ./internal/policyproof
  ./internal/presets
  ./internal/privatefs
  ./internal/runtime
  ./internal/runtime/agentsession
  ./internal/stackdetect
  ./internal/usercli
)

cd "$root"
"$go_cmd" test -count=1 -timeout=4m "${packages[@]}" -run "^(${tests})(/.*)?$"
