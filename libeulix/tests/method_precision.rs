// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
use libeulix::analyze::call_graph::prism_v2::build_call_graph_v2;
use libeulix::grammar_files::parse_source;
use libeulix::struc::kb_struct::CallGraph;
use std::path::Path;

fn graph(sources: &[(&str, &str)]) -> CallGraph {
    let files = sources
        .iter()
        .map(|(file, source)| {
            (
                file.to_string(),
                parse_source(Path::new(file), source).unwrap().1,
            )
        })
        .collect();
    build_call_graph_v2(&files)
}
fn edge(g: &CallGraph, from: &str, to: &str) -> bool {
    g.edges
        .iter()
        .any(|e| e.edge_type == "call" && e.from == from && e.to == to)
}

#[test]
fn dictionary_items_is_not_a_cookie_jar_method() {
    let g = graph(&[(
        "app.py",
        "class Jar:\n    def items(self): pass\ndef run():\n    d = {}\n    return d.items()\n",
    )]);
    assert!(!edge(&g, "func_run::app.py", "method_Jar_items::app.py"));
}
#[test]
fn bigint_to_string_is_not_an_error_method() {
    let g = graph(&[("app.ts", "class ErrorBox { toString() { return ''; } } function run(value: bigint) { return value.toString(); }")]);
    assert!(!edge(
        &g,
        "func_run::app.ts",
        "method_ErrorBox_toString::app.ts"
    ));
}
#[test]
fn a_missing_class_field_method_does_not_bind_a_different_type() {
    let g = graph(&[("app.ts", "class Context { header = (key: string) => key; } class Request { header(key: string) { return key; } } function run(c: Context) { return c.header('key'); }")]);
    assert!(!edge(
        &g,
        "func_run::app.ts",
        "method_Request_header::app.ts"
    ));
}
#[test]
fn delegated_method_does_not_become_recursion() {
    let g = graph(&[("app.rs", "struct Wrapper; impl Wrapper { fn ucp(&mut self, yes: bool) { self.builder.ucp(yes); } fn recursive(&self) { self.recursive(); } }")]);
    assert!(!edge(
        &g,
        "method_Wrapper_ucp::app.rs",
        "method_Wrapper_ucp::app.rs"
    ));
    assert!(edge(
        &g,
        "method_Wrapper_recursive::app.rs",
        "method_Wrapper_recursive::app.rs"
    ));
}
#[test]
fn dotted_project_module_selects_its_function() {
    let g = graph(&[
        (
            "app.py",
            "import project\ndef run(): return project.utils.rewind_body()\n",
        ),
        ("project/__init__.py", "def marker(): pass\n"),
        ("project/utils.py", "def rewind_body(): pass\n"),
        ("wrong.py", "def rewind_body(): pass\n"),
    ]);
    assert!(edge(
        &g,
        "func_run::app.py",
        "func_rewind_body::project/utils.py"
    ));
    assert!(!edge(&g, "func_run::app.py", "func_rewind_body::wrong.py"));
}
#[test]
fn namespace_import_follows_two_reexport_levels() {
    let g = graph(&[
        (
            "app.ts",
            "import * as core from './core/index.js'; function run() { core._boolean(); }",
        ),
        ("core/index.ts", "export * from './bridge.js';"),
        (
            "core/bridge.ts",
            "export { booleanImpl as _boolean } from './api.js';",
        ),
        ("core/api.ts", "export function booleanImpl() {}"),
        ("wrong.ts", "export function _boolean() {}"),
    ]);
    assert!(edge(
        &g,
        "func_run::app.ts",
        "func_booleanImpl::core/api.ts"
    ));
    assert!(!edge(&g, "func_run::app.ts", "func__boolean::wrong.ts"));
}

#[test]
fn unresolved_path_under_project_import_uses_legacy_name_fallback() {
    let g = graph(&[
        ("app.py", "import project\nimport external_missing\ndef local(): project.unknown.helper()\ndef external(): external_missing.unknown.helper()\n"),
        ("project/__init__.py", "def marker(): pass\n"),
        ("helper.py", "def helper(): pass\n"),
    ]);
    assert!(edge(&g, "func_local::app.py", "func_helper::helper.py"));
    assert!(!edge(&g, "func_external::app.py", "func_helper::helper.py"));
}

