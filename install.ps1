[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateNotNullOrEmpty()]
    [string]$Version = "0.8.8"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Get-ReconcEnvironmentValue {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name,
        [Parameter(Mandatory = $true)]
        [string]$Default
    )

    $value = [Environment]::GetEnvironmentVariable($Name)
    if ([string]::IsNullOrWhiteSpace($value)) {
        return $Default
    }
    return $value
}

function Assert-ReconcStableVersion {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Value
    )

    if ($Value -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') {
        throw "Version must be a stable semantic version: $Value"
    }
}

function Get-ReconcWindowsAssetName {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ReleaseVersion
    )

    $architecture = $env:PROCESSOR_ARCHITEW6432
    if ([string]::IsNullOrWhiteSpace($architecture)) {
        $architecture = $env:PROCESSOR_ARCHITECTURE
    }
    if ($architecture -ne "AMD64") {
        throw "Unsupported Windows architecture '$architecture'. Reconc releases currently ship Windows x64 only."
    }
    return "reconc-$ReleaseVersion-windows-amd64.exe"
}

function Get-ReconcFileSha256 {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "File not found: $Path"
    }
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Get-ReconcExpectedChecksum {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ManifestPath,
        [Parameter(Mandatory = $true)]
        [string]$AssetName
    )

    if (-not (Test-Path -LiteralPath $ManifestPath -PathType Leaf)) {
        throw "Checksum manifest not found: $ManifestPath"
    }

    $escapedName = [Regex]::Escape($AssetName)
    $candidatePattern = "[`t ]+\*?$escapedName[`t ]*$"
    $candidates = @(Get-Content -LiteralPath $ManifestPath -Encoding UTF8 |
        Where-Object { $_ -match $candidatePattern })
    if ($candidates.Count -ne 1) {
        throw "Expected exactly one SHA256SUMS entry for $AssetName; found $($candidates.Count)."
    }

    $entryPattern = "^(?<hash>[0-9A-Fa-f]{64})[`t ]+\*?$escapedName[`t ]*$"
    $match = [Regex]::Match($candidates[0], $entryPattern)
    if (-not $match.Success) {
        throw "Malformed SHA256SUMS entry for $AssetName."
    }
    return $match.Groups["hash"].Value.ToLowerInvariant()
}

function Invoke-ReconcHttpsDownload {
    param(
        [Parameter(Mandatory = $true)]
        [Uri]$Uri,
        [Parameter(Mandatory = $true)]
        [string]$Destination
    )

    if ($Uri.Scheme -ne [Uri]::UriSchemeHttps) {
        throw "Release downloads require HTTPS: $Uri"
    }
    if (Test-Path -LiteralPath $Destination) {
        throw "Download destination already exists: $Destination"
    }

    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    Add-Type -AssemblyName System.Net.Http
    $handler = New-Object System.Net.Http.HttpClientHandler
    $handler.AllowAutoRedirect = $false
    $client = New-Object System.Net.Http.HttpClient($handler)
    $client.Timeout = [TimeSpan]::FromMinutes(2)
    $client.DefaultRequestHeaders.UserAgent.ParseAdd("reconc-installer")
    $current = $Uri

    try {
        for ($redirects = 0; $redirects -le 5; $redirects++) {
            $response = $null
            try {
                $response = $client.GetAsync(
                    $current,
                    [Net.Http.HttpCompletionOption]::ResponseHeadersRead
                ).GetAwaiter().GetResult()
                $status = [int]$response.StatusCode
                if ($status -in @(301, 302, 303, 307, 308)) {
                    if ($redirects -eq 5 -or $null -eq $response.Headers.Location) {
                        throw "Release download exceeded the HTTPS redirect limit: $Uri"
                    }
                    $current = New-Object Uri -ArgumentList $current, $response.Headers.Location
                    if ($current.Scheme -ne [Uri]::UriSchemeHttps) {
                        throw "Release download redirected outside HTTPS: $current"
                    }
                    continue
                }

                [void]$response.EnsureSuccessStatusCode()
                $inputStream = $null
                $outputStream = $null
                try {
                    $inputStream = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
                    $outputStream = [IO.File]::Open(
                        $Destination,
                        [IO.FileMode]::CreateNew,
                        [IO.FileAccess]::Write,
                        [IO.FileShare]::None
                    )
                    $inputStream.CopyTo($outputStream)
                    $outputStream.Flush()
                }
                finally {
                    if ($null -ne $outputStream) {
                        $outputStream.Dispose()
                    }
                    if ($null -ne $inputStream) {
                        $inputStream.Dispose()
                    }
                }
                return
            }
            finally {
                if ($null -ne $response) {
                    $response.Dispose()
                }
            }
        }
    }
    catch {
        if (Test-Path -LiteralPath $Destination -PathType Leaf) {
            Remove-Item -LiteralPath $Destination -Force
        }
        throw
    }
    finally {
        $client.Dispose()
        $handler.Dispose()
    }
}

