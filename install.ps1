#!/usr/bin/env pwsh
#Requires -Version 5.1
<#
.SYNOPSIS
    Eulix installer for Windows 10/11 (64-bit).
.DESCRIPTION
    Resolves the latest Eulix release (or a pinned one via $env:EULIX_VERSION),
    downloads the release binary and vizeulize.html UI, verifies their checksums,
    installs them, sets up a Python venv via uv, and triggers first-run
    self-extraction of the embedded parser + eulix_embed.
#>

$ErrorActionPreference = 'Stop'

# Ensure TLS 1.2 for older PS 5.1 hosts (required by the GitHub API)
try {
    [Net.ServicePointManager]::SecurityProtocol = `
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch { }

# Colours / logging helpers
function Info    { param([string]$Msg) Write-Host "[INFO]  " -ForegroundColor Cyan   -NoNewline; Write-Host $Msg }
function Success { param([string]$Msg) Write-Host "[OK]    " -ForegroundColor Green  -NoNewline; Write-Host $Msg }
function Warn    { param([string]$Msg) Write-Host "[WARN]  " -ForegroundColor Yellow -NoNewline; Write-Host $Msg }
function Die     { param([string]$Msg) Write-Host "[ERROR] " -ForegroundColor Red    -NoNewline; Write-Host $Msg; exit 1 }

# Paths
$InstallDir        = Join-Path $env:LOCALAPPDATA 'Eulix\bin'
$EulixDir          = Join-Path $env:USERPROFILE  '.Eulix'
$EulixBinDir       = Join-Path $EulixDir 'bin'
$EulixVenv         = Join-Path $EulixDir '.venv'
$EulixParserPath   = Join-Path $EulixBinDir 'eulix_parser.exe'
$EulixEmbedPath    = Join-Path $EulixDir 'eulix_embed'
$vizeulizeHtmlPath = Join-Path $EulixBinDir 'vizeulize.html'

$Repo   = 'Nurysso/eulix'
$DocUrl = "https://github.com/$Repo/blob/main/docs/install.md"

# Resolved at runtime inside Main()
$ReleaseTag  = ''
$ReleaseBase = ''

$IsUv       = $false
$GpuVariant = ''
$AssetName  = ''

# ---------------------------------------------------------------------------
# Fetch the latest release tag from GitHub.
# Preferred: REST API. Fallback: resolve the /releases/latest redirect via
# curl.exe (bundled with Windows 10 1803+).
# ---------------------------------------------------------------------------
function Get-LatestReleaseTag {
    param([string]$Repo)

    $tag = $null

    # Preferred: GitHub REST API
    try {
        $apiUrl = "https://api.github.com/repos/$Repo/releases/latest"
        $resp   = Invoke-RestMethod -Uri $apiUrl -UseBasicParsing `
                                    -Headers @{ 'User-Agent' = 'eulix-installer' }
        if ($resp -and $resp.tag_name) { $tag = $resp.tag_name }
    } catch {
        Warn "GitHub API lookup failed: $_"
    }

    # Fallback: follow the /releases/latest redirect with curl.exe
    if (-not $tag) {
        Warn "Falling back to redirect resolution via curl.exe..."
        try {
            $redirect = & curl.exe -fsSLI -o NUL -w '%{url_effective}' `
                                   "https://github.com/$Repo/releases/latest" 2>$null
            if ($redirect -match '/tag/([^/\s]+)') { $tag = $Matches[1] }
        } catch { }
    }

    if (-not $tag) {
        Die "Could not determine the latest release tag for $Repo. Set `$env:EULIX_VERSION to pin one explicitly."
    }

    return $tag
}

# OS / architecture check (Windows 10/11, 64-bit only)
function Check-OS {
    Info "Detecting operating system..."

    if ($PSVersionTable.PSVersion.Major -ge 6 -and -not $IsWindows) {
        Write-Host "This script is for Windows only." -ForegroundColor Red
        Write-Host "For Linux/macOS, use install.sh instead:"
        Write-Host "  $DocUrl"
        exit 1
    }

    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($arch -ne 'AMD64') {
        Die "Unsupported architecture: $arch. This script supports 64-bit (AMD64) Windows only."
    }

    $osVersion = [System.Environment]::OSVersion.Version
    if ($osVersion.Major -lt 10) {
        Die "Unsupported Windows version. Windows 10/11 (64-bit) is required."
    }

    Success "Detected Windows $($osVersion.Major).$($osVersion.Minor) (AMD64)."
}

# Ask user which GPU/backend variant to download
function Choose-GpuVariant {
    Info "Select the ONNX backend variant to install:"
    Write-Host "  1) amd     - CPU / AMD (onnx-amd)"
    Write-Host "  2) nvidia  - NVIDIA GPU (onnx-nvidia)"

    while ($true) {
        $choice = Read-Host "Enter choice [1-2]"
        switch ($choice) {
            '1' { $script:GpuVariant = 'amd';    return }
            '2' { $script:GpuVariant = 'nvidia'; return }
            default { Warn "Invalid choice, enter 1 or 2." }
        }
    }
}

