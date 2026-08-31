#!/usr/bin/env pwsh
#Requires -Version 5.1
<#
.SYNOPSIS
    Eulix installer for Windows 11 (64-bit).
.DESCRIPTION
    Downloads the latest Eulix release binary, verifies its checksum,
    installs it, sets up a Python venv via uv, and triggers first-run
    self-extraction of the embedded parser + eulix_embed.
    Written by AI as i dont know windows or ps
#>

$ErrorActionPreference = 'Stop'

# Colours / logging helpers
function Info    { param([string]$Msg) Write-Host "[INFO]  " -ForegroundColor Cyan    -NoNewline; Write-Host $Msg }
function Success { param([string]$Msg) Write-Host "[OK]    " -ForegroundColor Green   -NoNewline; Write-Host $Msg }
function Warn    { param([string]$Msg) Write-Host "[WARN]  " -ForegroundColor Yellow  -NoNewline; Write-Host $Msg }
function Die     { param([string]$Msg) Write-Host "[ERROR] " -ForegroundColor Red     -NoNewline; Write-Host $Msg; exit 1 }

# Paths
$InstallDir       = Join-Path $env:LOCALAPPDATA 'Eulix\bin'
$EulixDir         = Join-Path $env:USERPROFILE  '.Eulix'
$EulixVenv        = Join-Path $EulixDir '.venv'
$EulixParserPath  = Join-Path $EulixDir 'bin\eulix_parser.exe'
$EulixEmbedPath   = Join-Path $EulixDir 'eulix_embed'

$Repo         = 'Nurysso/eulix'
$ReleaseTag   = 'v0.8.0'
$ReleaseBase  = "https://github.com/$Repo/releases/download/$ReleaseTag"
$DocUrl       = "https://github.com/$Repo/blob/main/docs/install.md"

$IsUv       = $false
$GpuVariant = ''
$AssetName  = ''

# OS / architecture check (Windows 11, 64-bit only)
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
    $venvPython = Join-Path $EulixVenv 'Scripts\python.exe'
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
        Warn "Existing venv at $EulixVenv is missing or incompatible (found: $(if ($existingVer) { $existingVer } else { 'none' })) - recreating."
    }

    uv venv --python 3.11 "$EulixVenv"
    if ($LASTEXITCODE -ne 0) {
        Die "Failed to create virtual environment with uv."
    }
    Success "Virtual environment created at $EulixVenv."
}

# Download the release binary, verify checksum, install it
function Download-Binary {
    $script:AssetName = "eulix-windows-amd64-onnx-$GpuVariant.exe"
    $assetUrl        = "$ReleaseBase/$AssetName"
    $checksumsUrl    = "$ReleaseBase/checksums.txt"
    $tmpBin          = Join-Path $env:TEMP $AssetName
    $tmpChecksums    = Join-Path $env:TEMP 'eulix_checksums.txt'

    Info "Downloading $AssetName ($ReleaseTag)..."
    try {
        Invoke-WebRequest -Uri $assetUrl -OutFile $tmpBin -UseBasicParsing
    } catch {
        Die "Failed to download $assetUrl`: $_"
    }

    Info "Downloading checksums.txt for verification..."
    try {
        Invoke-WebRequest -Uri $checksumsUrl -OutFile $tmpChecksums -UseBasicParsing
    } catch {
        Die "Failed to download $checksumsUrl`: $_"
    }

    Info "Verifying checksum..."
    $checksumLine = Select-String -Path $tmpChecksums -Pattern ([regex]::Escape($AssetName)) | Select-Object -First 1
    if (-not $checksumLine) {
        Warn "Could not find $AssetName in checksums.txt - skipping verification."
    } else {
        $expected = ($checksumLine.Line -split '\s+')[0]
        $actual   = (Get-FileHash -Path $tmpBin -Algorithm SHA256).Hash.ToLower()
        if ($expected.ToLower() -ne $actual) {
            Remove-Item -Force $tmpBin, $tmpChecksums -ErrorAction SilentlyContinue
            Die "Checksum mismatch for $AssetName! Expected $expected, got $actual."
        }
        Success "Checksum verified."
    }
    Remove-Item -Force $tmpChecksums -ErrorAction SilentlyContinue

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $destPath = Join-Path $InstallDir 'eulix.exe'
    Move-Item -Force -Path $tmpBin -Destination $destPath
    Success "Installed binary -> $destPath"
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

# PATH setup (persist to user PATH via setx, plus current session)
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
            Write-Host "    setx PATH `"`$env:Path;$InstallDir`""
            Write-Host ""
        }
    } else {
        $env:Path = "$env:Path;$InstallDir"
    }
}

# Main
function Main {
    Write-Host ""
    Write-Host "╔══════════════════════════════╗" -ForegroundColor Cyan
    Write-Host "║   Eulix Installer $ReleaseTag      ║" -ForegroundColor Cyan
    Write-Host "╚══════════════════════════════╝" -ForegroundColor Cyan
    Write-Host ""

    Check-OS
    Choose-GpuVariant
    Install-Uv
    Setup-Venv
    Download-Binary
    Run-FirstLaunchSetup
    Ensure-Path

    Write-Host ""
    Write-Host "✓ Eulix installed successfully!" -ForegroundColor Green
    Write-Host "  Binary        : $InstallDir\eulix.exe"
    Write-Host "  Data dir      : $EulixDir"
    Write-Host "  Venv          : $EulixVenv"
    Write-Host "  eulix_parser  : $EulixParserPath"
    Write-Host "  eulix_embed   : $EulixEmbedPath"
    Write-Host "  Run           : eulix --help"
    Write-Host ""
}

Main
