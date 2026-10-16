// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func releaseRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func releaseWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestInstallersRequireBothChecksums(t *testing.T) {
	for _, shell := range []string{"bash", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skipf("%s unavailable", shell)
			}
			for _, scenario := range []string{"missing-binary", "missing-html", "bad-binary", "bad-html", "valid"} {
				t.Run(scenario, func(t *testing.T) {
					dir := t.TempDir()
					asset := "eulix-linux-amd64-onnx-amd"
					if shell == "pwsh" {
						asset = "eulix-windows-amd64-onnx-amd.exe"
					}
					releaseWrite(t, filepath.Join(dir, "fixtures", asset), "binary")
					releaseWrite(t, filepath.Join(dir, "fixtures", "vizeulize.html"), "html")
					binHash, htmlHash := fmt.Sprintf("%x", sha256.Sum256([]byte("binary"))), fmt.Sprintf("%x", sha256.Sum256([]byte("html")))
					if scenario == "bad-binary" {
						binHash = strings.Repeat("0", 64)
					}
					if scenario == "bad-html" {
						htmlHash = strings.Repeat("0", 64)
					}
					checksum := ""
					if scenario != "missing-binary" {
						checksum += binHash + "  dist/" + asset + "\n"
					}
					if scenario != "missing-html" {
						checksum += htmlHash + "  vizEulize/vizeulize.html\n"
					}
					releaseWrite(t, filepath.Join(dir, "fixtures", "checksum.txt"), checksum)
					releaseWrite(t, filepath.Join(dir, "install", "eulix"), "existing")
					releaseWrite(t, filepath.Join(dir, "install", "eulix.exe"), "existing")
					releaseWrite(t, filepath.Join(dir, "ui", "vizeulize.html"), "existing-ui")
					if err := os.MkdirAll(filepath.Join(dir, "temp"), 0o700); err != nil {
						t.Fatal(err)
					}
					scriptName := "install.sh"
					if shell == "pwsh" {
						scriptName = "install.ps1"
					}
					source, err := os.ReadFile(filepath.Join(releaseRoot(t), scriptName))
					if err != nil {
						t.Fatal(err)
					}
					script := strings.TrimPrefix(strings.ReplaceAll(string(source), "\r\n", "\n"), "\ufeff")
					var args []string
					if shell == "bash" {
						script = strings.TrimSuffix(strings.TrimSpace(script), `main "$@"`)
						// Existing installer uses /tmp; redirect its scratch paths into this test's cache scope.
						script = strings.ReplaceAll(script, "/tmp/", `${EULIX_TEST_ROOT}/temp/`)
						script += `
INSTALL_DIR="$EULIX_TEST_ROOT/install"
EULIX_BIN_DIR="$EULIX_TEST_ROOT/ui"
VIZEULIZE_HTML_PATH="$EULIX_BIN_DIR/vizeulize.html"
RELEASE_BASE="https://fixture.invalid"
GPU_VARIANT=amd
curl() {
 local output="" url=""
 while (( $# )); do
  case "$1" in
   -o) output="$2"; shift 2 ;;
   https:*) url="$1"; shift ;;
   *) shift ;;
  esac
 done
 cp "$EULIX_TEST_ROOT/fixtures/${url##*/}" "$output"
}
download_binary
`
						args = []string{filepath.Join(dir, "runner.sh")}
					} else {
						script = strings.TrimSuffix(strings.TrimSpace(script), "Main")
						script += `
$InstallDir = Join-Path $env:EULIX_TEST_ROOT 'install'
$EulixBinDir = Join-Path $env:EULIX_TEST_ROOT 'ui'
$vizeulizeHtmlPath = Join-Path $EulixBinDir 'vizeulize.html'
$ReleaseBase = 'https://fixture.invalid'
$GpuVariant = 'amd'
function Invoke-WebRequest {
 param([string]$Uri,[string]$OutFile,[switch]$UseBasicParsing)
 $name = ([Uri]$Uri).Segments[-1]
 Copy-Item -LiteralPath (Join-Path $env:EULIX_TEST_ROOT "fixtures/$name") -Destination $OutFile
}
Download-Binary
`
						args = []string{"-NoLogo", "-NoProfile", "-File", filepath.Join(dir, "runner.ps1")}
					}
					releaseWrite(t, args[len(args)-1], script)
					cmd := exec.Command(shell, args...)
					cmd.Env = append(os.Environ(), "EULIX_TEST_ROOT="+dir, "TEMP="+filepath.Join(dir, "temp"), "LOCALAPPDATA="+dir, "USERPROFILE="+dir)
					out, runErr := cmd.CombinedOutput()
					installed := filepath.Join(dir, "install", "eulix")
					if shell == "pwsh" {
						installed += ".exe"
					}
					contents, err := os.ReadFile(installed)
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "valid" {
						if runErr != nil || string(contents) != "binary" {
							t.Fatalf("valid install: err=%v output=%s", runErr, out)
						}
					} else {
						if runErr == nil || string(contents) != "existing" {
							t.Fatalf("invalid checksum installed binary: err=%v output=%s", runErr, out)
						}
						ui, err := os.ReadFile(filepath.Join(dir, "ui", "vizeulize.html"))
						if err != nil || string(ui) != "existing-ui" {
							t.Fatalf("invalid checksum changed UI: %s, %v", ui, err)
						}
					}
				})
			}
		})
	}
}

