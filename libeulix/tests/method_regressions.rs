// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use libeulix::analyze::call_graph::{prism_v1, prism_v2::build_call_graph_v2};
use libeulix::grammar_files::parse_source;
use libeulix::struc::kb_struct::*;
use std::collections::BTreeMap;
use std::path::Path;

fn structure(sources: &[(&str, &str)]) -> BTreeMap<String, FileData> {
    sources
        .iter()
        .map(|(path, source)| {
            (
                path.to_string(),
                parse_source(Path::new(path), source).unwrap().1,
            )
        })
        .collect()
}

fn edge(g: &CallGraph, from: &str, to: &str) -> bool {
    g.edges.iter().any(|e| e.from == from && e.to == to)
}

#[test]
fn unknown_rust_type_import_does_not_bind_a_local_homonym() {
    let g = build_call_graph_v2(&structure(&[
        ("src/lib.rs", "mod local; use termcolor::BufferWriter; use util::run; fn external() { BufferWriter::stdout(); } fn start() { run(); util::run(); }"),
        ("src/local.rs", "pub struct BufferWriter; impl BufferWriter { pub fn stdout() {} }"),
        ("util/src/lib.rs", "pub fn run() {}"),
    ]));
    assert!(!edge(
        &g,
        "func_external::src/lib.rs",
        "method_BufferWriter_stdout::src/local.rs"
    ));
    assert!(edge(
        &g,
        "func_start::src/lib.rs",
        "func_run::util/src/lib.rs"
    ));
}

#[test]
fn rust_unknown_crate_type_resolves_outside_the_callers_crate() {
    let g = build_call_graph_v2(&structure(&[
        (
            "crates/app/src/lib.rs",
            "mod local; use util::Builder; fn run() { Builder::new(); }",
        ),
        (
            "crates/app/src/local.rs",
            "pub struct Builder; impl Builder { pub fn new() {} }",
        ),
        (
            "crates/util/src/lib.rs",
            "pub struct Builder; impl Builder { pub fn new() {} }",
        ),
    ]));
    assert!(edge(
        &g,
        "func_run::crates/app/src/lib.rs",
        "method_Builder_new::crates/util/src/lib.rs"
    ));
    assert!(!edge(
        &g,
        "func_run::crates/app/src/lib.rs",
        "method_Builder_new::crates/app/src/local.rs"
    ));
}

#[test]
fn rust_standalone_targets_can_import_the_package_type_by_crate_name() {
    for (caller, other_target) in [
        ("crates/util/benches/one.rs", "crates/util/benches/two.rs"),
        ("crates/util/tests/one.rs", "crates/util/tests/two.rs"),
        ("crates/util/examples/one.rs", "crates/util/examples/two.rs"),
    ] {
        let g = build_call_graph_v2(&structure(&[
            (caller, "use util::Builder; fn run() { Builder::new(); }"),
            (
                "crates/util/src/lib.rs",
                "pub struct Builder; impl Builder { pub fn new() {} }",
            ),
        ]));
        assert!(
            edge(
                &g,
                &format!("func_run::{caller}"),
                "method_Builder_new::crates/util/src/lib.rs"
            ),
            "{caller}"
        );

        // Separate standalone targets are distinct crates, despite sharing
        // a directory. A type in the caller file remains excluded.
        let g = build_call_graph_v2(&structure(&[
            (caller, "use util::Builder; mod local { pub struct Builder; impl Builder { pub fn new() {} } } fn run() { Builder::new(); }"),
            (other_target, "pub struct Builder; impl Builder { pub fn new() {} }"),
        ]));
        assert!(
            edge(
                &g,
                &format!("func_run::{caller}"),
                &format!("method_Builder_new::{other_target}")
            ),
            "{caller}"
        );
        assert!(
            !edge(
                &g,
                &format!("func_run::{caller}"),
                &format!("method_Builder_new::{caller}")
            ),
            "{caller}"
        );
    }
}

#[test]
fn rust_foreign_type_aliases_and_tuple_constructors_exclude_local_homonyms() {
    let g = build_call_graph_v2(&structure(&[
        (
            "crates/app/src/lib.rs",
            "mod local; use util::{Builder as B, Record as R}; fn run() { B::new(); R(1); }",
        ),
        (
            "crates/app/src/local.rs",
            "pub struct Builder; impl Builder { pub fn new() {} } pub struct Record(pub u8);",
        ),
        (
            "crates/util/src/lib.rs",
            "pub struct Builder; impl Builder { pub fn new() {} } pub struct Record(pub u8);",
        ),
    ]));
    assert!(edge(
        &g,
        "func_run::crates/app/src/lib.rs",
        "method_Builder_new::crates/util/src/lib.rs"
    ));
    assert!(edge(
        &g,
        "func_run::crates/app/src/lib.rs",
        "struct_Record::crates/util/src/lib.rs"
    ));
    assert!(!g
        .edges
        .iter()
        .any(|e| e.from == "func_run::crates/app/src/lib.rs"
            && e.to.ends_with("crates/app/src/local.rs")));
}

