// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use super::*;
use tower_lsp::LspService;

fn open_buffer(backend: &Backend, path: &Path, text: &str) -> Url {
    let uri = Url::from_file_path(path).unwrap();
    let parsed = Arc::new(syntax::parse(path, text).unwrap());
    backend.state.commit(path.to_path_buf(), parsed.clone());
    backend.state.documents.write().open.insert(
        uri.clone(),
        Document::new(text.to_string(), 1, Some(parsed)),
    );
    uri
}

#[tokio::test]
async fn workspace_scan_preserves_the_index_of_an_open_buffer() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("app.py");
    std::fs::write(&path, "def disk_only(): pass\n").unwrap();
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    open_buffer(backend, &path, "def buffer_only(): pass\n");
    backend.spawn_scan();
    tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            {
                let scan = backend.state.scan.read();
                if scan.indexed == 1 && !scan.running {
                    break;
                }
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .expect("scan did not finish");
    assert_eq!(
        backend.state.index.read().definition_count("buffer_only"),
        1
    );
    assert_eq!(backend.state.index.read().definition_count("disk_only"), 0);
}

#[tokio::test]
async fn closing_a_never_saved_buffer_removes_its_symbols() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("unsaved.py");
    let uri = open_buffer(backend, &path, "def unsaved_only(): pass\n");
    backend
        .did_close(DidCloseTextDocumentParams {
            text_document: TextDocumentIdentifier::new(uri),
        })
        .await;
    assert_eq!(
        backend.state.index.read().definition_count("unsaved_only"),
        0
    );
    assert!(!backend.state.workspace.read().files.contains_key(&path));
}

#[tokio::test]
async fn closing_an_edited_buffer_restores_the_disk_symbols() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    let path = dir.path().join("saved.py");
    std::fs::write(&path, "def disk_only(): pass\n").unwrap();
    let uri = open_buffer(backend, &path, "def buffer_only(): pass\n");
    backend
        .did_close(DidCloseTextDocumentParams {
            text_document: TextDocumentIdentifier::new(uri),
        })
        .await;
    assert_eq!(backend.state.index.read().definition_count("disk_only"), 1);
    assert_eq!(
        backend.state.index.read().definition_count("buffer_only"),
        0
    );
}

#[tokio::test]
async fn closing_a_non_utf8_file_restores_its_disk_symbols() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    let path = dir.path().join("legacy.py");
    std::fs::write(&path, b"# caf\xe9\ndef legacy_fn(): pass\n").unwrap();
    let uri = open_buffer(backend, &path, "def buffer_only(): pass\n");
    backend
        .did_close(DidCloseTextDocumentParams {
            text_document: TextDocumentIdentifier::new(uri),
        })
        .await;
    assert_eq!(backend.state.index.read().definition_count("legacy_fn"), 1);
}

#[tokio::test]
async fn disk_events_skip_open_buffers_with_client_encoded_uris() {
    for typ in [FileChangeType::CHANGED, FileChangeType::DELETED] {
        let (service, _) = LspService::new(Backend::new);
        let backend = service.inner();
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().join("c++");
        std::fs::create_dir(&root).unwrap();
        backend.state.workspace.write().root = Some(root.clone());
        let path = root.join("app.py");
        std::fs::write(&path, "def disk_only(): pass\n").unwrap();
        // VS Code percent-encodes '+' (and Windows drive colons); `url` does not.
        let encoded = Url::from_file_path(&path)
            .unwrap()
            .as_str()
            .replace("c++", "c%2B%2B");
        let uri = Url::parse(&encoded).unwrap();
        backend
            .did_open(DidOpenTextDocumentParams {
                text_document: TextDocumentItem::new(
                    uri.clone(),
                    "python".into(),
                    1,
                    "def buffer_only(): pass\n".into(),
                ),
            })
            .await;
        backend
            .did_change_watched_files(DidChangeWatchedFilesParams {
                changes: vec![FileEvent { uri, typ }],
            })
            .await;
        assert_eq!(
            backend.state.index.read().definition_count("buffer_only"),
            1
        );
        assert_eq!(backend.state.index.read().definition_count("disk_only"), 0);
    }
}