#[test]
fn unknown_factory_receiver_does_not_guess_a_method() {
    let g = graph(&[(
        "app.ts",
        "class Remote { uniqueMethod() {} } function run() { factory().uniqueMethod(); }",
    )]);
    assert!(!edge(
        &g,
        "func_run::app.ts",
        "method_Remote_uniqueMethod::app.ts"
    ));
}
#[test]
fn explicit_named_reexport_wins_over_a_wildcard() {
    let g = graph(&[
        (
            "app.ts",
            "import * as core from './index'; function run() { core.publicName(); }",
        ),
        (
            "index.ts",
            "export * from './wrong'; export { actual as publicName } from './right';",
        ),
        ("wrong.ts", "export function publicName() {}"),
        ("right.ts", "export function actual() {}"),
    ]);
    assert!(edge(&g, "func_run::app.ts", "func_actual::right.ts"));
    assert!(!edge(&g, "func_run::app.ts", "func_publicName::wrong.ts"));
}
#[test]
fn reexport_depth_is_bounded_and_cycles_terminate() {
    let g = graph(&[
        ("app.ts", "import * as core from './index'; function run() { core.publicName(); core.missing(); }"),
        ("index.ts", "export * from './one';"),
        ("one.ts", "export * from './two'; export * from './index';"),
        ("two.ts", "export { actual as publicName } from './three';"),
        ("three.ts", "export function actual() {}"),
    ]);
    assert!(!edge(&g, "func_run::app.ts", "func_actual::three.ts"));
}

#[test]
fn known_local_types_are_not_guessed_by_unique_name() {
    for (file, src) in [
        ("app.py", "class Remote:\n    def uniqueMethod(self): pass\ndef run():\n    obj: External = factory()\n    obj.uniqueMethod()\n"),
        ("app.ts", "class Remote { uniqueMethod() {} } function run() { let obj: External = factory(); obj.uniqueMethod(); }"),
        ("app.rs", "struct Remote; impl Remote { fn uniqueMethod(&self) {} } fn run() { let obj: External = factory(); obj.uniqueMethod(); }")
    ] {
        let g = graph(&[(file, src)]);
        assert!(!edge(&g, &format!("func_run::{file}"), &format!("method_Remote_uniqueMethod::{file}")), "{file}");
    }
}

#[test]
fn dotted_modules_keep_aliases_and_the_longest_import_prefix() {
    for import in ["from project import utils as api", "import project.utils"] {
        let receiver = if import.starts_with("from") {
            "api.deep"
        } else {
            "project.utils.deep"
        };
        let app = format!("{import}\ndef helper(): pass\ndef run(): {receiver}.helper()\n");
        let g = graph(&[
            ("app.py", &app),
            ("project/utils/__init__.py", "def marker(): pass\n"),
            ("project/utils/deep.py", "def helper(): pass\n"),
        ]);
        assert!(
            edge(&g, "func_run::app.py", "func_helper::project/utils/deep.py"),
            "{import}"
        );
        assert!(!edge(&g, "func_run::app.py", "func_helper::app.py"));
    }
}
#[test]
fn named_reexport_wins_over_a_private_local_declaration() {
    let g = graph(&[
        (
            "app.ts",
            "import * as core from './index'; function run() { core.publicName(); }",
        ),
        (
            "index.ts",
            "function publicName() {} export { actual as publicName } from './right';",
        ),
        ("right.ts", "export function actual() {}"),
    ]);
    assert!(edge(&g, "func_run::app.ts", "func_actual::right.ts"));
    assert!(!edge(&g, "func_run::app.ts", "func_publicName::index.ts"));
}

#[test]
fn project_imported_symbol_without_class_metadata_uses_legacy_fallback() {
    let g = graph(&[
        ("app.ts", "import { util } from './core/index'; import { api } from 'external_missing'; function run() { util.normalizeParams(); } function external() { api.normalizeParams(); } function nested() { util.deep.normalizeParams(); } function externalNested() { api.deep.normalizeParams(); }"),
        ("core/index.ts", "export * as util from './util';"),
        ("core/util.ts", "export function normalizeParams() {}"),
    ]);
    assert!(edge(
        &g,
        "func_run::app.ts",
        "func_normalizeParams::core/util.ts"
    ));
    assert!(!edge(
        &g,
        "func_external::app.ts",
        "func_normalizeParams::core/util.ts"
    ));
    assert!(edge(
        &g,
        "func_nested::app.ts",
        "func_normalizeParams::core/util.ts"
    ));
    assert!(!edge(
        &g,
        "func_externalNested::app.ts",
        "func_normalizeParams::core/util.ts"
    ));
}

#[test]
fn named_import_uses_the_same_reexport_resolution() {
    let g = graph(&[
        (
            "app.ts",
            "import { original as local } from './index'; function run() { local(); }",
        ),
        ("index.ts", "export { actual as original } from './impl';"),
        ("impl.ts", "export function actual() {}"),
        ("wrong.ts", "export function local() {}"),
    ]);
    assert!(edge(&g, "func_run::app.ts", "func_actual::impl.ts"));
    assert!(!edge(&g, "func_run::app.ts", "func_local::wrong.ts"));
}

#[test]
fn nested_callback_receiver_is_not_inferred_from_a_unique_method() {
    let g = graph(&[("app.ts", "class Context { header = (key: string) => key; } class Request { header(key: string) { return key; } } function middleware() { return (c: Context) => c.header('key'); }")]);
    assert!(!edge(
        &g,
        "func_middleware::app.ts",
        "method_Request_header::app.ts"
    ));
}
