use crate::struc::kb_struct::*;
use regex::Regex;
use std::sync::LazyLock;

#[allow(clippy::expect_used)] // patterns are compile-time literals; failure = programmer bug at first use
pub fn static_regex(pattern: &str) -> Regex {
    Regex::new(pattern).expect("static regex pattern must be valid")
}

static TODO_RE: LazyLock<Regex> = LazyLock::new(|| {
    static_regex(r"(?i)(?://|/\*).*?\b(?:TODO|FIXME|XXX)\b[:\s]*(.*?)(?:\*/\s*)?$")
});
pub fn extract_todos(source_code: &str) -> Vec<Todo> {
    source_code
        .lines()
        .enumerate()
        .filter_map(|(idx, line)| {
            TODO_RE.captures(line).and_then(|caps| {
                caps.get(1).map(|m| {
                    let text = m.as_str().trim().to_string();
                    let text_lower = text.to_lowercase();

                    let priority =
                        if text_lower.contains("critical") || text_lower.contains("urgent") {
                            "high"
                        } else if text_lower.contains("minor") {
                            "low"
                        } else {
                            "medium"
                        };

                    Todo {
                        line: idx + 1,
                        text,
                        priority: priority.to_string(),
                    }
                })
            })
        })
        .collect()
}