async fn watched(backend: &Backend, path: &Path, typ: FileChangeType) {
    backend
        .did_change_watched_files(DidChangeWatchedFilesParams {
            changes: vec![FileEvent {
                uri: Url::from_file_path(path).unwrap(),
                typ,
            }],
        })
        .await;
}

#[tokio::test]
async fn watched_files_outside_the_workspace_are_not_indexed() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().join("root");
    let sibling = dir.path().join("root-other");
    std::fs::create_dir(&root).unwrap();
    std::fs::create_dir(&sibling).unwrap();
    backend.state.workspace.write().root = Some(root);
    let outside = sibling.join("foreign.py");
    std::fs::write(&outside, "def outside_only(): pass\n").unwrap();
    watched(backend, &outside, FileChangeType::CREATED).await;
    assert_eq!(
        backend.state.index.read().definition_count("outside_only"),
        0
    );
    assert!(!backend.state.workspace.read().files.contains_key(&outside));
}

#[tokio::test]
async fn watched_outside_deletions_do_not_remove_explicitly_indexed_files() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().join("root");
    std::fs::create_dir(&root).unwrap();
    backend.state.workspace.write().root = Some(root);
    let outside = dir.path().join("foreign.py");
    backend.state.commit(
        outside.clone(),
        Arc::new(syntax::parse(&outside, "def outside_only(): pass\n").unwrap()),
    );
    watched(backend, &outside, FileChangeType::DELETED).await;
    assert_eq!(
        backend.state.index.read().definition_count("outside_only"),
        1
    );
}

#[cfg(unix)]
#[tokio::test]
async fn watched_symlinks_cannot_escape_the_workspace() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().join("root");
    let outside = dir.path().join("outside");
    std::fs::create_dir(&root).unwrap();
    std::fs::create_dir(&outside).unwrap();
    backend.state.workspace.write().root = Some(root.clone());
    let target = outside.join("foreign.py");
    std::fs::write(&target, "def outside_only(): pass\n").unwrap();
    let linked = root.join("linked.py");
    std::os::unix::fs::symlink(&target, &linked).unwrap();
    watched(backend, &linked, FileChangeType::CHANGED).await;
    assert_eq!(
        backend.state.index.read().definition_count("outside_only"),
        0
    );

    let link_dir = root.join("linked-dir");
    std::os::unix::fs::symlink(&outside, &link_dir).unwrap();
    let linked_child = link_dir.join("foreign.py");
    backend.state.commit(
        linked_child.clone(),
        Arc::new(syntax::parse(&linked_child, "def retained(): pass\n").unwrap()),
    );
    std::fs::remove_file(&target).unwrap();
    watched(backend, &linked_child, FileChangeType::DELETED).await;
    assert_eq!(backend.state.index.read().definition_count("retained"), 1);
}

#[tokio::test]
async fn watched_inside_changes_and_deleted_parents_still_update_the_index() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    let parent = dir.path().join("nested");
    std::fs::create_dir(&parent).unwrap();
    let path = parent.join("app.py");
    std::fs::write(&path, "def before(): pass\n").unwrap();
    watched(backend, &path, FileChangeType::CREATED).await;
    assert_eq!(backend.state.index.read().definition_count("before"), 1);
    std::fs::write(&path, "def after(): pass\n").unwrap();
    watched(backend, &path, FileChangeType::CHANGED).await;
    assert_eq!(backend.state.index.read().definition_count("before"), 0);
    assert_eq!(backend.state.index.read().definition_count("after"), 1);
    std::fs::remove_dir_all(&parent).unwrap();
    watched(backend, &path, FileChangeType::DELETED).await;
    assert_eq!(backend.state.index.read().definition_count("after"), 0);
}

