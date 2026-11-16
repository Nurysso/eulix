// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use super::*;
use tower_lsp::LspService;

// VS Code percent-encodes '+' in paths (and the drive colon on Windows);
// `Url::from_file_path` does not. Each test runs the plain spelling first as
// a control, then the encoded one, for the same file path.
fn client_uri(path: &str, encoded: bool) -> Url {
    let plain = Url::from_file_path(path).unwrap();
    if encoded {
        Url::parse(&plain.as_str().replace('+', "%2B")).unwrap()
    } else {
        plain
    }
}

async fn open(backend: &Backend, uri: &Url, text: &str) {
    backend
        .did_open(DidOpenTextDocumentParams {
            text_document: TextDocumentItem::new(uri.clone(), "python".into(), 1, text.into()),
        })
        .await;
}

#[tokio::test]
#[ignore = "known bug: rename finds call-site buffers through a rebuilt URI"]
async fn rename_edits_call_sites_for_client_encoded_uris() {
    for encoded in [false, true] {
        let (service, _) = LspService::new(Backend::new);
        let backend = service.inner();
        let uri = client_uri("/work/c++/app.py", encoded);
        open(
            backend,
            &uri,
            "def get():\n    pass\n\ndef caller():\n    return get()\n",
        )
        .await;
        let edit = backend
            .rename(RenameParams {
                text_document_position: TextDocumentPositionParams::new(
                    TextDocumentIdentifier::new(uri.clone()),
                    Position::new(0, 4),
                ),
                new_name: "fetch".into(),
                work_done_progress_params: Default::default(),
            })
            .await
            .unwrap()
            .unwrap();
        let mut lines: Vec<u32> = edit
            .changes
            .unwrap()
            .values()
            .flatten()
            .map(|e| e.range.start.line)
            .collect();
        lines.sort();
        assert_eq!(
            lines,
            [0, 4],
            "{uri}: rename must edit the declaration and its call"
        );
    }
}

#[tokio::test]
#[ignore = "known bug: location_for finds the target buffer through a rebuilt URI"]
async fn cross_file_definition_selects_the_name_for_client_encoded_uris() {
    for encoded in [false, true] {
        let (service, _) = LspService::new(Backend::new);
        let backend = service.inner();
        let lib = client_uri("/work/c++/lib.py", encoded);
        let app = client_uri("/work/c++/app.py", encoded);
        open(backend, &lib, "def helper():\n    pass\n").await;
        open(backend, &app, "def main():\n    return helper()\n").await;
        let Some(GotoDefinitionResponse::Link(links)) = backend
            .goto_definition(GotoDefinitionParams {
                text_document_position_params: TextDocumentPositionParams::new(
                    TextDocumentIdentifier::new(app),
                    Position::new(1, 12),
                ),
                work_done_progress_params: Default::default(),
                partial_result_params: Default::default(),
            })
            .await
            .unwrap()
        else {
            panic!("{lib}: no definition found");
        };
        assert_eq!(
            links[0].target_selection_range,
            Range::new(Position::new(0, 4), Position::new(0, 10)),
            "{lib}: the jump must select `helper`, not the whole line"
        );
    }
}
