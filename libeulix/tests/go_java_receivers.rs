// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
//
//! Receiver-aware resolution for Go and Java (PRISM-2, PRISM-5, PRISM-10).
use libeulix::analyze::call_graph::prism_v2::{build_with, CallGraphOptions};
use libeulix::grammar_files::parse_source;
use libeulix::struc::kb_struct::CallGraph;
use std::path::Path;

fn graph_in(module: Option<&str>, sources: &[(&str, &str)]) -> CallGraph {
    let files = sources
        .iter()
        .map(|(file, source)| {
            (
                file.to_string(),
                parse_source(Path::new(file), source).unwrap().1,
            )
        })
        .collect();
    let opts = CallGraphOptions {
        go_modules: module
            .map(|m| (String::new(), m.to_string()))
            .into_iter()
            .collect(),
        ..Default::default()
    };
    build_with(&files, &opts)
}
fn graph(sources: &[(&str, &str)]) -> CallGraph {
    graph_in(None, sources)
}
fn edge(g: &CallGraph, from: &str, to: &str) -> bool {
    g.edges
        .iter()
        .any(|e| e.edge_type == "call" && e.from == from && e.to == to)
}
fn targets(g: &CallGraph, from: &str) -> Vec<String> {
    let mut v: Vec<String> = g
        .edges
        .iter()
        .filter(|e| e.edge_type == "call" && e.from == from)
        .map(|e| e.to.clone())
        .collect();
    v.sort();
    v
}

// ─────────────────────────────────── Go ───────────────────────────────────

const GO_STORE: &str = "package demo\n\ntype Store struct{}\n\nfunc (s *Store) Save() { s.load() }\nfunc (s *Store) load() {}\n";

#[test]
fn go_call_on_the_methods_own_receiver_links_to_the_method() {
    let g = graph(&[("store.go", GO_STORE)]);
    assert!(edge(
        &g,
        "method_Store_Save::store.go",
        "method_Store_load::store.go"
    ));
}

#[test]
fn go_call_on_a_variable_of_a_known_struct_type_links_to_the_method() {
    let g = graph(&[
        ("store.go", GO_STORE),
        (
            "use.go",
            "package demo\n\nfunc param(m *Store) { m.Save() }\nfunc value(m Store) { m.Save() }\nfunc literal() {\n\tm := &Store{}\n\tm.Save()\n}\nfunc declared() {\n\tvar m Store\n\tm.Save()\n}\nfunc allocated() {\n\tm := new(Store)\n\tm.Save()\n}\nfunc closure() {\n\trun(func(m *Store) { m.Save() })\n}\nfunc run(f func(*Store)) {}\n",
        ),
    ]);
    for caller in [
        "param",
        "value",
        "literal",
        "declared",
        "allocated",
        "closure",
    ] {
        assert!(
            edge(
                &g,
                &format!("func_{caller}::use.go"),
                "method_Store_Save::store.go"
            ),
            "{caller}: {:?}",
            targets(&g, &format!("func_{caller}::use.go"))
        );
    }
}

#[test]
fn go_call_on_a_variable_of_unknown_type_gets_no_edge() {
    // `Save` is the only method of that name in the project: still no edge.
    // (`m := factory()` is typed from factory's declared result, see
    // go_local_typed_from_the_callees_result.)
    let g = graph(&[
        ("store.go", GO_STORE),
        (
            "use.go",
            "package demo\n\nfunc factory() *Store { return nil }\nfunc inferred() {\n\tm := factory()\n\tm.Save()\n}\nfunc untyped(m interface{ Save() }) { m.Save() }\nfunc chained() { factory().Save() }\nfunc ranged(ms []*Store) {\n\tfor _, m := range ms {\n\t\tm.Save()\n\t}\n}\n",
        ),
    ]);
    assert!(edge(
        &g,
        "func_inferred::use.go",
        "method_Store_Save::store.go"
    ));
    for caller in ["untyped", "chained", "ranged"] {
        assert!(
            !edge(
                &g,
                &format!("func_{caller}::use.go"),
                "method_Store_Save::store.go"
            ),
            "{caller} must not guess"
        );
    }
}

