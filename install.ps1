[CmdletBinding(DefaultParameterSetName = "Channel")]
param(
    [Parameter(ParameterSetName = "Version", Position = 0, Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$Version,
    [Parameter(ParameterSetName = "Channel")]
    [ValidateSet("Stable", "Preview")]
    [string]$Channel = "Stable",
    [switch]$AllowDowngrade
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

function Assert-ReconcSemanticVersion {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Value
    )

    if ($Value -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$') {
        throw "Version must be supported semantic versioning: $Value"
    }
    $coreVersion = ($Value -split "-", 2)[0]
    foreach ($component in ($coreVersion -split "\.")) {
        [UInt64]$parsedComponent = 0
        if (-not [UInt64]::TryParse($component, [ref]$parsedComponent)) {
            throw "Version component exceeds the supported unsigned 64-bit range: $Value"
        }
    }
    $separator = $Value.IndexOf("-", [StringComparison]::Ordinal)
    if ($separator -ge 0) {
        foreach ($identifier in $Value.Substring($separator + 1).Split(".")) {
            if ($identifier -match '^0[0-9]+$') {
                throw "Numeric prerelease identifiers must not contain leading zeroes: $Value"
            }
        }
    }
}

function Compare-ReconcSemanticVersion {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Left,
        [Parameter(Mandatory = $true)]
        [string]$Right
    )

    Assert-ReconcSemanticVersion -Value $Left
    Assert-ReconcSemanticVersion -Value $Right
    $pattern = '^(?<major>[0-9]+)\.(?<minor>[0-9]+)\.(?<patch>[0-9]+)(-(?<pre>.+))?$'
    $leftMatch = [Regex]::Match($Left, $pattern)
    $rightMatch = [Regex]::Match($Right, $pattern)
    foreach ($part in @("major", "minor", "patch")) {
        $leftValue = [UInt64]::Parse($leftMatch.Groups[$part].Value)
        $rightValue = [UInt64]::Parse($rightMatch.Groups[$part].Value)
        if ($leftValue -lt $rightValue) {
            return -1
        }
        if ($leftValue -gt $rightValue) {
            return 1
        }
    }

    $leftPre = $leftMatch.Groups["pre"].Value
    $rightPre = $rightMatch.Groups["pre"].Value
    if ($leftPre -eq $rightPre) {
        return 0
    }
    if ([string]::IsNullOrEmpty($leftPre)) {
        return 1
    }
    if ([string]::IsNullOrEmpty($rightPre)) {
        return -1
    }
    $leftIdentifiers = @($leftPre.Split("."))
    $rightIdentifiers = @($rightPre.Split("."))
    $count = [Math]::Min($leftIdentifiers.Count, $rightIdentifiers.Count)
    for ($index = 0; $index -lt $count; $index++) {
        if ($leftIdentifiers[$index] -ceq $rightIdentifiers[$index]) {
            continue
        }
        $leftNumeric = $leftIdentifiers[$index] -match '^[0-9]+$'
        $rightNumeric = $rightIdentifiers[$index] -match '^[0-9]+$'
        if ($leftNumeric -and $rightNumeric) {
            if ($leftIdentifiers[$index].Length -ne $rightIdentifiers[$index].Length) {
                return $(if ($leftIdentifiers[$index].Length -lt $rightIdentifiers[$index].Length) { -1 } else { 1 })
            }
            return [Math]::Sign([string]::CompareOrdinal($leftIdentifiers[$index], $rightIdentifiers[$index]))
        }
        if ($leftNumeric) {
            return -1
        }
        if ($rightNumeric) {
            return 1
        }
        return [Math]::Sign([string]::CompareOrdinal($leftIdentifiers[$index], $rightIdentifiers[$index]))
    }
    return $(if ($leftIdentifiers.Count -lt $rightIdentifiers.Count) { -1 } else { 1 })
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

function Assert-ReconcDownloadLength {
    param(
        [AllowNull()]
        [object]$ContentLength,
        [Parameter(Mandatory = $true)]
        [ValidateRange(1, 268435456)]
        [long]$MaximumBytes,
        [Parameter(Mandatory = $true)]
        [Uri]$Uri
    )

    if ($null -eq $ContentLength) {
        return
    }
    $reportedBytes = [long]$ContentLength
    if ($reportedBytes -lt 0) {
        throw "Release download returned an invalid negative Content-Length: $Uri"
    }
    if ($reportedBytes -gt $MaximumBytes) {
        throw "Release download exceeds $MaximumBytes bytes: $Uri"
    }
}

function Invoke-ReconcHttpsDownload {
    param(
        [Parameter(Mandatory = $true)]
        [Uri]$Uri,
        [Parameter(Mandatory = $true)]
        [string]$Destination,
        [ValidateRange(1, 268435456)]
        [long]$MaximumBytes = 268435456
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
                Assert-ReconcDownloadLength `
                    -ContentLength $response.Content.Headers.ContentLength `
                    -MaximumBytes $MaximumBytes `
                    -Uri $current
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
                    $buffer = New-Object byte[] 81920
                    $total = 0L
                    while (($read = $inputStream.Read($buffer, 0, $buffer.Length)) -gt 0) {
                        $total += $read
                        if ($total -gt $MaximumBytes) {
                            throw "Release download exceeds $MaximumBytes bytes: $current"
                        }
                        $outputStream.Write($buffer, 0, $read)
                    }
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

function Resolve-ReconcReleaseSelection {
    param(
        [string]$RequestedVersion = "",
        [ValidateSet("Stable", "Preview")]
        [string]$RequestedChannel = "Stable",
        [Parameter(Mandatory = $true)]
        [string]$TemporaryDirectory
    )

    if (-not [string]::IsNullOrWhiteSpace($RequestedVersion)) {
        Assert-ReconcSemanticVersion -Value $RequestedVersion
        return [PSCustomObject]@{
            Version = $RequestedVersion
            Channel = "exact"
        }
    }
    if (-not [string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable("RECONC_RELEASE_BASE"))) {
        throw "Channel discovery is unavailable with RECONC_RELEASE_BASE; use -Version."
    }
    $apiBase = Get-ReconcEnvironmentValue `
        -Name "RECONC_RELEASE_API_BASE" `
        -Default "https://api.github.com/repos/Christopher-Schulze/reconc"
    $metadataPath = Join-Path $TemporaryDirectory "release-selection.json"
    if ($RequestedChannel -eq "Stable") {
        $endpoint = "$($apiBase.TrimEnd('/'))/releases/latest"
    }
    else {
        $endpoint = "$($apiBase.TrimEnd('/'))/releases?per_page=32"
    }
    Invoke-ReconcHttpsDownload `
        -Uri ([Uri]$endpoint) `
        -Destination $metadataPath `
        -MaximumBytes 2097152
    $metadata = Get-Content -LiteralPath $metadataPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($RequestedChannel -eq "Stable") {
        $release = $metadata
        if ($release.draft -or $release.prerelease) {
            throw "Stable channel selected a draft or prerelease."
        }
    }
    else {
        $release = @($metadata | Where-Object { -not $_.draft -and $_.prerelease }) |
            Select-Object -First 1
        if ($null -eq $release) {
            throw "No non-draft preview release is available."
        }
    }
    $tag = [string]$release.tag_name
    if (-not $tag.StartsWith("reconc-v", [StringComparison]::Ordinal)) {
        throw "Release metadata returned a noncanonical tag: $tag"
    }
    $resolvedVersion = $tag.Substring("reconc-v".Length)
    Assert-ReconcSemanticVersion -Value $resolvedVersion
    $isPreview = $resolvedVersion.Contains("-")
    if (($RequestedChannel -eq "Preview") -ne $isPreview) {
        throw "Release metadata channel and semantic version disagree: $resolvedVersion"
    }
    return [PSCustomObject]@{
        Version = $resolvedVersion
        Channel = $RequestedChannel.ToLowerInvariant()
    }
}

function Confirm-ReconcAttestation {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ArtifactPath,
        [Parameter(Mandatory = $true)]
        [string]$Tool,
        [Parameter(Mandatory = $true)]
        [string]$Repository
    )

    $command = Get-Command -Name $Tool -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -eq $command) {
        throw "Build-provenance verification requires '$Tool'. Install GitHub CLI and retry."
    }

    try {
        $releaseVersion = [Regex]::Match(
            [IO.Path]::GetFileName($ArtifactPath),
            '^reconc-(?<version>[0-9A-Za-z][0-9A-Za-z.+-]*)-windows-amd64\.exe$'
        ).Groups["version"].Value
        if ([string]::IsNullOrWhiteSpace($releaseVersion)) {
            throw "Cannot derive the immutable release tag from $ArtifactPath."
        }
        $attestationOutput = & $command.Source attestation verify $ArtifactPath `
            --repo $Repository `
            --signer-workflow "$Repository/.github/workflows/reconc-release.yml" `
            --source-ref "refs/tags/reconc-v$releaseVersion" `
            --deny-self-hosted-runners 2>&1
    }
    catch {
        throw "Build-provenance verification could not run for $ArtifactPath. $($_.Exception.Message)"
    }
    if ($LASTEXITCODE -eq 0) {
        return "github-verified"
    }
    $detail = ($attestationOutput | Out-String).Trim()
    throw "Build-provenance verification failed for $ArtifactPath. Verify GitHub CLI access to $Repository and retry. $detail"
}

function Install-ReconcVerifiedArtifact {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ArtifactPath,
        [Parameter(Mandatory = $true)]
        [string]$ExpectedChecksum,
        [Parameter(Mandatory = $true)]
        [string]$InstallDirectory,
        [string]$ReleaseVersion = "",
        [string]$AssetName = "",
        [ValidateSet("stable", "preview", "exact")]
        [string]$InstallChannel = "exact",
        [ValidateSet("github-verified", "embedded-verified")]
        [string]$ProvenanceState = "embedded-verified"
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
    if (Test-Path -LiteralPath $targetPath -PathType Leaf) {
        $exclusiveTarget = [IO.File]::Open(
            $targetPath,
            [IO.FileMode]::Open,
            [IO.FileAccess]::ReadWrite,
            [IO.FileShare]::None
        )
        $exclusiveTarget.Dispose()
    }
    if ([string]::IsNullOrWhiteSpace($AssetName)) {
        $AssetName = [IO.Path]::GetFileName($ArtifactPath)
    }
    if ([string]::IsNullOrWhiteSpace($ReleaseVersion)) {
        $match = [Regex]::Match($AssetName, '^reconc-(?<version>[0-9A-Za-z][0-9A-Za-z.+-]*)-windows-amd64\.exe$')
        if (-not $match.Success) {
            throw "Cannot derive the Reconc release version from artifact '$AssetName'."
        }
        $ReleaseVersion = $match.Groups["version"].Value
    }

    $savedManager = [Environment]::GetEnvironmentVariable("RECONC_INSTALL_MANAGER", "Process")
    $savedChannel = [Environment]::GetEnvironmentVariable("RECONC_INSTALL_CHANNEL", "Process")
    $savedArtifact = [Environment]::GetEnvironmentVariable("RECONC_INSTALL_ARTIFACT", "Process")
    $savedTag = [Environment]::GetEnvironmentVariable("RECONC_INSTALL_RELEASE_TAG", "Process")
    $savedProvenance = [Environment]::GetEnvironmentVariable("RECONC_INSTALL_PROVENANCE", "Process")
    try {
        $env:RECONC_INSTALL_MANAGER = "direct"
        $env:RECONC_INSTALL_CHANNEL = $InstallChannel
        $env:RECONC_INSTALL_ARTIFACT = $AssetName
        $env:RECONC_INSTALL_RELEASE_TAG = "reconc-v$ReleaseVersion"
        $env:RECONC_INSTALL_PROVENANCE = $ProvenanceState
        $installOutput = & $ArtifactPath install-cli --install-dir $resolvedInstallDirectory --json 2>&1
        $installExitCode = $LASTEXITCODE
    }
    finally {
        $env:RECONC_INSTALL_MANAGER = $savedManager
        $env:RECONC_INSTALL_CHANNEL = $savedChannel
        $env:RECONC_INSTALL_ARTIFACT = $savedArtifact
        $env:RECONC_INSTALL_RELEASE_TAG = $savedTag
        $env:RECONC_INSTALL_PROVENANCE = $savedProvenance
    }
    if ($installExitCode -ne 0) {
        $detail = ($installOutput | Out-String).Trim()
        if ((Test-Path -LiteralPath $targetPath -PathType Leaf) -and
            (Get-ReconcFileSha256 -Path $targetPath) -eq $ExpectedChecksum) {
            throw "Install transaction failed after publishing the verified binary; ownership receipt may be incomplete. Rerun this installer for version $ReleaseVersion. $detail"
        }
        throw "Install transaction failed and retained or restored the previous target. $detail"
    }
    if (-not (Test-Path -LiteralPath $targetPath -PathType Leaf) -or
        (Get-ReconcFileSha256 -Path $targetPath) -ne $ExpectedChecksum) {
        $detail = ($installOutput | Out-String).Trim()
        throw "Verified Reconc transaction did not publish the expected binary. $detail"
    }
    return $targetPath
}

function Test-ReconcCommandMatches {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ExpectedChecksum,
        [string]$ExpectedPath = ""
    )

    $command = Get-Command -Name "reconc" -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -eq $command) {
        return $false
    }
    try {
        if (-not [string]::IsNullOrWhiteSpace($ExpectedPath) -and
            [IO.Path]::GetFullPath($command.Source) -ine [IO.Path]::GetFullPath($ExpectedPath)) {
            return $false
        }
        return (Get-ReconcFileSha256 -Path $command.Source) -eq $ExpectedChecksum
    }
    catch {
        return $false
    }
}

