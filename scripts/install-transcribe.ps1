# sussurro-transcribe Windows installer.
# Usage: irm https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install-transcribe.ps1 | iex

$ErrorActionPreference = "Stop"

# Windows PowerShell 5.1 still negotiates TLS 1.0 by default on some installs,
# which api.github.com rejects outright. PowerShell 7+ already defaults to 1.2+.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$previousProgress = $ProgressPreference
$ProgressPreference = "SilentlyContinue"

$repo       = "aploide/sussurro"
$assetName  = "sussurro-transcribe-windows-amd64.zip"
# Share the main app's install directory so both binaries land on one PATH entry.
$installDir = Join-Path $env:LOCALAPPDATA "Programs\Sussurro"
$extractDir = Join-Path $env:TEMP "sussurro-transcribe-extract"
$zipPath    = Join-Path $env:TEMP $assetName
$shaPath    = Join-Path $env:TEMP "$assetName.sha256"

function Write-Step($message) { Write-Host "  -> $message" }
function Write-Ok($message)   { Write-Host "  [ok] $message" -ForegroundColor Green }
function Write-Warn($message) { Write-Host "  [!] $message" -ForegroundColor Yellow }

try {
    Write-Host "sussurro-transcribe Windows Installer" -ForegroundColor Cyan
    Write-Host "====================================="

    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($arch -eq "ARM64") {
        Write-Warn "Detected ARM64. Only an x64 build is published; it will run under emulation and GPU acceleration may be unavailable."
    } elseif ($arch -ne "AMD64") {
        throw "Unsupported architecture '$arch'. Releases ship windows-amd64 only. Build from source: https://github.com/$repo/blob/master/docs/compilation.md"
    }

    # ffmpeg decodes the input audio; without it the CLI fails on every file.
    if (Get-Command ffmpeg -ErrorAction SilentlyContinue) {
        Write-Ok "ffmpeg found"
    } else {
        Write-Warn "ffmpeg not found - sussurro-transcribe needs it to decode audio files."
        Write-Warn "Install it with:  winget install Gyan.FFmpeg"
    }

    Write-Step "Fetching latest release..."
    $release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
    $tag = $release.tag_name
    $asset = $release.assets | Where-Object { $_.name -eq $assetName }
    if (-not $asset) {
        throw "No Windows transcribe build found in release $tag. Windows builds start after v2.3."
    }

    Write-Step "Downloading sussurro-transcribe $tag..."
    Invoke-WebRequest $asset.browser_download_url -OutFile $zipPath

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

    # Zip layout includes the CLI and its sibling LLM helper.
    $payload = Join-Path $extractDir "sussurro-transcribe-windows-amd64"
    $exe = Join-Path $payload "sussurro-transcribe.exe"
    $helper = Join-Path $payload "sussurro-llm-helper.exe"
    if (-not (Test-Path $exe) -or -not (Test-Path $helper)) {
        throw "Required binaries not found in sussurro-transcribe-windows-amd64"
    }
    # Copy only executables: config.example.yaml and INSTALL.txt must not
    # overwrite the main app's copies in the shared directory.
    Copy-Item $exe $installDir -Force
    Copy-Item $helper $installDir -Force

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ([string]::IsNullOrEmpty($userPath)) {
        [Environment]::SetEnvironmentVariable("Path", $installDir, "User")
        Write-Ok "Added $installDir to your user PATH (new terminals will pick it up)."
    } elseif (($userPath -split ';') -notcontains $installDir) {
        [Environment]::SetEnvironmentVariable("Path", "$($userPath.TrimEnd(';'));$installDir", "User")
        Write-Ok "Added $installDir to your user PATH (new terminals will pick it up)."
    }

    # Models are shared with the main app rather than downloaded by this CLI.
    if (-not (Test-Path (Join-Path $env:USERPROFILE ".sussurro\config.yaml"))) {
        Write-Warn "%USERPROFILE%\.sussurro\config.yaml not found."
        Write-Warn "Run 'sussurro' once to download the shared models, or pass -config <path>."
    }

    Write-Host ""
    Write-Host "sussurro-transcribe $tag installed!" -ForegroundColor Green
    Write-Host "  Basic:     sussurro-transcribe -i audio.mp3"
    Write-Host "  With LLM:  sussurro-transcribe -i audio.wav -clean"
    Write-Host "  To file:   sussurro-transcribe -i audio.mp3 -o out.txt"
    Write-Host "  Docs:      https://github.com/$repo/blob/master/docs/transcribe.md"
}
finally {
    Remove-Item $zipPath, $shaPath, $extractDir -Recurse -Force -ErrorAction SilentlyContinue
    $ProgressPreference = $previousProgress
}
