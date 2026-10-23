// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! Position arithmetic.
//!
//! LSP positions are (0-based line, 0-based UTF-16 code unit offset within
//! that line). Ropey uses (0-based line, char offset). Nothing else in the
//! server is allowed to do this conversion, because getting it subtly wrong
//! panics on any line containing a non-ASCII character.

use ropey::Rope;
use tower_lsp::lsp_types::{Position, Range};

// LSP Position to char index into `rope`.
//
// Returns None if the line doesn't exist. A `character` that lands inside a
// surrogate pair, or past the end of the line, clamps to the nearest char
// boundary rather than failing. Editors send those transiently while typing,
// and a clamped position is always better than a dropped request.
pub fn position_to_char(rope: &Rope, pos: Position) -> Option<usize> {
    let line = pos.line as usize;
    if line >= rope.len_lines() {
        return None;
    }
    let line_start = rope.line_to_char(line);
    let line_slice = rope.line(line);

    let mut utf16_seen: u32 = 0;
    let mut char_offset: usize = 0;
    for c in line_slice.chars() {
        if utf16_seen >= pos.character {
            break;
        }
        let width = c.len_utf16() as u32;
        // The requested offset points into the middle of this char's UTF-16
        // encoding (astral plane char), so stop at its start.
        if utf16_seen + width > pos.character {
            break;
        }
        utf16_seen += width;
        char_offset += 1;
    }
    Some(line_start + char_offset)
}

// char index to LSP Position.
pub fn char_to_position(rope: &Rope, char_idx: usize) -> Position {
    let char_idx = char_idx.min(rope.len_chars().saturating_sub(1).max(0));
    if rope.len_chars() == 0 {
        return Position::new(0, 0);
    }
    let line = rope.char_to_line(char_idx);
    let line_start = rope.line_to_char(line);
    let slice = rope.slice(line_start..char_idx);
    let character: usize = slice.chars().map(char::len_utf16).sum();
    Position::new(line as u32, character as u32)
}

// LSP Range to (start_char, end_char), or None if malformed.
pub fn range_to_chars(rope: &Rope, range: Range) -> Option<(usize, usize)> {
    let start = position_to_char(rope, range.start)?;
    let end = position_to_char(rope, range.end)?;
    if start > end {
        return None;
    }
    Some((start, end))
}

pub fn chars_to_range(rope: &Rope, start: usize, end: usize) -> Range {
    Range::new(char_to_position(rope, start), char_to_position(rope, end))
}

// Full extent of a 1-based line, clamped to the real line so the `character`
// never exceeds what the line actually holds.
pub fn full_line_range(rope: &Rope, line_1based: usize) -> Range {
    let line = line_1based.saturating_sub(1) as usize;
    if rope.len_chars() == 0 {
        return Range::new(Position::new(0, 0), Position::new(0, 0));
    }
    let line = line.min(rope.len_lines().saturating_sub(1));
    let start_char = rope.line_to_char(line);
    // `rope.line(n)` includes the trailing newline. Spanning it would make
    // the range end on the *next* line, which is what used to report
    // (line 1, char 0) for a range that should have ended on line 0.
    let mut end_char = start_char + rope.line(line).len_chars();
    if end_char > start_char && rope.char(end_char - 1) == '\n' {
        end_char -= 1;
        if end_char > start_char && rope.char(end_char - 1) == '\r' {
            end_char -= 1;
        }
    }
    chars_to_range(rope, start_char, end_char)
}

// Identifier extraction

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WordHit {
    pub text: String,
    // Range of the identifier itself, so callers can highlight or rename it
    // without recomputing the boundaries.
    pub range: Range,
}

fn is_ident_char(c: char) -> bool {
    // `is_alphanumeric` is Unicode aware, so identifiers outside ASCII work
    // the same way they do in the languages libeulix parses.
    c == '_' || c.is_alphanumeric()
}

pub fn word_at(rope: &Rope, pos: Position) -> Option<WordHit> {
    let idx = position_to_char(rope, pos)?;
    let len = rope.len_chars();
    if len == 0 {
        return None;
    }

    let mut start = idx.min(len);
    while start > 0 && is_ident_char(rope.char(start - 1)) {
        start -= 1;
    }
    let mut end = idx.min(len);
    while end < len && is_ident_char(rope.char(end)) {
        end += 1;
    }
    if start == end {
        return None;
    }

    Some(WordHit {
        text: rope.slice(start..end).to_string(),
        range: chars_to_range(rope, start, end),
    })
}

