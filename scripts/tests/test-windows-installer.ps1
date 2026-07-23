[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$InstallerPath,
    [Parameter(Mandatory = $true)]
    [string]$BinaryPath,
    [Parameter(Mandatory = $true)]
    [string]$ExpectedVersion,
    [bool]$LiveRelease = $false
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-ReconcTest {
    param(
        [Parameter(Mandatory = $true)]
        [bool]$Condition,
        [Parameter(Mandatory = $true)]
        [string]$Message
    )

    if (-not $Condition) {
        throw "windows-installer: $Message"
    }
}

function Assert-ReconcFailure {
    param(
        [Parameter(Mandatory = $true)]
        [scriptblock]$Operation,
        [Parameter(Mandatory = $true)]
        [string]$Message
    )

    try {
        & $Operation
    }
    catch {
        return
    }
    throw "windows-installer: operation unexpectedly succeeded: $Message"
}

$resolvedInstaller = (Resolve-Path -LiteralPath $InstallerPath).Path
$resolvedBinary = (Resolve-Path -LiteralPath $BinaryPath).Path
. $resolvedInstaller -Version $ExpectedVersion

$temporaryDirectory = Join-Path ([IO.Path]::GetTempPath()) "reconc-windows-installer-test-$([Guid]::NewGuid().ToString('N'))"
[void](New-Item -ItemType Directory -Path $temporaryDirectory)

try {
    $assetName = "reconc-$ExpectedVersion-windows-amd64.exe"
    $fixtureArtifact = Join-Path $temporaryDirectory $assetName
    Copy-Item -LiteralPath $resolvedBinary -Destination $fixtureArtifact
    $fixtureChecksum = Get-ReconcFileSha256 -Path $fixtureArtifact
    $manifestPath = Join-Path $temporaryDirectory "SHA256SUMS"
    Set-Content -LiteralPath $manifestPath -Encoding ASCII -Value "$fixtureChecksum  $assetName"

    $parsedChecksum = Get-ReconcExpectedChecksum -ManifestPath $manifestPath -AssetName $assetName
    Assert-ReconcTest ($parsedChecksum -eq $fixtureChecksum) "valid manifest entry was not parsed"

    Set-Content -LiteralPath $manifestPath -Encoding ASCII -Value "not-a-digest  $assetName"
    Assert-ReconcFailure {
        Get-ReconcExpectedChecksum -ManifestPath $manifestPath -AssetName $assetName
    } "malformed manifest"

    Set-Content -LiteralPath $manifestPath -Encoding ASCII -Value "$fixtureChecksum  another.exe"
    Assert-ReconcFailure {
        Get-ReconcExpectedChecksum -ManifestPath $manifestPath -AssetName $assetName
    } "missing manifest asset"

    Set-Content -LiteralPath $manifestPath -Encoding ASCII -Value @(
        "$fixtureChecksum  $assetName",
        "$fixtureChecksum  $assetName"
    )
    Assert-ReconcFailure {
        Get-ReconcExpectedChecksum -ManifestPath $manifestPath -AssetName $assetName
    } "duplicate manifest asset"

    $installDirectory = Join-Path $temporaryDirectory "install"
    $installedPath = Install-ReconcVerifiedArtifact `
        -ArtifactPath $fixtureArtifact `
        -ExpectedChecksum $fixtureChecksum `
        -InstallDirectory $installDirectory
    Assert-ReconcTest (Test-Path -LiteralPath $installedPath -PathType Leaf) "verified artifact was not installed"
    Assert-ReconcTest ((Get-ReconcFileSha256 -Path $installedPath) -eq $fixtureChecksum) "installed artifact checksum changed"

    $installedChecksum = Get-ReconcFileSha256 -Path $installedPath
    Assert-ReconcFailure {
        Install-ReconcVerifiedArtifact `
            -ArtifactPath $fixtureArtifact `
            -ExpectedChecksum ("0" * 64) `
            -InstallDirectory $installDirectory
    } "checksum mismatch"
    Assert-ReconcTest ((Get-ReconcFileSha256 -Path $installedPath) -eq $installedChecksum) "checksum failure replaced the existing binary"

    $missingArtifact = Join-Path $temporaryDirectory "missing.exe"
    Assert-ReconcFailure {
        Install-ReconcVerifiedArtifact `
            -ArtifactPath $missingArtifact `
            -ExpectedChecksum $fixtureChecksum `
            -InstallDirectory $installDirectory
    } "missing asset"
    Assert-ReconcTest ((Get-ReconcFileSha256 -Path $installedPath) -eq $installedChecksum) "missing asset replaced the existing binary"

    $invalidArtifact = Join-Path $temporaryDirectory "invalid.exe"
    Set-Content -LiteralPath $invalidArtifact -Encoding ASCII -Value "not a Windows executable"
    $invalidChecksum = Get-ReconcFileSha256 -Path $invalidArtifact
    Assert-ReconcFailure {
        Install-ReconcVerifiedArtifact `
            -ArtifactPath $invalidArtifact `
            -ExpectedChecksum $invalidChecksum `
            -InstallDirectory $installDirectory
    } "execution failure"
    Assert-ReconcTest ((Get-ReconcFileSha256 -Path $installedPath) -eq $installedChecksum) "execution failure replaced the existing binary"

    $lockedTarget = [IO.File]::Open(
        $installedPath,
        [IO.FileMode]::Open,
        [IO.FileAccess]::Read,
        [IO.FileShare]::Read
    )
    try {
        Assert-ReconcFailure {
            Install-ReconcVerifiedArtifact `
                -ArtifactPath $fixtureArtifact `
                -ExpectedChecksum $fixtureChecksum `
                -InstallDirectory $installDirectory
        } "locked target"
    }
    finally {
        $lockedTarget.Dispose()
    }
    Assert-ReconcTest ((Get-ReconcFileSha256 -Path $installedPath) -eq $installedChecksum) "publication failure replaced the existing binary"

    $unwritableTarget = Join-Path $temporaryDirectory "not-a-directory"
    Set-Content -LiteralPath $unwritableTarget -Encoding ASCII -Value "occupied"
    Assert-ReconcFailure {
        Install-ReconcVerifiedArtifact `
            -ArtifactPath $fixtureArtifact `
            -ExpectedChecksum $fixtureChecksum `
            -InstallDirectory $unwritableTarget
    } "unwritable target"
    Assert-ReconcTest ((Get-ReconcFileSha256 -Path $installedPath) -eq $installedChecksum) "unwritable target changed the existing install"

    $missingTool = "reconc-attestation-tool-$([Guid]::NewGuid().ToString('N'))"
    Confirm-ReconcAttestation `
        -ArtifactPath $fixtureArtifact `
        -Tool $missingTool `
        -Repository "example/reconc" `
        -Required $false
    Assert-ReconcFailure {
        Confirm-ReconcAttestation `
            -ArtifactPath $fixtureArtifact `
            -Tool $missingTool `
            -Repository "example/reconc" `
            -Required $true
    } "required attestation tool missing"

    $passTool = Join-Path $temporaryDirectory "attestation-pass.cmd"
    Set-Content -LiteralPath $passTool -Encoding ASCII -Value "@exit /b 0"
    Confirm-ReconcAttestation `
        -ArtifactPath $fixtureArtifact `
        -Tool $passTool `
        -Repository "example/reconc" `
        -Required $true

    $failTool = Join-Path $temporaryDirectory "attestation-fail.cmd"
    Set-Content -LiteralPath $failTool -Encoding ASCII -Value "@exit /b 1"
    Assert-ReconcFailure {
        Confirm-ReconcAttestation `
            -ArtifactPath $fixtureArtifact `
            -Tool $failTool `
            -Repository "example/reconc" `
            -Required $true
    } "required attestation failure"

    if ($LiveRelease) {
        $savedInstallDirectory = [Environment]::GetEnvironmentVariable("RECONC_INSTALL_DIR")
        $savedReleaseBase = [Environment]::GetEnvironmentVariable("RECONC_RELEASE_BASE")
        $savedAttestationTool = [Environment]::GetEnvironmentVariable("RECONC_ATTESTATION_TOOL")
        $savedRequireAttestation = [Environment]::GetEnvironmentVariable("RECONC_REQUIRE_ATTESTATION")
        $liveInstallDirectory = Join-Path $temporaryDirectory "live"
        try {
            [Environment]::SetEnvironmentVariable("RECONC_INSTALL_DIR", $liveInstallDirectory)
            [Environment]::SetEnvironmentVariable("RECONC_RELEASE_BASE", $null)
            [Environment]::SetEnvironmentVariable("RECONC_ATTESTATION_TOOL", $missingTool)
            [Environment]::SetEnvironmentVariable("RECONC_REQUIRE_ATTESTATION", "0")
            & $resolvedInstaller $ExpectedVersion
        }
        finally {
            [Environment]::SetEnvironmentVariable("RECONC_INSTALL_DIR", $savedInstallDirectory)
            [Environment]::SetEnvironmentVariable("RECONC_RELEASE_BASE", $savedReleaseBase)
            [Environment]::SetEnvironmentVariable("RECONC_ATTESTATION_TOOL", $savedAttestationTool)
            [Environment]::SetEnvironmentVariable("RECONC_REQUIRE_ATTESTATION", $savedRequireAttestation)
        }

        $liveBinary = Join-Path $liveInstallDirectory "reconc.exe"
        Assert-ReconcTest (Test-Path -LiteralPath $liveBinary -PathType Leaf) "live HTTPS install did not publish reconc.exe"
        $liveVersion = & $liveBinary --version
        Assert-ReconcTest (($liveVersion | Out-String) -match [Regex]::Escape($ExpectedVersion)) "live HTTPS install returned the wrong version"
    }
}
finally {
    if (Test-Path -LiteralPath $temporaryDirectory -PathType Container) {
        Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force
    }
}

Write-Host "windows-installer: ok"
exit 0
