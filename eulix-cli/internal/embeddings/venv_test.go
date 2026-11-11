// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package embeddings

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPrependPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	cases := []struct {
		name    string
		env     []string
		windows bool
		want    []string
	}{
		{"unix PATH", []string{"HOME=/h", "PATH=/usr/bin"}, false, []string{"HOME=/h", "PATH=/venv" + sep + "/usr/bin"}},
		{"unix is case-sensitive", []string{"Path=/x"}, false, []string{"Path=/x", "PATH=/venv"}},
		{"windows Path keeps the system entries", []string{"Path=C:\\Windows"}, true, []string{"Path=/venv" + sep + "C:\\Windows"}},
		{"no PATH at all", []string{"HOME=/h"}, true, []string{"HOME=/h", "PATH=/venv"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prependPath(tc.env, "/venv", tc.windows); !slices.Equal(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetVenvPython(t *testing.T) {
	fakeInstall(t) // venv python3 reports 3.11
	venv := filepath.Join(os.Getenv("HOME"), ".Eulix", ".venv")
	python, env, err := GetVenvPython(venv)
	if err != nil || python != filepath.Join(venv, "bin", "python3") {
		t.Fatalf("GetVenvPython = (%q, %v)", python, err)
	}
	if i := slices.IndexFunc(env, func(e string) bool { return strings.HasPrefix(e, "PATH=") }); i < 0 ||
		!strings.HasPrefix(env[i], "PATH="+filepath.Join(venv, "bin")+string(os.PathListSeparator)) {
		t.Fatalf("venv bin dir not first on PATH: %q", env)
	}

	if _, _, err := GetVenvPython(t.TempDir()); err == nil || !strings.Contains(err.Error(), "tried:") {
		t.Fatalf("missing interpreter: want an error listing the paths tried, got %v", err)
	}

	// install.sh builds the venv with 3.11; anything else is refused.
	other := t.TempDir()
	writeExec(t, filepath.Join(other, "bin", "python3"), `echo "Python 3.12.1"`)
	if _, _, err := GetVenvPython(other); err == nil || !strings.Contains(err.Error(), "3.12.1") {
		t.Fatalf("want an unsupported-version error, got %v", err)
	}
}

func TestFindEulixEmbedMissingScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads this on Windows
	if _, _, _, err := FindEulixEmbed(); err == nil || !strings.Contains(err.Error(), filepath.Join(home, ".Eulix", "eulix_embed", "main.py")) {
		t.Fatalf("want an error naming the expected script, got %v", err)
	}
}