#[test]
fn rust_foreign_lookup_excludes_binary_and_standalone_child_modules() {
    for (caller, child) in [
        ("src/main.rs", "src/local.rs"),
        ("examples/one.rs", "examples/local/mod.rs"),
        ("tests/one.rs", "tests/local.rs"),
    ] {
        let g = build_call_graph_v2(&structure(&[
            ("src/lib.rs", "pub fn library() {}"),
            (
                caller,
                "mod local; use termcolor::BufferWriter; fn external() { BufferWriter::stdout(); }",
            ),
            (
                child,
                "pub struct BufferWriter; impl BufferWriter { pub fn stdout() {} }",
            ),
        ]));
        assert!(
            !edge(
                &g,
                &format!("func_external::{caller}"),
                &format!("method_BufferWriter_stdout::{child}")
            ),
            "{caller}"
        );
    }
}

#[test]
fn rust_constructor_receivers_keep_the_foreign_type_binding() {
    let g = build_call_graph_v2(&structure(&[
        (
            "src/lib.rs",
            "mod local; use util::BufferWriter as BW; fn run() { BW().stdout(); }",
        ),
        (
            "src/local.rs",
            "pub struct BW(); impl BW { pub fn stdout(&self) {} }",
        ),
        (
            "util/src/lib.rs",
            "pub struct BufferWriter(); impl BufferWriter { pub fn stdout(&self) {} }",
        ),
    ]));
    assert!(edge(
        &g,
        "func_run::src/lib.rs",
        "method_BufferWriter_stdout::util/src/lib.rs"
    ));
    assert!(!edge(
        &g,
        "func_run::src/lib.rs",
        "method_BW_stdout::src/local.rs"
    ));
}

#[test]
fn rust_external_alias_chains_do_not_become_unknown_crate_type_imports() {
    for import in [
        "use std::io as external_io; use external_io::Error;",
        "use std as external; use external::io::Error;",
    ] {
        let source = format!("{import} fn external() {{ Error::new(); }}");
        let g = build_call_graph_v2(&structure(&[
            ("src/lib.rs", &source),
            (
                "util/src/lib.rs",
                "pub struct Error; impl Error { pub fn new() {} }",
            ),
        ]));
        assert!(!edge(
            &g,
            "func_external::src/lib.rs",
            "method_Error_new::util/src/lib.rs"
        ));
    }
}

#[test]
fn rust_public_local_type_import_is_not_treated_as_a_foreign_root() {
    let g = build_call_graph_v2(&structure(&[
        ("src/lib.rs", "mod local; pub use self::local::Choice; fn run() { Choice::new(); } #[cfg(test)] mod tests { use pkg::Choice; }"),
        ("src/local.rs", "pub struct Choice; impl Choice { pub fn new() {} }"),
    ]));
    assert!(edge(
        &g,
        "func_run::src/lib.rs",
        "method_Choice_new::src/local.rs"
    ));
}

#[test]
fn round5c_declared_rust_module_survives_inline_test_imports() {
    for imports in [
        "use std::env;",
        "use std::io as external_io; use external_io as env;",
        "use util::Other as env;",
    ] {
        let source = format!("mod env; fn run() {{ env::args(); }} mod tests {{ {imports} }}");
        let g = build_call_graph_v2(&structure(&[
            ("src/lib.rs", &source),
            ("src/env.rs", "pub fn args() {}"),
        ]));
        assert!(
            edge(&g, "func_run::src/lib.rs", "func_args::src/env.rs"),
            "{imports}"
        );
    }
}

