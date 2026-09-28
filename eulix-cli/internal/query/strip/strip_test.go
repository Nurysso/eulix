package strip

import (
	"strings"
	"testing"
)

func lines(s string) []string { return strings.Split(s, "\n") }

// StripCommentsAndDocs — dispatch & top-level edge cases

func TestStripCommentsAndDocs_NilInput(t *testing.T) {
	if got := StripCommentsAndDocs(nil, "go"); got != nil {
		t.Fatalf("nil input should pass through, got %v", got)
	}
}

func TestStripCommentsAndDocs_EmptyInput(t *testing.T) {
	if got := StripCommentsAndDocs([]string{}, "go"); len(got) != 0 {
		t.Fatalf("empty input should pass through, got %v", got)
	}
}

func TestStripCommentsAndDocs_UnknownLanguageUntouched(t *testing.T) {
	in := []string{"# not a comment here", "still(); not(); touched()"}
	got := strings.Join(StripCommentsAndDocs(in, "haskell"), "\n")
	if got != strings.Join(in, "\n") {
		t.Fatalf("unknown language should be returned verbatim, got:\n%s", got)
	}
}

func TestStripCommentsAndDocs_AllDispatchedLanguages(t *testing.T) {
	// Each language gets a comment written with its own marker, so the
	// assertion is "this language's stripper ran", not "all languages
	// share C-style syntax".
	cases := []struct {
		lang string
		src  string
	}{
		{"go", "code() // trailing comment"},
		{"javascript", "code(); // trailing comment"},
		{"typescript", "code(); // trailing comment"},
		{"rust", "code(); // trailing comment"},
		{"c", "code(); // trailing comment"},
		{"cpp", "code(); // trailing comment"},
		{"java", "code(); // trailing comment"},
		{"python", "code()  # trailing comment"},
	}

	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			got := StripCommentsAndDocs([]string{tc.src}, tc.lang)
			if len(got) != 1 {
				t.Fatalf("lang %s: expected 1 cleaned line, got %v", tc.lang, got)
			}
			if strings.Contains(got[0], "trailing comment") {
				t.Fatalf("lang %s: comment survived: %q", tc.lang, got[0])
			}
			if !strings.Contains(got[0], "code(") {
				t.Fatalf("lang %s: real code dropped: %q", tc.lang, got[0])
			}
		})
	}
}

// Go — go/scanner based stripper

func TestGo_NoCommentsPresent(t *testing.T) {
	// Exercises the `len(ranges) == 0` early return.
	src := `package main

func main() {
	println("hi")
}`
	out := strings.Join(StripCommentsAndDocs(lines(src), "go"), "\n")
	if !strings.Contains(out, "println") {
		t.Fatalf("no-comment source was mangled:\n%s", out)
	}
}

func TestGo_URLInStringSurvives(t *testing.T) {
	src := `func f() {
	// real comment, should go
	u := "https://example.com/path" // trailing comment, should go
	fmt.Println(u)
}`
	out := strings.Join(StripCommentsAndDocs(lines(src), "go"), "\n")
	if !strings.Contains(out, `"https://example.com/path"`) {
		t.Fatalf("URL string was mangled:\n%s", out)
	}
	if strings.Contains(out, "real comment") || strings.Contains(out, "trailing comment") {
		t.Fatalf("comments were not stripped:\n%s", out)
	}
}

func TestGo_RawStringWithCommentMarkersSurvives(t *testing.T) {
	src := "s := `/* not a comment */ // also not a comment`"
	out := strings.Join(StripCommentsAndDocs(lines(src), "go"), "\n")
	if !strings.Contains(out, "/* not a comment */ // also not a comment") {
		t.Fatalf("raw string contents were mangled:\n%s", out)
	}
}

func TestGo_RuneLiteralSlashSurvives(t *testing.T) {
	src := "sep := '/' // path separator"
	out := strings.Join(StripCommentsAndDocs(lines(src), "go"), "\n")
	if !strings.Contains(out, "sep := '/'") {
		t.Fatalf("rune literal was mangled:\n%s", out)
	}
	if strings.Contains(out, "path separator") {
		t.Fatalf("trailing comment not stripped:\n%s", out)
	}
}

func TestGo_MultilineBlockCommentRemoved(t *testing.T) {
	src := `x := 1
/* this
spans
lines */
y := 2`
	out := strings.Join(StripCommentsAndDocs(lines(src), "go"), "\n")
	if strings.Contains(out, "spans") {
		t.Fatalf("block comment body leaked:\n%s", out)
	}
	if !strings.Contains(out, "x := 1") || !strings.Contains(out, "y := 2") {
		t.Fatalf("real code lines were dropped:\n%s", out)
	}
}