// Locate `name` on a 1-based line, matching on identifier boundaries.
//
// libeulix records line numbers but no columns, so anything that needs a
// column for a known name (semantic tokens, definition selection ranges) has
// to find it in the text. A bare `str::find` would match `run` inside
// `rerun`, so we check the boundaries on both sides.
pub fn find_name_on_line(rope: &Rope, line_1based: usize, name: &str) -> Option<Range> {
    if name.is_empty() {
        return None;
    }
    let line = line_1based.saturating_sub(1) as usize;
    if line >= rope.len_lines() {
        return None;
    }
    let line_start = rope.line_to_char(line);
    let line_slice = rope.line(line);
    let text = line_slice.to_string();

    let bytes = text.as_bytes();
    let needle = name.as_bytes();
    let mut from = 0usize;
    while from + needle.len() <= bytes.len() {
        let Some(rel) = text[from..].find(name) else {
            return None;
        };
        let start = from + rel;
        let end = start + needle.len();
        let before_ok = start == 0 || !is_ident_char(prev_char(&text, start));
        let after_ok = end == bytes.len() || !is_ident_char(next_char(&text, end));
        if before_ok && after_ok {
            return Some(chars_to_range(rope, line_start + start, line_start + end));
        }
        // Keep searching from one byte later so overlapping candidates are
        // still considered.
        from = start + 1;
    }
    None
}

fn prev_char(text: &str, byte_idx: usize) -> char {
    text[..byte_idx].chars().next_back().unwrap_or(' ')
}

fn next_char(text: &str, byte_idx: usize) -> char {
    text[byte_idx..].chars().next().unwrap_or(' ')
}

