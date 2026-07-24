# Sussurro Windows installer.
# Usage: irm https://raw.githubusercontent.com/cesp99/sussurro/master/scripts/install.ps1 | iex

$ErrorActionPreference = "Stop"

$repo = "cesp99/sussurro"
$installDir = Join-Path $env:LOCALAPPDATA "Programs\Sussurro"

Write-Host "Sussurro Windows Installer" -ForegroundColor Cyan
Write-Host "=========================="

# Latest release tag
Write-Host "Fetching latest release..."
$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
$tag = $release.tag_name
$asset = $release.assets | Where-Object { $_.name -eq "sussurro-windows-amd64.zip" }
if (-not $asset) {
    Write-Error "No Windows build found in release $tag. Windows builds start after v2.3."
}

Write-Host "Downloading Sussurro $tag..."
$zipPath = Join-Path $env:TEMP "sussurro-windows-amd64.zip"
Invoke-WebRequest $asset.browser_download_url -OutFile $zipPath

Write-Host "Installing to $installDir..."
New-Item -ItemType Directory -Force $installDir | Out-Null
Expand-Archive -Path $zipPath -DestinationPath $env:TEMP\sussurro-extract -Force
Copy-Item "$env:TEMP\sussurro-extract\sussurro-windows-amd64\*" $installDir -Recurse -Force
Remove-Item $zipPath, "$env:TEMP\sussurro-extract" -Recurse -Force

# Add to user PATH if missing
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$installDir", "User")
    Write-Host "Added $installDir to your user PATH (new terminals will pick it up)."
}

Write-Host ""
Write-Host "Installed! Run 'sussurro' from a new terminal (or $installDir\sussurro.exe)." -ForegroundColor Green
Write-Host "First run downloads the AI models (~1.8 GB) and creates %USERPROFILE%\.sussurro."
Write-Host "Hold Ctrl+Shift+Space to talk, release to transcribe."
