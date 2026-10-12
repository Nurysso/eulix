// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! `LanguageServer` implementation.

use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;

use ropey::Rope;
use tokio::time::sleep;
use tower_lsp::jsonrpc::Result;
use tower_lsp::lsp_types::*;
use tower_lsp::{async_trait, Client, LanguageServer};
use tracing::{debug, error, info, warn};

use crate::config::Config;
use crate::index::Def;
use crate::position::{self, WordHit};
use crate::scan;
use crate::state::{Document, ServerState};
use crate::syntax::{self, ParsedFile};
use crate::{convert, diagnostics};

const MAX_COMPLETIONS: usize = 200;

// How long we wait for the client's `exit` notification before just killing
// the process. The spec says the client sends `exit` right after `shutdown`.
const EXIT_GRACE_MS: u64 = 2_000;

pub struct Backend {
    client: Client,
    state: Arc<ServerState>,
    // Set when we shut down. The scan loop checks this between files so it
    // can bail out early.
    scan_cancel: Arc<AtomicBool>,
}

impl Backend {
    pub fn new(client: Client) -> Self {
        Self {
            client,
            state: Arc::new(ServerState::new()),
            scan_cancel: Arc::new(AtomicBool::new(false)),
        }
    }

    // Parsing is CPU heavy, so this always runs on the blocking pool instead
    // of a reactor thread.
    async fn parse_one(
        path: PathBuf,
        text: String,
        cfg: Config,
    ) -> std::result::Result<Arc<ParsedFile>, String> {
        tokio::task::spawn_blocking(move || scan::parse_file(&path, &text, &cfg).map(Arc::new))
            .await
            .map_err(|e| format!("parse worker failed: {e}"))?
    }

    // Shared lookup helpers

    // Returns the ParsedFile for a URI. Prefers the open buffer if the file is
    // open, otherwise falls back to the cached copy.
    fn parsed_for(&self, uri: &Url) -> Option<Arc<ParsedFile>> {
        self.state.parsed_for_uri(uri)
    }

    // Gets both the text and the parse result for a URI.
    //
    // If a file is indexed but not open we have no live text, so position
    // based features won't work on it. We return None in that case instead of
    // an empty string, so that definition/hover can still resolve cross-file
    // targets.
    fn context_for(&self, uri: &Url) -> Option<(Rope, Arc<ParsedFile>)> {
        let rope = self.state.text_for_uri(uri).map(|(r, _)| r)?;
        let parsed = self.parsed_for(uri)?;
        Some((rope, parsed))
    }

    fn path_for(&self, uri: &Url) -> Option<PathBuf> {
        uri.to_file_path().ok()
    }

    fn word_and_parsed(
        &self,
        uri: &Url,
        pos: Position,
    ) -> Option<(WordHit, Rope, Arc<ParsedFile>, PathBuf)> {
        let (rope, parsed) = self.context_for(uri)?;
        let word = position::word_at(&rope, pos)?;
        let path = self.path_for(uri)?;
        Some((word, rope, parsed, path))
    }

    // Builds the best Location we can for a definition. Prefers a tight
    // selection range on the name itself.
    fn location_for(&self, def: &Def) -> Option<Location> {
        let uri = Url::from_file_path(&def.path).ok()?;
        let range = self
            .state
            .text_for_uri(&uri)
            .map(|(rope, _)| position::find_name_on_line(&rope, def.line_start, &def.name))
            .flatten()
            .unwrap_or_else(|| line_range(def.line_start));
        Some(Location { uri, range })
    }

    // Commits a parse result to state, publishes diagnostics, and refreshes
    // whatever depends on it.
    async fn apply_parse(
        &self,
        uri: Url,
        path: PathBuf,
        parsed: Option<Arc<ParsedFile>>,
        parse_error: Option<String>,
        version: Option<i32>,
    ) {
        match parsed {
            Some(p) => {
                self.state.commit(path.clone(), p.clone());
                let diags = {
                    let rope = self.state.text_for_uri(&uri).map(|(r, _)| r);
                    let cfg = self.state.config.read().clone();
                    let index = self.state.index.read();
                    diagnostics::diagnostics_for(&uri, &path, &p, rope.as_ref(), &cfg, &index)
                };
                self.client.publish_diagnostics(uri, diags, version).await;
            }
            None => {
                if let Some(err) = &parse_error {
                    let rope = self.state.text_for_uri(&uri).map(|(r, _)| r);
                    let diag = diagnostics::parse_failure_diagnostic(rope.as_ref(), err);
                    self.client
                        .publish_diagnostics(uri, vec![diag], version)
                        .await;
                }
            }
        }
    }

