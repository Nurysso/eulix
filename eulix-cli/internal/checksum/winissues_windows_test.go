// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//go:build winissues

package checksum

// Known Windows-only issues, see docs/windows-issues.md. Each test asserts the
// correct behavior, so it fails until the issue is fixed.
// Run with: go test -tags winissues ./internal/checksum

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// WIN-2 (checksum side): EvalSymlinks does not resolve directory junctions,
// and WalkDir does not enter a root that is a junction, so a project opened
// through a junction scans as empty.
func TestWinIssue_ScanThroughJunctionRootFindsNothing(t *testing.T) {
	real := t.TempDir()
	winWrite(t, filepath.Join(real, "main.py"))
	link := filepath.Join(t.TempDir(), "proj")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, real).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v %s", err, out)
	}

	if _, ok := winScan(t, link)["main.py"]; !ok {
		t.Error("scan through a junction to the project found no files")
	}
}