func TestMakePropagatesGoBuildFailure(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("requires bash")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("requires make")
	}
	dir := t.TempDir()
	releaseWrite(t, filepath.Join(dir, "bin", "go"), "#!/bin/sh\nexit 23\n")
	releaseWrite(t, filepath.Join(dir, "parser"), "fixture")
	makefile := "include " + filepath.Join(releaseRoot(t), "Makefile") + "\nregression:\n\t$(call BUILD_GO_VARIANT_CMD,linux,amd64,amd," + filepath.Join(dir, "parser") + ")\n"
	releaseWrite(t, filepath.Join(dir, "Makefile"), makefile)
	cmd := exec.Command("make", "-f", filepath.Join(dir, "Makefile"), "regression", "CLI_DIR="+dir, "BUILD_DIR="+dir)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("make swallowed go build failure: %s", out)
	}
}

func releaseStep(t *testing.T, workflow, step string) string {
	t.Helper()
	marker := "      - name: " + step + "\n"
	_, body, ok := strings.Cut(workflow, marker)
	if !ok {
		t.Fatalf("missing release step %s", step)
	}
	body, _, _ = strings.Cut(body, "\n      - ")
	_, run, ok := strings.Cut(body, "        run: |\n")
	if !ok {
		t.Fatalf("step %s has no script", step)
	}
	var lines []string
	for _, line := range strings.Split(run, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, strings.TrimPrefix(line, "          "))
	}
	return strings.Join(lines, "\n")
}

func TestReleaseStagesChecksummedInstallerAssets(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("requires bash")
	}
	root, dir := releaseRoot(t), t.TempDir()
	raw, err := os.ReadFile(filepath.Join(root, ".github/workflows/Release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	stage := releaseStep(t, workflow, "Stage web UI")
	releaseWrite(t, filepath.Join(dir, "release_assets", "eulix-linux-amd64-onnx-amd"), "binary")
	ui, err := os.ReadFile(filepath.Join(root, "vizEulize/vizeulize.html"))
	if err != nil {
		t.Fatal(err)
	}
	releaseWrite(t, filepath.Join(dir, "vizEulize/vizeulize.html"), string(ui))
	cmd := exec.Command("bash", "-euc", stage)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stage UI: %v %s", err, out)
	}
	cmd = exec.Command("bash", "-euc", releaseStep(t, workflow, "Checksums"))
	cmd.Dir = filepath.Join(dir, "release_assets")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checksum step: %v %s", err, out)
	}
	checksum, err := os.ReadFile(filepath.Join(cmd.Dir, "checksum.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(checksum), "vizeulize.html") || strings.Contains(string(checksum), "checksum.txt") {
		t.Fatalf("invalid release manifest: %s", checksum)
	}
	cmd = exec.Command("sha256sum", "-c", "checksum.txt")
	cmd.Dir = filepath.Join(dir, "release_assets")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release checksums: %v %s", err, out)
	}
}

func TestReleaseCheckoutPrecedesArtifactDownloads(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(releaseRoot(t), ".github/workflows/Release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_, job, ok := strings.Cut(string(raw), "name: Create Release")
	if !ok {
		t.Fatal("missing release job")
	}
	checkout := strings.Index(job, "uses: actions/checkout@v4")
	download := strings.Index(job, "uses: actions/download-artifact@v4")
	if checkout < 0 || download < 0 || checkout > download {
		t.Fatal("checkout must precede downloads: checkout clears a fresh job workspace")
	}
}
