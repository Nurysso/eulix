// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use libeulix::analyze::call_graph::prism_v2::build_call_graph_v2;
use libeulix::grammar_files::parse_source;
use libeulix::struc::kb_struct::{CallGraph, FileData};
use std::collections::BTreeMap;
use std::path::Path;

fn project(ext: &str) -> BTreeMap<String, FileData> {
    ["app", "base", "helpers"]
        .into_iter()
        .map(|stem| {
            let file = format!("{stem}.{ext}");
            let path = Path::new(env!("CARGO_MANIFEST_DIR"))
                .join("tests/fixtures/method_calls")
                .join(&file);
            let source = std::fs::read_to_string(path).unwrap();
            let (_, fd) = parse_source(Path::new(&file), &source).unwrap();
            (file, fd)
        })
        .collect()
}

fn id(p: &BTreeMap<String, FileData>, file: &str, owner: &str, name: &str) -> String {
    let fd = &p[file];
    let functions = if owner.is_empty() {
        &fd.functions
    } else {
        &fd.classes.iter().find(|c| c.name == owner).unwrap().methods
    };
    functions
        .iter()
        .find(|f| f.name == name)
        .unwrap()
        .id
        .clone()
}

fn edges_from(g: &CallGraph, caller: &str) -> Vec<String> {
    let mut ids: Vec<_> = g
        .edges
        .iter()
        .filter(|e| e.edge_type == "call" && e.from == caller)
        .map(|e| e.to.clone())
        .collect();
    ids.sort();
    ids
}

fn check(ext: &str, case: &str) {
    let p = project(ext);
    let g = build_call_graph_v2(&p);
    let app = format!("app.{ext}");
    let expected: Vec<String> = match case {
        "run" => vec![
            id(&p, &app, "Client", "send"),
            id(&p, &format!("base.{ext}"), "Base", "inherited"),
        ],
        "factory" | "shadow" => vec![id(&p, &app, "Client", "send")],
        "bare" | "static_call" => vec![id(&p, &app, "", "send")],
        "missing" => vec![],
        "unknown" => vec![id(&p, &app, "Other", "unique")],
        "imported" => vec![id(&p, &format!("helpers.{ext}"), "", "helper")],
        _ => unreachable!(),
    };
    let owner = if ["unknown", "imported"].contains(&case) {
        ""
    } else {
        "Client"
    };
    let mut expected = expected;
    expected.sort();
    assert_eq!(
        edges_from(&g, &id(&p, &app, owner, case)),
        expected,
        "{ext}: {case}"
    );
}

macro_rules! cases {
    ($module:ident, $ext:literal) => {
        mod $module {
            use super::*;
            #[test]
            fn self_and_inherited() {
                check($ext, "run");
            }
            #[test]
            fn class_receiver() {
                check($ext, "factory");
            }
            #[test]
            fn qualified_call_is_not_shadowed_by_callee_parameter() {
                check($ext, "shadow");
            }
            #[test]
            fn bare_call_is_a_module_function() {
                check($ext, "bare");
            }
            #[test]
            fn static_bare_call_is_a_module_function() {
                check($ext, "static_call");
            }
            #[test]
            fn missing_self_method_does_not_escape_class() {
                check($ext, "missing");
            }
            #[test]
            fn unknown_receiver_requires_unique_method() {
                check($ext, "unknown");
            }
            #[test]
            fn imports_and_external_aliases() {
                check($ext, "imported");
            }
        }
    };
}
cases!(python, "py");
cases!(typescript, "ts");
cases!(rust, "rs");

#[test]
fn receiver_json_is_additive_and_optional() {
    use libeulix::struc::kb_struct::FunctionCall;
    let old = serde_json::json!({"callee":"send", "defined_in":null, "line":2, "args":[], "is_conditional":false, "context":"unconditional"});
    let call: FunctionCall = serde_json::from_value(old.clone()).unwrap();
    assert!(call.receiver.is_none());
    assert_eq!(serde_json::to_value(&call).unwrap(), old);
    let mut new = old;
    new["receiver"] = "self".into();
    let call: FunctionCall = serde_json::from_value(new.clone()).unwrap();
    assert_eq!(call.receiver.as_deref(), Some("self"));
    assert_eq!(call.callee, "send");
    assert_eq!(serde_json::to_value(&call).unwrap(), new);
}

#[test]
fn receivers_survive_same_line_calls_and_keep_legacy_ids_and_callees() {
    for (file, source, expected) in [
        (
            "app.py",
            "def run():\n    first.send(); second.send(); Session().send()\n",
            vec!["first", "second", "Session()"],
        ),
        (
            "app.ts",
            "function run() { first.send(); second.send(); new Session().send(); }",
            vec!["first", "second", "new Session()"],
        ),
        (
            "app.rs",
            "fn run() { first.send(); second.send(); Session::send(); }",
            vec!["first", "second", "Session"],
        ),
    ] {
        let (_, fd) = parse_source(Path::new(file), source).unwrap();
        let run = fd.functions.iter().find(|f| f.name == "run").unwrap();
        assert_eq!(run.id, format!("func_run::{file}"));
        let calls: Vec<_> = run.calls.iter().filter(|c| c.callee == "send").collect();
        assert_eq!(
            calls
                .iter()
                .map(|c| c.receiver.as_deref().unwrap())
                .collect::<Vec<_>>(),
            expected,
            "{file}"
        );
    }
}

