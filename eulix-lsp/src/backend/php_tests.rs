// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use super::*;
use tower_lsp::LspService;

const BASE: &str = r#"<?php
namespace Vendor;
class Base { // @base
    public static function help() {} // @help
}
function task() {} // @task
"#;
const APP: &str = r#"<?php
namespace App;
use Vendor\Base as Alias;
class Other {
    public function own() {} // @other
    public function fallback() {} // @fallback
}
class Child extends Alias {
    public function own() {} // @own
    public function run($unknown) { // @run
        $this->OWN(); // @this
        self::own(); // @self
        static::OwN(); // @static
        parent::HELP(); // @parent
        Alias::help(); // @alias
        \vEnDoR\bAsE::HELP(); // @qualified
        $typed = new Alias(); // @new
        $typed->help(); // @typed
        $unknown->fallback(); // @unknown
        \VeNdOr\TASK(); // @function
        localTask(); // @local
        \Missing\Child::own(); // @missing
    }
}
function localTask() {} // @localdecl
function task() {} // @wrongfunction
"#;

fn install(backend: &Backend, name: &str, source: &str) -> Url {
    let path = PathBuf::from(format!("/workspace/{name}"));
    let uri = Url::from_file_path(&path).unwrap();
    let parsed = Arc::new(syntax::parse(&path, source).unwrap());
    backend.state.commit(path, parsed.clone());
    backend.state.documents.write().open.insert(
        uri.clone(),
        Document::new(source.to_string(), 1, Some(parsed)),
    );
    uri
}

fn line(source: &str, marker: &str) -> u32 {
    source.lines().position(|l| l.ends_with(marker)).unwrap() as u32
}

fn at(uri: &Url, source: &str, marker: &str, name: &str) -> TextDocumentPositionParams {
    let line = line(source, marker);
    let text = source.lines().nth(line as usize).unwrap();
    let character = text
        .to_ascii_lowercase()
        .find(&name.to_ascii_lowercase())
        .unwrap() as u32;
    TextDocumentPositionParams::new(
        TextDocumentIdentifier::new(uri.clone()),
        Position::new(line, character),
    )
}

async fn prepare(backend: &Backend, pos: TextDocumentPositionParams) -> Vec<CallHierarchyItem> {
    backend
        .prepare_call_hierarchy(CallHierarchyPrepareParams {
            text_document_position_params: pos,
            work_done_progress_params: Default::default(),
        })
        .await
        .unwrap()
        .unwrap_or_default()
}

async fn refs(backend: &Backend, pos: TextDocumentPositionParams) -> Vec<Location> {
    backend
        .references(ReferenceParams {
            text_document_position: pos,
            context: ReferenceContext {
                include_declaration: true,
            },
            work_done_progress_params: Default::default(),
            partial_result_params: Default::default(),
        })
        .await
        .unwrap()
        .unwrap_or_default()
}

async fn check_php_edge(marker: &str, name: &str, target_src: &str, target_marker: &str) {
    let (service, _) = LspService::new(Backend::new);
    let backend = service.inner();
    let app = install(backend, "app.php", APP);
    let base = install(backend, "base.php", BASE);
    let target = if target_src == APP { &app } else { &base };
    let target_name = if marker == "@new" { "Base" } else { name };
    let expected = line(target_src, target_marker);
    let target_pos = at(target, target_src, target_marker, target_name);
    let references = refs(backend, target_pos.clone()).await;
    assert!(
        references
            .iter()
            .any(|r| r.uri == app && r.range.start.line == line(APP, marker)),
        "{marker}: missing reference: {references:?}"
    );
    let items = prepare(backend, target_pos).await;
    assert_eq!(items.len(), 1, "declaration must select one symbol");
    let incoming = backend
        .incoming_calls(CallHierarchyIncomingCallsParams {
            item: items[0].clone(),
            work_done_progress_params: Default::default(),
            partial_result_params: Default::default(),
        })
        .await
        .unwrap()
        .unwrap_or_default();
    assert!(
        incoming.iter().any(|c| c.from.uri == app
            && c.from_ranges
                .iter()
                .any(|r| r.start.line == line(APP, marker))),
        "{marker}: missing incoming call"
    );
    let callers = prepare(backend, at(&app, APP, "@run", "run")).await;
    let outgoing = backend
        .outgoing_calls(CallHierarchyOutgoingCallsParams {
            item: callers[0].clone(),
            work_done_progress_params: Default::default(),
            partial_result_params: Default::default(),
        })
        .await
        .unwrap()
        .unwrap_or_default();
    assert!(
        outgoing.iter().any(|c| c.to.uri == *target
            && c.to.range.start.line == expected
            && c.from_ranges
                .iter()
                .any(|r| r.start.line == line(APP, marker))),
        "{marker}: missing outgoing call: {outgoing:?}"
    );
    let call_name = if marker == "@new" { "Alias" } else { name };
    let prepared = prepare(backend, at(&app, APP, marker, call_name)).await;
    assert_eq!(prepared.len(), 1, "prepare at call site: {marker}");
    assert_eq!(
        (&prepared[0].uri, prepared[0].range.start.line),
        (target, expected)
    );
}

