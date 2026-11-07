// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joho/godotenv"
)

func saveKey(t *testing.T, path, key, value string) {
	t.Helper()
	env := LoadEnvFile(path)
	env.Set(key, value)
	if err := env.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestSaveNewEnvIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	saveKey(t, path, "API_KEY", "secret")
	assertOwnerOnly(t, path)
}

func TestSaveTightensExistingEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("OLD=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	saveKey(t, path, "API_KEY", "secret")
	assertOwnerOnly(t, path)
}

func TestSavedEnvStaysUsableByOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	saveKey(t, path, "API_KEY", "first")
	saveKey(t, path, "API_KEY", "second") // rewrite through the restricted file
	assertOwnerOnly(t, path)

	got, err := godotenv.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["API_KEY"] != "second" {
		t.Fatalf("API_KEY = %q, want second", got["API_KEY"])
	}
	if n := strings.Count(readFile(t, path), "API_KEY="); n != 1 {
		t.Fatalf("API_KEY appears %d times, want 1", n)
	}
}

// config reads .env with godotenv, so a key must survive that exact parser.
func TestSecretsRoundTripThroughGodotenv(t *testing.T) {
	values := map[string]string{
		"plain":  "sk-abc123_DEF",
		"equals": "abc=def==",
		"hash":   "abc#def",
		"dollar": "abc$def",
		"quote":  `abc'def"ghi`,
		"space":  "abc def",
		"hashsp": "abc #def",
		"padded": " abc ",
	}
	for name, value := range values {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			saveKey(t, path, "API_KEY", value)
			got, err := godotenv.Read(path)
			if err != nil {
				t.Fatal(err)
			}
			if got["API_KEY"] != value {
				t.Fatalf("API_KEY = %q, want %q", got["API_KEY"], value)
			}
			if reloaded := LoadEnvFile(path); reloaded.values["API_KEY"] != value {
				t.Fatalf("LoadEnvFile read %q, want %q", reloaded.values["API_KEY"], value)
			}
		})
	}
}

// godotenv treats a leading quote as an opening quote, so writing such a value
// unquoted makes the whole file unparsable and config silently loses every key.
// Each value must either round-trip or be rejected without touching the file.
func TestSaveHandlesLeadingQuote(t *testing.T) {
	for name, value := range map[string]string{
		"double":      `"abc`,
		"double-both": `"abc"`,
		"single":      `'abc`,
		"single-both": `'abc'`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			const original = "OTHER=keep\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			env := LoadEnvFile(path)
			env.Set("API_KEY", value)
			if err := env.Save(); err != nil {
				if got := readFile(t, path); got != original {
					t.Fatalf("rejected value still changed the file to %q", got)
				}
				return
			}
			got, err := godotenv.Read(path)
			if err != nil {
				t.Fatalf("saved .env no longer parses: %v", err)
			}
			if got["OTHER"] != "keep" {
				t.Fatalf("OTHER = %q, want %q", got["OTHER"], "keep")
			}
			if got["API_KEY"] != value {
				t.Fatalf("API_KEY = %q, want %q", got["API_KEY"], value)
			}
		})
	}
}

// A rejected value must leave the file alone and stay out of the error text,
// which ends up on screen and in logs.
func TestSaveRejectsUnsafeValues(t *testing.T) {
	for name, value := range map[string]string{
		"newline":    "s3cret\nEVIL=1",
		"carriage":   "s3cret\rEVIL=1",
		"unquotable": "s3cret #'x",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			const original = "OTHER=keep\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			env := LoadEnvFile(path)
			env.Set("API_KEY", value)
			err := env.Save()
			if err == nil {
				t.Fatal("Save accepted an unsafe value")
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Fatalf("error leaks the value: %v", err)
			}
			if got := readFile(t, path); got != original {
				t.Fatalf("file changed to %q", got)
			}
		})
	}
}

func TestSaveKeepsCRLFFileIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("# note\r\nOTHER=keep\r\nAPI_KEY=old\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	saveKey(t, path, "API_KEY", "new")
	got, err := godotenv.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["OTHER"] != "keep" || got["API_KEY"] != "new" {
		t.Fatalf("saved values = %v", got)
	}
	if !strings.Contains(readFile(t, path), "# note") {
		t.Fatal("comment was lost")
	}
}

func TestSaveWithoutChangesLeavesNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := LoadEnvFile(path).Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Save with no changes created .env: %v", err)
	}
}

func TestSaveReportsUnwritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", ".env")
	env := LoadEnvFile(path)
	env.Set("API_KEY", "secret")
	if err := env.Save(); err == nil {
		t.Fatal("Save into a missing folder should fail")
	}
}