    fn spawn_scan(&self) {
        let state = self.state.clone();
        let client = self.client.clone();
        let cancel = self.scan_cancel.clone();

        // Kick any in-flight scan so this one supersedes it.
        cancel.store(true, Ordering::SeqCst);
        cancel.store(false, Ordering::SeqCst);

        tokio::spawn(async move {
            let Some(root) = state.workspace.read().root.clone() else {
                return;
            };
            let cfg = state.config.read().clone();

            // Walking the tree is I/O bound and blocking, so keep it off the
            // reactor.
            let (paths, truncated) = {
                let root = root.clone();
                let cfg = cfg.clone();
                match tokio::task::spawn_blocking(move || scan::collect_source_files(&root, &cfg))
                    .await
                {
                    Ok(v) => v,
                    Err(e) => {
                        error!(error = %e, "workspace walk failed");
                        return;
                    }
                }
            };
            if paths.is_empty() {
                info!("workspace scan found no indexable files");
                return;
            }
            info!(files = paths.len(), truncated, "workspace scan starting");

            state.scan.write().running = true;
            let on_progress = {
                let state = state.clone();
                move |n: usize| state.scan.write().indexed = n
            };
            let (ok, failed) =
                scan::parse_all(paths, cfg.clone(), cancel.clone(), on_progress).await;

            if cancel.load(Ordering::SeqCst) || state.is_shutdown() {
                debug!("scan superseded, discarding");
                state.scan.write().running = false;
                return;
            }

            // Publish everything in one pass so nobody ever sees a half-built
            // index.
            {
                let mut ix = state.index.write();
                for (path, parsed) in &ok {
                    ix.index_file(path, parsed);
                }
            }
            {
                let mut ws = state.workspace.write();
                for (path, parsed) in &ok {
                    ws.files.insert(path.clone(), parsed.clone());
                }
            }

            {
                let mut s = state.scan.write();
                s.running = false;
                s.indexed = ok.len();
                s.last_error = (!failed.is_empty())
                    .then(|| format!("{} file(s) failed to parse", failed.len()));
            }

            let summary = if truncated {
                format!(
                    "eulixlsp: indexed {} files (scan limit reached, index is partial; raise maxScanFiles to cover the workspace)",
                    ok.len()
                )
            } else {
                format!("eulixlsp: indexed {} files", ok.len())
            };
            info!(
                indexed = ok.len(),
                failed = failed.len(),
                truncated,
                symbols = state.index.read().def_count(),
                "workspace scan complete"
            );
            client.log_message(MessageType::INFO, summary).await;
        });
    }

    // Reparse a document after the debounce window, but only if it is still
    // the current version.
    fn spawn_debounced_reparse(&self, uri: Url, path: PathBuf, text: String, version: i32) {
        let state = self.state.clone();
        let client = self.client.clone();
        let delay = Duration::from_millis(state.config.read().debounce_ms);
        let generation = {
            let mut docs = state.documents.write();
            match docs.open.get_mut(&uri) {
                Some(d) => {
                    d.generation += 1;
                    d.generation
                }
                None => return,
            }
        };

        // Cancel this document's previous pending task.
        if let Some(prev) = state.documents.write().tasks.remove(&uri) {
            prev.abort();
        }

        // The task gets its own clone of the URI; the caller keeps the
        // original to use as the map key.
        let task_uri = uri.clone();
        let handle = tokio::spawn(async move {
            sleep(delay).await;

            // The document might have been closed or superseded during the
            // sleep.
            {
                let docs = state.documents.read();
                match docs.open.get(&task_uri) {
                    Some(d) if d.generation == generation => {}
                    _ => return,
                }
            }

            let cfg = state.config.read().clone();
            match Self::parse_one(path.clone(), text, cfg).await {
                Ok(parsed) => {
                    {
                        let mut docs = state.documents.write();
                        match docs.open.get_mut(&task_uri) {
                            Some(d) if d.generation == generation => {
                                d.parsed = Some(parsed.clone());
                                d.parse_error = None;
                            }
                            // A newer generation replaced us while the parse
                            // was running, so this result is stale and the
                            // newer task will handle it.
                            _ => return,
                        }
                    }
                    state.index.write().index_file(&path, &parsed);
                    let diags = {
                        let rope = state.text_for_uri(&task_uri).map(|(r, _)| r);
                        let cfg = state.config.read().clone();
                        let index = state.index.read();
                        diagnostics::diagnostics_for(
                            &task_uri,
                            &path,
                            &parsed,
                            rope.as_ref(),
                            &cfg,
                            &index,
                        )
                    };
                    client
                        .publish_diagnostics(task_uri, diags, Some(version))
                        .await;
                }
                Err(e) => {
                    debug!(error = %e, "debounced reparse failed; keeping previous symbols");
                }
            }
        });

        self.state.documents.write().tasks.insert(uri, handle);
    }
}

fn line_range(line_1based: usize) -> Range {
    let l = line_1based.saturating_sub(1) as u32;
    Range::new(Position::new(l, 0), Position::new(l, u32::MAX))
}

