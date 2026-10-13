// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! Shared server state.
//!
//! Locking discipline, which the whole server depends on:
//!
//! > A `parking_lot` lock is **never** held across an `.await`.
//!
//! Handlers are `async` but these locks are synchronous, so a lock held across
//! a yield point parks a tokio worker thread instead of releasing it. Every
//! accessor below is lock-and-copy; none of them return a guard.

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

use parking_lot::RwLock;
use ropey::Rope;
use tokio::task::JoinHandle;
use tower_lsp::lsp_types::Url;

use crate::config::Config;
use crate::index::Index;
use crate::syntax::ParsedFile;

// An open editor buffer.
pub struct Document {
    pub text: Rope,
    pub version: i32,
    // `None` only if the last parse failed outright. A file with syntax
    // errors still parses successfully and still yields symbols.
    pub parsed: Option<Arc<ParsedFile>>,
    // Why `parsed` is None, surfaced as a diagnostic.
    pub parse_error: Option<String>,
    // Bumped on every edit. A debounced reparse only publishes if the
    // generation still matches. This closes a window that `JoinHandle::abort`
    // alone leaves open: abort can't stop a task that's already inside
    // `spawn_blocking`, and the parse is CPU bound.
    pub generation: u64,
}

impl Document {
    pub fn new(text: String, version: i32, parsed: Option<Arc<ParsedFile>>) -> Self {
        Self {
            text: Rope::from_str(&text),
            version,
            parsed,
            parse_error: None,
            generation: 0,
        }
    }
}

#[derive(Default)]
pub struct Documents {
    pub open: HashMap<Url, Document>,
    // One debounce task per open document. A new edit replaces its own.
    pub tasks: HashMap<Url, JoinHandle<()>>,
}

#[derive(Default)]
pub struct Workspace {
    pub root: Option<PathBuf>,
    // Parsed content for every file we know about, open or not. Open buffers
    // shadow these, but that logic lives in `ServerState::parsed_for_uri`,
    // not here. See `Workspace::lookup`.
    pub files: HashMap<PathBuf, Arc<ParsedFile>>,
}

impl Workspace {
    // Parse result for a path from the disk cache.
    //
    // Open buffer shadowing deliberately does not live here. This map is
    // keyed by path while buffers are keyed by URI, and reconciling the two
    // here would mean a `to_file_path()` per entry. `ServerState::parsed_for_uri`
    // checks buffers first and falls through to this.
    pub fn lookup(&self, path: &Path) -> Option<Arc<ParsedFile>> {
        self.files.get(path).cloned()
    }
}

// Progress of the background workspace scan.
#[derive(Default)]
pub struct ScanState {
    pub running: bool,
    pub indexed: usize,
    pub last_error: Option<String>,
}

pub struct ServerState {
    pub config: RwLock<Config>,
    pub workspace: RwLock<Workspace>,
    pub documents: RwLock<Documents>,
    pub index: RwLock<Index>,
    pub scan: RwLock<ScanState>,
    // Set by `shutdown`. The scan loop polls it between files.
    pub shutdown: AtomicBool,
}

impl Default for ServerState {
    fn default() -> Self {
        Self::new()
    }
}

impl ServerState {
    pub fn new() -> Self {
        Self {
            config: RwLock::new(Config::default()),
            workspace: RwLock::new(Workspace::default()),
            documents: RwLock::new(Documents::default()),
            index: RwLock::new(Index::new()),
            scan: RwLock::new(ScanState::default()),
            shutdown: AtomicBool::new(false),
        }
    }

    pub fn is_shutdown(&self) -> bool {
        self.shutdown.load(Ordering::Relaxed)
    }

    // Cancel every pending debounce task. Called on shutdown and on exit.
    pub fn abort_all_tasks(&self) {
        let mut docs = self.documents.write();
        for (_, handle) in docs.tasks.drain() {
            handle.abort();
        }
    }

    // Index one parsed file into both the workspace cache and the name index.
    pub fn commit(&self, path: PathBuf, parsed: Arc<ParsedFile>) {
        {
            let mut ix = self.index.write();
            ix.index_file(&path, &parsed);
        }
        let mut ws = self.workspace.write();
        ws.files.insert(path, parsed);
    }

    // Drop a file from both the cache and the index.
    pub fn forget(&self, path: &Path) {
        {
            let mut ix = self.index.write();
            ix.remove_file(path);
        }
        let mut ws = self.workspace.write();
        ws.files.remove(path);
    }

    // Parse result for a URI, preferring the open buffer over the disk cache.
    pub fn parsed_for_uri(&self, uri: &Url) -> Option<Arc<ParsedFile>> {
        let path = uri.to_file_path().ok()?;
        {
            let docs = self.documents.read();
            if let Some(doc) = docs.open.get(uri) {
                if let Some(p) = doc.parsed.clone() {
                    return Some(p);
                }
            }
        }
        let ws = self.workspace.read();
        ws.lookup(&path)
    }

    // Text of an open buffer. `None` for files that aren't open.
    pub fn text_for_uri(&self, uri: &Url) -> Option<(Rope, i32)> {
        let docs = self.documents.read();
        docs.open.get(uri).map(|d| (d.text.clone(), d.version))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn commit_then_forget_removes_definitions() {
        let st = ServerState::new();
        let path = PathBuf::from("/w/a.rs");
        let parsed = Arc::new(ParsedFile {
            functions: vec![crate::syntax::Func {
                name: "foo".into(),
                line_start: 1,
                line_end: 2,
                ..Default::default()
            }],
            ..Default::default()
        });
        st.commit(path.clone(), parsed);
        assert!(st.index.read().resolve("foo", &path, None).is_some());
        st.forget(&path);
        assert!(st.index.read().resolve("foo", &path, None).is_none());
    }

    #[test]
    fn shutdown_flag_round_trips() {
        let st = ServerState::new();
        assert!(!st.is_shutdown());
        st.shutdown.store(true, Ordering::Relaxed);
        assert!(st.is_shutdown());
    }
}
