# This script is written by AI i have tested it in vms and actual systems it does
# But still needs some work

#Requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# ═══════════════════════════════════════════════════════════
#  EULIX INSTALLER - Windows PowerShell
#  Secure, user-friendly, Written By AI
# ═══════════════════════════════════════════════════════════

# ─── Configuration ────────────────────────────────────────
$REPO_URL    = "https://github.com/Nurysso/eulix"
$INSTALL_DIR = "$env:USERPROFILE\.local\bin"
$EULIX_DIR   = "$env:USERPROFILE\.Eulix"
$EULIX_VENV  = "$EULIX_DIR\.venv"
$CLONE_DIR   = "$env:USERPROFILE\eulix"

$script:IS_UV            = $false
$script:COMPUTE_PLATFORM = ""
$script:Arch             = if ($env:PROCESSOR_ARCHITECTURE -like "*ARM64*") { "arm64" } else { "amd64" }

# ─── Tool Requirements ──────────────────────────────────────
# Only pin minimum versions and critical bootstrappers
$REQUIRED_TOOLS = @{
    "Git" = @{
        Command     = "git"
        MinVersion  = "2.40.0"
        GetVersion  = { (git --version) -replace 'git version ',''-split '\.windows' | Select-Object -First 1 }
        InstallUrl  = "https://git-scm.com/download/win"
        Critical    = $true
    }
    "Python" = @{
        Command     = "python"
        MinVersion  = "3.10.0"
        GetVersion  = { (python --version) -replace 'Python ',''.Trim() }
        InstallUrl  = "https://www.python.org/downloads/"
        Critical    = $true
        Fallback    = "uv"  # Can be installed via uv
    }
    "Go" = @{
        Command     = "go"
        MinVersion  = "1.24.0"
        GetVersion  = { ((go version) -split '\s+')[2] -replace 'go','' }
        InstallUrl  = "https://go.dev/dl/"
        Critical    = $true
    }
    "Cargo" = @{
        Command     = "cargo"
        MinVersion  = "1.70.0"
        GetVersion  = { ((cargo --version) -split '\s+')[1] }
        InstallUrl  = "https://rustup.rs"
        Critical    = $true
    }
    "uv" = @{
        Command     = "uv"
        MinVersion  = "0.1.0"
        GetVersion  = { ((uv --version) -split '\s+')[1] }
        InstallUrl  = "https://docs.astral.sh/uv/getting-started/installation/"
        Critical    = $false
        NiceToHave  = "Python and dependency management"
    }
}

# ─── Logging Functions ─────────────────────────────────────
function Write-Info    { param($msg) Write-Host "[INFO]  $msg" -ForegroundColor Cyan }
function Write-Success { param($msg) Write-Host "[OK]    $msg" -ForegroundColor Green }
function Write-Warn    { param($msg) Write-Host "[WARN]  $msg" -ForegroundColor Yellow }
function Write-Error   { param($msg) Write-Host "[ERROR] $msg" -ForegroundColor Red }
function Write-Banner  {
    param($msg)
    Write-Host ""
    Write-Host "════════════════════════════════════" -ForegroundColor Cyan
    Write-Host "  $msg" -ForegroundColor Cyan
    Write-Host "════════════════════════════════════" -ForegroundColor Cyan
    Write-Host ""
}

