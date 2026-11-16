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
fn rust_sibling_module_prefers_its_file_to_a_homonym() {
    let g = graph(&[
        (
            "src/lib.rs",
            "mod lines; fn run() { lines::count(); } fn count() {}",
        ),
        ("src/lines.rs", "pub fn count() {}"),
        ("other/src/lines.rs", "pub fn count() {}"),
    ]);
    assert!(edge(&g, "func_run::src/lib.rs", "func_count::src/lines.rs"));
    assert!(!edge(&g, "func_run::src/lib.rs", "func_count::src/lib.rs"));
}
#[test]
fn rust_crate_path_resolves_nested_module() {
    let g = graph(&[
        (
            "src/lib.rs",
            "mod a; fn run() { crate::a::b::f(); } fn f() {}",
        ),
        ("src/a/mod.rs", "pub mod b;"),
        ("src/a/b.rs", "pub fn f() {}"),
    ]);
    assert!(edge(&g, "func_run::src/lib.rs", "func_f::src/a/b.rs"));
}
#[test]
fn rust_super_resolves_parent_module() {
    let g = graph(&[
        ("src/lib.rs", "mod a; fn f() {}"),
        ("src/a/mod.rs", "pub mod child; pub fn f() {}"),
        ("src/a/child.rs", "fn run() { super::f(); } fn f() {}"),
    ]);
    assert!(edge(&g, "func_run::src/a/child.rs", "func_f::src/a/mod.rs"));
    assert!(!edge(
        &g,
        "func_run::src/a/child.rs",
        "func_f::src/a/child.rs"
    ));
}
#[test]
fn rust_declared_module_without_file_uses_name_fallback() {
    let g = graph(&[(
        "src/lib.rs",
        "mod generated; fn run() { generated::f(); } fn f() {}",
    )]);
    assert!(edge(&g, "func_run::src/lib.rs", "func_f::src/lib.rs"));
}
#[test]
fn rust_internal_alias_without_module_file_uses_fallback() {
    let g = graph(&[(
        "src/lib.rs",
        "use crate::generated as generated_api; fn run() { generated_api::f(); } fn f() {}",
    )]);
    assert!(edge(&g, "func_run::src/lib.rs", "func_f::src/lib.rs"));
}
#[test]
fn rust_external_alias_is_not_a_project_module_even_with_matching_file() {
    let g = graph(&[
        ("src/lib.rs", "use std::env; fn run() { env::args(); std::env::args(); core::args(); alloc::args(); u32::args(); }"),
        ("src/env.rs", "pub fn args() {}"),
        ("src/core.rs", "pub fn args() {}"),
        ("src/alloc.rs", "pub fn args() {}"),
    ]);
    assert!(!g
        .edges
        .iter()
        .any(|e| e.edge_type == "call" && e.from == "func_run::src/lib.rs"));
}
#[test]
fn rust_sibling_workspace_crate_keeps_the_name_lookup() {
    let g = graph(&[
        (
            "crates/app/src/lib.rs",
            "use util::run; use util as u; pub fn start() { run(); } pub fn qualified() { util::run(); } pub fn aliased() { u::run(); }",
        ),
        ("crates/util/src/lib.rs", "pub fn run() {}"),
        (
            "crates/app/src/out.rs",
            "fn print() { termcolor::BufferWriter::stdout(); } pub fn stdout() {}",
        ),
    ]);
    assert!(!edge(
        &g,
        "func_print::crates/app/src/out.rs",
        "func_stdout::crates/app/src/out.rs"
    ));
    for caller in ["start", "qualified", "aliased"] {
        assert!(
            edge(
                &g,
                &format!("func_{caller}::crates/app/src/lib.rs"),
                "func_run::crates/util/src/lib.rs"
            ),
            "{caller}"
        );
    }
}
#[test]
fn rust_imported_module_alias_stays_in_the_callers_crate() {
    let g = graph(&[
        (
            "one/src/lib.rs",
            "use crate::lines as ln; fn run() { ln::count(); }",
        ),
        ("one/src/lines.rs", "pub fn count() {}"),
        ("aaa/src/lines.rs", "pub fn count() {}"),
    ]);
    assert!(edge(
        &g,
        "func_run::one/src/lib.rs",
        "func_count::one/src/lines.rs"
    ));
    assert!(!edge(
        &g,
        "func_run::one/src/lib.rs",
        "func_count::aaa/src/lines.rs"
    ));
}
#[test]
fn javascript_receiver_does_not_see_typescript_method_names() {
    let g = graph(&[
        ("app.js", "function run(res) { return res.text(); }"),
        ("request.ts", "class Request { text() {} }"),
    ]);
    assert!(!edge(
        &g,
        "func_run::app.js",
        "method_Request_text::request.ts"
    ));
}