#[test]
fn go_redeclared_variable_has_no_single_type() {
    // The parameter `m` is a Store, the range variable `m` is not.
    let g = graph(&[
        ("store.go", GO_STORE),
        (
            "use.go",
            "package demo\n\ntype Other struct{}\n\nfunc shadow(m *Store, others []Other) {\n\tfor _, m := range others {\n\t\tm.Save()\n\t}\n}\n",
        ),
    ]);
    assert!(targets(&g, "func_shadow::use.go").is_empty());
}

#[test]
fn go_standard_library_call_gets_no_edge_even_with_a_local_homonym() {
    let g = graph(&[(
        "main.go",
        "package main\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc Println(a ...any) {}\nfunc New(s string) error { return nil }\nfunc Exit(code int) {}\n\nfunc run() {\n\tfmt.Println(\"x\")\n\t_ = errors.New(\"x\")\n\tos.Exit(1)\n}\nfunc local() { Println(\"x\") }\n",
    )]);
    assert!(targets(&g, "func_run::main.go").is_empty());
    assert!(edge(&g, "func_local::main.go", "func_Println::main.go"));
}

#[test]
fn go_package_call_resolves_inside_the_imported_directory() {
    let g = graph_in(
        Some("example.com/demo"),
        &[
            (
                "main.go",
                "package main\n\nimport (\n\t\"example.com/demo/sub\"\n\talias \"example.com/demo/other\"\n\t\"github.com/else/sub2\"\n)\n\nfunc Helper() {}\n\nfunc run() {\n\tsub.Helper()\n\talias.Helper()\n\tsub2.Helper()\n}\n",
            ),
            ("sub/sub.go", "package sub\n\nfunc Helper() {}\n"),
            ("other/other.go", "package other\n\nfunc Helper() {}\n"),
            ("vendor/sub2/x.go", "package sub2\n\nfunc Helper() {}\n"),
        ],
    );
    assert_eq!(
        targets(&g, "func_run::main.go"),
        ["func_Helper::other/other.go", "func_Helper::sub/sub.go"]
    );
}

#[test]
fn go_type_from_an_imported_package_types_the_receiver() {
    let g = graph_in(
        Some("example.com/demo"),
        &[
            (
                "main.go",
                "package main\n\nimport \"example.com/demo/db\"\n\ntype Store struct{}\n\nfunc (s *Store) Save() {}\n\nfunc run(m *db.Store) { m.Save() }\n",
            ),
            (
                "db/db.go",
                "package db\n\ntype Store struct{}\n\nfunc (s *Store) Save() {}\n",
            ),
        ],
    );
    assert_eq!(
        targets(&g, "func_run::main.go"),
        ["method_Store_Save::db/db.go"]
    );
}

#[test]
fn go_method_declared_in_another_file_of_the_package_belongs_to_the_type() {
    let g = graph(&[
        ("store.go", "package demo\n\ntype Store struct{}\n"),
        (
            "store_io.go",
            "package demo\n\nfunc (s *Store) Close() {}\nfunc Close() {}\n\nfunc run(s *Store) { s.Close() }\nfunc plain() { Close() }\n",
        ),
    ]);
    assert_eq!(
        targets(&g, "func_run::store_io.go"),
        ["method_Store_Close::store_io.go"]
    );
    assert_eq!(
        targets(&g, "func_plain::store_io.go"),
        ["func_Close::store_io.go"]
    );
}

#[test]
fn go_method_on_a_named_non_struct_type_resolves_through_its_receiver() {
    let g = graph(&[(
        "chain.go",
        "package demo\n\ntype Chain []int\n\nfunc (c Chain) Last() int { return c.size() }\nfunc (c Chain) size() int { return len(c) }\n",
    )]);
    assert!(edge(
        &g,
        "method_Chain_Last::chain.go",
        "method_Chain_size::chain.go"
    ));
}

#[test]
fn go_bare_call_never_reaches_a_method_or_another_package() {
    let g = graph(&[
        (
            "a/a.go",
            "package a\n\ntype T struct{}\n\nfunc (t *T) Close() {}\nfunc (t *T) Run() { Close() }\nfunc plain() { Elsewhere() }\n",
        ),
        ("b/b.go", "package b\n\nfunc Elsewhere() {}\n"),
    ]);
    assert!(targets(&g, "method_T_Run::a/a.go").is_empty());
    assert!(targets(&g, "func_plain::a/a.go").is_empty());
}

