// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package setup

import "os"

// restrictToOwner makes path readable and writable by its owner only.
func restrictToOwner(path string) error {
	return os.Chmod(path, 0o600)
}
