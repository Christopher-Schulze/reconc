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
    foreach ($version in @("0.9.0", "0.9.0-preview.1", "10.20.30-rc.7")) {
        Assert-ReconcSemanticVersion -Value $version
    }
    foreach ($version in @("01.9.0", "0.9", "0.9.0-preview.01", "0.9.0+build", "18446744073709551616.0.0")) {
        Assert-ReconcFailure {
            Assert-ReconcSemanticVersion -Value $version
        } "invalid semantic version $version"
    }
    Assert-ReconcTest ((Compare-ReconcSemanticVersion -Left "0.9.0" -Right "0.9.0-preview.1") -gt 0) "stable precedence is incorrect"
    Assert-ReconcTest ((Compare-ReconcSemanticVersion -Left "0.9.0-preview.2" -Right "0.9.0-preview.10") -lt 0) "numeric prerelease precedence is incorrect"
    Assert-ReconcTest ((Compare-ReconcSemanticVersion -Left "0.9.0-preview.18446744073709551616" -Right "0.9.0-preview.9999999999999999999") -gt 0) "large numeric prerelease precedence is incorrect"
    Assert-ReconcTest ((Compare-ReconcSemanticVersion -Left "1.0.0" -Right "0.9.99") -gt 0) "core version precedence is incorrect"

    $downloadUri = [Uri]"https://example.com/reconc.exe"
    Assert-ReconcDownloadLength -ContentLength $null -MaximumBytes 1024 -Uri $downloadUri
    Assert-ReconcDownloadLength -ContentLength ([long]1024) -MaximumBytes 1024 -Uri $downloadUri
    Assert-ReconcFailure {
        Assert-ReconcDownloadLength -ContentLength ([long]1025) -MaximumBytes 1024 -Uri $downloadUri
    } "numeric Content-Length over the download limit"
    Assert-ReconcFailure {
        Assert-ReconcDownloadLength -ContentLength ([long]-1) -MaximumBytes 1024 -Uri $downloadUri
    } "negative Content-Length"

    $savedArchitecture = $env:PROCESSOR_ARCHITECTURE
    $savedWowArchitecture = $env:PROCESSOR_ARCHITEW6432
    try {
        $env:PROCESSOR_ARCHITECTURE = "ARM64"
        $env:PROCESSOR_ARCHITEW6432 = ""
        Assert-ReconcFailure {
            Get-ReconcWindowsAssetName -ReleaseVersion $ExpectedVersion
        } "Windows arm64 must be rejected explicitly"
    }
    finally {
        $env:PROCESSOR_ARCHITECTURE = $savedArchitecture
        $env:PROCESSOR_ARCHITEW6432 = $savedWowArchitecture
    }

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
    $installedPath = Join-Path ([IO.Path]::GetFullPath($installDirectory)) "reconc.exe"
    $partialInstallMessage = ""
    try {
        Install-ReconcVerifiedArtifact `
            -ArtifactPath $fixtureArtifact `
            -ExpectedChecksum $fixtureChecksum `
            -InstallDirectory $installDirectory
    }
    catch {
        $partialInstallMessage = $_.Exception.Message
    }
    Assert-ReconcTest ($partialInstallMessage.Contains("ownership receipt may be incomplete")) "off-PATH install lacked exact partial-state failure"
    Assert-ReconcTest (Test-Path -LiteralPath $installedPath -PathType Leaf) "verified artifact was not installed"
    Assert-ReconcTest ((Get-ReconcFileSha256 -Path $installedPath) -eq $fixtureChecksum) "installed artifact checksum changed"

    $savedProcessPath = $env:Path
    $savedReconcHome = $env:RECONC_HOME
    $shadowDirectory = Join-Path $temporaryDirectory "shadow"
    $receiptHome = Join-Path $temporaryDirectory "reconc-home"
    [void](New-Item -ItemType Directory -Path $shadowDirectory)
    Copy-Item -LiteralPath $fixtureArtifact -Destination (Join-Path $shadowDirectory "reconc.exe")
    try {
        $env:Path = "$installDirectory;$savedProcessPath"
        $env:RECONC_HOME = $receiptHome
        Assert-ReconcTest (Test-ReconcCommandMatches -ExpectedChecksum $fixtureChecksum -ExpectedPath $installedPath) "current installed command was not recognized on PATH"
        $receiptedPath = Install-ReconcVerifiedArtifact `
            -ArtifactPath $fixtureArtifact `
            -ExpectedChecksum $fixtureChecksum `
            -InstallDirectory $installDirectory `
            -ReleaseVersion $ExpectedVersion `
            -AssetName $assetName `
            -ProvenanceState "embedded-verified"
        Assert-ReconcTest ($receiptedPath -eq $installedPath) "receipted install changed the canonical target"
        $receiptPath = Join-Path $receiptHome "install\receipt.json"
        Assert-ReconcTest (Test-Path -LiteralPath $receiptPath -PathType Leaf) "PATH-ready install did not publish an ownership receipt"
        $globalDiagnostic = (& $installedPath doctor --global --json | Out-String) | ConvertFrom-Json
        Assert-ReconcTest ($globalDiagnostic.status -eq "healthy") "PATH-ready install is not globally healthy"
        Assert-ReconcTest ($globalDiagnostic.owner -eq "direct") "Windows installer did not retain direct ownership"
        Assert-ReconcTest ($globalDiagnostic.channel -eq "exact") "Windows installer did not retain the exact channel"
        $env:Path = "$shadowDirectory;$installDirectory;$savedProcessPath"
        Assert-ReconcTest (-not (Test-ReconcCommandMatches -ExpectedChecksum $fixtureChecksum -ExpectedPath $installedPath)) "shadowed PATH command was incorrectly accepted"
    }
    finally {
        $env:Path = $savedProcessPath
        $env:RECONC_HOME = $savedReconcHome
    }

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
    Assert-ReconcFailure {
        Confirm-ReconcAttestation `
            -ArtifactPath $fixtureArtifact `
            -Tool $missingTool `
            -Repository "example/reconc"
    } "mandatory attestation tool missing"

    $passTool = Join-Path $temporaryDirectory "attestation-pass.cmd"
    $attestationArgumentsPath = Join-Path $temporaryDirectory "attestation-arguments.txt"
    $savedAttestationArgumentsPath = $env:RECONC_TEST_ATTESTATION_ARGUMENTS
    try {
        $env:RECONC_TEST_ATTESTATION_ARGUMENTS = $attestationArgumentsPath
        Set-Content -LiteralPath $passTool -Encoding ASCII -Value @(
            "@echo off",
            "echo %* > `"%RECONC_TEST_ATTESTATION_ARGUMENTS%`"",
            "exit /b 0"
        )
        Confirm-ReconcAttestation `
            -ArtifactPath $fixtureArtifact `
            -Tool $passTool `
            -Repository "example/reconc"
    }
    finally {
        $env:RECONC_TEST_ATTESTATION_ARGUMENTS = $savedAttestationArgumentsPath
    }
    $attestationArguments = Get-Content -LiteralPath $attestationArgumentsPath -Raw -Encoding ASCII
    Assert-ReconcTest ($attestationArguments.Contains("attestation verify")) "attestation verifier did not receive the candidate operation"
    Assert-ReconcTest ($attestationArguments.Contains("--repo example/reconc")) "attestation verifier did not bind the repository"
    Assert-ReconcTest ($attestationArguments.Contains("--signer-workflow example/reconc/.github/workflows/reconc-release.yml")) "attestation verifier did not bind the release workflow"
    Assert-ReconcTest ($attestationArguments.Contains("--source-ref refs/tags/reconc-v$ExpectedVersion")) "attestation verifier did not bind the exact release tag"
    Assert-ReconcTest ($attestationArguments.Contains("--deny-self-hosted-runners")) "attestation verifier did not bind hosted-runner provenance"

    $failTool = Join-Path $temporaryDirectory "attestation-fail.cmd"
    Set-Content -LiteralPath $failTool -Encoding ASCII -Value "@exit /b 1"
    Assert-ReconcFailure {
        Confirm-ReconcAttestation `
            -ArtifactPath $fixtureArtifact `
            -Tool $failTool `
            -Repository "example/reconc"
    } "mandatory attestation failure"

    $failingArtifact = Join-Path $temporaryDirectory "install-cli-failure.cmd"
    Set-Content -LiteralPath $failingArtifact -Encoding ASCII -Value @(
        "@echo off",
        "if `"%~1`"==`"--version`" (echo reconc $ExpectedVersion& exit /b 0)",
        "if not `"%~1`"==`"install-cli`" exit /b 2",
        "if not `"%~2`"==`"--install-dir`" exit /b 3",
        "if not exist `"%~3`" mkdir `"%~3`"",
        "copy /Y `"%~f0`" `"%~3\reconc.exe`" >nul",
        "echo {`"partial`":true}",
        "exit /b 23"
    )
    $failingChecksum = Get-ReconcFileSha256 -Path $failingArtifact
    $failingInstallDirectory = Join-Path $temporaryDirectory "install-cli-failure"
    $failureMessage = ""
    try {
        Install-ReconcVerifiedArtifact `
            -ArtifactPath $failingArtifact `
            -ExpectedChecksum $failingChecksum `
            -InstallDirectory $failingInstallDirectory `
            -ReleaseVersion $ExpectedVersion `
            -AssetName $assetName `
            -ProvenanceState "github-verified"
    }
    catch {
        $failureMessage = $_.Exception.Message
    }
    Assert-ReconcTest ($failureMessage.Contains("ownership receipt may be incomplete")) "non-zero install-cli result lacked exact partial-state failure"
    Copy-Item -LiteralPath $fixtureArtifact -Destination (Join-Path $failingInstallDirectory "reconc.exe") -Force
    $failureMessage = ""
    try {
        Install-ReconcVerifiedArtifact `
            -ArtifactPath $failingArtifact `
            -ExpectedChecksum $failingChecksum `
            -InstallDirectory $failingInstallDirectory `
            -ReleaseVersion $ExpectedVersion `
            -AssetName $assetName `
            -ProvenanceState "github-verified"
    }
    catch {
        $failureMessage = $_.Exception.Message
    }
    Assert-ReconcTest ($failureMessage.Contains("ownership receipt may be incomplete")) "non-zero upgrade result lacked exact partial-state failure"

    if ($LiveRelease) {
        $savedInstallDirectory = [Environment]::GetEnvironmentVariable("RECONC_INSTALL_DIR")
        $savedReleaseBase = [Environment]::GetEnvironmentVariable("RECONC_RELEASE_BASE")
        $savedLiveProcessPath = $env:Path
        $liveInstallDirectory = Join-Path $temporaryDirectory "live"
        try {
            [Environment]::SetEnvironmentVariable("RECONC_INSTALL_DIR", $liveInstallDirectory)
            [Environment]::SetEnvironmentVariable("RECONC_RELEASE_BASE", $null)
            $env:Path = "$liveInstallDirectory;$savedLiveProcessPath"
            & $resolvedInstaller $ExpectedVersion
        }
        finally {
            [Environment]::SetEnvironmentVariable("RECONC_INSTALL_DIR", $savedInstallDirectory)
            [Environment]::SetEnvironmentVariable("RECONC_RELEASE_BASE", $savedReleaseBase)
            $env:Path = $savedLiveProcessPath
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