#[test]
fn go_embedded_struct_promotes_its_methods() {
    let g = graph(&[(
        "router.go",
        "package demo\n\ntype Group struct{}\n\nfunc (g *Group) GET() {}\n\ntype Engine struct {\n\tGroup\n\tname string\n}\n\nfunc run(e *Engine) { e.GET() }\n",
    )]);
    assert_eq!(
        targets(&g, "func_run::router.go"),
        ["method_Group_GET::router.go"]
    );
}

#[test]
fn go_embedded_field_is_named_after_its_type() {
    let g = graph(&[(
        "router.go",
        "package demo\n\ntype Group struct{}\n\nfunc (g *Group) Use() {}\n\ntype Engine struct {\n\t*Group\n}\n\nfunc (e *Engine) Use() { e.Group.Use() }\n",
    )]);
    assert_eq!(
        targets(&g, "method_Engine_Use::router.go"),
        ["method_Group_Use::router.go"]
    );
}

#[test]
fn go_generic_call_links_to_the_function() {
    let g = graph(&[(
        "typed.go",
        "package demo\n\nfunc getTyped[T any](c int, key string) (res T) { return }\n\nfunc run() string { return getTyped[string](1, \"k\") }\n",
    )]);
    assert_eq!(
        targets(&g, "func_run::typed.go"),
        ["func_getTyped::typed.go"]
    );
}

#[test]
fn go_struct_field_of_a_known_type_types_the_receiver() {
    let g = graph(&[(
        "ctx.go",
        "package demo\n\ntype Engine struct{}\n\nfunc (e *Engine) Reset() {}\n\ntype Ctx struct {\n\tengine *Engine\n\tany    interface{}\n}\n\nfunc (c *Ctx) Run() {\n\tc.engine.Reset()\n\tc.any.Reset()\n\tc.missing.Reset()\n}\n",
    )]);
    assert_eq!(
        targets(&g, "method_Ctx_Run::ctx.go"),
        ["method_Engine_Reset::ctx.go"]
    );
}

#[test]
fn go_interface_receiver_links_to_the_interface_method() {
    let g = graph(&[(
        "saver.go",
        "package demo\n\ntype Saver interface{ Save() }\n\nfunc persist(s Saver) { s.Save() }\n",
    )]);
    assert_eq!(
        targets(&g, "func_persist::saver.go"),
        ["ifacemethod_Save::saver.go"]
    );
}

#[test]
fn go_local_variable_shadows_a_package_function_and_an_import() {
    let g = graph_in(
        Some("example.com/demo"),
        &[
            (
                "main.go",
                "package main\n\nimport \"example.com/demo/sub\"\n\nfunc helper() {}\n\nfunc run() {\n\thelper := func() {}\n\thelper()\n\tsub := pick()\n\tsub.Helper()\n}\nfunc pick() int { return 0 }\n",
            ),
            ("sub/sub.go", "package sub\n\nfunc Helper() {}\n"),
        ],
    );
    assert_eq!(targets(&g, "func_run::main.go"), ["func_pick::main.go"]);
}

// ────────────────────────────────── Java ──────────────────────────────────

#[test]
fn java_static_call_links_through_the_class() {
    let g = graph(&[
        (
            "app/App.java",
            "package app;\npublic class App {\n  static void run() { Util.helper(); util(); }\n  static void util() {}\n}\n",
        ),
        (
            "app/Util.java",
            "package app;\nclass Util { static void helper() {} }\n",
        ),
    ]);
    assert_eq!(
        targets(&g, "method_App_run::app/App.java"),
        [
            "method_App_util::app/App.java",
            "method_Util_helper::app/Util.java"
        ]
    );
}

#[test]
fn java_this_and_bare_calls_resolve_through_the_class() {
    let g = graph(&[(
        "App.java",
        "public class App {\n  void run() { this.one(); two(); }\n  void one() {}\n  void two() {}\n}\nclass Other { void one() {} void two() {} }\n",
    )]);
    assert_eq!(
        targets(&g, "method_App_run::App.java"),
        ["method_App_one::App.java", "method_App_two::App.java"]
    );
}