#[test]
fn rust_binary_root_does_not_resolve_into_the_library_crate() {
    for file in ["src/main.rs", "src/bin/tool.rs"] {
        let g = graph(&[
            (
                file,
                "fn helper() {} fn run() { self::helper(); crate::helper(); }",
            ),
            ("src/lib.rs", "pub fn helper() {}"),
        ]);
        assert!(edge(
            &g,
            &format!("func_run::{file}"),
            &format!("func_helper::{file}")
        ));
        assert!(!edge(
            &g,
            &format!("func_run::{file}"),
            "func_helper::src/lib.rs"
        ));
    }
}
#[test]
fn javascript_class_receiver_keeps_base_method_keys() {
    let g = graph(&[
        ("app.js", "function run() { Request.text(); }"),
        ("request.ts", "class Request { static text() {} }"),
    ]);
    assert!(!edge(
        &g,
        "func_run::app.js",
        "method_Request_text::request.ts"
    ));
}

#[test]
fn rust_grouped_self_alias_names_the_module_itself() {
    let g = graph(&[
        (
            "src/lib.rs",
            "use crate::lines::{self as ln}; fn run() { ln::count(); } fn count() {}",
        ),
        ("src/lines.rs", "pub fn count() {}"),
    ]);
    assert!(edge(&g, "func_run::src/lib.rs", "func_count::src/lines.rs"));
    assert!(!edge(&g, "func_run::src/lib.rs", "func_count::src/lib.rs"));
}

#[test]
fn rust_grouped_self_without_alias_retains_the_parent_binding() {
    let g = graph(&[
        (
            "src/lib.rs",
            "use crate::outer::{lines::{self}}; fn run() { lines::count(); } fn count() {}",
        ),
        ("src/outer/lines.rs", "pub fn count() {}"),
    ]);
    assert!(edge(
        &g,
        "func_run::src/lib.rs",
        "func_count::src/outer/lines.rs"
    ));
}

#[test]
fn rust_binary_submodule_uses_its_nearest_crate_root() {
    let g = graph(&[
        ("src/bin/tool/main.rs", "mod child; fn helper() {}"),
        ("src/bin/tool/child.rs", "fn run() { crate::helper(); }"),
        ("src/lib.rs", "pub fn helper() {}"),
    ]);
    assert!(edge(
        &g,
        "func_run::src/bin/tool/child.rs",
        "func_helper::src/bin/tool/main.rs"
    ));
    assert!(!edge(
        &g,
        "func_run::src/bin/tool/child.rs",
        "func_helper::src/lib.rs"
    ));
}

#[test]
fn rust_instance_with_a_module_name_does_not_use_module_fallback() {
    let g = graph(&[
        (
            "src/lib.rs",
            "mod reader; fn run(reader: Unknown) { reader.count(); }",
        ),
        ("src/reader.rs", "pub fn count() {}"),
    ]);
    assert!(!edge(
        &g,
        "func_run::src/lib.rs",
        "func_count::src/reader.rs"
    ));
}
#[test]
fn rust_module_fallback_does_not_turn_an_enum_variant_into_a_struct() {
    let g = graph(&[
        (
            "src/lib.rs",
            "mod wire; fn run() { wire::Message::End(wire::End {}); }",
        ),
        (
            "src/wire.rs",
            "pub enum Message { End(End) } pub struct End {}",
        ),
    ]);
    assert!(!edge(&g, "func_run::src/lib.rs", "struct_End::src/wire.rs"));
}

#[test]
fn rust_instance_does_not_use_an_imported_namespace() {
    for (import, receiver) in [
        ("use crate::reader;", "reader"),
        ("use crate::reader as api;", "api"),
    ] {
        let source = format!("{import} fn run({receiver}: Unknown) {{ {receiver}.count(); }}");
        let g = graph(&[
            ("src/lib.rs", &source),
            ("src/reader.rs", "pub fn count() {}"),
        ]);
        assert!(!edge(
            &g,
            "func_run::src/lib.rs",
            "func_count::src/reader.rs"
        ));
    }
}
#[test]
fn rust_exact_module_keeps_tuple_struct_constructors() {
    let g = graph(&[
        (
            "src/lib.rs",
            "use crate::wire as w; fn run() { w::Record(1); }",
        ),
        ("src/wire.rs", "pub struct Record(pub u8);"),
    ]);
    assert!(edge(
        &g,
        "func_run::src/lib.rs",
        "struct_Record::src/wire.rs"
    ));
}