#[test]
fn named_import_aliases_select_the_imported_class() {
    for (ext, source, base) in [
        ("py", "from base import Base as Alias\nclass Local:\n    def inherited(self): return 2\ndef run(): return Alias.inherited()\n", "class Base:\n    @staticmethod\n    def inherited(): return 1\n"),
        ("ts", "import { Base as Alias } from './base.js'; class Local { inherited() { return 2; } } function run() { Alias.inherited(); }", "export class Base { static inherited() { return 1; } }"),
        ("rs", "use crate::base::Base as Alias; struct Local; impl Local { fn inherited() {} } fn run() { Alias::inherited(); }", "pub struct Base; impl Base { pub fn inherited() {} }"),
    ] {
        let mut p = BTreeMap::new();
        for (stem, src) in [("app",source),("base",base)] {
            let file = format!("{stem}.{ext}");
            p.insert(file.clone(), parse_source(Path::new(&file), src).unwrap().1);
        }
        let g = build_call_graph_v2(&p);
        assert_eq!(edges_from(&g, &format!("func_run::app.{ext}")), vec![format!("method_Base_inherited::base.{ext}")]);
    }
}

#[test]
fn rust_grouped_imports_keep_modules_and_renamed_types() {
    let sources = [
        ("app.rs", "use crate::{helpers as api, base::{Base as Alias}}; fn run() { api::helper(); Alias::inherited(); }"),
        ("helpers.rs", "pub fn helper() {}"),
        ("base.rs", "pub struct Base; impl Base { pub fn inherited() {} }"),
    ];
    let p: BTreeMap<_, _> = sources
        .into_iter()
        .map(|(file, source)| {
            (
                file.to_string(),
                parse_source(Path::new(file), source).unwrap().1,
            )
        })
        .collect();
    assert_eq!(
        edges_from(&build_call_graph_v2(&p), "func_run::app.rs"),
        vec!["func_helper::helpers.rs", "method_Base_inherited::base.rs"]
    );
}

#[test]
fn missing_relative_import_does_not_bind_an_unrelated_file_with_the_same_stem() {
    let p: BTreeMap<_, _> = [
        (
            "app.ts",
            "import * as api from './missing.js'; function run() { api.helper(); }",
        ),
        ("other/missing.ts", "export function helper() {}"),
    ]
    .into_iter()
    .map(|(file, source)| {
        (
            file.to_string(),
            parse_source(Path::new(file), source).unwrap().1,
        )
    })
    .collect();
    assert!(edges_from(&build_call_graph_v2(&p), "func_run::app.ts").is_empty());
}

#[test]
fn constructor_receivers_keep_the_class_even_when_the_method_is_ambiguous() {
    for (ext, source, base) in [
        ("py", "from base import Base as Alias\nclass Other:\n    def send(self): pass\ndef run(): Alias().send()\n", "class Base:\n    def send(self): pass\n"),
        ("ts", "import { Base as Alias } from './base'; class Other { send() {} } function run() { new Alias().send(); }", "export class Base { send() {} }"),
    ] {
        let p: BTreeMap<_,_> = [("app",source),("base",base)].into_iter().map(|(stem, src)| {
            let file = format!("{stem}.{ext}");
            let fd = parse_source(Path::new(&file), src).unwrap().1;
            (file,fd)
        }).collect();
        let mut expected = vec![format!("method_Base_send::base.{ext}")];
        // Python records the nested constructor too. TypeScript currently
        // extracts call_expression, but not new_expression.
        if ext == "py" {
            expected.insert(0, "class_Base::base.py".to_string());
        }
        assert_eq!(edges_from(&build_call_graph_v2(&p), &format!("func_run::app.{ext}")), expected);
    }
}

#[test]
fn rust_self_module_path_is_not_an_instance_receiver() {
    let p: BTreeMap<_, _> = [("app.rs".to_string(), parse_source(Path::new("app.rs"),
        "fn helper() {} struct Client; impl Client { fn helper(&self) {} fn run(&self) { self::helper(); self.helper(); } } fn run() { self::helper(); }").unwrap().1)].into();
    let g = build_call_graph_v2(&p);
    assert_eq!(
        edges_from(&g, "func_run::app.rs"),
        vec!["func_helper::app.rs"]
    );
    assert_eq!(
        edges_from(&g, "method_Client_run::app.rs"),
        vec!["func_helper::app.rs", "method_Client_helper::app.rs"]
    );
}

#[test]
fn python_from_import_can_bind_a_submodule() {
    let p: BTreeMap<_, _> = [
        (
            "app.py",
            "from pkg import helpers as api\ndef run(): api.helper()\n",
        ),
        ("pkg/helpers.py", "def helper(): pass\n"),
        ("wrong.py", "def helper(): pass\n"),
    ]
    .into_iter()
    .map(|(file, source)| {
        (
            file.to_string(),
            parse_source(Path::new(file), source).unwrap().1,
        )
    })
    .collect();
    assert_eq!(
        edges_from(&build_call_graph_v2(&p), "func_run::app.py"),
        vec!["func_helper::pkg/helpers.py"]
    );
}

#[test]
fn a_local_constructor_beats_a_remote_function() {
    let p: BTreeMap<_, _> = [
        ("app.py", "class Model: pass\ndef run(): return Model()\n"),
        ("helpers.py", "def Model(): pass\n"),
    ]
    .into_iter()
    .map(|(file, source)| {
        (
            file.to_string(),
            parse_source(Path::new(file), source).unwrap().1,
        )
    })
    .collect();
    assert_eq!(
        edges_from(&build_call_graph_v2(&p), "func_run::app.py"),
        vec!["class_Model::app.py"]
    );
}