#[test]
fn java_inherited_calls_resolve_through_the_ancestors() {
    let g = graph(&[
        (
            "p/Child.java",
            "package p;\nclass Child extends Base {\n  void run() { inherited(); this.shared(); super.shared(); }\n  void shared() {}\n}\n",
        ),
        (
            "p/Base.java",
            "package p;\nclass Base extends Root { void shared() {} }\n",
        ),
        ("p/Root.java", "package p;\nclass Root { void inherited() {} }\n"),
    ]);
    assert_eq!(
        targets(&g, "method_Child_run::p/Child.java"),
        [
            "method_Base_shared::p/Base.java",
            "method_Child_shared::p/Child.java",
            "method_Root_inherited::p/Root.java"
        ]
    );
}

#[test]
fn java_constructor_call_links_to_the_constructor_or_the_class() {
    let g = graph(&[(
        "App.java",
        "public class App {\n  void run() { new Foo(1); new Bar(); new Gen<String>(); }\n}\nclass Foo { Foo(int x) {} }\nclass Bar {}\nclass Gen<T> {}\n",
    )]);
    assert_eq!(
        targets(&g, "method_App_run::App.java"),
        [
            "class_Bar::App.java",
            "class_Gen::App.java",
            "ctor_Foo_Foo::App.java"
        ]
    );
}

#[test]
fn java_call_on_a_variable_of_a_declared_type_links_to_the_method() {
    let g = graph(&[(
        "App.java",
        "public class App {\n  private final Foo field = new Foo();\n  void local() { Foo f = new Foo(); f.bar(); }\n  void param(Foo f) { f.bar(); }\n  void member() { field.bar(); this.field.bar(); }\n  void fresh() { new Foo().bar(); }\n}\nclass Foo { void bar() {} }\nclass Decoy { void bar() {} }\n",
    )]);
    for caller in ["local", "param", "member", "fresh"] {
        assert!(
            edge(
                &g,
                &format!("method_App_{caller}::App.java"),
                "method_Foo_bar::App.java"
            ),
            "{caller}: {:?}",
            targets(&g, &format!("method_App_{caller}::App.java"))
        );
    }
}

#[test]
fn java_cast_and_qualified_this_state_the_receiver_type() {
    let g = graph(&[(
        "Outer.java",
        "public class Outer {\n  void helper() {}\n  class Inner {\n    void helper() {}\n    void run(Object o) { Outer.this.helper(); ((Foo) o).bar(); ((Missing) o).bar(); }\n  }\n}\nclass Foo { void bar() {} }\n",
    )]);
    assert_eq!(
        targets(&g, "method_Outer.Inner_run::Outer.java"),
        [
            "method_Foo_bar::Outer.java",
            "method_Outer_helper::Outer.java"
        ]
    );
}

#[test]
fn java_call_on_an_unknown_receiver_gets_no_edge() {
    // `bar` is the only method of that name in the project: still no edge.
    let g = graph(&[(
        "App.java",
        "import java.util.List;\npublic class App {\n  Object make() { return null; }\n  void inferred() { var f = make(); f.bar(); }\n  void chained() { make().bar(); }\n  void external(List<Foo> l) { l.bar(); }\n  void lambda() { run(x -> x.bar()); }\n  void missing() { Missing.bar(); nowhere.bar(); }\n  void run(Object o) {}\n}\nclass Foo { void bar() {} }\n",
    )]);
    for caller in ["inferred", "chained", "external", "lambda", "missing"] {
        assert!(
            !edge(
                &g,
                &format!("method_App_{caller}::App.java"),
                "method_Foo_bar::App.java"
            ),
            "{caller} must not guess"
        );
    }
}

#[test]
fn java_redeclared_variable_has_no_single_type() {
    let g = graph(&[(
        "App.java",
        "public class App {\n  void run(java.util.List<Foo> foos, java.util.List<Bar> bars) {\n    for (Foo x : foos) { }\n    for (Bar x : bars) { x.go(); }\n  }\n}\nclass Foo { void go() {} }\nclass Bar { }\n",
    )]);
    assert!(targets(&g, "method_App_run::App.java").is_empty());
}