#[tokio::test]
async fn php_this_self_static_keep_the_enclosing_class() {
    for marker in ["@this", "@self", "@static"] {
        check_php_edge(marker, "own", APP, "@own").await;
    }
}

#[tokio::test]
async fn php_parent_alias_and_qualified_calls_resolve_across_files() {
    for marker in ["@parent", "@alias", "@qualified", "@typed"] {
        check_php_edge(marker, "help", BASE, "@help").await;
    }
}

#[tokio::test]
async fn php_unknown_receiver_resolves_a_unique_method_name() {
    check_php_edge("@unknown", "fallback", APP, "@fallback").await;
}

#[tokio::test]
async fn php_new_references_the_qualified_class() {
    check_php_edge("@new", "Alias", BASE, "@base").await;
}

#[tokio::test]
async fn php_namespaced_functions_are_case_insensitive() {
    check_php_edge("@function", "task", BASE, "@task").await;
    check_php_edge("@local", "localTask", APP, "@localdecl").await;
}

#[tokio::test]
async fn php_known_receivers_do_not_leak_to_unrelated_declarations() {
    let (service, _) = LspService::new(Backend::new);
    let b = service.inner();
    let app = install(b, "app.php", APP);
    install(b, "base.php", BASE);
    for (marker, name) in [("@other", "own"), ("@wrongfunction", "task")] {
        let found = refs(b, at(&app, APP, marker, name)).await;
        assert_eq!(found.len(), 1, "unrelated declaration: {found:?}");
        assert_eq!(found[0].range.start.line, line(APP, marker));
    }
    assert!(prepare(b, at(&app, APP, "@missing", "own"))
        .await
        .is_empty());
}

#[tokio::test]
async fn php_inherited_methods_resolve_after_parent_edits_and_removal() {
    let source = APP.replace("$this->OWN(); // @this", "$this->help(); // @this");
    let (service, _) = LspService::new(Backend::new);
    let b = service.inner();
    let app = install(b, "app.php", &source);
    let base = install(b, "base.php", BASE);
    let pos = at(&app, &source, "@this", "help");
    let items = prepare(b, pos.clone()).await;
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].uri, base);
    let changed = BASE.replace("help()", "renamed()");
    install(b, "base.php", &changed);
    assert!(prepare(b, pos.clone()).await.is_empty());
    install(b, "base.php", BASE);
    assert_eq!(prepare(b, pos.clone()).await.len(), 1);
    b.state.forget(&base.to_file_path().unwrap());
    assert!(prepare(b, pos).await.is_empty());
}

#[tokio::test]
async fn php_prepare_does_not_resolve_arbitrary_words_on_a_call_line() {
    let (service, _) = LspService::new(Backend::new);
    let b = service.inner();
    let app = install(b, "app.php", APP);
    install(b, "base.php", BASE);
    assert!(prepare(b, at(&app, APP, "@new", "typed")).await.is_empty());
    assert!(prepare(b, at(&app, APP, "@unknown", "unknown"))
        .await
        .is_empty());
}

#[tokio::test]
async fn python_method_reference_and_hierarchy_control() {
    let source = "class Router:\n    def helper(self):\n        return 7\n    def dispatch(self):\n        return self.helper()\n";
    let (service, _) = LspService::new(Backend::new);
    let b = service.inner();
    let uri = install(b, "app.py", source);
    let pos =
        TextDocumentPositionParams::new(TextDocumentIdentifier::new(uri), Position::new(1, 8));
    assert_eq!(refs(b, pos.clone()).await.len(), 2);
    let items = prepare(b, pos).await;
    let incoming = b
        .incoming_calls(CallHierarchyIncomingCallsParams {
            item: items[0].clone(),
            work_done_progress_params: Default::default(),
            partial_result_params: Default::default(),
        })
        .await
        .unwrap()
        .unwrap();
    assert_eq!(incoming.len(), 1);
}

#[tokio::test]
async fn php_attributed_declarations_are_selectable() {
    let source = "<?php\n#[Example]\nclass Widget { // @class\n    #[Example]\n    function helper() {} // @helper\n    function run() {\n        $this->helper(); // @call\n    }\n}\n#[Example]\nfunction task() {} // @task\ntask();\nnew Widget();\n";
    let (service, _) = LspService::new(Backend::new);
    let b = service.inner();
    let uri = install(b, "attributes.php", source);
    for (marker, name) in [
        ("@class", "Widget"),
        ("@helper", "helper"),
        ("@task", "task"),
    ] {
        let pos = at(&uri, source, marker, name);
        assert_eq!(prepare(b, pos.clone()).await.len(), 1, "{name}");
        assert_eq!(refs(b, pos).await.len(), 2, "{name}");
    }
}