// === LSP surface ===

#[async_trait]
impl LanguageServer for Backend {
    async fn initialize(&self, params: InitializeParams) -> Result<InitializeResult> {
        let caps = params.capabilities;
        // We store this for possible per-client tuning later. Nested symbols
        // are returned unconditionally because clients that don't support
        // hierarchical symbols will flatten them anyway, and the spec allows
        // both shapes.
        let hierarchical = caps
            .text_document
            .as_ref()
            .and_then(|t| t.document_symbol.as_ref())
            .and_then(|d| d.hierarchical_document_symbol_support)
            .unwrap_or(false);
        tracing::debug!(hierarchical, "client capabilities recorded");

        let root = params
            .root_uri
            .as_ref()
            .and_then(|u| u.to_file_path().ok())
            .or_else(|| {
                params
                    .workspace_folders
                    .as_ref()
                    .and_then(|f| f.first())
                    .and_then(|f| f.uri.to_file_path().ok())
            });

        let (cfg, notes) = Config::from_value(params.initialization_options.as_ref());
        let debounce = cfg.debounce_ms;
        {
            *self.state.config.write() = cfg;
            self.state.workspace.write().root = root.clone();
        }
        for n in &notes {
            warn!("{n}");
        }

        match &root {
            Some(r) => {
                info!(root = %r.display(), debounce_ms = debounce, "initialized workspace root")
            }
            None => warn!("no workspace root supplied; cross-file features stay unavailable"),
        }

        let token_types: Vec<SemanticTokenType> = convert::TOKEN_TYPES
            .iter()
            .map(|s| SemanticTokenType::new(*s))
            .collect();
        let token_modifiers: Vec<SemanticTokenModifier> = convert::TOKEN_MODIFIERS
            .iter()
            .map(|s| SemanticTokenModifier::new(*s))
            .collect();

        Ok(InitializeResult {
            server_info: Some(ServerInfo {
                name: "eulixlsp".into(),
                version: Some(env!("CARGO_PKG_VERSION").into()),
            }),
            capabilities: ServerCapabilities {
                text_document_sync: Some(TextDocumentSyncCapability::Options(
                    TextDocumentSyncOptions {
                        open_close: Some(true),
                        change: Some(TextDocumentSyncKind::INCREMENTAL),
                        save: Some(TextDocumentSyncSaveOptions::Supported(true)),
                        will_save: Some(false),
                        will_save_wait_until: Some(false),
                    },
                )),
                document_symbol_provider: Some(OneOf::Left(true)),
                hover_provider: Some(HoverProviderCapability::Simple(true)),
                definition_provider: Some(OneOf::Left(true)),
                references_provider: Some(OneOf::Left(true)),
                document_highlight_provider: Some(OneOf::Left(true)),
                completion_provider: Some(CompletionOptions {
                    resolve_provider: Some(false),
                    trigger_characters: Some(vec![".".into(), "/".into(), ":".into(), ">".into()]),
                    all_commit_characters: None,
                    work_done_progress_options: Default::default(),
                    completion_item: None,
                }),
                workspace_symbol_provider: Some(OneOf::Left(true)),
                rename_provider: Some(OneOf::Left(true)),
                folding_range_provider: Some(FoldingRangeProviderCapability::Simple(true)),
                call_hierarchy_provider: Some(CallHierarchyServerCapability::Simple(true)),
                semantic_tokens_provider: Some(
                    SemanticTokensServerCapabilities::SemanticTokensOptions(
                        SemanticTokensOptions {
                            legend: SemanticTokensLegend {
                                token_types,
                                token_modifiers,
                            },
                            full: Some(SemanticTokensFullOptions::Bool(true)),
                            range: None,
                            work_done_progress_options: Default::default(),
                        },
                    ),
                ),
                workspace: Some(WorkspaceServerCapabilities {
                    workspace_folders: Some(WorkspaceFoldersServerCapabilities {
                        supported: Some(true),
                        change_notifications: Some(OneOf::Left(true)),
                    }),
                    file_operations: None,
                }),
                ..Default::default()
            },
        })
    }

    async fn initialized(&self, _: InitializedParams) {
        info!("client reports initialized");
        if self.state.config.read().scan_on_start {
            self.spawn_scan();
        }
        self.client
            .log_message(
                MessageType::INFO,
                "eulixlsp ready — Ctrl+Shift+P \u{203a} eulix: restart scan to re-index",
            )
            .await;
    }

    async fn shutdown(&self) -> Result<()> {
        self.state.shutdown.store(true, Ordering::SeqCst);
        self.scan_cancel.store(true, Ordering::SeqCst);
        self.state.abort_all_tasks();
        info!("shutdown requested");
        tokio::spawn(async {
            tokio::time::sleep(std::time::Duration::from_millis(EXIT_GRACE_MS)).await;
            info!("no exit notification after shutdown; terminating");
            std::process::exit(0);
        });

        Ok(())
    }

