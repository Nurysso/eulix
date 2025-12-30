# Windows Install Guide (should work hopefully)

## **Step 1: Check if you have the required tools**

```powershell
cargo --version
go version
make --version
```

If `make` is missing, you can either install make or build eulix manually (see Option 2 below).

---

## **Option 1: Using Make (Recommended)**

```powershell
# Make sure you're in the eulix project directory
cd path\to\eulix

# Build all binaries
make build

# Install to C:\Users\<uname>\AppData\Local\Programs\eulix
make install
```

---

## **Option 2: Manual Build (if make doesn't work)**

```powershell
# Go to project directory
cd path\to\eulix

# Build parser
cd eulix-parser
cargo build --release
cd ..

# Build embedder (CPU backend) for cuda or rocm backend
# add features cuda -> cuda backend and rocm for rocm backend
cd eulix-embed
cargo build --release --features cpu
cd ..

# Build CLI
mkdir build
go build -o build\eulix.exe .\cmd\eulix\main.go

# Create install directory
mkdir $env:LOCALAPPDATA\Programs\eulix

# Copy binaries
copy eulix-parser\target\release\eulix_parser.exe $env:LOCALAPPDATA\Programs\eulix\
copy eulix-embed\target\release\eulix_embed.exe $env:LOCALAPPDATA\Programs\eulix\
copy build\eulix.exe $env:LOCALAPPDATA\Programs\eulix\
```

---

## **Step 2: Verify installation**

```powershell
ls $env:LOCALAPPDATA\Programs\eulix
```

You should see all three `.exe` files.

## **Step 3: Test the commands**

```powershell
eulix --version
# or
eulix --help
```

If it still doesn't work, close and reopen PowerShell, then try again.