#[tokio::test]
async fn php_later_namespace_script_calls_fall_back_only_when_unqualified() {
    let source = "<?php\nnamespace { function task() {} } // @task\nnamespace A { task(); } // @first\nnamespace B { task(); } // @later\nnamespace C { \\Missing\\task(); } // @absolute\n";
    let (service, _) = LspService::new(Backend::new);
    let b = service.inner();
    let uri = install(b, "script.php", source);
    let found = refs(b, at(&uri, source, "@task", "task")).await;
    assert_eq!(found.len(), 3, "{found:?}");
    assert!(prepare(b, at(&uri, source, "@absolute", "task"))
        .await
        .is_empty());
}

#[tokio::test]
async fn php_same_line_call_keeps_its_known_receiver() {
    let source = "<?php\nclass A { public static function foo() {} }\nclass B { public function foo() { A::foo(); } }\n";
    let (service, _) = LspService::new(Backend::new);
    let b = service.inner();
    let uri = install(b, "compact.php", source);
    let text = source.lines().nth(2).unwrap();
    let pos = TextDocumentPositionParams::new(
        TextDocumentIdentifier::new(uri),
        Position::new(2, text.rfind("foo").unwrap() as u32),
    );
    let items = prepare(b, pos.clone()).await;
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].range.start.line, 1);
    assert_eq!(refs(b, pos).await.len(), 2);
}

#[tokio::test]
async fn php_unknown_receiver_requires_a_unique_method_ignoring_case() {
    let a_src = "<?php\nclass A {\n    function get() {} // @get\n    function unico() {} // @unique\n}\nfunction unico() {}\n";
    let b_src = "<?php\nclass B {\n    function GeT() {} // @get\n}\n";
    let calls =
        "<?php\nfunction run($x) {\n    $x->gEt(); // @ambiguous\n    $x->UNICO(); // @unique\n}\n";
    let (service, _) = LspService::new(Backend::new);
    let b = service.inner();
    let a_uri = install(b, "a.php", a_src);
    let b_uri = install(b, "b.php", b_src);
    let caller = install(b, "calls.php", calls);
    for (uri, source) in [(&a_uri, a_src), (&b_uri, b_src)] {
        let pos = at(uri, source, "@get", "get");
        // Only the declaration: the ambiguous receiver is not a reference.
        assert_eq!(refs(b, pos.clone()).await.len(), 1);
        let item = prepare(b, pos).await.remove(0);
        let incoming = b
            .incoming_calls(CallHierarchyIncomingCallsParams {
                item,
                work_done_progress_params: Default::default(),
                partial_result_params: Default::default(),
            })
            .await
            .unwrap()
            .unwrap_or_default();
        assert!(incoming.is_empty());
    }
    assert!(prepare(b, at(&caller, calls, "@ambiguous", "gEt"))
        .await
        .is_empty());
    let unique = prepare(b, at(&caller, calls, "@unique", "UNICO")).await;
    assert_eq!(unique.len(), 1);
    assert_eq!(unique[0].uri, a_uri);
    assert_eq!(unique[0].range.start.line, line(a_src, "@unique"));
    assert_eq!(
        refs(b, at(&a_uri, a_src, "@unique", "unico")).await.len(),
        2
    );
    let incoming = b
        .incoming_calls(CallHierarchyIncomingCallsParams {
            item: unique[0].clone(),
            work_done_progress_params: Default::default(),
            partial_result_params: Default::default(),
        })
        .await
        .unwrap()
        .unwrap();
    assert_eq!(incoming.len(), 1);
    assert_eq!(incoming[0].from.uri, caller);
}

#[tokio::test]
async fn php_unknown_receiver_uniqueness_tracks_reindex_and_removal() {
    let a_src = "<?php\nclass A { function get() {} }\n";
    let b_src = "<?php\nclass B { function GET() {} }\n";
    let calls = "<?php\nfunction run($x) {\n    $x->get(); // @call\n}\n";
    let (service, _) = LspService::new(Backend::new);
    let b = service.inner();
    let caller = install(b, "calls.php", calls);
    let pos = at(&caller, calls, "@call", "get");
    assert!(prepare(b, pos.clone()).await.is_empty());
    let a_uri = install(b, "a.php", a_src);
    assert_eq!(prepare(b, pos.clone()).await.len(), 1);
    let b_uri = install(b, "b.php", b_src);
    assert!(prepare(b, pos.clone()).await.is_empty());
    b.state.forget(&b_uri.to_file_path().unwrap());
    assert_eq!(prepare(b, pos.clone()).await.len(), 1);
    install(b, "a.php", a_src); // replacing the same file must not add a candidate
    assert_eq!(prepare(b, pos.clone()).await.len(), 1);
    let duplicate = install(b, "duplicate.php", a_src); // same qualified symbol, two definitions
    assert!(prepare(b, pos.clone()).await.is_empty());
    b.state.forget(&a_uri.to_file_path().unwrap());
    let remaining = prepare(b, pos.clone()).await;
    assert_eq!(remaining.len(), 1);
    assert_eq!(remaining[0].uri, duplicate);
    install(b, "duplicate.php", &a_src.replace("get()", "renamed()"));
    assert!(prepare(b, pos.clone()).await.is_empty());
    b.state.forget(&duplicate.to_file_path().unwrap());
    install(b, "b.php", b_src);
    assert_eq!(prepare(b, pos).await.len(), 1);
}