function Confirm-ReconcAttestation {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ArtifactPath,
        [Parameter(Mandatory = $true)]
        [string]$Tool,
        [Parameter(Mandatory = $true)]
        [string]$Repository,
        [Parameter(Mandatory = $true)]
        [bool]$Required
    )

    $command = Get-Command -Name $Tool -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -eq $command) {
        if ($Required) {
            throw "Attestation verification is required, but '$Tool' is unavailable."
        }
        Write-Host "note: '$Tool' is unavailable; checksum verification remains enforced."
        return
    }

    try {
        $attestationOutput = & $command.Source attestation verify $ArtifactPath --repo $Repository 2>&1
    }
    catch {
        if ($Required) {
            throw "Attestation verification could not run for $ArtifactPath. $($_.Exception.Message)"
        }
        Write-Warning "Attestation verification could not run; checksum verification remains enforced. $($_.Exception.Message)"
        return
    }
    if ($LASTEXITCODE -eq 0) {
        return
    }
    $detail = ($attestationOutput | Out-String).Trim()
    if ($Required) {
        throw "Attestation verification failed for $ArtifactPath. $detail"
    }
    Write-Warning "Attestation verification failed; checksum verification remains enforced. $detail"
}

function Install-ReconcVerifiedArtifact {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ArtifactPath,
        [Parameter(Mandatory = $true)]
        [string]$ExpectedChecksum,
        [Parameter(Mandatory = $true)]
        [string]$InstallDirectory
    )

    if ($ExpectedChecksum -notmatch '^[0-9a-f]{64}$') {
        throw "Expected checksum is not a lowercase hexadecimal SHA-256 digest."
    }
    $downloadedChecksum = Get-ReconcFileSha256 -Path $ArtifactPath
    if ($downloadedChecksum -ne $ExpectedChecksum) {
        throw "Checksum mismatch for downloaded Windows binary."
    }

    [void](New-Item -ItemType Directory -Path $InstallDirectory -Force)
    $resolvedInstallDirectory = [IO.Path]::GetFullPath($InstallDirectory)
    $targetPath = Join-Path $resolvedInstallDirectory "reconc.exe"
    $stagePath = Join-Path $resolvedInstallDirectory ".reconc-stage-$([Guid]::NewGuid().ToString('N')).exe"
    $backupPath = Join-Path $resolvedInstallDirectory ".reconc-backup-$([Guid]::NewGuid().ToString('N')).exe"
    $hadExistingTarget = Test-Path -LiteralPath $targetPath -PathType Leaf
    $published = $false
    $preserveBackup = $false

    try {
        Copy-Item -LiteralPath $ArtifactPath -Destination $stagePath
        $stagedChecksum = Get-ReconcFileSha256 -Path $stagePath
        if ($stagedChecksum -ne $ExpectedChecksum) {
            throw "Checksum mismatch after staging the Windows binary."
        }

        $smokeOutput = & $stagePath --version 2>&1
        if ($LASTEXITCODE -ne 0) {
            $detail = ($smokeOutput | Out-String).Trim()
            throw "Staged Windows binary failed its version smoke test. $detail"
        }

        if ($hadExistingTarget) {
            [IO.File]::Replace($stagePath, $targetPath, $backupPath)
        }
        else {
            [IO.File]::Move($stagePath, $targetPath)
        }
        $published = $true

        if ((Get-ReconcFileSha256 -Path $targetPath) -ne $ExpectedChecksum) {
            throw "Checksum mismatch after publishing the Windows binary."
        }
    }
    catch {
        if ($published) {
            if ($hadExistingTarget -and (Test-Path -LiteralPath $backupPath -PathType Leaf)) {
                try {
                    [IO.File]::Replace($backupPath, $targetPath, $null)
                }
                catch {
                    $preserveBackup = $true
                    throw "Publication verification failed and automatic rollback failed. The previous binary remains at $backupPath. $($_.Exception.Message)"
                }
            }
            elseif (-not $hadExistingTarget -and (Test-Path -LiteralPath $targetPath -PathType Leaf)) {
                Remove-Item -LiteralPath $targetPath -Force
            }
        }
        throw
    }
    finally {
        if (Test-Path -LiteralPath $stagePath -PathType Leaf) {
            Remove-Item -LiteralPath $stagePath -Force
        }
        if (-not $preserveBackup -and (Test-Path -LiteralPath $backupPath -PathType Leaf)) {
            try {
                Remove-Item -LiteralPath $backupPath -Force
            }
            catch {
                Write-Warning "Installed successfully, but could not remove backup file: $backupPath"
            }
        }
    }

    return $targetPath
}

