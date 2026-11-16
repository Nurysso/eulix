package checksum

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestIgnoreMatcherGitignoreSemantics(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"config/**/secrets.py", "config/secrets.py", true}, // zero directories
		{"config/**/secrets.py", "config/eu/prod/secrets.py", true},
		{"config/**/secrets.py", "config/topsecrets.py", false},
		{"**/private/**", "private/x.py", true},
		{"**/private/**", "deep/a/b/private/z.py", true},
		{"**/private/**", "deep/privateer/z.py", false},
		{"keys/**", "keys/sub/k2.py", true},
		{"src/*/generated/", "src/a/generated/deep/x.go", true}, // a glob can name a parent dir
		{"src/*.go", "src/a/b.go", false},                       // "*" never crosses "/"
		{"file?.txt", "dir/file1.txt", true},
		{"[abc].md", "notes/b.md", true},
		{"**/[!a]*.log", "x/b.log", true},
		{"**/[!a]*.log", "x/a.log", false},
		{"/anchored/", "sub/anchored/root.py", false}, // a leading slash anchors to the root
		{"logs/", "app/logs/l.py", true},              // dir-only without a slash matches at any depth
		{"*.pem", "keys/server.pem", true},
		{"!neg/keep.py", "neg/keep.py", false}, // negation is not supported: never matches
	}
	for _, c := range cases {
		d := &Detector{projectPath: "/repo", ignorePatterns: []string{c.pattern}}
		if got := d.shouldIgnore("/repo/" + c.path); got != c.want {
			t.Errorf("pattern %q, path %q: ignored=%v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestIgnoreFilesNestedLongLinesAndCRLF(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".euignore", "\ufeff"+strings.Repeat("x", 70_000)+"\r\n# note\r\nroot_secret.py\r\n")
	write("svc/pay/.euignore", "creds.py\n/local.py\n")

	ignored := IgnoreMatcher(root)
	for rel, want := range map[string]bool{
		"root_secret.py":        true,  // after a >64 KiB line, with CRLF endings
		"svc/pay/creds.py":      true,  // nested rules apply below their directory
		"svc/pay/deep/creds.py": true,  // ... at any depth
		"svc/pay/local.py":      true,  // anchored to the nested file's directory
		"svc/pay/deep/local.py": false, // ... and only there
		"svc/other/creds.py":    false, // not to siblings
		"creds.py":              false,
	} {
		if got := ignored(filepath.Join(root, filepath.FromSlash(rel))); got != want {
			t.Errorf("%s: ignored=%v, want %v", rel, got, want)
		}
	}
}

func TestServableMatcher(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".euignore"), []byte("secrets.py\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	servable := ServableMatcher(root)
	for rel, want := range map[string]bool{
		"app/billing.py":    true,
		"secrets.py":        false, // .euignore'd
		".env":              false, // hidden
		"app/.cache/x.py":   false, // hidden directory
		"../outside.py":     false,
		"app/../../out.py":  false,
		"/etc/passwd":       false,
		"":                  false,
		"node_modules/x.js": false, // built-in default
	} {
		if got := servable(rel); got != want {
			t.Errorf("servable(%q) = %v, want %v", rel, got, want)
		}
	}
}