    // ==== Document lifecycle ====

    async fn did_open(&self, params: DidOpenTextDocumentParams) {
        let uri = params.text_document.uri;
        let Some(path) = self.path_for(&uri) else {
            error!(%uri, "not a file:// uri; ignoring");
            return;
        };
        let text = params.text_document.text;
        let version = params.text_document.version;
        {
            let mut docs = self.state.documents.write();
            docs.open
                .insert(uri.clone(), Document::new(text.clone(), version, None));
        }

        let cfg = self.state.config.read().clone();
        match Self::parse_one(path.clone(), text, cfg).await {
            Ok(parsed) => {
                {
                    let mut docs = self.state.documents.write();
                    if let Some(d) = docs.open.get_mut(&uri) {
                        d.parsed = Some(parsed.clone());
                        d.parse_error = None;
                    }
                }
                self.apply_parse(uri.clone(), path, Some(parsed), None, Some(version))
                    .await;
            }
            Err(e) => {
                warn!(%uri, error = %e, "parse failed on open; buffer still tracked");
                {
                    let mut docs = self.state.documents.write();
                    if let Some(d) = docs.open.get_mut(&uri) {
                        d.parse_error = Some(e.clone());
                    }
                }
                self.apply_parse(uri.clone(), path, None, Some(e), Some(version))
                    .await;
            }
        }
    }

    async fn did_change(&self, params: DidChangeTextDocumentParams) {
        let uri = params.text_document.uri;
        let version = params.text_document.version;
        let Some(path) = self.path_for(&uri) else {
            return;
        };

        // Apply the edits to the Rope. This is synchronous and we hold the
        //  lock only briefly because later changes assume the buffer
        //  already reflects this one.
        let text = {
            let mut docs = self.state.documents.write();
            let Some(doc) = docs.open.get_mut(&uri) else {
                warn!(%uri, "did_change for a document that is not open; ignoring");
                return;
            };
            // Out of order delivery: don't let an older version win.
            if version < doc.version {
                debug!(%uri, incoming = version, current = doc.version, "stale change ignored");
                return;
            }

            for change in &params.content_changes {
                match change.range {
                    None => doc.text = Rope::from_str(&change.text),
                    Some(range) => {
                        let Some((start, end)) = position::range_to_chars(&doc.text, range) else {
                            warn!(%uri, "change range does not resolve; skipping that edit");
                            continue;
                        };
                        doc.text.remove(start..end);
                        doc.text.insert(start, &change.text);
                    }
                }
            }
            doc.version = version;
            doc.text.to_string()
        };

        {
            let mut docs = self.state.documents.write();
            if let Some(d) = docs.open.get_mut(&uri) {
                d.parsed = None;
            }
        }

        self.spawn_debounced_reparse(uri, path, text, version);
    }

    async fn did_save(&self, params: DidSaveTextDocumentParams) {
        let uri = params.text_document.uri;
        let Some(path) = self.path_for(&uri) else {
            return;
        };

        // Skip the debounce. A save means the user wants current answers.
        if let Some(h) = self.state.documents.write().tasks.remove(&uri) {
            h.abort();
        }
        let Some((rope, version)) = self.state.text_for_uri(&uri) else {
            return;
        };
        let text = rope.to_string();
        let cfg = self.state.config.read().clone();

        match Self::parse_one(path.clone(), text, cfg).await {
            Ok(parsed) => {
                {
                    let mut docs = self.state.documents.write();
                    if let Some(d) = docs.open.get_mut(&uri) {
                        d.parsed = Some(parsed.clone());
                        d.parse_error = None;
                    }
                }
                self.apply_parse(uri, path, Some(parsed), None, Some(version))
                    .await;
            }
            Err(e) => debug!(error = %e, "reparse on save failed"),
        }
    }

    async fn did_close(&self, params: DidCloseTextDocumentParams) {
        let uri = params.text_document.uri;
        {
            let mut docs = self.state.documents.write();
            docs.open.remove(&uri);
            if let Some(h) = docs.tasks.remove(&uri) {
                h.abort();
            }
        }
        self.client.publish_diagnostics(uri, Vec::new(), None).await;
    }

    async fn did_change_watched_files(&self, params: DidChangeWatchedFilesParams) {
        let mut changed = 0usize;
        let mut removed = 0usize;
        for ev in params.changes {
            let uri = ev.uri;
            let Ok(path) = uri.to_file_path() else {
                continue;
            };
            if !syntax::is_supported(&path) {
                continue;
            }
            match ev.typ {
                FileChangeType::DELETED => {
                    self.state.forget(&path);
                    removed += 1;
                    self.client.publish_diagnostics(uri, Vec::new(), None).await;
                }
                _ => {
                    let Ok(text) = tokio::fs::read_to_string(&path).await else {
                        continue;
                    };
                    let cfg = self.state.config.read().clone();
                    if let Ok(parsed) = Self::parse_one(path.clone(), text, cfg).await {
                        self.state.commit(path, parsed);
                        changed += 1;
                    }
                }
            }
        }
        if changed > 0 || removed > 0 {
            info!(changed, removed, "watched files updated");
        }
    }