function Test-ReconcCommandMatches {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ExpectedChecksum
    )

    $command = Get-Command -Name "reconc" -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -eq $command) {
        return $false
    }
    try {
        return (Get-ReconcFileSha256 -Path $command.Source) -eq $ExpectedChecksum
    }
    catch {
        return $false
    }
}

function Invoke-ReconcInstall {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ReleaseVersion
    )

    Assert-ReconcStableVersion -Value $ReleaseVersion
    if ($env:OS -ne "Windows_NT") {
        throw "install.ps1 supports Windows only."
    }
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        throw "LOCALAPPDATA is unavailable; set RECONC_INSTALL_DIR explicitly."
    }

    $assetName = Get-ReconcWindowsAssetName -ReleaseVersion $ReleaseVersion
    $defaultInstallDirectory = Join-Path $env:LOCALAPPDATA "Programs\Reconc\bin"
    $installDirectory = Get-ReconcEnvironmentValue -Name "RECONC_INSTALL_DIR" -Default $defaultInstallDirectory
    $defaultReleaseBase = "https://github.com/Christopher-Schulze/reconc/releases/download/reconc-v$ReleaseVersion"
    $releaseBase = (Get-ReconcEnvironmentValue -Name "RECONC_RELEASE_BASE" -Default $defaultReleaseBase).TrimEnd('/')
    $attestationTool = Get-ReconcEnvironmentValue -Name "RECONC_ATTESTATION_TOOL" -Default "gh"
    $attestationRepository = Get-ReconcEnvironmentValue -Name "RECONC_ATTESTATION_REPO" -Default "Christopher-Schulze/reconc"
    $requireAttestation = (Get-ReconcEnvironmentValue -Name "RECONC_REQUIRE_ATTESTATION" -Default "0") -eq "1"

    $temporaryDirectory = Join-Path ([IO.Path]::GetTempPath()) "reconc-install-$([Guid]::NewGuid().ToString('N'))"
    [void](New-Item -ItemType Directory -Path $temporaryDirectory)
    try {
        $artifactPath = Join-Path $temporaryDirectory $assetName
        $manifestPath = Join-Path $temporaryDirectory "SHA256SUMS"
        Invoke-ReconcHttpsDownload -Uri ([Uri]"$releaseBase/$assetName") -Destination $artifactPath
        Invoke-ReconcHttpsDownload -Uri ([Uri]"$releaseBase/SHA256SUMS") -Destination $manifestPath
        $expectedChecksum = Get-ReconcExpectedChecksum -ManifestPath $manifestPath -AssetName $assetName
        if ((Get-ReconcFileSha256 -Path $artifactPath) -ne $expectedChecksum) {
            throw "Checksum mismatch for downloaded Windows binary."
        }
        Confirm-ReconcAttestation `
            -ArtifactPath $artifactPath `
            -Tool $attestationTool `
            -Repository $attestationRepository `
            -Required $requireAttestation
        $targetPath = Install-ReconcVerifiedArtifact `
            -ArtifactPath $artifactPath `
            -ExpectedChecksum $expectedChecksum `
            -InstallDirectory $installDirectory
        Write-Host "installed reconc $ReleaseVersion at $targetPath"

        if (-not (Test-ReconcCommandMatches -ExpectedChecksum $expectedChecksum)) {
            $escapedDirectory = $installDirectory.Replace("'", "''")
            Write-Host "Put the install directory first on your user PATH, then open a new terminal:"
            Write-Host "`$userPath = [Environment]::GetEnvironmentVariable('Path', 'User'); [Environment]::SetEnvironmentVariable('Path', (('$escapedDirectory;' + `$userPath).TrimEnd(';')), 'User')"
        }
    }
    finally {
        if (Test-Path -LiteralPath $temporaryDirectory -PathType Container) {
            Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force
        }
    }
}

if ($MyInvocation.InvocationName -ne ".") {
    Invoke-ReconcInstall -ReleaseVersion $Version
}
