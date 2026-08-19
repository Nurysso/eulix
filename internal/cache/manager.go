//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package cache provides the local history store implementation for eulix.

/*
This file no longer implements a "return the same answer for the same
query" cache. It is a plain history log: every query the user asks, and
the reasoning/answer the model produced for it, gets appended here so it
can be listed and re-read later (e.g. the TUI's /history view and a
'show-reason' command in app.go). There is no TTL, no checksum-based
invalidation, and no cache-hit short-circuiting of the model call — that
whole layer has been removed on purpose.
*/

package cache

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"eulix/internal/config"

	bolt "go.etcd.io/bbolt"
)

// reasoningTagRe / answerTagRe pull the model's <reasoning>/<thinking> and
// <answer> blocks apart. Shared here so the app.go's show-reason
// command, and the TUI all agree on exactly the same parsing.
var (
	reasoningTagRe = regexp.MustCompile(`(?is)<\s*(?:reasoning|thinking)\s*>(.*?)<\s*/\s*(?:reasoning|thinking)\s*>`)
	answerTagRe    = regexp.MustCompile(`(?is)<\s*answer\s*>(.*?)<\s*/\s*answer\s*>`)
)

// entriesBucket is the single bucket every record lives in, keyed by an
// 8-byte big-endian auto-increment ID so iteration comes back in
// insertion order for free.
var entriesBucket = []byte("entries")

// Manager is a thin wrapper around a local BoltDB file. It only knows how
// to append entries and read them back — it is intentionally not a cache
// in the "avoid recomputation" sense anymore.
type Manager struct {
	db *bolt.DB
}

// CacheEntry is one recorded query turn. Reasoning and Answer are stored
// as separate fields (rather than one combined Response blob) so callers
// — the TUI's /think toggle, and app.go's show-reason command — can
// choose to display the reasoning trace independently of the answer.
type CacheEntry struct {
	ID        uint64    `json:"id"`
	Query     string    `json:"query"`
	Reasoning string    `json:"reasoning"`
	Answer    string    `json:"answer"`
	CreatedAt time.Time `json:"created_at"`
}

// SplitReasoningAndAnswer pulls a <reasoning>/<thinking> block and an
// <answer> block out of a raw model response. If there's no <answer> tag
// at all, the whole response (minus any reasoning block) is treated as
// the answer, so untagged responses still split cleanly.
func SplitReasoningAndAnswer(raw string) (reasoning, answer string) {
	var reasoningParts []string
	for _, m := range reasoningTagRe.FindAllStringSubmatch(raw, -1) {
		if trimmed := strings.TrimSpace(m[1]); trimmed != "" {
			reasoningParts = append(reasoningParts, trimmed)
		}
	}
	reasoning = strings.Join(reasoningParts, "\n\n")

	if m := answerTagRe.FindStringSubmatch(raw); m != nil {
		answer = strings.TrimSpace(m[1])
		return reasoning, answer
	}

	answer = strings.TrimSpace(reasoningTagRe.ReplaceAllString(raw, ""))
	return reasoning, answer
}

// CacheController opens (creating if needed) the local BoltDB history
// file. If history is disabled in config it returns (nil, nil) — callers
// already treat a nil *Manager as "history not enabled" (see the TUI's
// /history command).
func CacheController(cfg *config.Config) (*Manager, error) {
	if !cfg.Cache.Enable {
		return nil, nil
	}

	path := cfg.Cache.Path
	if path == "" {
		path = ".eulix/history.db"
	}

	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open history database: %w", err)
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(entriesBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to initialize history bucket: %w", err)
	}

	return &Manager{db: db}, nil
}

// Save appends a new entry and returns it with its assigned ID and
// timestamp filled in. Both reasoning and answer are stored as given —
// pass an empty string for reasoning when a response had none.
func (m *Manager) Save(query, reasoning, answer string) (CacheEntry, error) {
	entry := CacheEntry{
		Query:     query,
		Reasoning: reasoning,
		Answer:    answer,
		CreatedAt: time.Now(),
	}

	err := m.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)

		id, err := b.NextSequence()
		if err != nil {
			return err
		}
		entry.ID = id

		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}

		return b.Put(idKey(id), data)
	})
	if err != nil {
		return CacheEntry{}, fmt.Errorf("failed to save history entry: %w", err)
	}

	return entry, nil
}

// Get returns a single entry by ID.
func (m *Manager) Get(id uint64) (CacheEntry, bool, error) {
	var entry CacheEntry
	found := false

	err := m.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(entriesBucket).Get(idKey(id))
		if data == nil {
			return nil
		}
		found = true
		return json.Unmarshal(data, &entry)
	})
	if err != nil {
		return CacheEntry{}, false, fmt.Errorf("failed to read history entry: %w", err)
	}

	return entry, found, nil
}

// ListAll returns every stored entry, newest first.
func (m *Manager) ListAll() ([]CacheEntry, error) {
	var entries []CacheEntry

	err := m.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(entriesBucket).ForEach(func(_, data []byte) error {
			var entry CacheEntry
			if err := json.Unmarshal(data, &entry); err != nil {
				return err
			}
			entries = append(entries, entry)
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list history entries: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})

	return entries, nil
}

// Delete removes a single entry by ID.
func (m *Manager) Delete(id uint64) error {
	err := m.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(entriesBucket).Delete(idKey(id))
	})
	if err != nil {
		return fmt.Errorf("failed to delete history entry: %w", err)
	}
	return nil
}

// Clear removes every stored entry.
func (m *Manager) Clear() error {
	err := m.db.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(entriesBucket); err != nil && err != bolt.ErrBucketNotFound {
			return err
		}
		_, err := tx.CreateBucket(entriesBucket)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to clear history: %w", err)
	}
	return nil
}

// Close closes the underlying database file.
func (m *Manager) Close() error {
	if m == nil || m.db == nil {
		return nil
	}
	return m.db.Close()
}

func idKey(id uint64) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, id)
	return key
}
