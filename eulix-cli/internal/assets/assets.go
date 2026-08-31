//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Package assets holds the binaries and scripts that ship inside the eulix
// executable.
//
// Directory layout expected at build time:
//
//	assets/
//	  bins/
//	    eulix_parser_darwin      ← macOS (Mach-O), via embed_darwin.go
//	    eulix_parser_linux       ← Linux (ELF), compiled in via embed_linux.go
//	    eulix_parser_windows.exe ← Windows PE, via embed_windows.go
//	    eulix-embed.zip          ← OS-agnostic zip of the eulix_embed Python scripts
//
// OS-specific parser binary (selectable via build tags) and
// multi-platform scripts inside a zip asset. Accepts an optional `-ldflags`
// hash at build time to catch pre-build supply chain tampering; relies on runtime checks otherwise.
//
// Runtime layout after extraction:
//
//	$HOME/.Eulix/
//	  bin/
//	    eulix_parser[.exe]   ← OS-specific, executable
//	  eulix_embed/
//	    main.py              ← contents of eulix-embed.zip, flattened by one level
//	    ...
//	  .venv/                 ← created by the user via `uv venv --python 3.11`
package assets

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"eulix/internal/utils"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// EmbedZip holds the eulix-embed.zip archive (the eulix_embed Python
// scripts). This file is OS-agnostic and always embedded.
//
//go:embed bins/eulix-embed.zip
var EmbedZip embed.FS

const (
	embedZipPath        = "bins/eulix-embed.zip"
	eulixDirName        = ".Eulix"
	parserSubDir        = "bin"
	scriptsSubDir       = "eulix_embed"
	venvSubDir          = ".venv"
	requiredPythonMajor = 3
	requiredPythonMinor = 11
	hashNameParser      = "eulix_parser"
	hashNameEmbedDir    = "eulix_embed"
)

// -ldflags "-X eulix/internal/assets.ParserHash=<sha256>" hash for verification of parser bin, skip if empty
var ParserHash string
var embed_requirements = "onnx-amd.txt"

var extractMu sync.Mutex
var ErrIntegrityMismatch = errors.New("asset integrity check failed")

type FileHash struct {
	Name   string
	SHA256 string
	Size   int64
}

var (
	hashMu    sync.Mutex
	hashCache []FileHash
)

type InitializedEulixGlobal struct {
	AppName     string    `json:"app_name"`
	Version     string    `json:"version"`
	InstalledAt time.Time `json:"installed_at"`
}

func writeHiddenFile(path string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(path, data, perm); err != nil {
		return err
	}
	// Apply OS-specific hidden flags
	return setHiddenAttribute(path)
}

// Hashes computes, validates, and caches SHA-256 integrity hashes for all embedded artifacts.
// It verifies the parser binary against build-time expectations, hashes the zip payload contents,
// and caches a sorted list to protect against tampered build binaries.
func Hashes() ([]FileHash, error) {
	hashMu.Lock()
	defer hashMu.Unlock()

	if hashCache != nil {
		return hashCache, nil
	}

	var out []FileHash

	parserBytes, _, err := parserBinaryBytes()
	if err != nil {
		return nil, fmt.Errorf("read embedded parser binary: %w", err)
	}
	parserFH := hashOf(hashNameParser, parserBytes)

	if ParserHash != "" && parserFH.SHA256 != ParserHash {
		return nil, fmt.Errorf(
			"%w: \ncompiled time issue occured: The eulix_parser binary does not match build manifest.\n"+
				"got sha256=%s\nwant sha256=%s\n"+
				"this build may have embedded a corrupted or tampered binary and should not be trusted",
			ErrIntegrityMismatch, parserFH.SHA256, ParserHash,
		)
	}
	out = append(out, parserFH)

	zipBytes, err := EmbedZip.ReadFile(embedZipPath)
	if err != nil {
		return nil, fmt.Errorf("read embedded zip %q: %w", embedZipPath, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("open embedded zip: %w", err)
	}

	strip := commonTopLevelDir(zr.File)

	var fileHashes []FileHash
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		relName := zf.Name
		if strip != "" {
			relName = strings.TrimPrefix(relName, strip+"/")
		}

		content, err := readZipEntry(zf)
		if err != nil {
			return nil, err
		}

		fileHashes = append(fileHashes, hashOf(relName, content))
	}
	sort.Slice(fileHashes, func(i, j int) bool { return fileHashes[i].Name < fileHashes[j].Name })

	out = append(out, combinedHash(hashNameEmbedDir, fileHashes))
	out = append(out, fileHashes...)

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	hashCache = out
	return out, nil
}

