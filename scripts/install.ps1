# Sussurro Windows installer.
# Usage: irm https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install.ps1 | iex

$ErrorActionPreference = "Stop"

# Windows PowerShell 5.1 still negotiates TLS 1.0 by default on some installs,
# which api.github.com rejects outright. PowerShell 7+ already defaults to 1.2+.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

# Invoke-WebRequest's progress bar makes large downloads several times slower
# in Windows PowerShell; suppress it and report progress ourselves.
$previousProgress = $ProgressPreference
$ProgressPreference = "SilentlyContinue"

$repo       = "aploide/sussurro"
$assetName  = "sussurro-windows-amd64.zip"
$installDir = Join-Path $env:LOCALAPPDATA "Programs\Sussurro"
$extractDir = Join-Path $env:TEMP "sussurro-extract"
$zipPath    = Join-Path $env:TEMP $assetName
$shaPath    = Join-Path $env:TEMP "$assetName.sha256"

function Write-Step($message) { Write-Host "  -> $message" }
function Write-Ok($message)   { Write-Host "  [ok] $message" -ForegroundColor Green }
function Write-Warn($message) { Write-Host "  [!] $message" -ForegroundColor Yellow }

try {
    Write-Host "Sussurro Windows Installer" -ForegroundColor Cyan
    Write-Host "=========================="

    # Releases publish an x64 build only. Windows on ARM runs it through the
    # x64 emulation layer, but Vulkan acceleration depends on the emulated
    # driver stack, so warn rather than pretend it is a supported target.
    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($arch -eq "ARM64") {
        Write-Warn "Detected ARM64. Only an x64 build is published; it will run under emulation and GPU acceleration may be unavailable."
    } elseif ($arch -ne "AMD64") {
        throw "Unsupported architecture '$arch'. Releases ship windows-amd64 only. Build from source: https://github.com/$repo/blob/master/docs/compilation.md"
    }

    # The overlay and settings window are WebView2-backed, so a missing runtime
    # surfaces as a silent UI failure at first launch rather than an install error.
    $webview2Keys = @(
        "HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}",
        "HKLM:\SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}",
        "HKCU:\SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
    )
    $hasWebView2 = $webview2Keys | Where-Object { Test-Path $_ } | Select-Object -First 1
    if (-not $hasWebView2) {
        Write-Warn "WebView2 runtime not detected. The overlay and settings window need it."
        Write-Warn "Install it from https://developer.microsoft.com/microsoft-edge/webview2/ (preinstalled on Windows 11)."
    }

    # Latest release
    Write-Step "Fetching latest release..."
    $release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
    $tag = $release.tag_name
    $asset = $release.assets | Where-Object { $_.name -eq $assetName }
    if (-not $asset) {
        throw "No Windows build found in release $tag. Windows builds start after v2.3."
    }

    Write-Step "Downloading Sussurro $tag..."
    Invoke-WebRequest $asset.browser_download_url -OutFile $zipPath

    # Every release asset ships a `<asset>.sha256` from scripts/package-release.sh.
    $checksumAsset = $release.assets | Where-Object { $_.name -eq "$assetName.sha256" }
    if ($checksumAsset) {
        Write-Step "Verifying checksum..."
        # Download to a file rather than reading .Content: GitHub serves assets as
        # application/octet-stream, and Invoke-WebRequest hands back a Byte[] instead
        # of a String for non-text content types on some PowerShell versions.
        Invoke-WebRequest $checksumAsset.browser_download_url -OutFile $shaPath
        # Format is `<sha256>  <asset-name>`, as emitted by sha256sum.
        $expected = ((Get-Content $shaPath -Raw).Trim() -split '\s+')[0]
        $actual = (Get-FileHash $zipPath -Algorithm SHA256).Hash
        if ([string]::IsNullOrWhiteSpace($expected)) {
            Write-Warn "Checksum file is empty - skipping verification."
        } elseif ($actual -ne $expected) {
            throw "Checksum mismatch - refusing to install.`n    expected: $expected`n    actual:   $actual"
        } else {
            Write-Ok "Checksum verified"
        }
    } else {
        Write-Warn "Checksum file not published for $tag - skipping verification."
    }

    Write-Step "Installing to $installDir..."
    New-Item -ItemType Directory -Force $installDir | Out-Null
    if (Test-Path $extractDir) { Remove-Item $extractDir -Recurse -Force }
    Expand-Archive -Path $zipPath -DestinationPath $extractDir -Force

    # Zip layout includes the app and its sibling LLM helper.
    $payload = Join-Path $extractDir "sussurro-windows-amd64"
    if (-not (Test-Path (Join-Path $payload "sussurro.exe")) -or
        -not (Test-Path (Join-Path $payload "sussurro-llm-helper.exe"))) {
        throw "Required binaries not found in sussurro-windows-amd64"
    }
    Copy-Item "$payload\*" $installDir -Recurse -Force

    # Add to user PATH if missing
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ([string]::IsNullOrEmpty($userPath)) {
        [Environment]::SetEnvironmentVariable("Path", $installDir, "User")
        Write-Ok "Added $installDir to your user PATH (new terminals will pick it up)."
    } elseif (($userPath -split ';') -notcontains $installDir) {
        [Environment]::SetEnvironmentVariable("Path", "$($userPath.TrimEnd(';'));$installDir", "User")
        Write-Ok "Added $installDir to your user PATH (new terminals will pick it up)."
    }

    Write-Host ""
    Write-Host "Sussurro $tag installed!" -ForegroundColor Green
    Write-Host "Run 'sussurro' from a new terminal (or $installDir\sussurro.exe)."
    Write-Host "First run downloads the AI models (~1.8 GB) and creates %USERPROFILE%\.sussurro."
    Write-Host "Hold Ctrl+Shift+Space to talk, release to transcribe."
}
finally {
    Remove-Item $zipPath, $shaPath, $extractDir -Recurse -Force -ErrorAction SilentlyContinue
    $ProgressPreference = $previousProgress
}
