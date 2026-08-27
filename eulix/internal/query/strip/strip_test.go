package strip

import (
	"strings"
	"testing"
)

func lines(s string) []string { return strings.Split(s, "\n") }

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

func TestPython_HashPreservedInsideTripleQuoteBoundaryCheck(t *testing.T) {
	// A '#' character sitting right inside a single/double-quoted string
	// that itself lives right after a real comment on the previous line.
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
