use regex::bytes::Regex;

#[allow(clippy::expect_used)] // patterns are compile-time literals; failure = programmer bug at first use
pub fn static_regex(pattern: &str) -> Regex {
    Regex::new(pattern).expect("static regex pattern must be valid")
}