#[test]
fn rust_integration_test_keeps_imported_tuple_struct() {
    let g = graph(&[
        (
            "crates/example/tests/check.rs",
            "mod util; use crate::util::Record; fn run() { Record(1); }",
        ),
        ("crates/example/tests/util.rs", "pub struct Record(pub u8);"),
        ("crates/example/src/lib.rs", "pub struct Record(pub u8);"),
    ]);
    assert!(edge(
        &g,
        "func_run::crates/example/tests/check.rs",
        "struct_Record::crates/example/tests/util.rs"
    ));
    assert!(!edge(
        &g,
        "func_run::crates/example/tests/check.rs",
        "struct_Record::crates/example/src/lib.rs"
    ));
}
#[test]
fn rust_type_from_another_crate_skips_that_crates_test_targets() {
    // A file under another crate's tests/, benches/ or examples/ is its own
    // target and can never be imported, so its homonym must not win.
    let g = graph(&[
        (
            "crates/printer/src/summary.rs",
            "#[cfg(test)] mod tests { use grep_regex::RegexMatcher; #[test] fn t() { let m = RegexMatcher::new(\"a\"); } }",
        ),
        ("crates/regex/src/lib.rs", "mod matcher; pub use matcher::RegexMatcher;"),
        (
            "crates/regex/src/matcher.rs",
            "pub struct RegexMatcher; impl RegexMatcher { pub fn new(p: &str) -> RegexMatcher { RegexMatcher } }",
        ),
        (
            "crates/matcher/tests/util.rs",
            "pub struct RegexMatcher; impl RegexMatcher { pub fn new(p: &str) -> RegexMatcher { RegexMatcher } }",
        ),
    ]);
    assert!(!edge(
        &g,
        "func_t::crates/printer/src/summary.rs",
        "method_RegexMatcher_new::crates/matcher/tests/util.rs"
    ));
    assert!(edge(
        &g,
        "func_t::crates/printer/src/summary.rs",
        "method_RegexMatcher_new::crates/regex/src/matcher.rs"
    ));
}
#[test]
fn rust_imported_crate_name_picks_its_package_among_homonyms() {
    // `grep_regex` is the package in crates/regex; crates/pcre2 has a
    // RegexMatcher too, and neither is the caller's crate.
    let g = graph(&[
        (
            "crates/printer/src/summary.rs",
            "use grep_regex::RegexMatcher; fn t() { let m = RegexMatcher::new(\"a\"); }",
        ),
        (
            "crates/pcre2/src/matcher.rs",
            "pub struct RegexMatcher; impl RegexMatcher { pub fn new(p: &str) -> RegexMatcher { RegexMatcher } }",
        ),
        (
            "crates/regex/src/matcher.rs",
            "pub struct RegexMatcher; impl RegexMatcher { pub fn new(p: &str) -> RegexMatcher { RegexMatcher } }",
        ),
    ]);
    assert!(edge(
        &g,
        "func_t::crates/printer/src/summary.rs",
        "method_RegexMatcher_new::crates/regex/src/matcher.rs"
    ));
    assert!(!edge(
        &g,
        "func_t::crates/printer/src/summary.rs",
        "method_RegexMatcher_new::crates/pcre2/src/matcher.rs"
    ));
}
#[test]
fn rust_impl_in_another_file_gives_its_methods_to_the_type() {
    let g = graph(&[
        (
            "src/lib.rs",
            "mod other; pub struct E; impl E { fn go(&self) { self.show(); Self::make(); } } pub fn run() { E::make(); }",
        ),
        (
            "src/other.rs",
            "use crate::E; impl E { pub fn show(&self) { helper(); } pub fn make() -> E { E } pub fn twice(&self) { self.go(); } } fn helper() {}",
        ),
    ]);
    // ...and `self` in the other impl reaches the type's own methods.
    assert!(edge(
        &g,
        "method_E_twice::src/other.rs",
        "method_E_go::src/lib.rs"
    ));
    // `self` / `Self` in the type's own file reach methods of its other impl.
    assert!(edge(
        &g,
        "method_E_go::src/lib.rs",
        "method_E_show::src/other.rs"
    ));
    assert!(edge(
        &g,
        "method_E_go::src/lib.rs",
        "method_E_make::src/other.rs"
    ));
    assert!(edge(
        &g,
        "func_run::src/lib.rs",
        "method_E_make::src/other.rs"
    ));
    assert!(edge(
        &g,
        "method_E_show::src/other.rs",
        "func_helper::src/other.rs"
    ));
    // The impl-only entry is not a type a call can land on.
    assert!(!g.edges.iter().any(|e| e.to.starts_with("impl_")));
}
#[test]
fn rust_impl_of_a_foreign_type_keeps_its_methods_without_a_owner() {
    let g = graph(&[(
        "src/lib.rs",
        "trait Shout { fn shout(&self); } impl Shout for String { fn shout(&self) { loud(); } } fn loud() {}",
    )]);
    assert!(edge(
        &g,
        "method_String_shout::src/lib.rs",
        "func_loud::src/lib.rs"
    ));
}
