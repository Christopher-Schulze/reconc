#!/usr/bin/env bash

set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_cmd=${GO:-go}
tests='TestReplaceFileUsesRootedWriteThroughHandle|TestReplaceFileFallsBackToLegacyRootedRename|TestReplaceFileRejectsReparseSourceAndCleansTemporary|TestSecureWindowsDescriptorPersistsProtectedDACL|TestSecureWindowsHandleTargetsOpenedIdentityAfterReplacement|TestOpenLockRejectsWindowsReparsePointWithoutMutatingTargetACL|TestTaskPathGuardRejectsReplacementAfterRead|TestTaskPathGuardRejectsSymlinkAndNonDirectoryComponents'
packages=(
  ./internal/atomicfile
  ./internal/privatefs
  ./internal/tasklifecycle
)

cd "$root"
"$go_cmd" test -p=2 -count=1 -timeout=90s "${packages[@]}" -run "^(${tests})(/.*)?$"