    async fn document_symbol(
        &self,
        params: DocumentSymbolParams,
    ) -> Result<Option<DocumentSymbolResponse>> {
        let uri = params.text_document.uri;
        let Some((rope, parsed)) = self.context_for(&uri) else {
            return Ok(None);
        };
        let symbols = convert::document_symbols(&rope, &parsed);
        Ok(Some(DocumentSymbolResponse::Nested(symbols)))
    }

    async fn folding_range(&self, params: FoldingRangeParams) -> Result<Option<Vec<FoldingRange>>> {
        let uri = params.text_document.uri;
        let Some((rope, parsed)) = self.context_for(&uri) else {
            return Ok(None);
        };
        Ok(Some(convert::folding_ranges(&rope, &parsed)))
    }

    async fn hover(&self, params: HoverParams) -> Result<Option<Hover>> {
        let p = params.text_document_position_params;
        let uri = p.text_document.uri;
        let Some((word, _rope, parsed, _path)) = self.word_and_parsed(&uri, p.position) else {
            return Ok(None);
        };

        // If the word matches both a class and a method, class hover wins.
        if let Some(c) = parsed.classes.iter().find(|c| c.name == word.text) {
            return Ok(Some(Hover {
                contents: HoverContents::Markup(MarkupContent {
                    kind: MarkupKind::Markdown,
                    value: convert::class_hover_markdown(c),
                }),
                range: Some(word.range),
            }));
        }
        let found = parsed
            .functions
            .iter()
            .find(|f| f.name == word.text)
            .map(|f| (f, None))
            .or_else(|| {
                parsed.classes.iter().find_map(|c| {
                    c.methods
                        .iter()
                        .find(|m| m.name == word.text)
                        .map(|m| (m, Some(c.name.as_str())))
                })
            });
        let Some((f, container)) = found else {
            return Ok(None);
        };
        Ok(Some(Hover {
            contents: HoverContents::Markup(MarkupContent {
                kind: MarkupKind::Markdown,
                value: convert::hover_markdown(f, container),
            }),
            range: Some(word.range),
        }))
    }

    async fn goto_definition(
        &self,
        params: GotoDefinitionParams,
    ) -> Result<Option<GotoDefinitionResponse>> {
        let p = params.text_document_position_params;
        let uri = p.text_document.uri;
        let Some((word, _rope, parsed, path)) = self.word_and_parsed(&uri, p.position) else {
            return Ok(None);
        };

        // Same file, exact match. Cheap and always correct.
        let local: Option<(usize, usize)> = parsed
            .functions
            .iter()
            .find(|f| f.name == word.text)
            .map(|f| (f.line_start, f.line_end))
            .or_else(|| {
                parsed
                    .classes
                    .iter()
                    .find(|c| c.name == word.text)
                    .map(|c| (c.line_start, c.line_end))
            })
            .or_else(|| {
                parsed.classes.iter().find_map(|c| {
                    c.methods
                        .iter()
                        .find(|m| m.name == word.text)
                        .map(|m| (m.line_start, m.line_end))
                })
            });
        if let Some((s, _e)) = local {
            // Returned as a Link rather than a Scalar so that the same
            // request always has the same shape, whether the target is in
            // this file or another. Clients shouldn't have to branch on that.
            let rope = self
                .state
                .text_for_uri(&uri)
                .map(|(r, _)| r)
                .unwrap_or_default();
            let range =
                position::find_name_on_line(&rope, s, &word.text).unwrap_or_else(|| line_range(s));
            return Ok(Some(GotoDefinitionResponse::Link(vec![LocationLink {
                origin_selection_range: Some(word.range),
                target_uri: uri.clone(),
                target_range: range,
                target_selection_range: range,
            }])));
        }

        // Cross-file, via the index. Resolution is total and stable, so
        //  we don't land on a random same-named function any more.
        let root = self.state.workspace.read().root.clone();
        let index = self.state.index.read();
        let Some(def) = index.resolve(&word.text, &path, root.as_deref()) else {
            return Ok(None);
        };

        let Some(loc) = self.location_for(&def) else {
            return Ok(None);
        };
        Ok(Some(GotoDefinitionResponse::Link(vec![LocationLink {
            origin_selection_range: Some(word.range),
            target_uri: loc.uri,
            target_range: loc.range,
            target_selection_range: loc.range,
        }])))
    }