#[tokio::test]
async fn watched_disk_changes_preserve_open_buffer_symbols() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    let path = dir.path().join("app.py");
    std::fs::write(&path, "def disk_only(): pass\n").unwrap();
    open_buffer(backend, &path, "def buffer_only(): pass\n");
    watched(backend, &path, FileChangeType::CHANGED).await;
    assert_eq!(
        backend.state.index.read().definition_count("buffer_only"),
        1
    );
    assert_eq!(backend.state.index.read().definition_count("disk_only"), 0);
    std::fs::remove_file(&path).unwrap();
    watched(backend, &path, FileChangeType::DELETED).await;
    assert_eq!(
        backend.state.index.read().definition_count("buffer_only"),
        1
    );
}

#[tokio::test]
async fn watched_files_require_a_workspace_root() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("app.py");
    std::fs::write(&path, "def outside_only(): pass\n").unwrap();
    watched(backend, &path, FileChangeType::CREATED).await;
    assert_eq!(
        backend.state.index.read().definition_count("outside_only"),
        0
    );
}

fn many_functions(prefix: &str) -> String {
    (0..20_000)
        .map(|i| format!("fn {prefix}_{i}() {{}}\n"))
        .collect()
}

#[tokio::test]
async fn close_parse_cannot_overwrite_a_newer_watched_disk_update() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    let path = dir.path().join("race.rs");
    std::fs::write(&path, many_functions("old_disk")).unwrap();
    let uri = open_buffer(backend, &path, "fn buffer_only() {}\n");
    let close = backend.did_close(DidCloseTextDocumentParams {
        text_document: TextDocumentIdentifier::new(uri),
    });
    let update = async {
        tokio::time::sleep(Duration::from_millis(25)).await;
        std::fs::write(&path, "fn fresh_disk() {}\n").unwrap();
        watched(backend, &path, FileChangeType::CHANGED).await;
        assert_eq!(backend.state.index.read().definition_count("fresh_disk"), 1);
    };
    tokio::join!(close, update);
    assert_eq!(backend.state.index.read().definition_count("fresh_disk"), 1);
    assert_eq!(
        backend
            .state
            .index
            .read()
            .definition_count("old_disk_19999"),
        0
    );
}

#[tokio::test]
async fn in_flight_open_does_not_recreate_a_closed_unsaved_buffer() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("missing.rs");
    let uri = Url::from_file_path(&path).unwrap();
    let open = backend.did_open(DidOpenTextDocumentParams {
        text_document: TextDocumentItem::new(
            uri.clone(),
            "rust".into(),
            1,
            many_functions("ghost"),
        ),
    });
    let close = async {
        tokio::time::sleep(Duration::from_millis(25)).await;
        backend
            .did_close(DidCloseTextDocumentParams {
                text_document: TextDocumentIdentifier::new(uri.clone()),
            })
            .await;
    };
    tokio::join!(open, close);
    assert!(!backend.state.documents.read().open.contains_key(&uri));
    assert_eq!(
        backend.state.index.read().definition_count("ghost_19999"),
        0
    );
    assert!(!backend.state.workspace.read().files.contains_key(&path));
}

#[tokio::test]
async fn in_flight_open_cannot_replace_a_reopened_session_with_the_same_version() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("reopened.rs");
    let uri = Url::from_file_path(&path).unwrap();
    let open = backend.did_open(DidOpenTextDocumentParams {
        text_document: TextDocumentItem::new(
            uri.clone(),
            "rust".into(),
            1,
            many_functions("ghost"),
        ),
    });
    let replace = async {
        tokio::time::sleep(Duration::from_millis(25)).await;
        backend
            .did_close(DidCloseTextDocumentParams {
                text_document: TextDocumentIdentifier::new(uri.clone()),
            })
            .await;
        backend
            .did_open(DidOpenTextDocumentParams {
                text_document: TextDocumentItem::new(
                    uri.clone(),
                    "rust".into(),
                    1,
                    "fn reopened() {}".into(),
                ),
            })
            .await;
    };
    tokio::join!(open, replace);
    assert_eq!(backend.state.index.read().definition_count("reopened"), 1);
    assert_eq!(
        backend.state.index.read().definition_count("ghost_19999"),
        0
    );
    assert_eq!(
        backend.state.parsed_for_uri(&uri).unwrap().functions[0].name,
        "reopened"
    );
}