#[test]
fn round5c_local_class_beats_import_as_inheritance_base() {
    for (path, source, helper_path, helper, prefix) in [
        ("app.py", "from helpers import Base\nclass Base: pass\nclass Child(Base): pass\n", "helpers.py", "class Base: pass\n", "class"),
        ("app.ts", "import { Base } from './helpers'; class Base {} class Child extends Base {}", "helpers.ts", "export class Base {}", "class"),
        ("src/lib.rs", "mod sub; pub trait Base {} pub trait Child: Base {} mod tests { use crate::sub::Base; }", "src/sub.rs", "pub trait Base {}", "trait"),
    ] {
        let g = build_call_graph_v2(&structure(&[(path, source), (helper_path, helper)]));
        assert!(edge(&g, &format!("{prefix}_Child::{path}"), &format!("{prefix}_Base::{path}")), "{path}");
        assert!(!edge(&g, &format!("{prefix}_Child::{path}"), &format!("{prefix}_Base::{helper_path}")), "{path}");
    }
}

#[test]
fn round5c_local_function_beats_an_import_in_receiver_languages() {
    for (path, source, helper_path, helper) in [
        ("src/lib.rs", "mod sub; pub fn action() {} pub fn run() { action(); } mod tests { use crate::sub::action; }", "src/sub.rs", "pub fn action() {}"),
        ("app.ts", "import { action } from './helpers'; function action() {} function run() { action(); }", "helpers.ts", "export function action() {}"),
        ("app.py", "from helpers import action\ndef action(): pass\ndef run(): action()\n", "helpers.py", "def action(): pass\n"),
    ] {
        let g = build_call_graph_v2(&structure(&[(path, source), (helper_path, helper)]));
        assert!(edge(&g, &format!("func_run::{path}"), &format!("func_action::{path}")), "{path}");
        assert!(!edge(&g, &format!("func_run::{path}"), &format!("func_action::{helper_path}")), "{path}");
    }
}

#[test]
fn round5c_rust_module_and_imported_function_keep_separate_bindings() {
    for (import, target) in [
        ("use crate::sub::action;", "func_action::src/sub.rs"),
        ("use crate::sub::other as action;", "func_other::src/sub.rs"),
    ] {
        let source = format!(
            "mod action; mod a; mod sub; {import} pub fn run() {{ action(); action::child(); }}"
        );
        let g = build_call_graph_v2(&structure(&[
            ("src/lib.rs", &source),
            ("src/action.rs", "pub fn child() {}"),
            ("src/a.rs", "pub fn action() {}"),
            ("src/sub.rs", "pub fn action() {} pub fn other() {}"),
        ]));
        assert!(edge(&g, "func_run::src/lib.rs", target), "{import}");
        assert!(edge(
            &g,
            "func_run::src/lib.rs",
            "func_child::src/action.rs"
        ));
        assert!(!edge(&g, "func_run::src/lib.rs", "func_action::src/a.rs"));
    }
}

#[test]
fn legacy_callers_do_not_see_receiver_language_method_keys() {
    for (path, source) in [
        (
            "app.go",
            "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hello\") }",
        ),
        ("app.c", "void main() { Println(); }"),
        ("app.cpp", "void main() { logger.Println(); }"),
        (
            "app.java",
            "class App { void main() { logger.Println(); } }",
        ),
        ("app.js", "function main() { logger.Println(); }"),
    ] {
        let g = build_call_graph_v2(&structure(&[
            (path, source),
            ("box.ts", "class Logger { Println(message: string) {} }"),
        ]));
        assert!(
            !g.edges
                .iter()
                .any(|e| e.to == "method_Logger_Println::box.ts"),
            "{path}"
        );
    }
}

#[test]
fn declared_rust_core_module_takes_precedence_over_external_roots() {
    let g = build_call_graph_v2(&structure(&[
        ("src/lib.rs", "use core::action; use core as api; mod core; fn run() { core::action(); } fn imported() { action(); } fn aliased() { api::action(); }"),
        ("src/core.rs", "pub fn action() {}"),
    ]));
    assert!(edge(&g, "func_run::src/lib.rs", "func_action::src/core.rs"));
    assert!(edge(
        &g,
        "func_imported::src/lib.rs",
        "func_action::src/core.rs"
    ));
    assert!(edge(
        &g,
        "func_aliased::src/lib.rs",
        "func_action::src/core.rs"
    ));
}

#[test]
fn python_relative_package_imports_resolve_calls_and_bases() {
    let g = build_call_graph_v2(&structure(&[
        ("pkg/__init__.py", "class Base: pass\ndef action(): pass\n"),
        (
            "pkg/app.py",
            "from . import action, Base\nclass Child(Base): pass\ndef run(): action()\n",
        ),
        (
            "pkg/sub/app.py",
            "from .. import action\ndef run(): action()\n",
        ),
        (
            "wrong/__init__.py",
            "class Base: pass\ndef action(): pass\n",
        ),
    ]));
    assert!(edge(
        &g,
        "func_run::pkg/app.py",
        "func_action::pkg/__init__.py"
    ));
    assert!(edge(
        &g,
        "class_Child::pkg/app.py",
        "class_Base::pkg/__init__.py"
    ));
    assert!(edge(
        &g,
        "func_run::pkg/sub/app.py",
        "func_action::pkg/__init__.py"
    ));
    assert!(!g.edges.iter().any(|e| e.to.ends_with("wrong/__init__.py")));
}