// Every occurrence of `name` on a 1-based line, on identifier boundaries.
// Rename needs all of them: `f(); f();` is two call sites on one line.
pub fn find_names_on_line(rope: &Rope, line_1based: usize, name: &str) -> Vec<Range> {
    let mut out = Vec::new();
    let line = line_1based.saturating_sub(1);
    if name.is_empty() || line >= rope.len_lines() {
        return out;
    }
    let line_start = rope.line_to_char(line);
    let text = rope.line(line).to_string();
    let mut from = 0usize;
    while let Some(rel) = text[from..].find(name) {
        let start = from + rel;
        let end = start + name.len();
        let before_ok = start == 0 || !is_ident_char(prev_char(&text, start));
        let after_ok = end == text.len() || !is_ident_char(next_char(&text, end));
        if before_ok && after_ok {
            let start_char = line_start + text[..start].chars().count();
            out.push(chars_to_range(
                rope,
                start_char,
                start_char + name.chars().count(),
            ));
            from = end;
        } else {
            from = start + text[start..].chars().next().map_or(1, char::len_utf8);
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn r(s: &str) -> Rope {
        Rope::from_str(s)
    }

    #[test]
    fn out_of_bounds_line_returns_none_instead_of_panicking() {
        // The regression that motivated this module: `>` instead of `>=` let
        // `line == len_lines()` reach `rope.line()`.
        let r = r("a\nb\n");
        assert!(position_to_char(&r, Position::new(99, 0)).is_none());
        assert!(position_to_char(&r, Position::new(r.len_lines() as u32, 0)).is_none());
    }

    #[test]
    fn utf16_offsets_are_not_byte_offsets() {
        // "é" is 2 bytes, 1 char, 1 UTF-16 unit. "𝄞" is 4 bytes, 1 char, 2 UTF-16 units.
        let r = r("let x = \"é𝄞\";\nlet y = 1;");
        let line0 = Rope::line(&r, 0).to_string();
        let char_idx_of_x = line0.find('x').unwrap();
        // UTF-16 offset of 'x' in `let x = "é𝄞";`
        let utf16_x = line0
            .chars()
            .take(line0[..char_idx_of_x].chars().count())
            .map(char::len_utf16)
            .sum::<usize>();
        let got = position_to_char(&r, Position::new(0, utf16_x as u32)).unwrap();
        assert_eq!(r.char(got), 'x');
    }

    #[test]
    fn astral_chars_count_as_two_utf16_units() {
        let r = r("𝄞ab");
        // After the 𝄞 (2 units) comes 'a'.
        assert_eq!(
            position_to_char(&r, Position::new(0, 2)).map(|i| r.char(i)),
            Some('a')
        );
        // A request landing inside the surrogate pair clamps to its start.
        assert_eq!(
            position_to_char(&r, Position::new(0, 1)).map(|i| r.char(i)),
            Some('𝄞')
        );
    }

    #[test]
    fn roundtrip_position_char_position() {
        let r = r("héllo\n𝄞 world\nz");
        // Every valid char index, not `=line.len_chars()`: one past the end
        // of the final line is out of range and is not a round-trip case.
        for abs in 0..r.len_chars() {
            let p = char_to_position(&r, abs);
            assert_eq!(
                position_to_char(&r, p),
                Some(abs),
                "roundtrip failed at {abs}"
            );
        }
    }

    #[test]
    fn word_at_cursor_and_one_past_cursor() {
        let r = r("foo(bar)\n");
        let inside = word_at(&r, Position::new(0, 1)).unwrap();
        assert_eq!(inside.text, "foo");
        let after = word_at(&r, Position::new(0, 3)).unwrap();
        assert_eq!(
            after.text, "foo",
            "cursor one past the end still resolves the word"
        );
        let on_bar = word_at(&r, Position::new(0, 5)).unwrap();
        assert_eq!(on_bar.text, "bar");
    }

    #[test]
    fn word_at_unicode_does_not_panic_or_misresolve() {
        // Byte-offset implementation sliced mid-codepoint here.
        let r = r("let café = naïve();\n");
        let hit = word_at(&r, Position::new(0, 4)).unwrap();
        assert_eq!(hit.text, "café");
        let n = word_at(&r, Position::new(0, 11)).unwrap();
        assert_eq!(n.text, "naïve");
    }

    #[test]
    fn word_at_returns_none_on_whitespace_and_punctuation() {
        let r = r("a = (b);\n");
        // One past `a` intentionally still resolves `a`: that is the nudge
        // that makes hover usable while the cursor sits after a word.
        assert_eq!(word_at(&r, Position::new(0, 1)).unwrap().text, "a");
        assert!(word_at(&r, Position::new(0, 2)).is_none(), "space");
        assert!(word_at(&r, Position::new(0, 4)).is_none(), "open paren");
    }

    #[test]
    fn find_name_respects_identifier_boundaries() {
        let r = r("let rerun = 1; let run = 2;\n");
        // `run` must not match inside `rerun`.
        let hit = find_name_on_line(&r, 1, "run").expect("should find run");
        let start = position_to_char(&r, hit.start).unwrap();
        let end = position_to_char(&r, hit.end).unwrap();
        assert_eq!(r.slice(start..end).to_string(), "run");
        assert!(
            start > 0 && r.char(start - 1) == ' ',
            "matched inside `rerun`"
        );
    }

    #[test]
    fn find_name_handles_unicode_and_missing_lines() {
        let r = r("let café = 1;\n");
        assert!(find_name_on_line(&r, 1, "café").is_some());
        assert!(find_name_on_line(&r, 99, "café").is_none());
        assert!(find_name_on_line(&r, 1, "").is_none());
    }

    #[test]
    fn find_names_returns_every_bounded_match_on_the_line() {
        let r = r("fn run() { helper(); rehelper(); helper(); }\n");
        let starts: Vec<u32> = find_names_on_line(&r, 1, "helper")
            .iter()
            .map(|range| range.start.character)
            .collect();
        assert_eq!(starts, vec![11, 33], "`rehelper` must not match");
    }

    #[test]
    fn find_names_counts_characters_and_skips_multibyte_candidates() {
        let r = r("x = 'ção'; éa(); xéa(); éa()\n");
        let starts: Vec<u32> = find_names_on_line(&r, 1, "éa")
            .iter()
            .map(|range| range.start.character)
            .collect();
        assert_eq!(starts, vec![11, 24]);
        assert!(find_names_on_line(&r, 99, "éa").is_empty());
        assert!(find_names_on_line(&r, 1, "").is_empty());
    }

    #[test]
    fn full_line_range_is_clamped_to_the_line() {
        let r = r("ab\ncd\n");
        let range = full_line_range(&r, 1);
        assert_eq!(range.start, Position::new(0, 0));
        assert_eq!(range.end, Position::new(0, 2), "must not be u32::MAX");
        // Out-of-range line clamps instead of panicking.
        let _ = full_line_range(&r, 9999);
    }
}