// readZipEntry opens, fully reads, and closes a zip entry, correctly
// propagating a close error if the read itself succeeded but Close failed.
func readZipEntry(zf *zip.File) (data []byte, err error) {
	rc, err := zf.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %q: %w", zf.Name, err)
	}
	defer func() {
		if cErr := rc.Close(); cErr != nil && err == nil {
			err = fmt.Errorf("close zip entry %q: %w", zf.Name, cErr)
		}
	}()

	data, err = io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read zip entry %q: %w", zf.Name, err)
	}
	return data, nil
}

// parserHash return just the "eulix_parser" Hash
func parserHash() (FileHash, error) {
	all, err := Hashes()
	if err != nil {
		return FileHash{}, err
	}
	for _, h := range all {
		if h.Name == hashNameParser {
			return h, nil
		}
	}
	return FileHash{}, fmt.Errorf("internal error: %q hash missing", hashNameParser)
}

func hashOf(name string, content []byte) FileHash {
	sum := sha256.Sum256(content)
	return FileHash{
		Name:   name,
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(content)),
	}
}

// combinedHash produces a single deterministic FileHash over a sorted list
// of file hashes, by hashing "name:sha256\n" for each entry in order. This
// changes if and only if any contained file's content, name, or the set of
// files changes.
func combinedHash(name string, parts []FileHash) FileHash {
	var buf bytes.Buffer
	var total int64
	for _, p := range parts {
		fmt.Fprintf(&buf, "%s:%s\n", p.Name, p.SHA256)
		total += p.Size
	}
	sum := sha256.Sum256(buf.Bytes())
	return FileHash{
		Name:   name,
		SHA256: hex.EncodeToString(sum[:]),
		Size:   total,
	}
}

// eulixRoot returns $HOME/.Eulix without creating or checking it.
func EulixRoot() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}
	return filepath.Join(homeDir, eulixDirName), nil
}

// ExtractAll unpacks embedded binaries and scripts to $HOME/.Eulix idempotently.
// Uses fast size-only checks to skip existing files—does NOT guarantee integrity against local tampering.
func ExtractAll() (string, error) {
	extractMu.Lock()
	defer extractMu.Unlock()

	root, err := EulixRoot()
	if err != nil {
		return "", err
	}

	parserDir := filepath.Join(root, parserSubDir)
	scriptsDir := filepath.Join(root, scriptsSubDir)

	if err := os.MkdirAll(parserDir, 0755); err != nil {
		return "", fmt.Errorf("create parser dir: %w", err)
	}
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return "", fmt.Errorf("create scripts dir: %w", err)
	}

	if err := extractParser(parserDir); err != nil {
		return "", err
	}
	if err := extractEmbedZip(scriptsDir); err != nil {
		return "", err
	}

	return root, nil
}

// ParserPath returns the on-disk path of the extracted eulix_parser binary,
// extracting it first if necessary.
func ParserPath() (string, error) {
	root, err := ExtractAll()
	if err != nil {
		return "", err
	}
	_, name, err := parserBinaryBytes()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, parserSubDir, name), nil
}

// EmbedScriptsDir returns the on-disk directory that the eulix_embed Python
// scripts were extracted into, extracting them first if necessary.
func EmbedScriptsDir() (string, error) {
	root, err := ExtractAll()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, scriptsSubDir), nil
}

// extractParser writes the embedded, OS-specific parser binary into dir,
// making it executable. It skips rewriting if a same-size file already
// exists (cheap "already extracted" check; see ExtractAll's note on why
// this is not an integrity check).
func extractParser(dir string) error {
	content, name, err := parserBinaryBytes()
	if err != nil {
		return fmt.Errorf("read embedded parser binary: %w", err)
	}

	destPath := filepath.Join(dir, name)
	if info, err := os.Stat(destPath); err == nil && info.Size() == int64(len(content)) {
		return nil // already extracted, skip
	}

	if err := os.WriteFile(destPath, content, 0755); err != nil {
		return fmt.Errorf("write extracted parser binary %q: %w", destPath, err)
	}
	return nil
}