    async fn references(&self, params: ReferenceParams) -> Result<Option<Vec<Location>>> {
        let p = params.text_document_position;
        let uri = p.text_document.uri;
        let Some((word, _rope, parsed, _path)) = self.word_and_parsed(&uri, p.position) else {
            return Ok(None);
        };
        let include_decl = params.context.include_declaration;

        let mut out: Vec<Location> = Vec::new();
        let mut seen: HashSet<(String, u32)> = HashSet::new();

        if include_decl {
            // The declaration in the current file.
            let decls: Vec<usize> = parsed
                .functions
                .iter()
                .filter(|f| f.name == word.text)
                .map(|f| f.line_start)
                .chain(
                    parsed
                        .classes
                        .iter()
                        .filter(|c| c.name == word.text)
                        .map(|c| c.line_start),
                )
                .chain(parsed.classes.iter().flat_map(|c| {
                    c.methods
                        .iter()
                        .filter(|m| m.name == word.text)
                        .map(|m| m.line_start)
                }))
                .collect();
            for line in decls {
                if seen.insert((uri.to_string(), line as u32)) {
                    out.push(Location {
                        uri: uri.clone(),
                        range: line_range(line),
                    });
                }
            }
        }

        // Call sites across the workspace, pulled from the reverse index.
        let index = self.state.index.read();
        for site in index.call_sites(&word.text) {
            let Ok(site_uri) = Url::from_file_path(&site.path) else {
                continue;
            };
            if !seen.insert((site_uri.to_string(), site.line as u32)) {
                continue;
            }
            out.push(Location {
                uri: site_uri,
                range: line_range(site.line),
            });
        }

        out.sort_by(|a, b| {
            a.uri
                .as_str()
                .cmp(b.uri.as_str())
                .then(a.range.start.line.cmp(&b.range.start.line))
        });
        Ok(Some(out))
    }

    async fn document_highlight(
        &self,
        params: DocumentHighlightParams,
    ) -> Result<Option<Vec<DocumentHighlight>>> {
        let p = params.text_document_position_params;
        let uri = p.text_document.uri;
        let Some((word, _rope, parsed, _path)) = self.word_and_parsed(&uri, p.position) else {
            return Ok(None);
        };

        let mut out = Vec::new();
        let mut push = |line: usize, kind: DocumentHighlightKind| {
            if line == 0 {
                return;
            }
            out.push(DocumentHighlight {
                range: line_range(line),
                kind: Some(kind),
            });
        };

        for f in &parsed.functions {
            if f.name == word.text {
                push(f.line_start, DocumentHighlightKind::WRITE);
            }
            for c in &f.calls {
                if c.callee == word.text {
                    push(c.line, DocumentHighlightKind::READ);
                }
            }
        }
        for c in &parsed.classes {
            if c.name == word.text {
                push(c.line_start, DocumentHighlightKind::WRITE);
            }
            for m in &c.methods {
                if m.name == word.text {
                    push(m.line_start, DocumentHighlightKind::WRITE);
                }
                for call in &m.calls {
                    if call.callee == word.text {
                        push(call.line, DocumentHighlightKind::READ);
                    }
                }
            }
        }

        out.sort_by_key(|h| h.range.start.line);
        out.dedup_by_key(|h| h.range.start.line);
        Ok(Some(out))
    }

    async fn completion(&self, params: CompletionParams) -> Result<Option<CompletionResponse>> {
        let p = params.text_document_position;
        let uri = p.text_document.uri;
        let Some(path) = self.path_for(&uri) else {
            return Ok(None);
        };
        let Some((rope, _parsed)) = self.context_for(&uri) else {
            return Ok(None);
        };

        // The prefix is the identifier fragment that ends at the cursor.
        let Some(idx) = position::position_to_char(&rope, p.position) else {
            return Ok(None);
        };
        let mut start = idx;
        while start > 0 {
            let c = rope.char(start - 1);
            if c == '_' || c.is_alphanumeric() {
                start -= 1;
            } else {
                break;
            }
        }
        let prefix = rope.slice(start..idx).to_string();

        let root = self.state.workspace.read().root.clone();
        let index = self.state.index.read();
        let defs = index.completions(&prefix, &path, root.as_deref(), MAX_COMPLETIONS);
        let items: Vec<CompletionItem> = defs.iter().map(convert::completion_item).collect();
        Ok(Some(CompletionResponse::Array(items)))
    }

    async fn symbol(
        &self,
        params: WorkspaceSymbolParams,
    ) -> Result<Option<Vec<SymbolInformation>>> {
        let query = params.query.trim().to_string();
        if query.is_empty() {
            return Ok(Some(Vec::new()));
        }
        let root = self.state.workspace.read().root.clone();
        let index = self.state.index.read();
        let defs = index.completions(&query, Path::new(""), root.as_deref(), MAX_COMPLETIONS);
        Ok(Some(
            defs.iter()
                .map(|d| convert::workspace_symbol(&query, d))
                .collect(),
        ))
    }

