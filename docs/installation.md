# Installation Guide For Eulix

Eulix is built from three parts:

| Part           | Language | Location        | Role                                                         |
| -------------- | -------- | --------------- | ------------------------------------------------------------ |
| `eulix-parser` | Rust     | `eulix-parser/` | Code parser. Compiled per-OS and **embedded inside the CLI** |
| `eulix-embed`  | Python   | `eulix-embed/`  | Embedder. Zipped and **embedded inside the CLI**             |
| `eulix` (CLI)  | Go       | `eulix-cli/`    | Main orchestrator. The only binary you actually run          |

The parser and the embedder are no longer copied around by hand. The build step places them in `eulix-cli/internal/assets/bins/` and the Go build bakes them into the final `eulix` binary. The embedder's Python dependencies come from an ONNX requirements file chosen at build time (`onnx-amd.txt` or `onnx-nvidia.txt`).

### Requirements

- **Go 1.26 or newer** (main CLI)
- **Rust, edition 2021** for eulix-parser (Windows needs the Visual Studio C++ build tools **or** MinGW for the linker)
  - Edition 2024 may work but hasn't been tested. Stick to 2021 for now.
- **Python 3.11** (embedder, runs at runtime). 3.11 is required because of the embedder's dependencies, so older versions like 3.10 won't work.
- uv (Its used by eulix cli to get deps automatically, if building manually use uv to create the venve too)
- Git to clone the repo duh
- `zip` (packages the embedder)
- `sha256sum` (Linux) or `shasum` (macOS) to hash the parser binary. The hash is compiled into the CLI so it can verify the embedded parser at runtime.

### Check your versions

```bash
go version        # go1.26.x or newer
rustc --version   # any recent stable; the crate uses edition 2021
python3 --version # 3.11.x (use uv, see below)
```

Don't have Python 3.11 (Ubuntu 22.04 ships 3.10, for example)? Let uv fetch it:

```bash
uv python install 3.11
```

---

## Quick Install (Automated)

Don't want to do all this manually? The install scripts handle everything:

```bash
# Linux / macOS
curl -sSf https://raw.githubusercontent.com/Nurysso/eulix/main/install.sh | bash
```

```powershell
# Windows still needs Visual Studio C++ build tools or MinGW installed manually
irm https://raw.githubusercontent.com/Nurysso/eulix/main/install.ps1 | iex
```

Otherwise, keep reading to build from source.

---

## Install Requirements

> **Python note:** the `python`/`python3` packages below install whatever version your distro or Homebrew currently ships, which may be newer or older than 3.11. Eulix needs 3.11, so after installing uv run `uv python install 3.11` and use that rather than the system Python.

### Linux

#### Arch

```bash
sudo pacman -S python golang rust uv git zip
```

#### Ubuntu / Debian

```bash
sudo apt-get update
sudo apt-get install -y python3 golang-go rustup git zip
```

> `golang-go` on Ubuntu is almost always older than the Go 1.26+ Eulix needs. Add the backports PPA, or grab Go from https://go.dev/dl/:
>
> ```bash
> sudo add-apt-repository ppa:longsleep/golang-backports && sudo apt-get update
> ```

uv isn't in apt, install it separately:

```bash
curl -LsSf https://astral.sh/uv/install.sh | sh
```

#### Fedora / RHEL

```bash
sudo dnf install -y python3 golang rust cargo git zip
curl -LsSf https://astral.sh/uv/install.sh | sh
```

#### openSUSE

```bash
sudo zypper install -y python3 go rust cargo git zip
curl -LsSf https://astral.sh/uv/install.sh | sh
```

### macOS

```bash
brew install python go rust git uv zip
```

> No Homebrew? https://brew.sh

`shasum` ships with macOS, so no extra hashing tool is needed. If you want the GNU `sha256sum` instead (for running the build script as-is), run `brew install coreutils`.

### Windows