function Invoke-ReconcInstall {
    param(
        [string]$RequestedVersion = "",
        [ValidateSet("Stable", "Preview")]
        [string]$RequestedChannel = "Stable",
        [bool]$DowngradeAllowed = $false
    )

    if ($env:OS -ne "Windows_NT") {
        throw "install.ps1 supports Windows only."
    }
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        throw "LOCALAPPDATA is unavailable; set RECONC_INSTALL_DIR explicitly."
    }

    $defaultInstallDirectory = Join-Path $env:LOCALAPPDATA "Programs\Reconc\bin"
    $installDirectory = Get-ReconcEnvironmentValue -Name "RECONC_INSTALL_DIR" -Default $defaultInstallDirectory
    $attestationTool = "gh"
    $attestationRepository = "Christopher-Schulze/reconc"

    $temporaryDirectory = Join-Path ([IO.Path]::GetTempPath()) "reconc-install-$([Guid]::NewGuid().ToString('N'))"
    [void](New-Item -ItemType Directory -Path $temporaryDirectory)
    try {
        $selection = Resolve-ReconcReleaseSelection `
            -RequestedVersion $RequestedVersion `
            -RequestedChannel $RequestedChannel `
            -TemporaryDirectory $temporaryDirectory
        $releaseVersion = $selection.Version
        $installChannel = $selection.Channel
        $assetName = Get-ReconcWindowsAssetName -ReleaseVersion $releaseVersion
        $defaultReleaseBase = "https://github.com/Christopher-Schulze/reconc/releases/download/reconc-v$releaseVersion"
        $releaseBase = (Get-ReconcEnvironmentValue -Name "RECONC_RELEASE_BASE" -Default $defaultReleaseBase).TrimEnd('/')
        $artifactPath = Join-Path $temporaryDirectory $assetName
        $manifestPath = Join-Path $temporaryDirectory "SHA256SUMS"
        Invoke-ReconcHttpsDownload `
            -Uri ([Uri]"$releaseBase/$assetName") `
            -Destination $artifactPath `
            -MaximumBytes 268435456
        Invoke-ReconcHttpsDownload `
            -Uri ([Uri]"$releaseBase/SHA256SUMS") `
            -Destination $manifestPath `
            -MaximumBytes 2097152
        $expectedChecksum = Get-ReconcExpectedChecksum -ManifestPath $manifestPath -AssetName $assetName
        if ((Get-ReconcFileSha256 -Path $artifactPath) -ne $expectedChecksum) {
            throw "Checksum mismatch for downloaded Windows binary."
        }
        $targetPath = Join-Path ([IO.Path]::GetFullPath($installDirectory)) "reconc.exe"
        if (Test-Path -LiteralPath $targetPath -PathType Leaf) {
            $installedOutput = (& $targetPath --version 2>$null | Out-String).Trim()
            $installedMatch = [Regex]::Match($installedOutput, '^reconc (?<version>.+)$')
            if ($installedMatch.Success) {
                $installedVersion = $installedMatch.Groups["version"].Value
                Assert-ReconcSemanticVersion -Value $installedVersion
                if ((Compare-ReconcSemanticVersion -Left $installedVersion -Right $releaseVersion) -gt 0 -and
                    -not $DowngradeAllowed) {
                    throw "Refusing downgrade from $installedVersion to $releaseVersion; rerun with -AllowDowngrade."
                }
            }
        }
        $provenanceState = Confirm-ReconcAttestation `
            -ArtifactPath $artifactPath `
            -Tool $attestationTool `
            -Repository $attestationRepository
        $targetPath = Install-ReconcVerifiedArtifact `
            -ArtifactPath $artifactPath `
            -ExpectedChecksum $expectedChecksum `
            -InstallDirectory $installDirectory `
            -ReleaseVersion $releaseVersion `
            -AssetName $assetName `
            -InstallChannel $installChannel `
            -ProvenanceState $provenanceState
        Write-Host "installed reconc $releaseVersion ($installChannel) at $targetPath"

        if (-not (Test-ReconcCommandMatches -ExpectedChecksum $expectedChecksum -ExpectedPath $targetPath)) {
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
    if ($PSCmdlet.ParameterSetName -eq "Version") {
        Invoke-ReconcInstall `
            -RequestedVersion $Version `
            -DowngradeAllowed $AllowDowngrade.IsPresent
    }
    else {
        Invoke-ReconcInstall `
            -RequestedChannel $Channel `
            -DowngradeAllowed $AllowDowngrade.IsPresent
    }
}