    async fn prepare_rename(
        &self,
        p: TextDocumentPositionParams,
    ) -> Result<Option<PrepareRenameResponse>> {
        let Some((word, _, _parsed, _path)) =
            self.word_and_parsed(&p.text_document.uri, p.position)
        else {
            return Ok(None);
        };
        // Renaming to a name that already exists in the workspace would
        // silently create a duplicate, so refuse if the target is taken.
        let existing = self.state.index.read().definition_count(&word.text);
        if existing > 1 {
            return Ok(None);
        }
        Ok(Some(PrepareRenameResponse::Range(word.range)))
    }

    async fn rename(&self, params: RenameParams) -> Result<Option<WorkspaceEdit>> {
        let p = params.text_document_position;
        let uri = p.text_document.uri;
        let new_name = params.new_name;

        if !is_valid_identifier(&new_name) {
            return Ok(None);
        }
        let Some((word, _rope, parsed, _path)) = self.word_and_parsed(&uri, p.position) else {
            return Ok(None);
        };
        let existing = self.state.index.read().definition_count(&word.text);
        if existing > 1 {
            return Ok(None);
        }

        let mut changes: HashMap<Url, Vec<TextEdit>> = HashMap::new();

        // Declaration in this file.
        let mut decl_lines: Vec<usize> = parsed
            .functions
            .iter()
            .filter(|f| f.name == word.text)
            .map(|f| f.line_start)
            .chain(
                parsed
                    .classes
                    .iter()
                    .filter(|c| c.name == word.text)
                    .map(|c| c.line_start),
            )
            .collect();
        decl_lines.extend(parsed.classes.iter().flat_map(|c| {
            c.methods
                .iter()
                .filter(|m| m.name == word.text)
                .map(|m| m.line_start)
        }));
        if let Some((rope, _)) = self.context_for(&uri) {
            for line in decl_lines {
                if let Some(range) = position::find_name_on_line(&rope, line, &word.text) {
                    changes.entry(uri.clone()).or_default().push(TextEdit {
                        range,
                        new_text: new_name.clone(),
                    });
                }
            }
        }

        // Call sites elsewhere. The reverse index only gives us file+line, so
        // the edit range has to be recovered from each file's text where we
        // have it.
        let sites = self.state.index.read().call_sites(&word.text);
        for site in sites {
            let Ok(site_uri) = Url::from_file_path(&site.path) else {
                continue;
            };
            if site_uri == uri {
                continue;
            }
            let Some((rope, _)) = self.context_for(&site_uri) else {
                // The file isn't open, so we have no text to build a precise
                // range from. A whole-line edit would be wrong, so skip it
                // rather than corrupt the file.
                continue;
            };
            if let Some(range) = position::find_name_on_line(&rope, site.line, &word.text) {
                changes.entry(site_uri).or_default().push(TextEdit {
                    range,
                    new_text: new_name.clone(),
                });
            }
        }

        for edits in changes.values_mut() {
            edits.sort_by_key(|e| e.range.start.line);
            edits.dedup_by_key(|e| e.range.start);
        }
        Ok(Some(WorkspaceEdit {
            changes: Some(changes),
            ..Default::default()
        }))
    }

    async fn semantic_tokens_full(
        &self,
        params: SemanticTokensParams,
    ) -> Result<Option<SemanticTokensResult>> {
        if !self.state.config.read().semantic_tokens {
            return Ok(None);
        }
        let uri = params.text_document.uri;
        let Some((rope, parsed)) = self.context_for(&uri) else {
            return Ok(None);
        };
        let tokens = convert::encode_semantic_tokens(&convert::semantic_tokens(&rope, &parsed));
        Ok(Some(SemanticTokensResult::Tokens(SemanticTokens {
            result_id: None,
            data: tokens,
        })))
    }

    async fn prepare_call_hierarchy(
        &self,
        params: CallHierarchyPrepareParams,
    ) -> Result<Option<Vec<CallHierarchyItem>>> {
        let p = params.text_document_position_params;
        let uri = p.text_document.uri;
        let Some((word, _rope, parsed, _path)) = self.word_and_parsed(&uri, p.position) else {
            return Ok(None);
        };
        let mut out = Vec::new();
        for f in parsed.functions.iter().filter(|f| f.name == word.text) {
            out.push(CallHierarchyItem {
                name: f.name.clone(),
                kind: SymbolKind::FUNCTION,
                tags: None,
                detail: Some(f.signature.clone()),
                uri: uri.clone(),
                range: line_range(f.line_start),
                selection_range: line_range(f.line_start),
                data: None,
            });
        }
        for c in &parsed.classes {
            for m in c.methods.iter().filter(|m| m.name == word.text) {
                out.push(CallHierarchyItem {
                    name: m.name.clone(),
                    kind: SymbolKind::METHOD,
                    tags: None,
                    detail: Some(format!("{}.{}", c.name, m.name)),
                    uri: uri.clone(),
                    range: line_range(m.line_start),
                    selection_range: line_range(m.line_start),
                    data: None,
                });
            }
        }
        if out.is_empty() {
            Ok(None)
        } else {
            Ok(Some(out))
        }
    }