See the [Windows](#windows) section below.

---

## Get The Source

```bash
git clone --depth 1 https://github.com/Nurysso/eulix.git
cd eulix
```

---

## Build With The Build Script (Linux host)

The repo's build script produces the release binaries for **Linux and Windows** in one go, from a Linux machine. Run it from the repo root.

### One-time setup

```bash
# Rust targets
rustup target add x86_64-unknown-linux-gnu
rustup target add x86_64-pc-windows-gnu
```

The Windows target is cross-compiled with MinGW, so install the cross toolchain too:

```bash
# Arch
sudo pacman -S mingw-w64-gcc

# Ubuntu / Debian
sudo apt-get install -y gcc-mingw-w64-x86-64

# Fedora
sudo dnf install -y mingw64-gcc
```

### Run it

```bash
chmod +x build.sh
./build.sh
```

### What the script does

1. **Checks** that the Rust targets are installed (warns if not) and that `sha256sum` exists (hard error if not).
2. **Builds `eulix-parser`** for `x86_64-unknown-linux-gnu` and `x86_64-pc-windows-gnu`.
3. **Copies the parser binaries** into `eulix-cli/internal/assets/bins/`:
   - `eulix_parser_linux`
   - `eulix_parser_windows.exe`
4. **Hashes** each parser binary with SHA-256. Each Go build embeds exactly one parser (selected by the build tags in `embed_linux.go`, `embed_darwin.go`, `embed_windows.go`), so the hash passed to a Go build must belong to the parser that build embeds.
5. **Zips the embedder** to `eulix-embed.zip` (excluding `.venv`, caches, `.git`, `__pycache__`, `*.pyc`) and copies it to `eulix-cli/internal/assets/bins/eulix-embed.zip`.
6. **Builds the Go CLI** twice per OS, once per ONNX backend variant.

### Output

Binaries land in `eulix-cli/`:

| Variant                    | Linux                | Windows                    |
| -------------------------- | -------------------- | -------------------------- |
| AMD (`onnx-amd.txt`)       | `eulix_linux_amd`    | `eulix_windows_amd.exe`    |
| NVIDIA (`onnx-nvidia.txt`) | `eulix_linux_nvidia` | `eulix_windows_nvidia.exe` |

Pick the variant that matches your GPU, then install it:

```bash
cp eulix-cli/eulix_linux_nvidia ~/.local/bin/eulix
chmod +x ~/.local/bin/eulix
```

Make sure `~/.local/bin` is on your PATH:

```bash
export PATH="$HOME/.local/bin:$PATH"  # add this to your .bashrc / .zshrc
```

---

## Build Manually

If you only need a binary for your own machine, you can skip the script. The steps are the same on every OS: build the parser, stage it, hash it, zip the embedder, build the CLI.

### Linux (manual)

```bash
# 1. Parser (native cpu optimisations)
cd eulix-parser
RUSTFLAGS="-C target-cpu=native" cargo build --release
cp target/release/eulix_parser ../eulix-cli/internal/assets/bins/eulix_parser_linux
cd ..

# 2. Hash the parser
HASH=$(sha256sum eulix-cli/internal/assets/bins/eulix_parser_linux | awk '{print $1}')

# 3. Package the embedder while ignoring unnecessary files
zip -r eulix-embed.zip eulix-embed/ \
  -x "*/.venv/*" "*/.ruff_cache/*" "*/.pytest_cache/*" "*/.mypy_cache/*" \
     "*/.git/*" "*/__pycache__/*" "*.pyc" ".codespell-ignore"
cp eulix-embed.zip eulix-cli/internal/assets/bins/eulix-embed.zip

# 4. CLI  (set REQ to onnx-nvidia.txt or onnx-amd.txt or torch-amd.txt or torch-nvidia.txt)
REQ=onnx-nvidia.txt
cd eulix-cli
CGO_ENABLED=0 go build \
  -ldflags="-s -w -X 'eulix/internal/assets.embed_requirements=${REQ}' -X 'eulix/internal/assets.embeddedParserHash=${HASH}'" \
  -trimpath -o ~/.local/bin/eulix ./cmd/eulix/main.go
```

---

## macOS

The build script has the macOS targets commented out, and macOS can't be cross-built with the stock toolchain, so there are two ways to do it. The Go build for darwin embeds `eulix_parser_macos_arm` (via the `embed_darwin.go` build tag), so that is the filename used below.

### Option A: Build natively on a Mac (recommended)

```bash
# 1. Parser
cd eulix-parser
cargo build --release
cp target/release/eulix_parser ../eulix-cli/internal/assets/bins/eulix_parser_darwin
cd ..

# 2. Hash the parser (macOS ships shasum, not sha256sum)
HASH=$(shasum -a 256 eulix-cli/internal/assets/bins/eulix_parser_darwin | awk '{print $1}')
echo "$HASH"

# 3. Package the embedder
zip -r eulix-embed.zip eulix-embed/ \
  -x "*/.venv/*" "*/.ruff_cache/*" "*/.pytest_cache/*" "*/.mypy_cache/*" \
     "*/.git/*" "*/__pycache__/*" "*.pyc" ".codespell-ignore"
cp eulix-embed.zip eulix-cli/internal/assets/bins/eulix-embed.zip

# 4. CLI
REQ=onnx-amd.txt   # see the note on requirements files below
cd eulix-cli
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build \
  -ldflags="-s -w -X 'eulix/internal/assets.embed_requirements=${REQ}' -X 'eulix/internal/assets.embeddedParserHash=${HASH}'" \
  -trimpath -o ~/.local/bin/eulix ./cmd/eulix/main.go
```

Then make sure `~/.local/bin` is on your PATH (add to `~/.zshrc`):

```bash
export PATH="$HOME/.local/bin:$PATH"
```

> **Requirements file on macOS:** the build script only defines two ONNX variants, `onnx-amd.txt` and `onnx-nvidia.txt`. There is no macOS-specific file. `onnx-nvidia.txt` expects CUDA, which Macs don't have, so `onnx-amd.txt` is the sensible default. Swap it if the repo adds a dedicated mac variant.

### Option B: Cross-compile from Linux with zig

This is what the commented-out lines in the build script do. It needs `zig` and `cargo-zigbuild`:

```bash
# one-time setup
rustup target add aarch64-apple-darwin
cargo install cargo-zigbuild
# plus zig itself: https://ziglang.org/download/  (or your package manager)

# parser
cd eulix-parser
cargo zigbuild --release --target aarch64-apple-darwin
cp target/aarch64-apple-darwin/release/eulix_parser ../eulix-cli/internal/assets/bins/eulix_parser_macos_arm
cd ..

HASH_MACOS_ARM=$(sha256sum eulix-cli/internal/assets/bins/eulix_parser_macos_arm | awk '{print $1}')

# (zip the embedder as in the manual Linux steps), then:
cd eulix-cli
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build \
  -ldflags="-s -w -X 'eulix/internal/assets.embed_requirements=onnx-amd.txt' -X 'eulix/internal/assets.embeddedParserHash=${HASH_MACOS_ARM}'" \
  -trimpath -o eulix_macos_arm_amd ./cmd/eulix/main.go
```

To enable macOS in the full build script, uncomment the `aarch64-apple-darwin` target check, the `zig` check, the `cargo zigbuild` line, the macOS `cp`, the `HASH_MACOS_ARM` lines, and the `build_variant darwin arm64 ...` lines. Binaries come out as `eulix_macos_arm_amd` and `eulix_macos_arm_nvidia`.

### Intel Macs

The script's comments reuse the ARM parser for the Intel build (`HASH_MACOS_INTEL="${HASH_MACOS_ARM}"`). An ARM parser binary won't run on an Intel Mac, so for a working Intel build:

1. Build the parser for `x86_64-apple-darwin` (natively on an Intel Mac, or `cargo zigbuild --release --target x86_64-apple-darwin`).
2. Hash **that** binary and pass it as `embeddedParserHash`.
3. Build the CLI with `GOARCH=amd64`.

Until the repo adds a separate Intel parser filename, `embed_darwin.go` still looks for `eulix_parser_macos_arm`, so you'll need to stage the Intel binary under that name or update the embed file.

### Gatekeeper

Binaries you build yourself aren't notarized. If macOS refuses to run a copied binary:

```bash
xattr -d com.apple.quarantine ~/.local/bin/eulix
```

---

## Windows

> requirements can be installed via winget or from the official websites below

### Linker (Rust needs one)

The build script targets `x86_64-pc-windows-gnu` (MinGW). If you build natively on Windows you can use either toolchain. Pick one.

#### Option A: MSVC (Visual Studio C++ linker)

Download Visual Studio and pick the C++ workload during setup, or install just the build tools, no IDE:

```powershell
Invoke-WebRequest -Uri "https://aka.ms/vs/17/release/vs_buildtools.exe" `
    -OutFile "$env:TEMP\vs_buildtools.exe"

Start-Process -Wait -FilePath "$env:TEMP\vs_buildtools.exe" -ArgumentList `
    "--quiet", "--wait", "--norestart", "--nocache",
    "--add", "Microsoft.VisualStudio.Workload.VCTools",
    "--add", "Microsoft.VisualStudio.Component.Windows11SDK.22621",
    "--includeRecommended"
```

Check that it finished and verify:

```powershell
# still running?
Get-Process -Name "vs_installer","vs_setup_bootstrapper" -ErrorAction SilentlyContinue

# check the linker is there
if (Test-Path "${env:ProgramFiles(x86)}\Microsoft Visual Studio\2022\BuildTools\VC\Tools\MSVC\*\bin\Hostx64\x64\link.exe") {
    Write-Host "Build Tools installed successfully!" -ForegroundColor Green
    Remove-Item "$env:TEMP\vs_buildtools.exe" -Force -ErrorAction SilentlyContinue
} else {
    Write-Host "Installation still in progress or failed" -ForegroundColor Yellow
    Write-Host "Check Task Manager for 'VS Installer' or 'vs_buildtools' processes"
}
```

Or just build a test project to confirm it works:

```powershell
cargo new test-build && cd test-build && cargo build --release
# if it builds you're good
```

#### Option B: MinGW (GNU toolchain, matches the build script)

No MSVC, no Visual Studio, no UAC prompts. Just gcc doing the linking.

**1. Install MinGW**

```powershell
winget install msys2.msys2
```

Then open the MSYS2 terminal it installs and run:

```bash
pacman -S mingw-w64-x86_64-gcc
```

**2. Add MinGW to PATH**

```powershell
$p = [Environment]::GetEnvironmentVariable("PATH","User")
[Environment]::SetEnvironmentVariable("PATH","C:\msys64\mingw64\bin;$p","User")
```

Open a new terminal and verify: `gcc --version`

**3. Add the GNU Rust target**

```powershell
rustup target add x86_64-pc-windows-gnu
```

**4. Switch the default toolchain**

```powershell
rustup set default-host x86_64-pc-windows-gnu
```

Or if you want to keep MSVC as default and only use GNU for eulix-parser, create a `rust-toolchain.toml` in the `eulix-parser` directory:

```toml
[toolchain]
channel = "stable"
targets = ["x86_64-pc-windows-gnu"]
```

### Install Dependencies

```powershell
winget install Git.Git GoLang.Go Rustlang.Rustup astral-sh.uv
```

Or grab them manually:

- Git: https://git-scm.com/download/win
- Go: https://go.dev/dl/
- Rust: https://rustup.rs
- uv: https://docs.astral.sh/uv/getting-started/installation/

PowerShell's built-in `Compress-Archive` and `Get-FileHash` replace `zip` and `sha256sum`, so nothing extra is needed.

### Build (manual)

```powershell
git clone --depth 1 https://github.com/Nurysso/eulix.git
cd eulix

# 1. Parser
cd eulix-parser
$env:RUSTFLAGS = "-C target-feature=+crt-static"
cargo build --release
Copy-Item target\release\eulix_parser.exe ..\eulix-cli\internal\assets\bins\eulix_parser_windows.exe
cd ..

# 2. Hash the parser
$HASH = (Get-FileHash eulix-cli\internal\assets\bins\eulix_parser_windows.exe -Algorithm SHA256).Hash.ToLower()

# 3. Package the embedder (remove .venv and caches first, or zip from a clean checkout)
Compress-Archive -Path eulix-embed -DestinationPath eulix-embed.zip -Force
Copy-Item eulix-embed.zip eulix-cli\internal\assets\bins\eulix-embed.zip -Force

# 4. CLI  (REQ is onnx-nvidia.txt or onnx-amd.txt)
$REQ = "onnx-nvidia.txt"
cd eulix-cli
$env:CGO_ENABLED = "0"
New-Item -ItemType Directory -Force "$env:USERPROFILE\.local\bin" | Out-Null
go build `
  -ldflags="-s -w -X 'eulix/internal/assets.embed_requirements=$REQ' -X 'eulix/internal/assets.embeddedParserHash=$HASH'" `
  -trimpath -o "$env:USERPROFILE\.local\bin\eulix.exe" .\cmd\eulix\main.go
```

> `Compress-Archive` doesn't support exclude patterns. Make sure `eulix-embed\.venv`, `__pycache__` and the tool caches aren't present before zipping, or use 7-Zip / WSL's `zip` with the exclude list from the Linux steps.

Add the bin directory to your PATH if it isn't there already:

```powershell
$p = [Environment]::GetEnvironmentVariable("PATH","User")
[Environment]::SetEnvironmentVariable("PATH","$env:USERPROFILE\.local\bin;$p","User")
```

Open a new terminal and verify everything works:

```powershell
eulix --help
```

---

## Choosing An ONNX Variant

| File              | Use for                                                                       |
| ----------------- | ----------------------------------------------------------------------------- |
| `onnx-nvidia.txt` | NVIDIA GPUs (CUDA)                                                            |
| `onnx-amd.txt`    | AMD GPUs, and the default for machines without an NVIDIA GPU (including Macs) |

The choice is baked into the binary at build time through `-X eulix/internal/assets.embed_requirements=<file>`. To switch backends, rebuild the CLI with the other file. PyTorch is no longer installed by hand, so the old per-platform `torch` install table has been removed.

---

## Troubleshooting

- **`eulix: command not found`**: `~/.local/bin` probably isn't on your PATH. Add `export PATH="$HOME/.local/bin:$PATH"` to your shell rc and reload.

- **`sha256sum is not installed. Required to hash parser binaries.`**: install `coreutils` (`brew install coreutils` on macOS). The build script exits if it's missing.

- **`x86_64-pc-windows-gnu target not installed`**: run `rustup target add x86_64-pc-windows-gnu`. On Linux you also need the MinGW cross toolchain (see setup above).

- **`error: linker 'link.exe' not found` (Windows)**: MSVC C++ tools aren't installed or aren't on PATH. Go through the Windows linker section above, or switch to the MinGW toolchain.

- **Build fails with a missing embed file (`pattern ...: no matching files found`)**: the parser binary or `eulix-embed.zip` isn't in `eulix-cli/internal/assets/bins/`. Rebuild the parser and re-run the copy and zip steps. The filename must match the OS: `eulix_parser_linux`, `eulix_parser_windows.exe`, or `eulix_parser_macos_arm`.

- **Parser hash mismatch at runtime**: the `embeddedParserHash` passed to `go build` didn't match the parser binary that build embedded. Re-hash the binary currently in `bins/` and rebuild the CLI.

- **GPU not detected by the embedder**: you probably built with the wrong variant. Rebuild with `onnx-nvidia.txt` for NVIDIA or `onnx-amd.txt` for AMD, and make sure your GPU drivers are up to date.

- **macOS: "cannot be opened because the developer cannot be verified"**: run `xattr -d com.apple.quarantine ~/.local/bin/eulix`.

Still stuck? open an issue: https://github.com/Nurysso/eulix/issues