#[test]
fn java_import_binds_the_class_and_external_packages_stay_external() {
    let g = graph(&[
        (
            "src/com/acme/app/App.java",
            "package com.acme.app;\nimport com.acme.util.Text;\nimport com.acme.io.*;\nimport java.util.List;\nimport static com.acme.util.Text.shout;\npublic class App {\n  void run() { Text.trim(); Reader.open(); List.of(); shout(); }\n}\n",
        ),
        (
            "src/com/acme/util/Text.java",
            "package com.acme.util;\npublic class Text { public static void trim() {} public static void shout() {} }\n",
        ),
        (
            "src/com/acme/other/Text.java",
            "package com.acme.other;\npublic class Text { public static void trim() {} }\n",
        ),
        (
            "src/com/acme/io/Reader.java",
            "package com.acme.io;\npublic class Reader { public static void open() {} }\n",
        ),
        (
            "src/com/acme/other/List.java",
            "package com.acme.other;\npublic class List { public static void of() {} }\n",
        ),
    ]);
    assert_eq!(
        targets(&g, "method_App_run::src/com/acme/app/App.java"),
        [
            "method_Reader_open::src/com/acme/io/Reader.java",
            "method_Text_shout::src/com/acme/util/Text.java",
            "method_Text_trim::src/com/acme/util/Text.java"
        ]
    );
}

#[test]
fn java_class_of_another_package_is_not_visible_without_an_import() {
    let g = graph(&[
        (
            "a/App.java",
            "package a;\npublic class App { void run() { Helper.go(); } }\n",
        ),
        (
            "b/Helper.java",
            "package b;\npublic class Helper { public static void go() {} }\n",
        ),
    ]);
    assert!(targets(&g, "method_App_run::a/App.java").is_empty());
}

#[test]
fn java_interface_receiver_links_to_the_interface_method() {
    let g = graph(&[(
        "App.java",
        "interface Shape { int area(); }\nclass Sq implements Shape { public int area() { return side(); } int side() { return 2; } }\nclass Uses { int go(Shape s) { return s.area(); } int sq(Sq s) { return s.area(); } }\n",
    )]);
    assert_eq!(
        targets(&g, "method_Uses_go::App.java"),
        ["method_Shape_area::App.java"]
    );
    assert_eq!(
        targets(&g, "method_Uses_sq::App.java"),
        ["method_Sq_area::App.java"]
    );
}

#[test]
fn java_inner_class_calls_reach_the_enclosing_class() {
    let g = graph(&[(
        "Outer.java",
        "public class Outer {\n  void helper() {}\n  static class Inner {\n    void own() {}\n    void run(Outer o) { own(); helper(); new Inner(); }\n  }\n}\n",
    )]);
    assert_eq!(
        targets(&g, "method_Outer.Inner_run::Outer.java"),
        [
            "class_Outer.Inner::Outer.java",
            "method_Outer.Inner_own::Outer.java",
            "method_Outer_helper::Outer.java"
        ]
    );
}

// ────────────────────────────── other languages ──────────────────────────────

#[test]
fn go_and_java_calls_stay_in_their_own_language() {
    let g = graph(&[
        ("main.go", "package main\n\nfunc run() { helper() }\n"),
        ("helper.py", "def helper():\n    pass\n"),
        (
            "App.java",
            "public class App { void run() { Tool.make(); new Tool(); } }\n",
        ),
        (
            "tool.py",
            "class Tool:\n    def make(self):\n        pass\n",
        ),
    ]);
    assert!(targets(&g, "func_run::main.go").is_empty());
    assert!(targets(&g, "method_App_run::App.java").is_empty());
}

#[test]
fn legacy_callers_keep_their_keys_for_go_and_java_declarations() {
    // JavaScript still resolves by the id-derived name: a Go or Java method is
    // spelled `{Type}_{name}` there, as before.
    let g = graph(&[
        ("store.go", GO_STORE),
        ("App.java", "public class App { void bar() {} }\n"),
        (
            "app.js",
            "function run() { Save(); bar(); Store_Save(); }\n",
        ),
    ]);
    assert_eq!(
        targets(&g, "func_run::app.js"),
        ["method_Store_Save::store.go"]
    );
}