#[tokio::test]
async fn edited_open_buffer_keeps_buffer_ast_during_scan() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("app.py");
    std::fs::write(&path, "def disk_only(): pass\n").unwrap();
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    let uri = Url::from_file_path(&path).unwrap();

    backend
        .did_open(DidOpenTextDocumentParams {
            text_document: TextDocumentItem::new(
                uri.clone(),
                "python".into(),
                1,
                "def buffer_only(): pass\n".into(),
            ),
        })
        .await;
    backend.state.config.write().debounce_ms = 60_000;
    backend
        .did_change(DidChangeTextDocumentParams {
            text_document: VersionedTextDocumentIdentifier::new(uri.clone(), 2),
            content_changes: vec![TextDocumentContentChangeEvent {
                range: None,
                range_length: None,
                text: "def buffer_only():\n    return 1\n".into(),
            }],
        })
        .await;

    backend.spawn_scan();
    tokio::time::timeout(Duration::from_secs(5), async {
        while backend.state.scan.read().running || backend.state.scan.read().indexed == 0 {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .expect("scan did not finish");

    let Some(DocumentSymbolResponse::Nested(syms)) = backend
        .document_symbol(DocumentSymbolParams {
            text_document: TextDocumentIdentifier::new(uri),
            work_done_progress_params: Default::default(),
            partial_result_params: Default::default(),
        })
        .await
        .unwrap()
    else {
        panic!()
    };
    assert_eq!(syms[0].name, "buffer_only");
}

#[cfg(unix)]
#[tokio::test]
async fn internal_symlink_events_do_not_index_disk_for_open_buffers() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().join("root");
    std::fs::create_dir(&root).unwrap();
    backend.state.workspace.write().root = Some(root.clone());
    let real = root.join("real.py");
    let link = root.join("link.py");
    std::fs::write(&real, "def disk_only(): pass\n").unwrap();
    std::os::unix::fs::symlink(&real, &link).unwrap();

    open_buffer(backend, &real, "def buffer_only(): pass\n");
    watched(backend, &link, FileChangeType::CHANGED).await;
    assert_eq!(backend.state.index.read().definition_count("disk_only"), 0);
}

#[tokio::test]
async fn closing_external_existing_files_discards_their_symbols() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().join("root");
    let outside = dir.path().join("outside");
    std::fs::create_dir(&root).unwrap();
    std::fs::create_dir(&outside).unwrap();
    backend.state.workspace.write().root = Some(root);
    let ext = outside.join("external.py");
    std::fs::write(&ext, "def external_disk(): pass\n").unwrap();

    let uri = open_buffer(backend, &ext, "def external_buffer(): pass\n");
    backend
        .did_close(DidCloseTextDocumentParams {
            text_document: TextDocumentIdentifier::new(uri),
        })
        .await;
    assert_eq!(
        backend.state.index.read().definition_count("external_disk"),
        0
    );
    assert_eq!(
        (
            backend
                .state
                .index
                .read()
                .definition_count("external_buffer"),
            backend.state.workspace.read().files.contains_key(&ext),
        ),
        (0, false),
    );
}

async fn outline_names(backend: &Backend, uri: Url) -> Vec<String> {
    match backend
        .document_symbol(DocumentSymbolParams {
            text_document: TextDocumentIdentifier::new(uri),
            work_done_progress_params: Default::default(),
            partial_result_params: Default::default(),
        })
        .await
        .unwrap()
    {
        Some(DocumentSymbolResponse::Nested(symbols)) => {
            symbols.into_iter().map(|s| s.name).collect()
        }
        _ => Vec::new(),
    }
}

