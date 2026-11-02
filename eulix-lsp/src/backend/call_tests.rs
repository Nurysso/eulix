// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use super::*;
use tower_lsp::LspService;

const APP: &str = "def get():\n    pass\n\ndef total(a, b):\n    return a.get() + b.get()\n";

#[tokio::test]
async fn same_line_calls_on_different_receivers_are_listed_once() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let path = PathBuf::from("/workspace/app.py");
    let uri = Url::from_file_path(&path).unwrap();
    let parsed = Arc::new(syntax::parse(&path, APP).unwrap());
    backend.state.commit(path, parsed.clone());
    backend
        .state
        .documents
        .write()
        .open
        .insert(uri.clone(), Document::new(APP.to_string(), 1, Some(parsed)));
    let items = backend
        .prepare_call_hierarchy(CallHierarchyPrepareParams {
            text_document_position_params: TextDocumentPositionParams::new(
                TextDocumentIdentifier::new(uri),
                Position::new(3, 4),
            ),
            work_done_progress_params: Default::default(),
        })
        .await
        .unwrap()
        .unwrap_or_default();
    assert_eq!(items.len(), 1);
    let outgoing = backend
        .outgoing_calls(CallHierarchyOutgoingCallsParams {
            item: items[0].clone(),
            work_done_progress_params: Default::default(),
            partial_result_params: Default::default(),
        })
        .await
        .unwrap()
        .unwrap_or_default();
    assert_eq!(outgoing.len(), 1, "{outgoing:?}");
}