// C-style — JS/TS/Rust/C/C++/Java

func TestJS_URLInStringSurvives(t *testing.T) {
	src := `const u = "http://example.com/api"; // fetch it
fetch(u);`
	out := strings.Join(StripCommentsAndDocs(lines(src), "javascript"), "\n")
	if !strings.Contains(out, `"http://example.com/api"`) {
		t.Fatalf("URL string was mangled:\n%s", out)
	}
	if strings.Contains(out, "fetch it") {
		t.Fatalf("comment not stripped:\n%s", out)
	}
}

func TestJS_TemplateLiteralWithSlashesSurvives(t *testing.T) {
	src := "const s = `path is // not a comment here`;"
	out := strings.Join(StripCommentsAndDocs(lines(src), "javascript"), "\n")
	if !strings.Contains(out, "path is // not a comment here") {
		t.Fatalf("template literal contents mangled:\n%s", out)
	}
}

func TestC_SQLStringWithCommentMarkersSurvives(t *testing.T) {
	src := `const char *q = "SELECT * FROM t /* not a comment */ WHERE id = 1"; // real comment`
	out := strings.Join(StripCommentsAndDocs(lines(src), "c"), "\n")
	if !strings.Contains(out, `"SELECT * FROM t /* not a comment */ WHERE id = 1"`) {
		t.Fatalf("SQL string was mangled:\n%s", out)
	}
	if strings.Contains(out, "real comment") {
		t.Fatalf("trailing comment not stripped:\n%s", out)
	}
}

func TestC_EscapedQuoteInStringDoesNotEndItEarly(t *testing.T) {
	src := `char *s = "she said \"// not a comment\" to me"; // this one is real`
	out := strings.Join(StripCommentsAndDocs(lines(src), "c"), "\n")
	if !strings.Contains(out, `she said \"// not a comment\" to me`) {
		t.Fatalf("escaped-quote string was mangled:\n%s", out)
	}
	if strings.Contains(out, "this one is real") {
		t.Fatalf("trailing comment not stripped:\n%s", out)
	}
}

func TestC_SingleQuoteCharLiteral(t *testing.T) {
	// Exercises the '\'' start-of-string branch and confirms a slash inside
	// a char literal doesn't kick off a comment.
	src := `char sep = '/'; // not inside the literal`
	out := strings.Join(StripCommentsAndDocs(lines(src), "c"), "\n")
	if !strings.Contains(out, "'/'") {
		t.Fatalf("char literal mangled:\n%s", out)
	}
	if strings.Contains(out, "not inside") {
		t.Fatalf("comment not stripped:\n%s", out)
	}
}

func TestC_UnterminatedBlockComment(t *testing.T) {
	// The state machine simply runs to EOF while inBlockComment is true.
	src := `int x = 1;
/* never closed
still in comment`
	out := StripCommentsAndDocs(lines(src), "c")
	if len(out) != 1 || !strings.Contains(out[0], "int x = 1") {
		t.Fatalf("expected only code line to survive, got %v", out)
	}
}

func TestC_TrailingSlashAtEOF(t *testing.T) {
	// Hits both `c == '/' && i+1 < n && ...` checks with i+1 >= n.
	out := StripCommentsAndDocs([]string{"/"}, "c")
	if len(out) != 1 || out[0] != "/" {
		t.Fatalf("expected '/' to survive, got %v", out)
	}
}