# ─── Security Utilities ────────────────────────────────────
function Invoke-SecureDownload {
    param(
        [string]$Uri,
        [string]$OutFile,
        [string]$Description = "file"
    )

    if ($Uri -notmatch '^https://') {
        throw "Refusing non-HTTPS download for $Description : $Uri"
    }

    Write-Info "Downloading $Description..."
    try {
        [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::Tls12 -bor [System.Net.SecurityProtocolType]::Tls13

        $webClient = New-Object System.Net.WebClient
        $webClient.Headers.Add("User-Agent", "Eulix-Installer/2.0")
        $webClient.DownloadFile($Uri, $OutFile)
        $webClient.Dispose()

        Write-Success "Downloaded $Description"
    } catch {
        throw "Failed to download $Description from $Uri : $_"
    }
}

function Test-FileIntegrity {
    param(
        [string]$FilePath,
        [string]$ExpectedHash,
        [string]$Description = "file"
    )

    if ([string]::IsNullOrWhiteSpace($ExpectedHash) -or $ExpectedHash -like "REPLACE_*") {
        Write-Warn "No checksum provided for $Description - skipping verification"
        return
    }

    Write-Info "Verifying $Description integrity..."
    $actual = (Get-FileHash -Path $FilePath -Algorithm SHA256).Hash.ToUpper()
    $expected = $ExpectedHash.ToUpper().Trim()

    if ($actual -ne $expected) {
        Remove-Item $FilePath -Force -ErrorAction SilentlyContinue
        throw @"
CHECKSUM MISMATCH for $Description!
  Expected: $expected
  Actual:   $actual
The file has been deleted for security. Aborting installation.
"@
    }
    Write-Success "$Description checksum verified"
}

function Expand-SafeZip {
    param(
        [string]$ZipPath,
        [string]$Destination
    )

    Write-Info "Extracting $ZipPath..."

    if (-not (Test-Path $Destination)) {
        New-Item -ItemType Directory -Path $Destination -Force | Out-Null
    }

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::OpenRead($ZipPath)
    try {
        foreach ($entry in $zip.Entries) {
            # Prevent path traversal
            if ($entry.FullName -match '\.\.' -or [System.IO.Path]::IsPathRooted($entry.FullName)) {
                throw "Path traversal detected in zip entry: $($entry.FullName)"
            }

            $destPath = [System.IO.Path]::Combine($Destination, $entry.FullName)
            $destDir = [System.IO.Path]::GetDirectoryName($destPath)

            if (-not (Test-Path $destDir)) {
                New-Item -ItemType Directory -Path $destDir -Force | Out-Null
            }

            if ($entry.Name -ne '') {
                [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $destPath, $true)
            }
        }
        Write-Success "Extraction complete"
    } finally {
        $zip.Dispose()
    }
}

# ─── Path Management ───────────────────────────────────────
function Add-ToUserPath {
    param([string]$Path)

    $currentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    if ($currentPath -like "*$Path*") {
        Write-Info "$Path already in user PATH"
        return
    }

    [Environment]::SetEnvironmentVariable("PATH", "$Path;$currentPath", "User")
    $env:PATH = "$Path;$env:PATH"
    Write-Success "Added $Path to user PATH"
}

function Update-SessionPath {
    $machinePath = [Environment]::GetEnvironmentVariable("PATH", "Machine")
    $userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    $env:PATH = "$machinePath;$userPath"

    # Remove duplicates
    $env:PATH = ($env:PATH -split ';' | Select-Object -Unique) -join ';'
}

# ─── Version Comparison ────────────────────────────────────
function Compare-Versions {
    param(
        [string]$Version1,
        [string]$Version2
    )

    $v1 = [version]($Version1 -replace '[^0-9.]', '')
    $v2 = [version]($Version2 -replace '[^0-9.]', '')

    return $v1.CompareTo($v2)
}

# ─── Tool Verification ─────────────────────────────────────
function Test-ToolAvailability {
    param(
        [string]$ToolName,
        [hashtable]$ToolConfig
    )

    Write-Info "Checking $ToolName..."

    $exists = Get-Command $ToolConfig.Command -ErrorAction SilentlyContinue
    if (-not $exists) {
        Write-Warn "$ToolName not found"
        return @{ Installed = $false; Version = $null }
    }

    try {
        $version = & $ToolConfig.GetVersion
        $version = $version.Trim()

        # Treat empty/whitespace version as "not usable" — catches the
        # Windows Store Python stub which prints nothing useful.
        if ([string]::IsNullOrWhiteSpace($version)) {
            Write-Warn "$ToolName found but returned no version (likely a stub/alias)"
            return @{ Installed = $false; Version = $null }
        }

        Write-Info "$ToolName version: $version"

        $comparison = Compare-Versions $version $ToolConfig.MinVersion
        if ($comparison -lt 0) {
            Write-Warn "$ToolName version $version is below minimum required ($($ToolConfig.MinVersion))"
            return @{ Installed = $false; Version = $version; TooOld = $true }
        }

        Write-Success "$ToolName $version OK"
        return @{ Installed = $true; Version = $version }

    } catch {
        Write-Warn "Could not determine $ToolName version: $_"
        # Treat unresolvable version as not installed rather than assuming OK.
        return @{ Installed = $false; Version = $null }
    }
}

function Request-ManualInstall {
    param(
        [string]$ToolName,
        [hashtable]$ToolConfig
    )

    Write-Host ""
    Write-Host "Missing dependency: $ToolName" -ForegroundColor Yellow
    Write-Host "  Required version : $($ToolConfig.MinVersion) or higher"
    Write-Host "  Download from    : $($ToolConfig.InstallUrl)" -ForegroundColor Cyan
    Write-Host ""

    # Guard: only print Purpose line when the key exists and is non-empty.
    if ($ToolConfig.ContainsKey('NiceToHave') -and -not [string]::IsNullOrWhiteSpace($ToolConfig.NiceToHave)) {
        Write-Host "  Purpose: $($ToolConfig.NiceToHave)" -ForegroundColor Gray
        Write-Host ""
    }

    if ($ToolConfig.Critical) {
        Write-Host "  [A] Abort  - install it manually then re-run"
        Write-Host ""
        $response = Read-Host "Your choice"
        Write-Host ""
        Write-Error "Installation aborted. Please install $ToolName and re-run this script."
        exit 1
    } else {
        Write-Host "  [A] Abort  - install it manually then re-run"
        Write-Host "  [S] Skip   - continue without $ToolName (some features may not work)"
        Write-Host ""

        do {
            $response = Read-Host "Your choice"
            switch ($response.ToUpper()) {
                'A' {
                    Write-Host ""
                    Write-Error "Installation aborted. Please install $ToolName and re-run this script."
                    exit 1
                }
                'S' {
                    Write-Warn "Skipping $ToolName"
                    return $false
                }
                default { Write-Host "  Please enter A or S" -ForegroundColor Yellow }
            }
        } while ($true)
    }
}

# ─── Tool Installation Functions ───────────────────────────
function Install-UV {
    if (Get-Command uv -ErrorAction SilentlyContinue) {
        Write-Success "uv is already installed"
        $script:IS_UV = $true
        return
    }

    Write-Banner "Installing uv"
    Write-Info "uv simplifies Python and dependency management"
    Write-Host "  Official installer will be downloaded from astral.sh"
    Write-Host ""

    $response = Read-Host "Install uv now? [Y/n]"
    if ($response -match '^[Nn]') {
        Write-Warn "Skipping uv installation. Some features may be limited."
        return
    }

    $uvInstaller = "$env:TEMP\uv-installer.ps1"
    try {
        Invoke-SecureDownload -Uri "https://astral.sh/uv/install.ps1" -OutFile $uvInstaller -Description "uv installer"

        # Verify the installer script
        $signature = Get-AuthenticodeSignature $uvInstaller
        if ($signature.Status -ne "Valid") {
            Write-Warn "uv installer is not digitally signed"
            $response = Read-Host "Continue anyway? [y/N]"
            if ($response -notmatch '^[Yy]') {
                throw "User declined unsigned installer"
            }
        }

        Write-Info "Running uv installer..."
        & $uvInstaller

        Update-SessionPath

        if (Get-Command uv -ErrorAction SilentlyContinue) {
            $script:IS_UV = $true
            Write-Success "uv installed successfully: $(uv --version)"
        } else {
            Write-Warn "uv installation may have failed. Try opening a new terminal."
            $script:IS_UV = $false
        }
    } catch {
        Write-Error "Failed to install uv: $_"
        Write-Host "  Manual installation: https://docs.astral.sh/uv/getting-started/installation/"
        $script:IS_UV = $false
    } finally {
        Remove-Item $uvInstaller -ErrorAction SilentlyContinue
    }
}

function Install-PythonViaUV {
    Write-Info "Installing Python 3.10 via uv..."

    if (-not $script:IS_UV) {
        throw "uv is required for automatic Python installation"
    }

    try {
        & uv python install 3.10
        Write-Success "Python 3.10 installed via uv"

        # Verify it's accessible
        $pythonPath = & uv python find 3.10
        Write-Info "Python location: $pythonPath"
    } catch {
        throw "Failed to install Python via uv: $_"
    }
}

function Install-Rust {
    Write-Banner "Installing Rust"
    Write-Info "Rust will be installed via rustup (official installer)"
    Write-Host "  rustup manages Rust versions and verifies downloads automatically"
    Write-Host ""

    $response = Read-Host "Install Rust now? [Y/n]"
    if ($response -match '^[Nn]') {
        throw "Rust/Cargo is required for building. Please install from https://rustup.rs"
    }

    $rustupExe = "$env:TEMP\rustup-init.exe"
    try {
        Invoke-SecureDownload `
            -Uri "https://static.rust-lang.org/rustup/dist/x86_64-pc-windows-msvc/rustup-init.exe" `
            -OutFile $rustupExe `
            -Description "rustup installer"

        Write-Info "Running rustup installer (this may take a few minutes)..."
        & $rustupExe -y --default-toolchain stable --no-modify-path

        # Add Cargo to PATH
        Add-ToUserPath "$env:USERPROFILE\.cargo\bin"
        Update-SessionPath

        if (Get-Command cargo -ErrorAction SilentlyContinue) {
            Write-Success "Rust installed successfully: $(cargo --version)"
        } else {
            throw "Rust installation succeeded but cargo not found in PATH"
        }
    } catch {
        throw "Failed to install Rust: $_"
    } finally {
        Remove-Item $rustupExe -ErrorAction SilentlyContinue
    }
}

function Install-Go {
    Write-Banner "Installing Go"

    $isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

    Write-Info "Go installer requires administrator privileges"
    if (-not $isAdmin) {
        Write-Warn "Not running as Administrator!"
        Write-Host "  Options:" -ForegroundColor White
        Write-Host "  1. Re-run this script as Administrator for automatic installation"
        Write-Host "  2. Install Go manually from https://go.dev/dl/"
        Write-Host ""
        throw "Administrator privileges required. Please run as Admin or install Go manually."
    }

    $response = Read-Host "Install Go now? [Y/n]"
    if ($response -match '^[Nn]') {
        throw "Go is required for building. Please install from https://go.dev/dl/"
    }

    $goVersion = "1.24.4"  # Latest stable
    $goMsi = "$env:TEMP\go-$goVersion-windows-$($script:Arch).msi"
    $goUrl = "https://go.dev/dl/go$goVersion.windows-$($script:Arch).msi"

    try {
        Invoke-SecureDownload -Uri $goUrl -OutFile $goMsi -Description "Go $goVersion"

        Write-Info "Installing Go (this will trigger UAC)..."
        Start-Process msiexec.exe -ArgumentList "/i `"$goMsi`" /quiet /norestart" -Wait

        Update-SessionPath

        if (Get-Command go -ErrorAction SilentlyContinue) {
            Write-Success "Go installed successfully: $(go version)"
        } else {
            Write-Warn "Go installed but not in PATH. You may need to restart your terminal."
        }
    } catch {
        throw "Failed to install Go: $_"
    } finally {
        Remove-Item $goMsi -ErrorAction SilentlyContinue
    }
}

# ─── Dependency Management ─────────────────────────────────
function Assert-Dependencies {
    Write-Banner "Checking Dependencies"

    $missingCritical = @()
    $missingOptional = @()

    foreach ($toolName in $REQUIRED_TOOLS.Keys) {
        $toolConfig = $REQUIRED_TOOLS[$toolName]
        $status = Test-ToolAvailability -ToolName $toolName -ToolConfig $toolConfig

        if (-not $status.Installed) {
            if ($toolConfig.Critical) {
                $missingCritical += $toolName
            } else {
                $missingOptional += $toolName
            }
        }
    }

    # Handle optional tools
    foreach ($toolName in $missingOptional) {
        $toolConfig = $REQUIRED_TOOLS[$toolName]
        if ($toolName -eq "uv" -and -not $script:IS_UV) {
            Install-UV
        } else {
            $null = Request-ManualInstall -ToolName $toolName -ToolConfig $toolConfig
        }
    }

    # Handle critical tools
    if ($missingCritical.Count -gt 0) {
        Write-Warn "Missing critical dependencies: $($missingCritical -join ', ')"
        Write-Host ""

        foreach ($toolName in $missingCritical) {
            $toolConfig = $REQUIRED_TOOLS[$toolName]

            # Python can be installed via uv if available
            if ($toolName -eq "Python" -and $script:IS_UV) {
                try {
                    Install-PythonViaUV
                    continue
                } catch {
                    Write-Warn "Failed to install Python via uv: $_"
                }
            }

            # Offer to install tools we have installers for
            if ($toolName -eq "Cargo") {
                try {
                    Install-Rust
                    continue
                } catch {
                    Write-Warn "Failed to install Rust: $_"
                }
            }

            if ($toolName -eq "Go") {
                try {
                    Install-Go
                    continue
                } catch {
                    Write-Warn "Failed to install Go: $_"
                }
            }

            # Ask user to install manually
            Request-ManualInstall -ToolName $toolName -ToolConfig $toolConfig
        }
    }

    Write-Success "All critical dependencies satisfied ✓"
}

# ─── OS Check ──────────────────────────────────────────────
function Assert-Windows {
    Write-Info "Verifying operating system..."

    if ($PSVersionTable.PSVersion.Major -ge 6) {
        if (-not $IsWindows) {
            throw "This script is for Windows only. Use install.sh for Linux/macOS."
        }
    } elseif (-not [System.Environment]::OSVersion.Platform -match "Win32") {
        throw "This script requires Windows."
    }

    Write-Success "Windows detected: $([System.Environment]::OSVersion.VersionString)"
}

# ─── GPU Detection ─────────────────────────────────────────
function Get-ComputePlatform {
    Write-Banner "GPU Detection"
    Write-Info "Detecting compute platform..."

    # Check for NVIDIA
    if (Get-Command nvidia-smi -ErrorAction SilentlyContinue) {
        try {
            $cudaInfo = & nvidia-smi --query-gpu=driver_version,cuda_version --format=csv,noheader 2>&1

            if ($cudaInfo -match '(\d+\.\d+)\s*,\s*(\d+\.\d+)') {
                $driverVersion = $Matches[1]
                $cudaVersion = [version]$Matches[2]

                Write-Success "NVIDIA GPU detected - Driver: $driverVersion, CUDA: $cudaVersion"

                # Map CUDA version to PyTorch index
                if ($cudaVersion -ge [version]"13.2") {
                    $script:COMPUTE_PLATFORM = "cuda132"
                } elseif ($cudaVersion -ge [version]"13.0") {
                    $script:COMPUTE_PLATFORM = "cuda130"
                } elseif ($cudaVersion -ge [version]"12.6") {
                    $script:COMPUTE_PLATFORM = "cuda126"
                } elseif ($cudaVersion -ge [version]"12.4") {
                    $script:COMPUTE_PLATFORM = "cuda124"
                } else {
                    Write-Warn "CUDA $cudaVersion is below 12.4 - falling back to CPU"
                    $script:COMPUTE_PLATFORM = "cpu"
                }

                Write-Success "Compute platform: $($script:COMPUTE_PLATFORM)"
                return
            }
        } catch {
            Write-Warn "nvidia-smi query failed: $_"
        }
    }

    # Check for AMD ROCm
    if ((Get-Command rocminfo -ErrorAction SilentlyContinue) -or
        (Test-Path "C:\Program Files\AMD\ROCm")) {
        Write-Warn "AMD ROCm detected but Windows support is limited"
        $script:COMPUTE_PLATFORM = "rocm72"
        return
    }

    # Fallback to CPU
    Write-Warn "No supported GPU detected - will install CPU-only PyTorch"
    $script:COMPUTE_PLATFORM = "cpu"
}

# ─── Repository Management ────────────────────────────────
function Update-Repository {
    Write-Banner "Preparing Eulix Repository"
    Write-Info "Cloning/updating repository at $CLONE_DIR..."

    if (Test-Path "$CLONE_DIR\.git") {
        Write-Info "Repository exists - updating..."
        Push-Location $CLONE_DIR
        try {
            & git pull --ff-only 2>&1 | Out-Null

            if ($LASTEXITCODE -ne 0) {
                Write-Warn "Pull failed - attempting to reset and pull"
                & git fetch origin
                & git reset --hard origin/main
            }
            Write-Success "Repository updated"
        } finally {
            Pop-Location
        }
    } else {
        if (Test-Path $CLONE_DIR) {
            Write-Warn "Directory exists but is not a git repository"
            $response = Read-Host "Remove and re-clone? [Y/n]"
            if ($response -notmatch '^[Nn]') {
                Remove-Item $CLONE_DIR -Recurse -Force
            } else {
                throw "Cannot proceed with existing directory. Please remove it manually."
            }
        }

        Write-Info "Cloning repository..."
        & git clone --depth 1 $REPO_URL $CLONE_DIR 2>&1 | Out-Null

        if ($LASTEXITCODE -ne 0) {
            throw "Failed to clone repository"
        }
        Write-Success "Repository cloned"
    }

    # Verify repository structure
    $requiredPaths = @(
        "$CLONE_DIR\eulix-parser",
        "$CLONE_DIR\cmd\eulix",
        "$CLONE_DIR\eulix-embed"
    )

    foreach ($path in $requiredPaths) {
        if (-not (Test-Path $path)) {
            throw "Repository structure invalid - missing: $path"
        }
    }

    Write-Success "Repository structure verified ✓"
}

# ─── Build Process ─────────────────────────────────────────
function Build-Eulix {
    Write-Banner "Building Eulix"

    # Create directories
    foreach ($dir in @($INSTALL_DIR, $EULIX_DIR)) {
        if (-not (Test-Path $dir)) {
            New-Item -ItemType Directory -Path $dir -Force | Out-Null
        }
    }

    # Build eulix-parser (Rust)
    Write-Info "Building eulix-parser (Rust)..."
    Push-Location "$CLONE_DIR\eulix-parser"
    try {
        $env:RUSTFLAGS = "-C target-feature=+crt-static"  # Static linking for portability

        & cargo build --release

        if ($LASTEXITCODE -ne 0) {
            throw "eulix-parser build failed"
        }

        $parserBin = "target\release\eulix_parser.exe"
        if (-not (Test-Path $parserBin)) {
            throw "eulix_parser.exe not found after build"
        }

        Copy-Item $parserBin "$INSTALL_DIR\eulix_parser.exe" -Force
        Write-Success "eulix_parser.exe → $INSTALL_DIR"
    } finally {
        $env:RUSTFLAGS = ""
        Pop-Location
    }

    # Build eulix CLI (Go)
    Write-Info "Building eulix CLI (Go)..."
    Push-Location $CLONE_DIR
    try {
        $env:CGO_ENABLED = "0"  # Pure Go, no C dependencies

        $goEntry = "cmd\eulix\main.go"
        if (-not (Test-Path $goEntry)) {
            throw "Go entry point not found: $goEntry"
        }

        & go build -ldflags="-s -w" -o "$INSTALL_DIR\eulix.exe" $goEntry

        if ($LASTEXITCODE -ne 0) {
            throw "eulix CLI build failed"
        }

        if (-not (Test-Path "$INSTALL_DIR\eulix.exe")) {
            throw "eulix.exe not found after build"
        }

        Write-Success "eulix.exe → $INSTALL_DIR"
    } finally {
        $env:CGO_ENABLED = ""
        Pop-Location
    }

    # Copy eulix-embed
    Write-Info "Setting up eulix-embed..."
    $embedSrc = "$CLONE_DIR\eulix-embed\eulix-embed.py"
    if (-not (Test-Path $embedSrc)) {
        throw "eulix-embed.py not found"
    }

    Copy-Item $embedSrc "$EULIX_DIR\eulix_embed.py" -Force
    Write-Success "eulix_embed.py → $EULIX_DIR"
}

# ─── Python Environment ────────────────────────────────────
function Initialize-PythonEnvironment {
    Write-Banner "Setting up Python Environment"

    # Create virtual environment
    Write-Info "Creating virtual environment at $EULIX_VENV..."

    if ($script:IS_UV) {
        & uv venv --python 3.10 $EULIX_VENV

        if ($LASTEXITCODE -ne 0) {
            throw "Failed to create virtual environment"
        }
    } else {
        $pythonExe = Get-Command python -ErrorAction SilentlyContinue
        if (-not $pythonExe) {
            throw "Python not found"
        }

        & $pythonExe.Source -m venv $EULIX_VENV

        if ($LASTEXITCODE -ne 0) {
            throw "Failed to create virtual environment"
        }
    }

    Write-Success "Virtual environment created"

    # Install requirements
    $venvPython = "$EULIX_VENV\Scripts\python.exe"
    $reqFile = "$CLONE_DIR\eulix-embed\requirements.txt"

    if (-not (Test-Path $reqFile)) {
        throw "requirements.txt not found at $reqFile"
    }

    Write-Info "Installing Python dependencies..."

    if ($script:IS_UV) {
        & uv pip install --python $venvPython -r $reqFile

        if ($LASTEXITCODE -ne 0) {
            throw "Failed to install Python dependencies"
        }
    } else {
        & $venvPython -m pip install --upgrade pip
        & $venvPython -m pip install -r $reqFile

        if ($LASTEXITCODE -ne 0) {
            throw "Failed to install Python dependencies"
        }
    }

    Write-Success "Python dependencies installed"

    # Install PyTorch
    Write-Info "Installing PyTorch for $($script:COMPUTE_PLATFORM)..."

    $torchArgs = @()
    switch ($script:COMPUTE_PLATFORM) {
        "cuda126" { $torchArgs = @("--index-url", "https://download.pytorch.org/whl/cu126") }
        "cuda124" { $torchArgs = @("--index-url", "https://download.pytorch.org/whl/cu124") }
        "cuda130" { $torchArgs = @("--index-url", "https://download.pytorch.org/whl/cu130") }
        "cuda132" { $torchArgs = @("--index-url", "https://download.pytorch.org/whl/cu132") }
        "rocm72"  { $torchArgs = @("--index-url", "https://download.pytorch.org/whl/rocm7.2") }
        "cpu"     { $torchArgs = @("--index-url", "https://download.pytorch.org/whl/cpu") }
    }

    if ($script:IS_UV) {
        & uv pip install --python $venvPython @torchArgs torch torchvision torchaudio
    } else {
        & $venvPython -m pip install @torchArgs torch torchvision torchaudio
    }

    if ($LASTEXITCODE -ne 0) {
        Write-Error "Failed to install PyTorch"
        Write-Host "  You can install it manually later:" -ForegroundColor Yellow
        Write-Host "  $venvPython -m pip install @torchArgs torch torchvision torchaudio"
        throw "PyTorch installation failed"
    }

    Write-Success "PyTorch ($($script:COMPUTE_PLATFORM)) installed"
}

# ─── Final Setup ───────────────────────────────────────────
function Complete-Setup {
    Write-Banner "Finalizing Installation"

    # Update PATH
    Add-ToUserPath $INSTALL_DIR

    # Refresh session PATH
    Update-SessionPath

    # Verify installation
    if (Get-Command eulix -ErrorAction SilentlyContinue) {
        Write-Success "eulix is ready to use!"
    } else {
        Write-Warn "eulix not found in current PATH"
        Write-Host "  Restart your terminal or run:" -ForegroundColor Yellow
        Write-Host "  `$env:PATH = '$INSTALL_DIR;' + `$env:PATH" -ForegroundColor White
    }

    # Display summary
    Write-Host ""
    Write-Host "╔═══════════════════════════════════════════════╗" -ForegroundColor Green
    Write-Host "║         Eulix Installation Complete!          ║" -ForegroundColor Green
    Write-Host "╚═══════════════════════════════════════════════╝" -ForegroundColor Green
    Write-Host ""
    Write-Host "  Binaries   : $INSTALL_DIR" -ForegroundColor White
    Write-Host "  Data       : $EULIX_DIR" -ForegroundColor White
    Write-Host "  Python venv: $EULIX_VENV" -ForegroundColor White
    Write-Host "  Repository : $CLONE_DIR" -ForegroundColor White
    Write-Host "  Compute    : $($script:COMPUTE_PLATFORM)" -ForegroundColor White
    Write-Host ""
    Write-Host "  Get started:  eulix --help" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  New terminal may need to be opened for PATH changes." -ForegroundColor Yellow
    Write-Host ""
}

# ─── Main Execution ────────────────────────────────────────
function Main {
    Write-Host ""
    Write-Host "╔═══════════════════════════════════════════════════════╗" -ForegroundColor Cyan
    Write-Host "║                                                       ║" -ForegroundColor Cyan
    Write-Host "║           Eulix Installer v2.0 for Windows            ║" -ForegroundColor Cyan
    Write-Host "║                                                       ║" -ForegroundColor Cyan
    Write-Host "╚═══════════════════════════════════════════════════════╝" -ForegroundColor Cyan
    Write-Host ""

    # Check if running as Administrator
    $isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if (-not $isAdmin) {
        Write-Warn "Not running as Administrator"
        Write-Host "  Some features (like Go MSI installation) require admin rights." -ForegroundColor Yellow
        Write-Host "  Consider re-running as Administrator for smoother experience." -ForegroundColor Yellow
        Write-Host ""
    }

    try {
        Assert-Windows

        # Install uv early (needed for Python management)
        Install-UV

        # Check and install/validate all dependencies
        Assert-Dependencies

        # Detect GPU
        Get-ComputePlatform

        # Get source code
        Update-Repository

        # Build
        Build-Eulix

        # Python environment
        Initialize-PythonEnvironment

        # Final setup
        Complete-Setup

    } catch {
        Write-Host ""
        Write-Error "Installation failed: $_"
        Write-Host ""
        Write-Host "Troubleshooting:" -ForegroundColor Yellow
        Write-Host "  1. Check that all dependencies are installed correctly"
        Write-Host "  2. Try running this script as Administrator"
        Write-Host "  3. Report issues at: $REPO_URL/issues"
        Write-Host ""
        exit 1
    }
}

# ─── Start ─────────────────────────────────────────────────
Main