# Check if uv is available
function Check-Uv {
    if (Get-Command uv -ErrorAction SilentlyContinue) {
        $script:IsUv = $true
        $ver = (uv --version)
        Success "uv found: $ver"
    } else {
        Warn "uv not found."
    }
}

# Install uv
function Install-Uv {
    Check-Uv
    if ($IsUv) {
        Info "uv already installed; skipping."
        return
    }

    Info "Installing uv..."
    powershell -ExecutionPolicy ByPass -Command "irm https://astral.sh/uv/install.ps1 | iex"

    # uv installs to %USERPROFILE%\.local\bin by default; refresh PATH for this session
    $uvBin = Join-Path $env:USERPROFILE '.local\bin'
    if (Test-Path $uvBin) {
        $env:Path = "$uvBin;$env:Path"
    }

    Check-Uv
    if (-not $IsUv) {
        Die "uv installation failed. Please install manually: https://github.com/astral-sh/uv"
    }
}

# Create the Python venv used by Eulix
function Setup-Venv {
    Info "Setting up Python venv at $EulixVenv..."
    New-Item -ItemType Directory -Force -Path $EulixDir | Out-Null

    $existingVer = ''
    $venvPython  = Join-Path $EulixVenv 'Scripts\python.exe'
    if (Test-Path $venvPython) {
        try {
            $existingVer = & $venvPython -c "import sys; print('%d.%d' % sys.version_info[:2])" 2>$null
        } catch {
            $existingVer = ''
        }
    }

    if ($existingVer -eq '3.11') {
        Success "Compatible virtual environment already exists (Python $existingVer) - reusing it."
        return
    }

    if (Test-Path $EulixVenv) {
        $found = if ($existingVer) { $existingVer } else { 'none' }
        Warn "Existing venv at $EulixVenv is missing or incompatible (found: $found) - recreating."
    }

    uv venv --python 3.11 "$EulixVenv"
    if ($LASTEXITCODE -ne 0) {
        Die "Failed to create virtual environment with uv."
    }
    Success "Virtual environment created at $EulixVenv."
}

# Helper to look up a checksum resiliently against directory prefixes in checksum.txt
function Get-Checksum {
    param(
        [string]$TargetFilename,
        [string]$ChecksumFile
    )

    $escaped = [regex]::Escape($TargetFilename)
    # Matches lines ending in '/filename' or '\filename' or ' filename'
    $match = Select-String -Path $ChecksumFile `
                           -Pattern "(?:^|[\/\s\\])$escaped$" |
             Select-Object -First 1
    if ($match) {
        return ($match.Line -split '\s+')[0].Trim()
    }
    return $null
}

# Download the release binary and vizeulize.html, verify checksums, install them
function Download-Binary {
    $script:AssetName = "eulix-windows-amd64-onnx-$GpuVariant.exe"
    $htmlAssetName    = 'vizeulize.html'

    $assetUrl     = "$ReleaseBase/$AssetName"
    $htmlUrl      = "$ReleaseBase/$htmlAssetName"
    $checksumUrl  = "$ReleaseBase/checksum.txt"

    $tmpBin       = Join-Path $env:TEMP $AssetName
    $tmpHtml      = Join-Path $env:TEMP $htmlAssetName
    $tmpChecksum  = Join-Path $env:TEMP 'eulix_checksum.txt'

    Info "Downloading $AssetName ($ReleaseTag)..."
    try {
        Invoke-WebRequest -Uri $assetUrl -OutFile $tmpBin -UseBasicParsing
    } catch {
        Die "Failed to download $assetUrl`: $_"
    }

    Info "Downloading $htmlAssetName ($ReleaseTag)..."
    try {
        Invoke-WebRequest -Uri $htmlUrl -OutFile $tmpHtml -UseBasicParsing
    } catch {
        Die "Failed to download $htmlUrl`: $_"
    }

    Info "Downloading checksum.txt for verification..."
    try {
        Invoke-WebRequest -Uri $checksumUrl -OutFile $tmpChecksum -UseBasicParsing
    } catch {
        Die "Failed to download $checksumUrl`: $_"
    }

    Info "Verifying checksums..."

    # Verify binary checksum
    $expectedBin = Get-Checksum -TargetFilename $AssetName -ChecksumFile $tmpChecksum
    if (-not $expectedBin) {
        Remove-Item -Force $tmpBin, $tmpHtml, $tmpChecksum -ErrorAction SilentlyContinue
        Die "Missing checksum entry for $AssetName."
    } else {
        $actualBin = (Get-FileHash -Path $tmpBin -Algorithm SHA256).Hash.ToLower()
        if ($expectedBin.ToLower() -ne $actualBin) {
            Remove-Item -Force $tmpBin, $tmpHtml, $tmpChecksum -ErrorAction SilentlyContinue
            Die "Checksum mismatch for $AssetName! Expected $expectedBin, got $actualBin."
        }
        Success "Binary checksum verified."
    }

    # Verify vizeulize.html checksum (matches 'vizeulize/vizeulize.html' in checksum.txt)
    $expectedHtml = Get-Checksum -TargetFilename $htmlAssetName -ChecksumFile $tmpChecksum
    if (-not $expectedHtml) {
        Remove-Item -Force $tmpBin, $tmpHtml, $tmpChecksum -ErrorAction SilentlyContinue
        Die "Missing checksum entry for $htmlAssetName."
    } else {
        $actualHtml = (Get-FileHash -Path $tmpHtml -Algorithm SHA256).Hash.ToLower()
        if ($expectedHtml.ToLower() -ne $actualHtml) {
            Remove-Item -Force $tmpBin, $tmpHtml, $tmpChecksum -ErrorAction SilentlyContinue
            Die "Checksum mismatch for $htmlAssetName! Expected $expectedHtml, got $actualHtml."
        }
        Success "vizeulize.html checksum verified."
    }

    Remove-Item -Force $tmpChecksum -ErrorAction SilentlyContinue

    # Install CLI binary
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $destPath = Join-Path $InstallDir 'eulix.exe'
    Move-Item -Force -Path $tmpBin -Destination $destPath
    Success "Installed binary -> $destPath"

    # Install vizeulize.html into %USERPROFILE%\.Eulix\bin\
    New-Item -ItemType Directory -Force -Path $EulixBinDir | Out-Null
    Move-Item -Force -Path $tmpHtml -Destination $vizeulizeHtmlPath
    Success "Installed UI webapp -> $vizeulizeHtmlPath"
}