// extractEmbedZip unpacks every file in the embedded eulix-embed.zip
// archive directly into dir as eulix_embed's contents. If every entry in the
// archive shares one common top-level directory.
func extractEmbedZip(dir string) error {
	zipBytes, err := EmbedZip.ReadFile(embedZipPath)
	if err != nil {
		return fmt.Errorf("read embedded zip %q: %w", embedZipPath, err)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return fmt.Errorf("open embedded zip: %w", err)
	}

	strip := commonTopLevelDir(zr.File)

	for _, zf := range zr.File {
		relName := zf.Name
		if strip != "" {
			relName = strings.TrimPrefix(relName, strip+"/")
			if relName == "" {
				continue // the wrapper directory entry itself
			}
		}

		destPath := filepath.Join(dir, filepath.FromSlash(relName))

		// Guard against zip-slip: ensure destPath stays within dir.
		if !isWithinDir(dir, destPath) {
			return fmt.Errorf("refusing to extract entry outside target dir: %q", zf.Name)
		}

		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return fmt.Errorf("create dir %q: %w", destPath, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("create parent dir for %q: %w", destPath, err)
		}

		if info, err := os.Stat(destPath); err == nil && info.Size() == int64(zf.UncompressedSize64) {
			continue // already extracted, skip
		}

		content, err := readZipEntry(zf)
		if err != nil {
			return err
		}

		mode := os.FileMode(0644)
		if zf.Mode()&0111 != 0 {
			mode = 0755 // preserve executable bit for scripts that need it
		}
		if err := os.WriteFile(destPath, content, mode); err != nil {
			return fmt.Errorf("write extracted file %q: %w", destPath, err)
		}
	}

	return nil
}

// commonTopLevelDir returns the single shared top-level directory name that
// every entry in files is nested under, or "" if there isn't exactly one
func commonTopLevelDir(files []*zip.File) string {
	top := ""
	for _, zf := range files {
		clean := path.Clean(zf.Name)
		parts := strings.SplitN(clean, "/", 2)

		if len(parts) < 2 {
			// Name has no "/": either the wrapper directory's own zip entry
			// (e.g. "eulix_embed/", which path.Clean reduces to
			// "eulix_embed") or a genuine file sitting at the archive root.
			// Only the former is compatible with a single common wrapper; a
			// root-level file means there is no common wrapper to strip.
			if !zf.FileInfo().IsDir() {
				return ""
			}
			if top == "" {
				top = parts[0]
			} else if top != parts[0] {
				return ""
			}
			continue
		}

		if top == "" {
			top = parts[0]
		} else if top != parts[0] {
			return ""
		}
	}
	return top
}

// isWithinDir reports whether target is dir or a descendant of dir.
func isWithinDir(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return rel != ".." && !hasDotDotPrefix(rel)
}

func hasDotDotPrefix(rel string) bool {
	return len(rel) >= 2 && rel[0] == '.' && rel[1] == '.' &&
		(len(rel) == 2 || os.IsPathSeparator(rel[2]))
}

// VerifyOrExtract ensures binary and script assets exist under root and verifies their hashes.
// eulix_parser binary hash mismatch = fatal; script changes = ok
func VerifyOrExtract(root string) error {
	parserDir := filepath.Join(root, parserSubDir)
	scriptsDir := filepath.Join(root, scriptsSubDir)

	_, parserName, err := parserBinaryBytes()
	if err != nil {
		return fmt.Errorf("read embedded parser binary: %w", err)
	}
	parserPath := filepath.Join(parserDir, parserName)

	parserMissing := !fileExists(parserPath)
	scriptsMissing := !dirHasEntries(scriptsDir)

	if parserMissing || scriptsMissing {
		if _, err := ExtractAll(); err != nil {
			return fmt.Errorf("extract embedded assets: %w", err)
		}
	}

	// Verify parser binary, fail if modified
	wantParser, err := parserHash()
	if err != nil {
		return err
	}

	gotParser, err := sha256File(parserPath)
	if err != nil {
		return fmt.Errorf("hash %q: %w", parserPath, err)
	}

	if gotParser != wantParser.SHA256 {
		// Print clear error with instructions
		fmt.Fprintf(os.Stderr, "\n"+
			"╔══════════════════════════════════════════════════════════════════════════════╗\n"+
			"║               OOPS    Eulix_parser BINARY INTEGRITY ERROR                    ║\n"+
			"╚══════════════════════════════════════════════════════════════════════════════╝\n\n"+
			"The eulix_parser binary has been modified or corrupted.\n\n"+
			"  File:     %[1]s\n"+
			"  Expected: %[2]s\n"+
			"  Got:      %[3]s\n\n"+
			"To fix this issue:\n\n"+
			"  1. Delete the corrupted binary:\n"+
			"     rm %[1]s\n\n"+
			"  2. Run eulix again - it will automatically extract a fresh copy:\n"+
			"     eulix analyze\n\n",
			parserPath, wantParser.SHA256, gotParser,
		)

		// Exit with error code
		os.Exit(1)
	}

	return nil
}

