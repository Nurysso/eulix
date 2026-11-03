// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"os"
	"path/filepath"
	"testing"

	"eulix/internal/config"
	"eulix/internal/utils"

	bolt "go.etcd.io/bbolt"
)

func open(t *testing.T, path string) *Manager {
	t.Helper()
	cfg := &config.Config{Cache: config.CacheConfig{Enable: true, Path: path}}
	m, err := CacheController(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestDisabledReturnsNil(t *testing.T) {
	m, err := CacheController(&config.Config{})
	if m != nil || err != nil {
		t.Fatalf("got %v, %v; want nil, nil when history is disabled", m, err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close on a nil Manager: %v", err)
	}
}

func TestDefaultPathIsUnderEulixDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir(utils.EulixDir, 0o755); err != nil {
		t.Fatal(err)
	}
	open(t, "")
	if _, err := os.Stat(filepath.Join(dir, utils.EulixDir, "history.db")); err != nil {
		t.Fatalf("default history file not created: %v", err)
	}
}

func TestOpenErrors(t *testing.T) {
	cfg := &config.Config{Cache: config.CacheConfig{Enable: true, Path: filepath.Join(t.TempDir(), "missing", "h.db")}}
	if m, err := CacheController(cfg); err == nil {
		_ = m.Close()
		t.Fatal("missing parent directory: want error")
	}
}

// A second process (another 'eulix chat') must time out on the file lock, not hang.
func TestSecondOpenTimesOutOnLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.db")
	open(t, path)
	cfg := &config.Config{Cache: config.CacheConfig{Enable: true, Path: path}}
	if m, err := CacheController(cfg); err == nil {
		_ = m.Close()
		t.Fatal("second open of a locked history file: want timeout error")
	}
}

func TestSaveGetListDelete(t *testing.T) {
	m := open(t, filepath.Join(t.TempDir(), "h.db"))

	first, err := m.Save("q1", "", "a1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Save("q2", "why", "a2")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == 0 || second.ID <= first.ID || first.CreatedAt.IsZero() {
		t.Fatalf("ids/timestamps not assigned: %+v %+v", first, second)
	}

	got, ok, err := m.Get(second.ID)
	if err != nil || !ok || got.Query != "q2" || got.Reasoning != "why" || got.Answer != "a2" || !got.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("Get = %+v, %v, %v", got, ok, err)
	}
	if _, ok, err := m.Get(999); ok || err != nil {
		t.Fatalf("Get(missing) = %v, %v; want not found, no error", ok, err)
	}

	all, err := m.ListAll()
	if err != nil || len(all) != 2 || all[0].ID != second.ID || all[1].ID != first.ID {
		t.Fatalf("ListAll = %+v, %v; want newest first", all, err)
	}

	if err := m.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(999); err != nil {
		t.Fatalf("deleting a missing id: %v", err)
	}
	if _, ok, _ := m.Get(first.ID); ok {
		t.Fatal("deleted entry still readable")
	}
}

func TestEntriesSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.db")
	m := open(t, path)
	e, err := m.Save("q", "r", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m = open(t, path)
	if got, ok, err := m.Get(e.ID); !ok || err != nil || got.Answer != "a" {
		t.Fatalf("after reopen: %+v, %v, %v", got, ok, err)
	}
	next, err := m.Save("q", "r", "a")
	if err != nil || next.ID <= e.ID {
		t.Fatalf("ids must keep increasing across reopen: %d then %d (%v)", e.ID, next.ID, err)
	}
}

func TestClear(t *testing.T) {
	m := open(t, filepath.Join(t.TempDir(), "h.db"))
	for range 3 {
		if _, err := m.Save("q", "", "a"); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Clear(); err != nil {
		t.Fatal(err)
	}
	if all, err := m.ListAll(); err != nil || len(all) != 0 {
		t.Fatalf("after Clear: %d entries, %v", len(all), err)
	}
	if _, err := m.Save("q", "", "a"); err != nil {
		t.Fatalf("Save after Clear: %v", err)
	}
}

func TestCorruptEntryIsAnError(t *testing.T) {
	m := open(t, filepath.Join(t.TempDir(), "h.db"))
	if err := m.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(entriesBucket).Put(idKey(7), []byte("{not json"))
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Get(7); err == nil {
		t.Error("Get of a corrupt entry: want error")
	}
	if _, err := m.ListAll(); err == nil {
		t.Error("ListAll with a corrupt entry: want error")
	}
}

func TestFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.db")
	open(t, path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Queries and answers can quote private source code.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("history file mode %o, want no group/other access", perm)
	}
}
