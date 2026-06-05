# Installation Guide For Eulix

### Requirements

- Python (embedder)
- Go (main cli)
- Rust for eulix-parser (windows will need Visual Studio C++ build tools for the linker)
- uv (not a must have but good to have for managing python versions and venvs)
- Git to clone the repo duh

---

## Linux / Mac

Requirements can be installed via system package manager

#### Arch

```bash
sudo pacman -S python golang rust uv git
```

#### Ubuntu / Debian

```bash
sudo apt-get update
sudo apt-get install -y python3 golang-go rustup git
```

> `golang-go` on older Ubuntu can be pretty stale. Add the backports PPA if you need a recent version:
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
sudo dnf install -y python3 golang rust cargo git
curl -LsSf https://astral.sh/uv/install.sh | sh
```

#### openSUSE

```bash
sudo zypper install -y python3 go rust cargo git
curl -LsSf https://astral.sh/uv/install.sh | sh
```

#### Mac

```bash
brew install python go rust git uv
```

> No Homebrew? https://brew.sh

---

## Installation

```bash
mkdir -p $HOME/.Eulix  # stores eulix_embed.py and the .venv

git clone --depth 1 https://github.com/Nurysso/eulix.git
cd eulix
```

### Build Binaries

#### Eulix Parser

```bash
cd eulix-parser

# linux — native cpu optimisations
RUSTFLAGS="-C target-cpu=native" cargo build --release

# mac
cargo build --release

# copy the binary
# note: directory is hyphen (eulix-parser), binary is underscore (eulix_parser)
cp target/release/eulix_parser ~/.local/bin/eulix_parser
```

#### Eulix Embedder

```bash
cd ..
cp eulix-embed/eulix_embed.py ~/.Eulix/
```

Create a python 3.10 venv in `$HOME/.Eulix` and install deps:

```bash
uv venv --python 3.10 $HOME/.Eulix/.venv
uv pip install --python $HOME/.Eulix/.venv/bin/python -r eulix-embed/requirements.txt
```

Then install PyTorch for your compute platform — check the official site for the latest commands: https://pytorch.org

| Compute Platform | Command                                                                                 | Notes                                |
| ---------------- | --------------------------------------------------------------------------------------- | ------------------------------------ |
| **`cuda126`**    | `uv pip install torch torchvision --index-url https://download.pytorch.org/whl/cu126`   | CUDA 12.6                            |
| **`cuda130`**    | `uv pip install torch torchvision`                                                      | standard PyPI wheels                 |
| **`cuda132`**    | `uv pip install torch torchvision --index-url https://download.pytorch.org/whl/cu132`   | CUDA 13.2                            |
| **`rocm72`**     | `uv pip install torch torchvision --index-url https://download.pytorch.org/whl/rocm7.2` | AMD ROCm 7.2                         |
| **`cpu`**        | `uv pip install torch torchvision --index-url https://download.pytorch.org/whl/cpu`     | CPU-only                             |
| **`mac`**        | `uv pip install torch torchvision`                                                      | standard PyPI wheels (MPS via Metal) |

> activate the venv first or pass `--python $HOME/.Eulix/.venv/bin/python` so it installs into the right place

#### Eulix CLI (Orchestrator)

```bash
# linux
CGO_ENABLED=0 go build -ldflags="-s -w" -o ~/.local/bin/eulix cmd/eulix/main.go

# mac
go build -o ~/.local/bin/eulix cmd/eulix/main.go
```

Make sure `~/.local/bin` is on your PATH:

```bash
export PATH="$HOME/.local/bin:$PATH"  # add this to your .bashrc / .zshrc
```

---

## Windows

> requirements can be installed via winget or from the official websites below

### C++ Linker (Rust needs this)

Rust on Windows defaults to the MSVC toolchain which needs the Visual Studio C++ linker. Two options:

**A** — download Visual Studio and pick the C++ workload during setup

**B** — install just the build tools, no IDE:

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

> prefer GCC over MSVC? install MinGW and switch the toolchain: `rustup set default-host x86_64-pc-windows-gnu`

### Install Dependencies

```powershell
winget install Git.Git GoLang.Go Rustlang.Rustup astral-sh.uv
```

Or grab them manually:

- Git: https://git-scm.com/download/win
- Go: https://go.dev/dl/
- Rust: https://rustup.rs
- uv: https://docs.astral.sh/uv/getting-started/installation/

### Installation

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.Eulix"

git clone --depth 1 https://github.com/Nurysso/eulix.git
cd eulix
```

### Build Binaries

#### Eulix Parser

```powershell
cd eulix-parser
$env:RUSTFLAGS = "-C target-feature=+crt-static"
cargo build --release
Copy-Item target\release\eulix_parser.exe "$env:USERPROFILE\.local\bin\eulix_parser.exe"
```

#### Eulix Embedder

```powershell
Copy-Item eulix-embed\eulix_embed.py "$env:USERPROFILE\.Eulix\"

uv venv --python 3.10 "$env:USERPROFILE\.Eulix\.venv"
uv pip install --python "$env:USERPROFILE\.Eulix\.venv\Scripts\python.exe" `
    -r eulix-embed\requirements.txt
```

Install PyTorch — same table as above, just prefix with the venv python:

```powershell
uv pip install --python "$env:USERPROFILE\.Eulix\.venv\Scripts\python.exe" `
    torch torchvision --index-url https://download.pytorch.org/whl/<platform>
```

#### Eulix CLI (Orchestrator)

```powershell
$env:CGO_ENABLED = "0"
go build -ldflags="-s -w" -o "$env:USERPROFILE\.local\bin\eulix.exe" cmd\eulix\main.go
```

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

## Install MinGW

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

**5. Build**

```powershell
cd eulix-parser
cargo build --release
```

No MSVC, no Visual Studio, no UAC prompts — just gcc doing the linking.

## Automated Installer

Don't want to do all this manually? the install scripts handle everything:

```bash
# Linux / macOS
curl -sSf https://raw.githubusercontent.com/Nurysso/eulix/main/install.sh | bash
```

```powershell
# Windows still needs to add visual Studio code or mingw manually cause i dont want to deal with ps1 again
irm https://raw.githubusercontent.com/Nurysso/eulix/main/install.ps1 | iex
```

---

## Troubleshooting

**`eulix: command not found`** — `~/.local/bin` probably isn't on your PATH. Add `export PATH="$HOME/.local/bin:$PATH"` to your shell rc and reload.

**`error: linker 'link.exe' not found` (Windows)** — MSVC C++ tools aren't installed or aren't on PATH. Go through the C++ linker section above.

**PyTorch not seeing CUDA after install** — make sure you installed the right `cu1xx` wheel for your driver version and that you're inside the `.venv`.

Still stuck? open an issue: https://github.com/Nurysso/eulix/issues