// sha256File hashes the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirHasEntries(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// CheckUv confirms the `uv` tool is installed and on PATH.
func CheckUv() error {
	if _, err := exec.LookPath("uv"); err != nil {
		return fmt.Errorf(
			"`uv` is required but was not found on PATH. Install it from https://docs.astral.sh/uv/ and try again",
		)
	}
	return nil
}

// CheckVenv confirms $HOME/.Eulix/.venv exists and reports Python 3.11.
func CheckVenv(root string) error {
	venvPath := filepath.Join(root, venvSubDir)

	info, err := os.Stat(venvPath)
	if err != nil || !info.IsDir() {
		return fmt.Errorf(
			"no virtual environment found at %[1]v. Run `uv venv --python %[2]v.%[3]v %[1]v` in that location and try again",
			venvPath, requiredPythonMajor, requiredPythonMinor,
		)
	}

	version, err := venvPythonVersion(venvPath)
	if err != nil {
		return fmt.Errorf("could not determine Python version in %q: %w", venvPath, err)
	}

	wantPrefix := fmt.Sprintf("%d.%d", requiredPythonMajor, requiredPythonMinor)
	if !strings.HasPrefix(version, wantPrefix) {
		return fmt.Errorf(
			"virtual environment at %q uses Python %s, but Python %s is required. "+
				"Recreate it with `uv venv --python %s`",
			venvPath, version, wantPrefix, wantPrefix,
		)
	}

	return nil
}

// venvPythonVersion runs the venv's own python to ask its version
func venvPythonVersion(venvPath string) (string, error) {
	pythonPath := filepath.Join(venvPath, "bin", "python")
	if runtime.GOOS == "windows" {
		pythonPath = filepath.Join(venvPath, "Scripts", "python.exe")
	}

	if !fileExists(pythonPath) {
		return "", fmt.Errorf("no python executable found at %q", pythonPath)
	}

	out, err := exec.Command(pythonPath, "-c", "import sys; print('%d.%d.%d' % sys.version_info[:3])").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// PrintReqFileCmd prints the base and embed-specific pip requirements to stdout
func PrintReqFileCmd() (string, error) {
	root, err := EulixRoot()
	if err != nil {
		return "", fmt.Errorf("eulix: %w", err)
	}

	basePath := filepath.Join(root, scriptsSubDir, "requiremenents", "requirements.base.txt")
	reqPath := filepath.Join(root, scriptsSubDir, "requiremenents", embed_requirements)

	baseData, err := os.ReadFile(basePath)
	if err != nil {
		return "", fmt.Errorf("read base requirements %q: %w", basePath, err)
	}
	fmt.Println(string(baseData))

	reqData, err := os.ReadFile(reqPath)
	if err != nil {
		return "", fmt.Errorf("read embed requirements %q: %w", reqPath, err)
	}
	lines := strings.Split(string(reqData), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "-r") {
			fmt.Println(trimmed)
		}
	}

	return embed_requirements, nil
}

// InstallEmbedDeps runs `uv pip install`, which uses uv to install the
// eulix_embed scripts' dependencies into the venv.
func InstallEmbedDeps(root string) error {
	venvPath := filepath.Join(root, venvSubDir)

	reqFilePath := filepath.Join(root, scriptsSubDir, "requiremenents", embed_requirements)

	cmdArgs := []string{
		"pip", "install",
		"--python", venvPath,
		"-r", reqFilePath,
	}
	cmd := exec.Command("uv", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install deps try to install manually: %w", err)
	}
	fmt.Println("\n\n You can also switch to PYtorch if you want.")
	return nil
}

func IsInitialized() bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("warning: failed to get user home dir: %v", err)
		return false
	}

	initializedFile := filepath.Join(homeDir, ".Eulix", ".initialized")

	_, err = os.Stat(initializedFile)
	if err == nil {
		return true // File exists, skip initialization
	}
	if errors.Is(err, os.ErrNotExist) {
		return false // File missing, proceed with initialization
	}

	log.Printf("warning: failed to check initialization status: %v", err)
	return false
}

// GlobalInitialized writes the .initialized file in $HOME/.Eulix. This is a
// hidden file that marks whether eulix was run before, and other metadata.
func GlobalInitialized(EulixRoot string) error {
	runAt := time.Now()
	config := InitializedEulixGlobal{
		AppName:     utils.AppName,
		Version:     utils.AppVersion,
		InstalledAt: runAt,
	}

	jsonData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode initialized config: %w", err)
	}

	filename := filepath.Join(EulixRoot, ".initialized")
	if err := writeHiddenFile(filename, jsonData, 0644); err != nil {
		return fmt.Errorf("write initialized file %q: %w", filename, err)
	}

	return nil
}