    async fn incoming_calls(
        &self,
        params: CallHierarchyIncomingCallsParams,
    ) -> Result<Option<Vec<CallHierarchyIncomingCall>>> {
        let item = params.item;
        let mut out: Vec<CallHierarchyIncomingCall> = Vec::new();
        for site in self.state.index.read().call_sites(&item.name) {
            let Ok(caller_uri) = Url::from_file_path(&site.path) else {
                continue;
            };
            let Some(caller_parsed) = self.parsed_for(&caller_uri) else {
                continue;
            };
            // Figure out which function encloses the call site so we can name
            // the caller.
            let caller_name = caller_parsed
                .functions
                .iter()
                .find(|f| f.line_start <= site.line && site.line <= f.line_end)
                .map(|f| f.name.clone())
                .or_else(|| {
                    caller_parsed.classes.iter().find_map(|c| {
                        c.methods
                            .iter()
                            .find(|m| m.line_start <= site.line && site.line <= m.line_end)
                            .map(|m| format!("{}.{}", c.name, m.name))
                    })
                })
                .unwrap_or_else(|| "<module>".into());
            let caller_line = caller_parsed
                .functions
                .iter()
                .find(|f| f.line_start <= site.line && site.line <= f.line_end)
                .map(|f| f.line_start)
                .or_else(|| {
                    caller_parsed.classes.iter().find_map(|c| {
                        c.methods
                            .iter()
                            .find(|m| m.line_start <= site.line && site.line <= m.line_end)
                            .map(|m| m.line_start)
                    })
                })
                .unwrap_or(site.line);
            out.push(CallHierarchyIncomingCall {
                from: CallHierarchyItem {
                    name: caller_name,
                    kind: SymbolKind::FUNCTION,
                    tags: None,
                    detail: None,
                    uri: caller_uri,
                    range: line_range(caller_line),
                    selection_range: line_range(caller_line),
                    data: None,
                },
                from_ranges: vec![line_range(site.line)],
            });
        }
        Ok(if out.is_empty() { None } else { Some(out) })
    }

    async fn outgoing_calls(
        &self,
        params: CallHierarchyOutgoingCallsParams,
    ) -> Result<Option<Vec<CallHierarchyOutgoingCall>>> {
        let item = params.item;
        let Some(parsed) = self.parsed_for(&item.uri) else {
            return Ok(None);
        };
        let start = item.range.start.line as usize + 1;

        // Find the function or method whose declaration starts on this line
        // and use its recorded call list.
        let owner = parsed
            .functions
            .iter()
            .find(|f| f.line_start == start)
            .map(|f| (f.calls.clone(), f.line_start, f.name.clone()))
            .or_else(|| {
                parsed.classes.iter().find_map(|c| {
                    c.methods.iter().find(|m| m.line_start == start).map(|m| {
                        (
                            m.calls.clone(),
                            m.line_start,
                            format!("{}.{}", c.name, m.name),
                        )
                    })
                })
            });
        let Some((calls, owner_line, _owner_name)) = owner else {
            return Ok(None);
        };

        let from_path = item.uri.to_file_path().ok();
        let root = self.state.workspace.read().root.clone();
        let index = self.state.index.read();

        let mut out = Vec::new();
        for call in calls {
            if let Some(path) = &from_path {
                let Some(def) = index.resolve(&call.callee, path, root.as_deref()) else {
                    continue;
                };
                let Some(uri) = Url::from_file_path(&def.path).ok() else {
                    continue;
                };
                out.push(CallHierarchyOutgoingCall {
                    to: CallHierarchyItem {
                        name: def.name.clone(),
                        kind: match def.kind {
                            syntax::DefKind::Function => SymbolKind::FUNCTION,
                            syntax::DefKind::Method => SymbolKind::METHOD,
                            syntax::DefKind::Class => SymbolKind::CLASS,
                        },
                        tags: None,
                        detail: def.container.clone(),
                        uri,
                        range: line_range(def.line_start),
                        selection_range: line_range(def.line_start),
                        data: None,
                    },
                    from_ranges: vec![line_range(call.line)],
                });
            }
        }
        let _ = owner_line;
        Ok(if out.is_empty() { None } else { Some(out) })
    }
}

fn is_valid_identifier(s: &str) -> bool {
    !s.is_empty()
        && !s.starts_with(|c: char| c.is_ascii_digit())
        && s.chars().all(|c| c == '_' || c.is_alphanumeric())
}
