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

    Set $env:EULIX_GPU to 'amd' or 'nvidia' to skip the backend prompt.

    Keep this file ASCII-only: Windows PowerShell 5.1 reads a saved .ps1
    without a BOM as ANSI, and multi-byte characters can break parsing.
#>

$ErrorActionPreference = 'Stop'

# Ensure TLS 1.2 for older PS 5.1 hosts (required by the GitHub API)
try {
    [Net.ServicePointManager]::SecurityProtocol = `
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch { }

# Colours / logging helpers.
# Die throws instead of calling exit: under `irm ... | iex` the script runs in
# the user's own session, and exit would close their PowerShell window.
function Info    { param([string]$Msg) Write-Host "[INFO]  " -ForegroundColor Cyan   -NoNewline; Write-Host $Msg }
function Success { param([string]$Msg) Write-Host "[OK]    " -ForegroundColor Green  -NoNewline; Write-Host $Msg }
function Warn    { param([string]$Msg) Write-Host "[WARN]  " -ForegroundColor Yellow -NoNewline; Write-Host $Msg }
function Die     { param([string]$Msg) Write-Host "[ERROR] " -ForegroundColor Red    -NoNewline; Write-Host $Msg; throw 'Eulix installation aborted.' }

# Paths
$InstallDir        = Join-Path $env:LOCALAPPDATA 'Eulix\bin'
$EulixDir          = Join-Path $env:USERPROFILE  '.Eulix'
$EulixBinDir       = Join-Path $EulixDir 'bin'
$EulixVenv         = Join-Path $EulixDir '.venv'
$EulixParserPath   = Join-Path $EulixBinDir 'eulix_parser.exe'
$EulixEmbedPath    = Join-Path $EulixDir 'eulix_embed'
$vizeulizeHtmlPath = Join-Path $EulixBinDir 'vizeulize.html'
$FirstRunLog       = Join-Path $EulixDir 'first_run.log'

$Repo   = 'Nurysso/eulix'
$DocUrl = "https://github.com/$Repo/blob/main/docs/Installation.md"

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
        Die "This script is for Windows only. On Linux or macOS use install.sh instead: $DocUrl"
    }

    # A 32-bit PowerShell on 64-bit Windows reports x86 here; the real
    # architecture is then in PROCESSOR_ARCHITEW6432.
    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }
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
    if ($env:EULIX_GPU) {
        if ($env:EULIX_GPU -notin @('amd', 'nvidia')) {
            Die "EULIX_GPU must be 'amd' or 'nvidia', got '$($env:EULIX_GPU)'."
        }
        $script:GpuVariant = $env:EULIX_GPU.ToLower()
        Success "Selected variant (EULIX_GPU): $GpuVariant"
        return
    }

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
        # uv refuses to overwrite an existing venv without --clear, which older uv lacks.
        Remove-Item -Recurse -Force $EulixVenv
    }

    uv venv --python 3.11 "$EulixVenv"
    if ($LASTEXITCODE -ne 0) {
        Die "Failed to create virtual environment with uv."
    }
    Success "Virtual environment created at $EulixVenv."
}

# Helper to look up a checksum resiliently against directory prefixes in checksums.txt
function Get-Checksum {
    param(
        [string]$TargetFilename,
        [string]$ChecksumFile
    )

    $escaped = [regex]::Escape($TargetFilename)
    # Matches lines ending in '/filename' or '\filename' or ' filename'
    $match = Select-String -Path $ChecksumFile `
                           -Pattern "(?:^|[\/\s\\*])$escaped$" |
             Select-Object -First 1
    if ($match) {
        return ($match.Line -split '\s+')[0].Trim()
    }
    return $null
}

