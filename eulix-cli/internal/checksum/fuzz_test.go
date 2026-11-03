// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package checksum

import (
	"strings"
	"testing"
)

// .euignore files come from the repository, which may be untrusted: no
// pattern may panic the matcher, which runs inside the MCP server.
func FuzzIgnorePattern(f *testing.F) {
	for _, p := range []string{"**/x/**", "a/**/b", "[!a]*", "[", "\\", "**", "/", "x/", "[]]", "a\\"} {
		f.Add(p, "a/x/b/c.py")
	}
	f.Fuzz(func(t *testing.T, pattern, rel string) {
		d := &Detector{projectPath: "/repo", ignorePatterns: []string{pattern}}
		_ = d.shouldIgnore("/repo/" + strings.TrimPrefix(rel, "/"))
	})
}
