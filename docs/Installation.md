# Installation

This guide shows how to install Eulix with the install script, and how to build it from source.

> **Platforms:** Linux (x86-64) and Windows 11 (64-bit) have release builds. The release also publishes a macOS (Apple Silicon, `darwin-arm64`) binary, but `install.sh` refuses to run on macOS, so download it from the release page or build from source.
> **Last verified against:** `ad744e9`. I read the install scripts, `build.sh` and the `Makefile`; I did not run the installers.

## Which way should I install?

| You want to...                | Use                                            |
| ----------------------------- | ---------------------------------------------- |
| Just use Eulix                | [The install script](#install-with-the-script) |
| Use macOS, or change the code | [Build from source](#build-from-source)        |

## What you are installing

`eulix` is a single Go program. The parser (`eulix_parser`, Rust) and the embedding scripts (`eulix_embed`, Python) are packed inside it, and unpacked into `~/.Eulix/` the first time it runs. So there is one thing to install. What the script adds on top is a Python 3.11 environment for the embedding scripts, and `vizeulize.html` for the call-graph explorer. See [Assets](eulix-cli/Assets.md) for how the unpacking and integrity check work.

## Requirements

For the install script, you need only the following. The script installs the rest.

| Need                                                          | Why                                                                                                                                             |
| ------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Linux with `bash` and `curl`, or Windows with PowerShell 5.1+ | To run the script.                                                                                                                              |
| Network access                                                | To download the release, `uv`, Python 3.11 and the embedding model's Python packages. The model itself is downloaded the first time it is used. |
| About a few GB of disk                                        | Python packages and the model.                                                                                                                  |

**GPU:** the script asks you to pick a variant. Choose `nvidia` for NVIDIA GPUs (CUDA). Choose `amd` for AMD GPUs or for machines with no NVIDIA GPU, including CPU-only ones. The choice decides which ONNX packages are installed. If you have an AMD GPU and get strange search results, read [EMB-1](Known-Issues/Embeddings.md#emb-1-onnx-on-rocm-produces-wrong-embeddings).

## Install with the script

### Linux

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/Nurysso/eulix/main/install.sh)
```

### Windows

Run in PowerShell:

```powershell
irm https://raw.githubusercontent.com/Nurysso/eulix/main/install.ps1 | iex
```

### What the script does

1. Finds the latest release on GitHub, or the one you name in `EULIX_VERSION` (for example `EULIX_VERSION=v0.8.3`).
2. Asks which GPU variant you want.
3. Installs `uv` if it is missing, and creates a Python 3.11 virtual environment at `~/.Eulix/.venv`.
4. Downloads the `eulix` binary for your system and `vizeulize.html`, and **checks both against the release's `checksums.txt`**. It stops on a mismatch.
5. Installs them: `eulix` into `~/.local/bin` on Linux or `%LOCALAPPDATA%\Eulix\bin` on Windows, and the web page into `~/.Eulix/bin/`.
6. Runs `eulix --help` once, which unpacks the parser and embedding scripts into `~/.Eulix/` and installs their Python packages.

### After the script: PATH

If the install folder is not on your `PATH`, the script says so. On Linux add this to `~/.bashrc` or `~/.zshrc`:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Open a new terminal and check:

```bash
eulix version
```

Expected result: the versions of `eulix`, `eulix_parser` and `eulix_embed`.

## Your first project

```bash
cd your-project
eulix init        # creates .eulix/, .euignore, eulix.toml (a short wizard)
eulix analyze     # parse and build vectors
eulix chat        # ask questions
```

Choose a language model in the wizard, or in `eulix.toml` ([Config](eulix-cli/Config.md)). A local model needs no key. For hosted models, put the key in your environment or the project's `.env`, not in `eulix.toml`.

## Build from source

- [Linux](#build-on-linux)
- [Windows](#build-on-windows)
- [macOS](#build-on-macos)

First get the code (all systems):

```bash
git clone --depth 1 https://github.com/Nurysso/eulix.git
cd eulix
```

### Tools every system needs

| Tool         | Version              | Used for                                                              |
| ------------ | -------------------- | --------------------------------------------------------------------- |
| Go           | 1.26 or newer        | The `eulix` program.                                                  |
| Rust         | stable, edition 2021 | The parser.                                                           |
| Python       | 3.11                 | The embedding tool at run time. Get it with `uv python install 3.11`. |
| `uv`         | any recent           | Creates the Python environment and installs packages.                 |
| `git`, `zip` | any                  | Cloning and packaging the embedding scripts.                          |

> Install the version for your OS.

### Choosing the ONNX file

| File              | For                                   |
| ----------------- | ------------------------------------- |
| `onnx-nvidia.txt` | NVIDIA GPUs (CUDA).                   |
| `onnx-amd.txt`    | AMD GPUs, CPU-only machines and Macs. |

> choosing onnx file installs specific version of ONNXRUNTIME you can switch
> between nvidia and amd or even pytorch if you want

### Build on Linux

Extra tool: `sha256sum` (part of coreutils), to hash the parser. The hash is compiled into `eulix`.

**Option 1: `make`**

```bash
make build-single                          # Linux, AMD/CPU variant by default
make build-single GPU=nvidia               # NVIDIA variant
make build-all                             # all four variants (Linux and Windows)
make help                                  # every target
```

Binaries are written to `build/`. Copy the one you want to a folder on your `PATH`:

```bash
cp build/<the binary> ~/.local/bin/eulix && chmod +x ~/.local/bin/eulix
```

**Option 2: `build.sh`** (builds Linux and Windows binaries)

```bash
rustup target add x86_64-unknown-linux-gnu x86_64-pc-windows-gnu
# MinGW cross toolchain for the Windows build:
#   Arch: sudo pacman -S mingw-w64-gcc    Ubuntu: sudo apt-get install -y gcc-mingw-w64-x86-64
chmod +x build.sh && ./build.sh
```

Output goes to `eulix-cli/`: `eulix_linux_amd`, `eulix_linux_nvidia`, `eulix_windows_amd.exe`, `eulix_windows_nvidia.exe`.

**Option 3: by hand**

```bash
# 1. Parser
cd eulix-parser && cargo build --release && cd ..
cp eulix-parser/target/release/eulix_parser eulix-cli/internal/assets/bins/eulix_parser_linux

# 2. Hash it (compiled in so eulix can verify the parser at run time)
HASH=$(sha256sum eulix-cli/internal/assets/bins/eulix_parser_linux | awk '{print $1}')

# 3. Package the embedding scripts
zip -r eulix-embed.zip eulix-embed/ \
  -x "*/.venv/*" "*/.ruff_cache/*" "*/.pytest_cache/*" "*/.mypy_cache/*" "*/.git/*" "*/__pycache__/*" "*.pyc"
cp eulix-embed.zip eulix-cli/internal/assets/bins/eulix-embed.zip

# 4. The program (use onnx-nvidia.txt or onnx-amd.txt)
cd eulix-cli
CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X 'eulix/internal/assets.embed_requirements=onnx-amd.txt' -X 'eulix/internal/assets.ParserHash=${HASH}'" \
  -o ~/.local/bin/eulix ./cmd/eulix/main.go
```

### Build on Windows

```ps1
winget install Git.Git GoLang.Go Rustlang.Rustup astral-sh.uv
```

> **The Windows build uses `--target x86_64-pc-windows-gnu`, not the default toolchain.** The `Makefile` and `build.sh` both run `cargo build --release --target x86_64-pc-windows-gnu` (with `RUSTFLAGS="-C link-args=-ladvapi32"`). They do not rely on the default stable toolchain, which on Windows targets MSVC (`x86_64-pc-windows-msvc`). So you need the GNU target and a MinGW linker, even if you already have the Visual Studio C++ build tools.

Install MinGW through MSYS2 and add the Rust target:

```ps1
winget install msys2.msys2
# in the MSYS2 shell:
pacman -S mingw-w64-x86_64-gcc
# in PowerShell:
rustup target add x86_64-pc-windows-gnu
```

Make sure the MinGW `bin` folder (for example `C:\msys64\mingw64\bin`) is on your `PATH`.

**Option 1: `make`**

The `Makefile` detects Windows and uses PowerShell for hashing and zipping.

```ps1
make build-single OS_TARGET=windows GPU=amd
make build-single OS_TARGET=windows GPU=nvidia
```

The binary is written to `build/`. `make` and the `Makefile`'s shell recipes need a POSIX `sh` (for example from Git for Windows or MSYS2) on your `PATH`.

**Option 2: by hand**

If you would rather use the default MSVC toolchain (Visual Studio Build Tools with the C++ workload), build by hand. This is the only way that does not use `--target`; the output folder differs because there is no target subfolder.

```ps1
# 1. Parser (MSVC, default toolchain)
cd eulix-parser; cargo build --release; cd ..
copy eulix-parser\target\release\eulix_parser.exe eulix-cli\internal\assets\bins\eulix_parser_windows.exe

# 2. Hash it
$HASH = (Get-FileHash eulix-cli\internal\assets\bins\eulix_parser_windows.exe -Algorithm SHA256).Hash.ToLower()

# 3. Package the embedding scripts
Compress-Archive -Path eulix-embed\* -DestinationPath eulix-embed.zip -Force
copy eulix-embed.zip eulix-cli\internal\assets\bins\eulix-embed.zip

# 4. The program (use onnx-nvidia.txt or onnx-amd.txt)
cd eulix-cli
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -X 'eulix/internal/assets.embed_requirements=onnx-amd.txt' -X 'eulix/internal/assets.ParserHash=$HASH'" -o eulix.exe .\cmd\eulix\main.go
```

`Compress-Archive` does not skip `.venv` or cache folders. Delete them from `eulix-embed\` first, or the zip will be large.

Then move `eulix.exe` to `%LOCALAPPDATA%\Eulix\bin` and add that folder to your `PATH`.

### Build on macOS

```zsh
brew install python go rust git uv zip
```

The `Makefile` and `build.sh` do not build for macOS. Use the by-hand steps with `GOOS=darwin`, and `GOARCH=arm64` (Apple Silicon) or `amd64` (Intel; build the parser on that Mac).

```zsh
# 1. Parser
cd eulix-parser && cargo build --release && cd ..
cp eulix-parser/target/release/eulix_parser eulix-cli/internal/assets/bins/eulix_parser_darwin

# 2. Hash it
HASH=$(shasum -a 256 eulix-cli/internal/assets/bins/eulix_parser_darwin | awk '{print $1}')

# 3. Package the embedding scripts
zip -r eulix-embed.zip eulix-embed/ \
  -x "*/.venv/*" "*/.ruff_cache/*" "*/.pytest_cache/*" "*/.mypy_cache/*" "*/.git/*" "*/__pycache__/*" "*.pyc"
cp eulix-embed.zip eulix-cli/internal/assets/bins/eulix-embed.zip

# 4. The program. Use onnx-amd.txt: there is no CUDA on a Mac.
cd eulix-cli
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
  -ldflags="-s -w -X 'eulix/internal/assets.embed_requirements=onnx-amd.txt' -X 'eulix/internal/assets.ParserHash=${HASH}'" \
  -o ~/.local/bin/eulix ./cmd/eulix/main.go
```

Binaries you build are not notarized. If macOS blocks one, run `xattr -d com.apple.quarantine ~/.local/bin/eulix`. The `darwin-arm64` release binary has not been tested on macOS.

### Upgrading from v0.8.3

Node IDs changed in v0.9.0. In v0.8.3 they held the absolute path of the machine (`class_AuthBase::/home/.../src/requests/auth.py`); now they are relative to the repository (`class_AuthBase::src/requests/auth.py`). After upgrading, run `eulix analyze` again in each repository so the knowledge base and the embeddings are rebuilt.

### File names and the parser hash

The file names matter: the Go build picks `eulix_parser_linux`, `eulix_parser_darwin` or `eulix_parser_windows.exe` by operating system. The `ParserHash` must be the hash of the exact parser that build embeds, otherwise the program refuses to run ([Assets](eulix-cli/Assets.md)).

## Update

Run the install script again; it installs the latest release over the old one. Then re-run `eulix analyze` in your projects, because a new version may change the index files ([Binary Format](eulix-embed/Binary-Format.md) requires an exact version match).

## Uninstall

```bash
rm ~/.local/bin/eulix          # Windows: delete %LOCALAPPDATA%\Eulix\bin
rm -rf ~/.Eulix                # unpacked tools, Python environment, explorer page
rm -rf your-project/.eulix your-project/eulix.toml your-project/.euignore   # per project
```

## Troubleshooting

| Symptom                                            | Cause and fix                                                                                                                                                                                                              |
| -------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `eulix: command not found`                         | The install folder is not on `PATH`. Add it and open a new terminal.                                                                                                                                                       |
| `eulix_parser BINARY INTEGRITY ERROR`              | The extracted parser does not match the hash compiled into `eulix`. Delete `~/.Eulix/bin/eulix_parser` and run `eulix` again to extract a fresh copy. If you built from source, re-hash the parser in `bins/` and rebuild. |
| Build error `pattern ...: no matching files found` | The parser or `eulix-embed.zip` is not in `eulix-cli/internal/assets/bins/`, or has the wrong file name for your system.                                                                                                   |
| `uv` not found, or Python is not 3.11              | Install `uv`, then `uv python install 3.11`. Run `eulix getEmbedDeps` to retry the package install.                                                                                                                        |
| The first run is slow or stalls                    | It is downloading Python packages. Run `eulix getEmbedDeps` in a terminal to watch the progress.                                                                                                                           |
| `error: linker 'link.exe' not found` (Windows)     | Install the C++ build tools if you build by hand with the default MSVC toolchain. `make` and `build.sh` use `--target x86_64-pc-windows-gnu` and need MinGW instead.                                                       |
| Poor semantic search on an AMD GPU                 | See [EMB-1](Known-Issues/Embeddings.md#emb-1-onnx-on-rocm-produces-wrong-embeddings). Use `engine = "ehnas"` in `eulix.toml`.                                                                                              |
| macOS says the developer cannot be verified        | `xattr -d com.apple.quarantine ~/.local/bin/eulix`.                                                                                                                                                                        |

More known problems: [Known Issues](Known-Issues/Overview.md). Still stuck? Open an issue at https://github.com/Nurysso/eulix/issues.

## Related Docs

- [Welcome](Welcome.md): what Eulix is.
- [eulix-cli Overview](eulix-cli/Overview.md): the commands.
- [Assets](eulix-cli/Assets.md): first-run unpacking and the integrity check.
- [Config](eulix-cli/Config.md): `eulix.toml`.