func TestC_BackslashAtEOFInsideString(t *testing.T) {
	// Hits `c == '\\' && i+1 < n` with i+1 >= n while inside a string.
	out := StripCommentsAndDocs([]string{`"abc\`}, "c")
	if len(out) != 1 || out[0] != `"abc\` {
		t.Fatalf("expected raw string preserved, got %v", out)
	}
}

func TestC_StarAtEOFInsideBlockComment(t *testing.T) {
	// Hits `c == '*' && i+1 < n` with i+1 >= n while inside a block comment.
	out := StripCommentsAndDocs([]string{"/**"}, "c")
	if len(out) != 0 {
		t.Fatalf("expected fully-commented line to be dropped, got %v", out)
	}
}

// Python

func TestPython_HashInStringSurvives(t *testing.T) {
	src := `url = "https://example.com/page#section"  # real comment
print(url)`
	out := strings.Join(StripCommentsAndDocs(lines(src), "python"), "\n")
	if !strings.Contains(out, `"https://example.com/page#section"`) {
		t.Fatalf("string with '#' was mangled:\n%s", out)
	}
	if strings.Contains(out, "real comment") {
		t.Fatalf("comment not stripped:\n%s", out)
	}
}

func TestPython_SingleQuoteString(t *testing.T) {
	// Exercises the single-quote start path (Python uses both '' and "").
	src := `s = 'hello # world'  # real comment`
	out := strings.Join(StripCommentsAndDocs(lines(src), "python"), "\n")
	if !strings.Contains(out, "'hello # world'") {
		t.Fatalf("single-quote string mangled:\n%s", out)
	}
	if strings.Contains(out, "real comment") {
		t.Fatalf("comment not stripped:\n%s", out)
	}
}

func TestPython_EscapedQuoteInString(t *testing.T) {
	// Hits the escape branch (`c == '\\' && i+1 < n`) inside a Python string,
	// so a trailing \" doesn't end the string and swallow the '#'.
	src := `s = "she said \"#"  # real`
	out := strings.Join(StripCommentsAndDocs(lines(src), "python"), "\n")
	if !strings.Contains(out, `\"#`) {
		t.Fatalf("escaped-quote string mangled:\n%s", out)
	}
	if strings.Contains(out, "real") {
		t.Fatalf("comment not stripped:\n%s", out)
	}
}

func TestPython_TripleQuoteDocstringRemoved(t *testing.T) {
	src := `def f():
    """
    This is a docstring with a # that must not start a comment.
    """
    return 1`
	out := strings.Join(StripCommentsAndDocs(lines(src), "python"), "\n")
	if strings.Contains(out, "docstring") {
		t.Fatalf("docstring body leaked:\n%s", out)
	}
	if !strings.Contains(out, "def f():") || !strings.Contains(out, "return 1") {
		t.Fatalf("real code lines dropped:\n%s", out)
	}
}

func TestPython_TripleSingleQuoteDocstring(t *testing.T) {
	// Same as above but with '''…''' instead of """…""".
	src := `def f():
    '''
    docstring body
    '''
    return 1`
	out := strings.Join(StripCommentsAndDocs(lines(src), "python"), "\n")
	if strings.Contains(out, "docstring") {
		t.Fatalf("triple-single docstring leaked:\n%s", out)
	}
	if !strings.Contains(out, "return 1") {
		t.Fatalf("real code dropped:\n%s", out)
	}
}

func TestPython_HashPreservedInsideTripleQuoteBoundaryCheck(t *testing.T) {
	src := `# setup
color = "#ff00ff"  # a color, not code
print(color)`
	out := strings.Join(StripCommentsAndDocs(lines(src), "python"), "\n")
	if !strings.Contains(out, `"#ff00ff"`) {
		t.Fatalf("hex color string was mangled:\n%s", out)
	}
	if strings.Contains(out, "a color, not code") || strings.Contains(out, "# setup") {
		t.Fatalf("comments not stripped:\n%s", out)
	}
}

func TestPython_ShortQuoteRunAtEOF(t *testing.T) {
	// `i+2 < n` is false at the first quote, so the triple look-ahead
	// declines and we fall into plain single-string handling.
	src := `x = ""`
	out := StripCommentsAndDocs([]string{src}, "python")
	if len(out) != 1 || out[0] != src {
		t.Fatalf("expected %q to pass through, got %v", src, out)
	}
}

func TestPython_TripleQuoteTerminatorAtEOF(t *testing.T) {
	// Inside a triple string, the terminator check's `i+2 < n` guard is
	// false at EOF, so the state machine stays inTriple to the end.
	src := `"""a""`
	out := StripCommentsAndDocs([]string{src}, "python")
	if len(out) != 0 {
		t.Fatalf("expected fully-commented line dropped, got %v", out)
	}
}

func TestPython_UnterminatedTripleQuote(t *testing.T) {
	src := `x = 1
"""
never terminated`
	out := StripCommentsAndDocs(lines(src), "python")
	if len(out) != 1 || !strings.Contains(out[0], "x = 1") {
		t.Fatalf("expected only 'x = 1' to survive, got %v", out)
	}
}

func TestBlankLinesPreserved(t *testing.T) {
	src := "a := 1\n\nb := 2"
	out := StripCommentsAndDocs(lines(src), "go")
	if len(out) != 3 || out[1] != "" {
		t.Fatalf("blank line handling changed: %#v", out)
	}
}

func TestCommentOnlyLineDropped(t *testing.T) {
	src := "a := 1\n// just a comment\nb := 2"
	out := StripCommentsAndDocs(lines(src), "go")
	if len(out) != 2 {
		t.Fatalf("expected comment-only line to be dropped, got: %#v", out)
	}
}
