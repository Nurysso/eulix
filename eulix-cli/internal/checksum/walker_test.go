package checksum

import "testing"

// BUG 1: "**" globstar is not understood by filepath.Match. Because
// shouldIgnore() discards the ErrBadPattern, "**/node_modules/" matches
// *nothing at all*, and "**/*.log" only matches exactly one path element.
func TestIgnoreMatcher_GlobstarBroken(t *testing.T) {
	t.Run("trailing-slash globstar matches nothing", func(t *testing.T) {
		d := &Detector{projectPath: "/repo", ignorePatterns: []string{"**/node_modules/"}}
		// gitignore semantics: "**/node_modules/" matches the dir at any depth.
		for _, p := range []string{
			"/repo/node_modules/x.js",     // zero levels
			"/repo/a/node_modules/x.js",   // one level
			"/repo/a/b/node_modules/x.js", // two levels
		} {
			if !d.shouldIgnore(p) {
				t.Errorf("BUG: %q not ignored by \"**/node_modules/\" (globstar unsupported)", p)
			}
		}
	})

	t.Run("glob file pattern only matches one level", func(t *testing.T) {
		d := &Detector{projectPath: "/repo", ignorePatterns: []string{"**/*.log"}}
		cases := []struct {
			path string
			want bool
		}{
			{"/repo/build.log", true}, // 0 levels: BUG (missed)
			{"/repo/a/c.log", true},   // 1 level:  works by accident
			{"/repo/a/b/c.log", true}, // 2 levels: BUG (missed)
		}
		for _, c := range cases {
			if got := d.shouldIgnore(c.path); got != c.want {
				t.Errorf("BUG: shouldIgnore(%q) = %v, want %v (\"**\" is not globstar)", c.path, got, c.want)
			}
		}
	})
}

// BUG 2: gitignore-style anchored patterns with a leading "/" match nothing,
// because the "/" is kept in the pattern and then tested against a single
// path component with filepath.Match.
func TestIgnoreMatcher_LeadingSlashMatchesNothing(t *testing.T) {
	d := &Detector{projectPath: "/repo", ignorePatterns: []string{"/build/"}}
	// "/build/" should ignore /repo/build/... (top level) only.
	if !d.shouldIgnore("/repo/build/out.o") {
		t.Errorf("BUG: \"/build/\" failed to ignore top-level build/ — leading slash leaks into pattern")
	}
	// And per gitignore it should NOT ignore a nested x/build.
	if d.shouldIgnore("/repo/x/build/out.o") {
		t.Errorf("BUG: \"/build/\" ignored nested x/build/ — anchoring not implemented")
	}
}

// BUG 3: when root passed to the walker is outside projectPath, filepath.Rel
// yields a leading ".." component. Dotfile-style patterns like ".*" then match
// that synthetic "..", silently ignoring *every* file.
func TestIgnoreMatcher_DotPatternMatchesDotDot(t *testing.T) {
	d := &Detector{projectPath: "/repo", ignorePatterns: []string{".*"}}
	// A regular, non-hidden file in a tree walked from outside projectPath.
	if d.shouldIgnore("/other/file.go") {
		t.Errorf("BUG: \".*\" matched the synthetic \"..\" from filepath.Rel; every file outside projectPath is ignored")
	}
	// Control: a real dotfile should still be ignored.
	if !d.shouldIgnore("/repo/.env") {
		t.Errorf("control: \".*\" should ignore /repo/.env")
	}
}
