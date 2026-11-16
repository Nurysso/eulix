// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package checksum

import "testing"

// Runs on every OS, so the bit logic is covered even from Linux CI.
func TestAttrsHidden(t *testing.T) {
	tests := []struct {
		attrs uint32
		want  bool
	}{
		{0x0, false},
		{0x2, true},   // HIDDEN
		{0x22, true},  // HIDDEN | ARCHIVE
		{0x26, true},  // HIDDEN | SYSTEM | ARCHIVE
		{0x20, false}, // ARCHIVE only
		{0x10, false}, // DIRECTORY only
		{0x4, false},  // SYSTEM only
	}
	for _, tc := range tests {
		if got := attrsHidden(tc.attrs); got != tc.want {
			t.Errorf("attrsHidden(%#x) = %v, want %v", tc.attrs, got, tc.want)
		}
	}
}