# Compare a downloaded file against its checksums.txt entry; die on mismatch.
function Confirm-Checksum {
    param([string]$Path, [string]$Name, [string]$ChecksumFile)

    $expected = Get-Checksum -TargetFilename $Name -ChecksumFile $ChecksumFile
    if (-not $expected) {
        Die "Missing checksum entry for $Name."
    }
    $actual = (Get-FileHash -Path $Path -Algorithm SHA256).Hash.ToLower()
    if ($expected.ToLower() -ne $actual) {
        Die "Checksum mismatch for $Name! Expected $expected, got $actual."
    }
    Success "$Name checksum verified."
}

# Download the release binary and vizeulize.html, verify checksums, install them
function Download-Binary {
    # Windows PowerShell 5.1 redraws the progress bar per chunk, which makes
    # large downloads many times slower. Scoped to this function.
    $ProgressPreference = 'SilentlyContinue'

    $script:AssetName = "eulix-windows-amd64-onnx-$GpuVariant.exe"
    $htmlAssetName    = 'vizeulize.html'

    $assetUrl = "$ReleaseBase/$AssetName"
    $htmlUrl  = "$ReleaseBase/$htmlAssetName"

    $tmpDir      = Join-Path $env:TEMP ("eulix-install-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null
    $tmpBin      = Join-Path $tmpDir $AssetName
    $tmpHtml     = Join-Path $tmpDir $htmlAssetName
    $tmpChecksum = Join-Path $tmpDir 'checksums.txt'

    try {
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

        # Releases up to v0.8.3 named the file checksum.txt.
        Info "Downloading checksums for verification..."
        try {
            Invoke-WebRequest -Uri "$ReleaseBase/checksums.txt" -OutFile $tmpChecksum -UseBasicParsing
        } catch {
            try {
                Invoke-WebRequest -Uri "$ReleaseBase/checksum.txt" -OutFile $tmpChecksum -UseBasicParsing
            } catch {
                Die "Failed to download checksums.txt (or checksum.txt) from $ReleaseBase`: $_"
            }
        }

        Info "Verifying checksums..."
        Confirm-Checksum -Path $tmpBin  -Name $AssetName     -ChecksumFile $tmpChecksum
        Confirm-Checksum -Path $tmpHtml -Name $htmlAssetName -ChecksumFile $tmpChecksum

        # Install CLI binary. A running eulix.exe (an MCP server, say) cannot be
        # overwritten or deleted, but it can be renamed out of the way.
        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
        Get-ChildItem -Path $InstallDir -Filter 'eulix.exe.old-*' -ErrorAction SilentlyContinue |
            Remove-Item -Force -ErrorAction SilentlyContinue
        $destPath = Join-Path $InstallDir 'eulix.exe'
        if (Test-Path $destPath) {
            try {
                Remove-Item -Force $destPath
            } catch {
                Rename-Item -Force -Path $destPath -NewName "eulix.exe.old-$([DateTime]::Now.Ticks)"
            }
        }
        Move-Item -Force -Path $tmpBin -Destination $destPath
        Success "Installed binary -> $destPath"

        # Install vizeulize.html into %USERPROFILE%\.Eulix\bin\
        New-Item -ItemType Directory -Force -Path $EulixBinDir | Out-Null
        Move-Item -Force -Path $tmpHtml -Destination $vizeulizeHtmlPath
        Success "Installed UI webapp -> $vizeulizeHtmlPath"
    } finally {
        Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
    }
}

# eulix unpacks its parser and eulix_embed scripts only when they are missing,
# then checks the parser's hash. On an upgrade the old files are still there,
# so the new binary would fail that check; and an existing .initialized marker
# would skip installing the Python packages for the variant just chosen.
# Remove all three so the first launch below sets them up fresh.
function Reset-ExtractedAssets {
    $removed = $false

    foreach ($p in @($EulixEmbedPath, (Join-Path $EulixDir '.initialized'))) {
        if (Test-Path $p) {
            Remove-Item -Recurse -Force $p
            $removed = $true
        }
    }

    if (Test-Path $EulixBinDir) {
        foreach ($f in @(Get-ChildItem -Path $EulixBinDir -Filter 'eulix_parser.exe*' -Force)) {
            $removed = $true
            try {
                Remove-Item -Force $f.FullName
            } catch {
                # Still running (e.g. under the MCP server): move it aside. eulix
                # deletes these .old-* files on a later run.
                if ($f.Name -eq 'eulix_parser.exe') {
                    Rename-Item -Force -Path $f.FullName -NewName "eulix_parser.exe.old-$([DateTime]::Now.Ticks)"
                }
            }
        }
    }

    if ($removed) {
        Info "Removed files unpacked by a previous install; this version unpacks its own."
    }
}

# Trigger first-run self-extraction (unpacks embedded parser + eulix_embed)
function Run-FirstLaunchSetup {
    Info "Running first-launch setup (self-extracts parser + eulix_embed, installs Python packages)..."
    Info "Downloading the Python packages can take a few minutes."
    $exePath = Join-Path $InstallDir 'eulix.exe'
    New-Item -ItemType Directory -Force -Path $EulixDir | Out-Null

    # With 'Stop', Windows PowerShell 5.1 turns the first stderr line of a
    # redirected native command into a terminating error, and uv reports its
    # progress on stderr. Use the exit code instead.
    $ErrorActionPreference = 'Continue'
    & $exePath --help *> $FirstRunLog
    $code = $LASTEXITCODE
    $ErrorActionPreference = 'Stop'

    if ($code -eq 0) {
        Success "First-launch setup complete."
        return $true
    }
    Warn "First-run setup exited with code $code - check $FirstRunLog for details."
    return $false
}

# Releases up to v0.8.3 list onnxruntime-rocm in onnx-amd.txt, and it has no
# Windows build, so the first launch cannot install the embedding packages for
# the amd variant. Install the plain onnxruntime wheel (CPU) instead.
function Ensure-AmdEmbedDeps {
    if ($GpuVariant -ne 'amd') { return $false }

    $venvPython = Join-Path $EulixVenv 'Scripts\python.exe'
    $ErrorActionPreference = 'Continue'
    & $venvPython -c 'import onnxruntime, tokenizers' *> $null
    $code = $LASTEXITCODE
    $ErrorActionPreference = 'Stop'
    if ($code -eq 0) { return $true }

    $baseReq = Join-Path $EulixEmbedPath 'requiremenents\requirements.base.txt'
    if (-not (Test-Path $baseReq)) {
        Warn "Cannot find $baseReq; skipping the onnxruntime fallback."
        return $false
    }

    Warn "The embedding packages are not installed; installing onnxruntime (CPU) instead of onnxruntime-rocm."
    uv pip install --python "$EulixVenv" -r "$baseReq" onnxruntime tokenizers
    if ($LASTEXITCODE -eq 0) {
        Success "Embedding packages installed."
        return $true
    }
    Warn "Package install failed. Retry with: uv pip install --python `"$EulixVenv`" -r `"$baseReq`" onnxruntime tokenizers"
    return $false
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
            # Not `setx PATH`: it truncates at 1024 characters and would copy the
            # machine PATH into the user PATH.
            Write-Host ""
            Write-Host "  Add it later via System Properties > Environment Variables, or run:" -ForegroundColor Yellow
            Write-Host "    [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';$InstallDir', 'User')"
            Write-Host ""
        }
    } elseif ($env:Path -notlike "*$InstallDir*") {
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
    Write-Host "+$bar+"          -ForegroundColor Cyan
    Write-Host "|$bannerText|"   -ForegroundColor Cyan
    Write-Host "+$bar+"          -ForegroundColor Cyan
    Write-Host ""

    Check-OS
    Choose-GpuVariant
    Install-Uv
    Setup-Venv
    Download-Binary
    Reset-ExtractedAssets
    $firstRunOk = Run-FirstLaunchSetup
    $depsOk     = Ensure-AmdEmbedDeps
    if (-not $firstRunOk -and -not $depsOk) {
        Warn "Retry the Python package install with: eulix getEmbedDeps"
    }
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
