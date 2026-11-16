<#
.SYNOPSIS
    Native Windows build for Eulix (MSVC Rust parser + Go CLI).

.PARAMETER Gpu
    amd (CPU/AMD) or nvidia. Selects onnx-<gpu>.txt requirements. Default: amd.

.PARAMETER SkipEmbed
    Don't re-create eulix-embed.zip.

.PARAMETER Clean
    Remove build artifacts and exit.

.EXAMPLE
    .\build.ps1
    .\build.ps1 -Gpu nvidia
#>
[CmdletBinding()]
param(
    [ValidateSet('amd', 'nvidia')][string]$Gpu = 'amd',
    [switch]$SkipEmbed,
    [switch]$Clean
)

$ErrorActionPreference = 'Stop'
$Root      = $PSScriptRoot
$ParserDir = Join-Path $Root 'eulix-parser'
$EmbedDir  = Join-Path $Root 'eulix-embed'
$CliDir    = Join-Path $Root 'eulix-cli'
$AssetsBin = Join-Path $CliDir 'internal\assets\bins'
$BuildDir  = Join-Path $Root 'build'
$ParserOut = Join-Path $AssetsBin 'eulix_parser_windows.exe'

function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Run-Native([scriptblock]$cmd) {
    & $cmd
    if ($LASTEXITCODE -ne 0) { throw "Command failed with exit code $LASTEXITCODE" }
}

if ($Clean) {
    Step 'Cleaning'
    Push-Location $ParserDir; try { cargo clean } finally { Pop-Location }
    Remove-Item -Recurse -Force $BuildDir -ErrorAction SilentlyContinue
    Remove-Item -Force (Join-Path $Root 'eulix-embed.zip') -ErrorAction SilentlyContinue
    Remove-Item -Force $ParserOut -ErrorAction SilentlyContinue
    return
}

foreach ($tool in 'cargo', 'go') {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { throw "'$tool' not found in PATH" }
}

New-Item -ItemType Directory -Force $BuildDir, $AssetsBin | Out-Null

# 1. Rust parser (native MSVC; needs advapi32.lib)
Step 'Building eulix-parser (x86_64-pc-windows-msvc)'
$oldFlags = $env:RUSTFLAGS
$env:RUSTFLAGS = '-C link-arg=advapi32.lib'
Push-Location $ParserDir
try { Run-Native { cargo build --release } }
finally { Pop-Location; $env:RUSTFLAGS = $oldFlags }

$builtParser = Join-Path $ParserDir 'target\release\eulix_parser.exe'
if (-not (Test-Path $builtParser)) { throw "Parser not found at $builtParser" }
Copy-Item -Force $builtParser $ParserOut

# 2. Embed package
if (-not $SkipEmbed) {
    Step 'Packaging eulix-embed.zip'
    $stage = Join-Path ([IO.Path]::GetTempPath()) ("eulix-embed-" + [guid]::NewGuid())
    $zip   = Join-Path $Root 'eulix-embed.zip'
    try {
        New-Item -ItemType Directory -Force (Join-Path $stage 'eulix-embed') | Out-Null
        & robocopy $EmbedDir (Join-Path $stage 'eulix-embed') /E /NFL /NDL /NJH /NJS /NP `
            /XD .venv tests .ruff_cache .pytest_cache .mypy_cache .git __pycache__ `
            /XF *.pyc __init__* .codespell-ignore | Out-Null
        if ($LASTEXITCODE -ge 8) { throw "robocopy failed ($LASTEXITCODE)" }
        Remove-Item -Force $zip -ErrorAction SilentlyContinue
        Compress-Archive -Path (Join-Path $stage 'eulix-embed') -DestinationPath $zip -Force
    }
    finally { Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue }
    Copy-Item -Force $zip (Join-Path $AssetsBin 'eulix-embed.zip')
}

# 3. Go CLI
Step "Building eulix CLI (windows/amd64, gpu=$Gpu)"
$hash   = (Get-FileHash -Algorithm SHA256 $ParserOut).Hash.ToLower()
$outExe = Join-Path $BuildDir "eulix_windows_$Gpu.exe"
$ldflags = "-s -w -X 'eulix/internal/assets.embed_requirements=onnx-$Gpu.txt' -X 'eulix/internal/assets.ParserHash=$hash'"

$env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
Push-Location $CliDir
try { Run-Native { go build "-ldflags=$ldflags" -trimpath -o $outExe .\cmd\eulix\main.go } }
finally { Pop-Location; Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue }

Write-Host "`nDone: $outExe" -ForegroundColor Green