#[tokio::test]
async fn watched_changes_and_deletions_preserve_pending_buffer_content() {
    for kind in [FileChangeType::CHANGED, FileChangeType::DELETED] {
        let (service, _) = LspService::new(Backend::new);
        let backend = service.inner();
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("app.py");
        std::fs::write(&path, "def disk_only(): pass\n").unwrap();
        backend.state.workspace.write().root = Some(dir.path().to_path_buf());
        let uri = open_buffer(backend, &path, "def buffer_only(): pass\n");
        backend.state.config.write().debounce_ms = 60_000;
        backend
            .did_change(DidChangeTextDocumentParams {
                text_document: VersionedTextDocumentIdentifier::new(uri.clone(), 2),
                content_changes: vec![TextDocumentContentChangeEvent {
                    range: None,
                    range_length: None,
                    text: "def buffer_only():\n    return 1\n".into(),
                }],
            })
            .await;
        if kind == FileChangeType::DELETED {
            std::fs::remove_file(&path).unwrap();
        }
        watched(backend, &path, kind).await;
        assert_eq!(outline_names(backend, uri).await, ["buffer_only"]);
        assert_eq!(
            backend.state.workspace.read().files[&path].functions[0].name,
            "buffer_only"
        );
        assert_eq!(
            backend.state.index.read().definition_count("buffer_only"),
            1
        );
        assert_eq!(backend.state.index.read().definition_count("disk_only"), 0);
    }
}

#[tokio::test]
async fn first_buffer_parse_does_not_fall_back_to_cached_disk_content() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let path = PathBuf::from("/workspace/app.py");
    backend.state.commit(
        path.clone(),
        Arc::new(syntax::parse(&path, "def disk_only(): pass\n").unwrap()),
    );
    let uri = Url::from_file_path(&path).unwrap();
    backend.state.documents.write().open.insert(
        uri.clone(),
        Document::new("def buffer_only(): pass\n".into(), 1, None),
    );
    assert!(backend.state.parsed_for_uri(&uri).is_none());
    assert!(outline_names(backend, uri).await.is_empty());
}

#[tokio::test]
async fn closing_existing_files_without_a_workspace_discards_their_symbols() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("external.py");
    std::fs::write(&path, "def external_disk(): pass\n").unwrap();
    let uri = open_buffer(backend, &path, "def external_buffer(): pass\n");
    backend
        .did_close(DidCloseTextDocumentParams {
            text_document: TextDocumentIdentifier::new(uri),
        })
        .await;
    assert_eq!(
        backend.state.index.read().definition_count("external_disk"),
        0
    );
    assert_eq!(
        backend
            .state
            .index
            .read()
            .definition_count("external_buffer"),
        0
    );
    assert!(!backend.state.workspace.read().files.contains_key(&path));
}

#[cfg(unix)]
#[tokio::test]
async fn internal_symlink_directories_do_not_alias_open_buffers() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let real = dir.path().join("real");
    std::fs::create_dir(&real).unwrap();
    let path = real.join("app.py");
    std::fs::write(&path, "def disk_only(): pass\n").unwrap();
    let link = dir.path().join("link");
    std::os::unix::fs::symlink(&real, &link).unwrap();
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    open_buffer(backend, &path, "def buffer_only(): pass\n");
    watched(backend, &link.join("app.py"), FileChangeType::CHANGED).await;
    assert_eq!(backend.state.index.read().definition_count("disk_only"), 0);
    assert!(!backend
        .state
        .workspace
        .read()
        .files
        .contains_key(&link.join("app.py")));
}

