#Requires -Version 5.1
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# ─── Config ───────────────────────────────────────────────────────────────────
$InstallDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { "$HOME\AppData\Local\bin" }
$RepoRoot   = $PSScriptRoot

# ─── Logging ──────────────────────────────────────────────────────────────────
function Log-Info  ([string]$msg) { Write-Host "[info]  $msg" -ForegroundColor Cyan   }
function Log-Ok    ([string]$msg) { Write-Host "[ ok ]  $msg" -ForegroundColor Green  }
function Log-Warn  ([string]$msg) { Write-Host "[warn]  $msg" -ForegroundColor Yellow }
function Log-Error ([string]$msg) { Write-Host "[err ]  $msg" -ForegroundColor Red; exit 1 }

# ─── Dependency checks ────────────────────────────────────────────────────────
function Check-Deps {
    Log-Info "Checking dependencies..."
    if (!(Get-Command cargo -ErrorAction SilentlyContinue)) { Log-Error "Rust/Cargo not found. Install via https://rustup.rs" }
    if (!(Get-Command go    -ErrorAction SilentlyContinue)) { Log-Error "Go not found. Install via https://go.dev" }
    Log-Ok "cargo $((cargo --version) -replace 'cargo ','')"
    Log-Ok "go $((go version) -replace '.*go version ','' -replace ' .*','')"
}

# ─── GPU Detection ────────────────────────────────────────────────────────────
function Detect-GPU {
    Log-Info "Detecting hardware..."

    try {
        $gpus = @(Get-CimInstance Win32_VideoController -ErrorAction Stop |
                  Where-Object { $_.AdapterRAM -gt 0 -or $_.CurrentHorizontalResolution -gt 0 })
    } catch {
        Log-Warn "Could not query GPU info: $_"
        return "cpu"
    }

    if ($gpus.Count -eq 0) {
        Log-Warn "No video controller found — building for CPU."
        return "cpu"
    }

    # Check all adapters; prefer discrete over integrated
    $nvidiaGpu = $gpus | Where-Object { $_.Caption -match "NVIDIA" } | Select-Object -First 1
    $amdGpu    = $gpus | Where-Object { $_.Caption -match "AMD|Radeon" -or $_.AdapterCompatibility -match "Advanced Micro Devices" } | Select-Object -First 1

    if ($nvidiaGpu) {
        # Verify CUDA is actually usable via nvidia-smi
        if (Get-Command nvidia-smi -ErrorAction SilentlyContinue) {
            Log-Ok "NVIDIA GPU: $($nvidiaGpu.Caption) (CUDA)"
        } else {
            Log-Warn "NVIDIA GPU found ($($nvidiaGpu.Caption)) but nvidia-smi missing — CUDA drivers may not be installed."
        }
        return "cuda"
    }

    if ($amdGpu) {
        Log-Ok "AMD GPU: $($amdGpu.Caption) (ROCm)"
        return "rocm"
    }

    Log-Warn "No discrete GPU detected — building for CPU."
    return "cpu"
}

# ─── Build step helper ────────────────────────────────────────────────────────
function Invoke-Build ([string]$label, [scriptblock]$block) {
    Log-Info "Building $label..."
    & $block
    if ($LASTEXITCODE -ne 0) { Log-Error "$label build failed (exit code $LASTEXITCODE)." }
    Log-Ok "$label built."
}

# ─── Build ────────────────────────────────────────────────────────────────────
function Run-Build ([string]$gpuType) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null

    $cargoFlags = switch ($gpuType) {
        "cuda" { @("--features", "cuda") }
        "rocm" { @("--features", "rocm") }
        default { @() }
    }

    # 1. eulix_embed (Rust)
    Invoke-Build "eulix_embed" {
        Push-Location "$RepoRoot\eulix\eulix_embed"
        & cargo build --release @cargoFlags
        Move-Item "target\release\eulix_embed.exe" "$InstallDir\" -Force
        Pop-Location
    }

    # 2. eulix-parser (Rust)
    Invoke-Build "eulix-parser" {
        Push-Location "$RepoRoot\eulix-parser"
        & cargo build --release
        Move-Item "target\release\eulix-parser.exe" "$InstallDir\" -Force
        Pop-Location
    }

    # 3. eulix (Go)
    Invoke-Build "eulix (Go)" {
        Set-Location $RepoRoot
        & go build -o "$InstallDir\eulix.exe" cmd/eulix/main.go
    }

    Log-Ok "All binaries installed to: $InstallDir"
}

# ─── PATH reminder ────────────────────────────────────────────────────────────
function Check-Path {
    $machinePath = [System.Environment]::GetEnvironmentVariable("Path", "Machine")
    $userPath    = [System.Environment]::GetEnvironmentVariable("Path", "User")
    $allPaths    = "$machinePath;$userPath"

    if ($allPaths -notlike "*$InstallDir*") {
        Log-Warn "$InstallDir is not in your system PATH."
        Log-Warn "Run this to add it permanently:"
        Log-Warn '  [System.Environment]::SetEnvironmentVariable("Path", $env:Path + ";' + $InstallDir + '", "User")'
    }
}

# ─── Main ─────────────────────────────────────────────────────────────────────
Set-Location $RepoRoot
Check-Deps
$gpuType = Detect-GPU
Run-Build $gpuType
Check-Path
Log-Ok "Installation complete."