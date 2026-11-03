// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use super::*;
use tower_lsp::LspService;

#[tokio::test]
async fn workspace_symbol_keeps_real_names_for_prefix_queries() {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let path = PathBuf::from("/workspace/demo.rs");
    let parsed = Arc::new(syntax::parse(&path, "fn run() {}\nfn runner() {}\n").unwrap());
    backend.state.commit(path, parsed);
    let symbols = backend
        .symbol(WorkspaceSymbolParams {
            query: "ru".into(),
            ..Default::default()
        })
        .await
        .unwrap()
        .unwrap();
    let names: Vec<_> = symbols.iter().map(|s| s.name.as_str()).collect();
    assert_eq!(names, vec!["run", "runner"]);
}