# Trigger first-run self-extraction (unpacks embedded parser + eulix_embed)
function Run-FirstLaunchSetup {
    Info "Running first-launch setup (self-extracts parser + eulix_embed, installs deps)..."
    $exePath = Join-Path $InstallDir 'eulix.exe'
    $logPath = Join-Path $env:TEMP 'eulix_first_run.log'

    try {
        & $exePath --help *> $logPath
        Success "First-launch setup complete."
    } catch {
        Warn "First-run setup exited with an error - check $logPath for details."
    }
}

# PATH setup (persist to user PATH via Environment API, plus current session)
function Ensure-Path {
    $userPath = [System.Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -notlike "*$InstallDir*") {
        Warn "$InstallDir is not in your PATH."
        $add = Read-Host "Add it to your user PATH now? [Y/n]"
        if ($add -eq '' -or $add -match '^[Yy]') {
            $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
            [System.Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
            $env:Path = "$env:Path;$InstallDir"
            Success "Added $InstallDir to your user PATH. Restart your terminal to pick it up in new sessions."
        } else {
            Write-Host ""
            Write-Host "  Add this manually via System Properties > Environment Variables, or run:" -ForegroundColor Yellow
            Write-Host "    setx PATH `"$env:Path;$InstallDir`""
            Write-Host ""
        }
    } else {
        $env:Path = "$env:Path;$InstallDir"
    }
}

# Main
function Main {
    # --- Resolve release tag (latest, or pinned via EULIX_VERSION) ---
    if ($env:EULIX_VERSION) {
        $script:ReleaseTag = $env:EULIX_VERSION
        Success "Using pinned release: $ReleaseTag"
    } else {
        Info "Resolving latest Eulix release..."
        $script:ReleaseTag = Get-LatestReleaseTag -Repo $Repo
        Success "Latest release: $ReleaseTag"
    }
    $script:ReleaseBase = "https://github.com/$Repo/releases/download/$ReleaseTag"

    $bannerText = "  Eulix Installer  $ReleaseTag  "
    $bar        = '=' * $bannerText.Length

    Write-Host ""
    Write-Host "╔$bar╗"          -ForegroundColor Cyan
    Write-Host "║$bannerText║"   -ForegroundColor Cyan
    Write-Host "╚$bar╝"          -ForegroundColor Cyan
    Write-Host ""

    Check-OS
    Choose-GpuVariant
    Install-Uv
    Setup-Venv
    Download-Binary
    Run-FirstLaunchSetup
    Ensure-Path

    Write-Host ""
    Write-Host "Eulix installed successfully!" -ForegroundColor Green
    Write-Host "  Version       : $ReleaseTag"
    Write-Host "  Binary        : $InstallDir\eulix.exe"
    Write-Host "  Data dir      : $EulixDir"
    Write-Host "  Venv          : $EulixVenv"
    Write-Host "  eulix_parser  : $EulixParserPath"
    Write-Host "  eulix_embed   : $EulixEmbedPath"
    Write-Host "  vizeulize.html: $vizeulizeHtmlPath"
    Write-Host "  Run           : eulix --help"
    Write-Host ""
}

Main
