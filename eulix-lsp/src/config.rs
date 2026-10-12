// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! Runtime configuration, read from `initializationOptions`.
//!
//! Everything here has a working default. Most clients send nothing at all,
//! and a server that refuses to start without config is a bad neighbour.

use serde::Deserialize;
use serde_json::Value;

#[derive(Debug, Clone, Deserialize)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct Config {
    // How long to wait after the last edit before reparsing.
    pub debounce_ms: u64,
    // Files bigger than this are indexed by name only and never parsed.
    pub max_file_bytes: usize,
    // Whether to scan the workspace root when `initialized` fires.
    pub scan_on_start: bool,
    // Don't scan at startup, only on the first cross-file navigation
    // request. Handy for huge workspaces where a full scan takes minutes.
    pub lazy_scan: bool,
    // Hard cap on how many files a scan will pull into the index.
    pub max_scan_files: usize,
    // Total bytes of source to scan before stopping. Keeps the scan from
    // eating your whole machine.
    pub scan_budget_mb: usize,
    // Threads used for parsing during a scan.
    pub scan_threads: usize,
    // Flag calls whose target isn't in the workspace.
    //
    // Off by default. At this level of analysis, a call into the standard
    // library, a macro, a dynamic dispatch, or a symbol from a file the scan
    // skipped all look the same as a typo, so turning this on without a
    // curated stdlib allowlist just produces noise.
    pub report_unresolved_calls: bool,
    // Surface `TODO`/`FIXME` markers as information diagnostics.
    pub report_todos: bool,
    // Surface parser-detected security notes as warnings.
    pub report_security_notes: bool,
    // Emit semantic tokens.
    pub semantic_tokens: bool,
    // Extra directory names to skip while scanning.
    pub ignore_dirs: Vec<String>,
}

impl Default for Config {
    fn default() -> Self {
        Self {
            debounce_ms: 200,
            max_file_bytes: 2 * 1024 * 1024,
            scan_on_start: true,
            lazy_scan: false,
            max_scan_files: 50_000,
            scan_budget_mb: 500,
            scan_threads: std::thread::available_parallelism()
                .map_or(4, |n| n.get())
                .min(4),
            report_unresolved_calls: false,
            report_todos: true,
            report_security_notes: true,
            semantic_tokens: true,
            ignore_dirs: vec![
                ".git".into(),
                ".eulix".into(),
                "node_modules".into(),
                "target".into(),
                "dist".into(),
                "build".into(),
                "__pycache__".into(),
                ".venv".into(),
                "venv".into(),
                ".mypy_cache".into(),
                ".pytest_cache".into(),
            ],
        }
    }
}

impl Config {
    // Parse config out of the client's `initializationOptions`.
    //
    // This never fails the handshake. Unknown keys or a wrong type fall back
    // to defaults, since a bad config object should degrade the server, not
    // keep it from starting.
    pub fn from_value(v: Option<&Value>) -> (Self, Vec<String>) {
        let Some(v) = v.filter(|v| v.is_object()) else {
            return (Self::default(), Vec::new());
        };
        match serde_json::from_value::<Config>(v.clone()) {
            Ok(c) => (c, Vec::new()),
            Err(e) => {
                // Retry key by key so one bad field doesn't throw away the
                // rest of the config.
                let mut cfg = Self::default();
                let mut notes = vec![format!("configuration partially ignored: {e}")];
                if let Some(obj) = v.as_object() {
                    for (k, val) in obj {
                        // Deserialize just this key against a default Config
                        // so one bad value can't take the others down with
                        // it.
                        let single = match serde_json::from_value::<Config>(Value::Object(
                            std::iter::once((k.clone(), val.clone())).collect(),
                        )) {
                            Ok(c) => c,
                            Err(_) => {
                                notes.push(format!("ignoring unknown or malformed option `{k}`"));
                                continue;
                            }
                        };
                        // These keys are the wire names (camelCase), not the
                        // Rust field names.
                        match k.as_str() {
                            "debounceMs" => cfg.debounce_ms = single.debounce_ms,
                            "maxFileBytes" => cfg.max_file_bytes = single.max_file_bytes,
                            "scanOnStart" => cfg.scan_on_start = single.scan_on_start,
                            "lazyScan" => cfg.lazy_scan = single.lazy_scan,
                            "maxScanFiles" => cfg.max_scan_files = single.max_scan_files,
                            "scanBudgetMb" => cfg.scan_budget_mb = single.scan_budget_mb,
                            "scanThreads" => cfg.scan_threads = single.scan_threads.min(4),
                            "reportUnresolvedCalls" => {
                                cfg.report_unresolved_calls = single.report_unresolved_calls
                            }
                            "reportTodos" => cfg.report_todos = single.report_todos,
                            "reportSecurityNotes" => {
                                cfg.report_security_notes = single.report_security_notes
                            }
                            "semanticTokens" => cfg.semantic_tokens = single.semantic_tokens,
                            "ignoreDirs" => cfg.ignore_dirs = single.ignore_dirs,
                            other => notes.push(format!("ignoring unknown option `{other}`")),
                        }
                    }
                }
                (cfg, notes)
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn defaults_apply_when_no_options_are_sent() {
        let (c, notes) = Config::from_value(None);
        assert!(notes.is_empty());
        assert_eq!(c.debounce_ms, 200);
        assert!(!c.report_unresolved_calls);
    }

    #[test]
    fn valid_options_are_applied() {
        let (c, notes) =
            Config::from_value(Some(&json!({ "debounceMs": 42, "scanOnStart": false })));
        assert!(notes.is_empty());
        assert_eq!(c.debounce_ms, 42);
        assert!(!c.scan_on_start);
    }

    #[test]
    fn one_bad_field_does_not_discard_the_good_ones() {
        let (c, notes) = Config::from_value(Some(
            &json!({ "debounceMs": "not a number", "reportTodos": false }),
        ));
        assert!(!notes.is_empty(), "the rejection should be reported");
        assert_eq!(c.debounce_ms, 200, "bad field falls back to default");
        assert!(!c.report_todos, "good field is still applied");
    }

    #[test]
    fn non_object_options_are_ignored() {
        let (c, _) = Config::from_value(Some(&json!("nope")));
        assert_eq!(c.debounce_ms, 200);
    }
}