#[cfg(unix)]
#[tokio::test]
async fn symlink_workspace_uses_its_configured_path_without_canonical_aliases() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let real = dir.path().join("real");
    std::fs::create_dir(&real).unwrap();
    let link = dir.path().join("root");
    std::os::unix::fs::symlink(&real, &link).unwrap();
    let path = real.join("app.py");
    std::fs::write(&path, "def disk_only(): pass\n").unwrap();
    backend.state.workspace.write().root = Some(link.clone());
    open_buffer(backend, &link.join("app.py"), "def buffer_only(): pass\n");
    watched(backend, &path, FileChangeType::CHANGED).await;
    watched(backend, &link.join("app.py"), FileChangeType::CHANGED).await;
    assert_eq!(backend.state.index.read().definition_count("disk_only"), 0);
    assert!(!backend.state.workspace.read().files.contains_key(&path));
    let closed = link.join("closed.py");
    std::fs::write(&closed, "def closed_disk(): pass\n").unwrap();
    watched(backend, &closed, FileChangeType::CREATED).await;
    assert_eq!(
        backend.state.index.read().definition_count("closed_disk"),
        1
    );
}

#[tokio::test]
async fn empty_scans_do_not_start_progress_or_replace_previous_counts() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    backend.state.scan.write().indexed = 7;
    backend.state.scan.write().last_error = Some("previous result".into());
    backend.spawn_scan();
    assert!(!backend.state.scan.read().running);
    tokio::time::sleep(Duration::from_millis(25)).await;
    assert_eq!(backend.state.scan.read().indexed, 7);
    assert_eq!(
        backend.state.scan.read().last_error.as_deref(),
        Some("previous result")
    );
}

#[tokio::test]
async fn failed_first_buffer_parse_removes_prior_disk_symbols_and_cache() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("app.py");
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    backend.state.commit(
        path.clone(),
        Arc::new(syntax::parse(&path, "def disk_only(): pass\n").unwrap()),
    );
    backend.state.config.write().max_file_bytes = 64;
    let uri = Url::from_file_path(&path).unwrap();
    backend
        .did_open(DidOpenTextDocumentParams {
            text_document: TextDocumentItem::new(
                uri.clone(),
                "python".into(),
                1,
                "def buffer_only(): pass\n".repeat(10),
            ),
        })
        .await;
    assert!(backend.state.documents.read().open[&uri]
        .parse_error
        .is_some());
    assert_eq!(backend.state.index.read().definition_count("disk_only"), 0);
    assert!(!backend.state.workspace.read().files.contains_key(&path));
    assert!(backend.state.parsed_for_uri(&uri).is_none());
}

#[tokio::test]
async fn pending_first_buffer_parse_hides_prior_disk_symbols_and_cache() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("app.rs");
    backend.state.commit(
        path.clone(),
        Arc::new(syntax::parse(&path, "fn disk_only() {}\n").unwrap()),
    );
    let uri = Url::from_file_path(&path).unwrap();
    let open = backend.did_open(DidOpenTextDocumentParams {
        text_document: TextDocumentItem::new(
            uri.clone(),
            "rust".into(),
            1,
            many_functions("buffer"),
        ),
    });
    let inspect_pending = async {
        assert!(backend.state.documents.read().open[&uri].parsed.is_none());
        assert_eq!(backend.state.index.read().definition_count("disk_only"), 0);
        assert!(!backend.state.workspace.read().files.contains_key(&path));
    };
    tokio::join!(biased; open, inspect_pending);
    assert_eq!(
        backend.state.index.read().definition_count("buffer_19999"),
        1
    );
}