#[test]
fn typescript_parent_import_reaches_the_root_index() {
    let g = build_call_graph_v2(&structure(&[
        ("index.ts", "export function rootHelper() {}"),
        (
            "sub/app.ts",
            "import { rootHelper } from '..'; export function run() { rootHelper(); }",
        ),
    ]));
    assert!(edge(
        &g,
        "func_run::sub/app.ts",
        "func_rootHelper::index.ts"
    ));
}

#[test]
fn python_top_level_definition_shadows_an_earlier_import() {
    let g = build_call_graph_v2(&structure(&[
        (
            "app.py",
            "from helpers import action\ndef action(): pass\ndef run(): action()\n",
        ),
        ("helpers.py", "def action(): pass\n"),
    ]));
    assert!(edge(&g, "func_run::app.py", "func_action::app.py"));
    assert!(!edge(&g, "func_run::app.py", "func_action::helpers.py"));
}

#[test]
fn typescript_inheritance_follows_reexported_classes() {
    let g = build_call_graph_v2(&structure(&[
        ("base.ts", "export class Base<T> { inherited() {} }"),
        ("index.ts", "export { Base as PublicBase } from './base';"),
        ("app.ts", "import { PublicBase as Alias } from './index'; export class Child extends Alias<number> { run() { this.inherited(); } }"),
        ("wrong.ts", "export class Alias {}"),
    ]));
    assert!(edge(&g, "class_Child::app.ts", "class_Base::base.ts"));
    assert!(edge(
        &g,
        "method_Child_run::app.ts",
        "method_Base_inherited::base.ts"
    ));
    assert!(!edge(&g, "class_Child::app.ts", "class_Alias::wrong.ts"));
}

#[test]
fn round5_review_constructor_receiver_keeps_the_existing_function_fallback() {
    let g = build_call_graph_v2(&structure(&[
        ("base.ts", "export class Base { send() {} }"),
        ("index.ts", "export { Base } from './base';"),
        (
            "app.ts",
            "import { Base } from './index'; function run() { new Base().send(); }",
        ),
        ("unrelated.ts", "export function send() {}"),
    ]));
    assert!(edge(&g, "func_run::app.ts", "func_send::unrelated.ts"));
    assert!(!edge(&g, "func_run::app.ts", "method_Base_send::base.ts"));
}

#[test]
fn round5_review_nested_super_import_does_not_block_the_local_rust_type() {
    let g = build_call_graph_v2(&structure(&[
        ("src/lib.rs", "pub struct Foo; impl Foo { pub fn new() {} } fn run() { Foo::new(); } #[cfg(test)] mod tests { use super::Foo; }"),
    ]));
    assert!(edge(
        &g,
        "func_run::src/lib.rs",
        "method_Foo_new::src/lib.rs"
    ));
}

#[test]
fn prism_v1_lists_same_line_calls_and_reverse_callers_once() {
    let mut kb = KnowledgeBase {
        metadata: Metadata {
            version: String::new(), git_hash: String::new(), project_name: String::new(),
            project_hash: String::new(), parsed_at: String::new(), languages: vec![],
            total_files: 0, total_loc: 0, total_functions: 0, total_classes: 0, total_methods: 0,
        },
        structure: structure(&[
            ("app.py", "import helper as a\nimport helper as b\ndef total():\n    a.get() + b.get()\n    a.get()\n"),
            ("helper.py", "def get(): pass\n"),
        ]),
        call_graph: Default::default(), dependency_graph: Default::default(),
        indices: Default::default(), entry_points: vec![], external_dependencies: vec![],
        patterns: Default::default(),
    };
    prism_v1::resolve_call_locations(&mut kb);
    let g = prism_v1::build_call_graph(&kb.structure);
    assert_eq!(g.edges.len(), 2);
    assert_eq!(
        g.nodes
            .iter()
            .find(|n| n.id == "func_get::helper.py")
            .unwrap()
            .call_count_estimate,
        2
    );
    prism_v1::populate_called_by(&mut kb);
    assert_eq!(kb.structure["helper.py"].functions[0].called_by.len(), 2);
}