#[test]
fn close_releases_buffer_state_before_io_and_a_newer_failed_watcher() {
    // Occupy the only blocking worker to suspend close at filesystem I/O.
    // Advancing the close future is explicit: no sleeps or parser-speed race.
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .max_blocking_threads(1)
        .build()
        .unwrap();
    runtime.block_on(async {
        let (service, _) = LspService::new(Backend::new);
        let backend = service.inner();
        let dir = tempfile::tempdir().unwrap();
        backend.state.workspace.write().root = Some(dir.path().to_path_buf());
        let path = dir.path().join("race_close.rs");
        std::fs::write(&path, "fn old_disk() {}\n".repeat(8)).unwrap();
        let uri = open_buffer(backend, &path, "fn buffer_only() {}\n");
        let (started_tx, started_rx) = std::sync::mpsc::channel();
        let (release_tx, release_rx) = std::sync::mpsc::channel::<()>();
        let blocker = tokio::task::spawn_blocking(move || {
            started_tx.send(()).unwrap();
            // Dropping release_tx also unblocks on assertion failure.
            let _ = release_rx.recv();
        });
        started_rx.recv().unwrap();
        let mut close = Box::pin(backend.did_close(DidCloseTextDocumentParams {
            text_document: TextDocumentIdentifier::new(uri.clone()),
        }));
        let pending = std::future::poll_fn(|cx| {
            std::task::Poll::Ready(std::future::Future::poll(close.as_mut(), cx).is_pending())
        })
        .await;
        assert!(pending, "close did not suspend at the blocked I/O");
        assert!(!backend.state.documents.read().open.contains_key(&uri));
        let pending_symbols = backend.state.index.read().definition_count("buffer_only");
        let pending_ast = backend.state.parsed_for_uri(&uri).is_some();
        drop(release_tx);
        blocker.await.unwrap();
        backend.state.config.write().max_file_bytes = 64;
        watched(backend, &path, FileChangeType::CHANGED).await;
        close.await;
        let final_symbols = backend.state.index.read().definition_count("buffer_only");
        let final_ast = backend.state.parsed_for_uri(&uri).is_some();
        assert_eq!(
            (pending_symbols, pending_ast, final_symbols, final_ast),
            (0, false, 0, false)
        );
        assert!(!backend.state.workspace.read().files.contains_key(&path));
    });
}

#[tokio::test]
async fn failed_watched_read_removes_closed_file_state() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    let path = dir.path().join("missing.rs");
    backend.state.commit(
        path.clone(),
        Arc::new(syntax::parse(&path, "fn stale_disk() {}\n").unwrap()),
    );
    // Missing path is intentional: CHANGED can arrive after a file vanished.
    watched(backend, &path, FileChangeType::CHANGED).await;
    assert_eq!(backend.state.index.read().definition_count("stale_disk"), 0);
    assert!(!backend.state.workspace.read().files.contains_key(&path));
}

#[tokio::test]
async fn failed_watched_parse_removes_closed_file_state() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let dir = tempfile::tempdir().unwrap();
    backend.state.workspace.write().root = Some(dir.path().to_path_buf());
    let path = dir.path().join("oversized.rs");
    backend.state.commit(
        path.clone(),
        Arc::new(syntax::parse(&path, "fn stale_disk() {}\n").unwrap()),
    );
    std::fs::write(&path, "fn new_disk() {}\n".repeat(8)).unwrap();
    backend.state.config.write().max_file_bytes = 64;
    watched(backend, &path, FileChangeType::CHANGED).await;
    assert_eq!(backend.state.index.read().definition_count("stale_disk"), 0);
    assert!(!backend.state.workspace.read().files.contains_key(&path));
}

#[tokio::test]
async fn failed_watched_reads_and_parses_preserve_open_buffers() {
    for parse_failure in [false, true] {
        let (service, _) = LspService::new(Backend::new);
        let backend = service.inner();
        let dir = tempfile::tempdir().unwrap();
        backend.state.workspace.write().root = Some(dir.path().to_path_buf());
        let path = dir.path().join("buffer.rs");
        let uri = open_buffer(backend, &path, "fn buffer_only() {}\n");
        if parse_failure {
            std::fs::write(&path, "fn new_disk() {}\n".repeat(8)).unwrap();
            backend.state.config.write().max_file_bytes = 64;
        }
        watched(backend, &path, FileChangeType::CHANGED).await;
        assert_eq!(
            backend.state.index.read().definition_count("buffer_only"),
            1
        );
        assert_eq!(
            backend.state.workspace.read().files[&path].functions[0].name,
            "buffer_only"
        );
        assert_eq!(
            backend.state.parsed_for_uri(&uri).unwrap().functions[0].name,
            "buffer_only"
        );
    }
}
