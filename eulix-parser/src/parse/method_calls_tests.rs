// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use super::slim::{slim, strip_emitted};
use libeulix::analyze::call_graph::prism_v2::build_call_graph_v2;
use libeulix::grammar_files::parse_source;
use std::collections::BTreeMap;
use std::path::Path;

#[test]
fn slimming_keeps_receiver_resolution_and_imports() {
    for ext in ["py", "ts", "rs"] {
        let mut structure = BTreeMap::new();
        for stem in ["app", "base", "helpers"] {
            let file = format!("{stem}.{ext}");
            let fixture = Path::new(env!("CARGO_MANIFEST_DIR"))
                .join("../libeulix/tests/fixtures/method_calls")
                .join(&file);
            let (_, mut fd) =
                parse_source(Path::new(&file), &std::fs::read_to_string(fixture).unwrap()).unwrap();
            slim(&mut fd);
            strip_emitted(&mut fd);
            structure.insert(file, fd);
        }
        let graph = build_call_graph_v2(&structure);
        for (from, to) in [
            (
                format!("method_Client_run::app.{ext}"),
                format!("method_Base_inherited::base.{ext}"),
            ),
            (
                format!("func_imported::app.{ext}"),
                format!("func_helper::helpers.{ext}"),
            ),
        ] {
            assert!(
                graph.edges.iter().any(|e| e.from == from && e.to == to),
                "missing {from} -> {to}"
            );
        }
        assert!(!graph
            .edges
            .iter()
            .any(|e| e.from == format!("method_Client_run::app.{ext}")
                && e.to == format!("func_send::app.{ext}")));
    }
}

#[test]
fn slimming_keeps_project_reexports() {
    let mut structure = BTreeMap::new();
    for (file, source) in [
        (
            "app.ts",
            "import * as core from './index'; function run() { core.publicName(); }",
        ),
        ("index.ts", "export * from './middle';"),
        (
            "middle.ts",
            "export { actual as publicName } from './impl';",
        ),
        ("impl.ts", "export function actual() {}"),
    ] {
        let (_, mut fd) = parse_source(Path::new(file), source).unwrap();
        slim(&mut fd);
        strip_emitted(&mut fd);
        structure.insert(file.to_owned(), fd);
    }
    assert!(build_call_graph_v2(&structure)
        .edges
        .iter()
        .any(|e| { e.from == "func_run::app.ts" && e.to == "func_actual::impl.ts" }));
}

fn slimmed(sources: &[(&str, &str)]) -> BTreeMap<String, libeulix::struc::kb_struct::FileData> {
    sources
        .iter()
        .map(|(file, source)| {
            let (_, mut fd) = parse_source(Path::new(file), source).unwrap();
            slim(&mut fd);
            strip_emitted(&mut fd);
            (file.to_string(), fd)
        })
        .collect()
}

fn call_targets(
    structure: &BTreeMap<String, libeulix::struc::kb_struct::FileData>,
    opts: &libeulix::analyze::call_graph::prism_v2::CallGraphOptions,
    from: &str,
) -> Vec<String> {
    let graph = libeulix::analyze::call_graph::prism_v2::build_with(structure, opts);
    let mut targets: Vec<String> = graph
        .edges
        .iter()
        .filter(|e| e.edge_type == "call" && e.from == from)
        .map(|e| e.to.clone())
        .collect();
    targets.sort();
    targets
}

// PRISM-5: the receiver, the imports and the declared types of variables and
// fields are what types a Go receiver; all of them must outlive slimming.
#[test]
fn slimming_keeps_what_go_receivers_need() {
    let structure = slimmed(&[
        (
            "main.go",
            "package main\n\nimport (\n\t\"fmt\"\n\t\"example.com/demo/sub\"\n)\n\ntype Group struct{}\n\nfunc (g *Group) GET() {}\n\ntype Engine struct {\n\tGroup\n\tstore *Store\n}\n\ntype Store struct{}\n\nfunc (s *Store) Save() { s.load() }\nfunc (s *Store) load() {}\n\nfunc Println() {}\nfunc Helper() {}\n\nfunc run(e *Engine, unknown interface{}) {\n\te.GET()\n\te.Group.GET()\n\te.store.Save()\n\tm := &Store{}\n\tm.load()\n\tsub.Helper()\n\tfmt.Println()\n\tunknown.Save()\n}\n",
        ),
        ("sub/sub.go", "package sub\n\nfunc Helper() {}\n"),
    ]);
    let opts = libeulix::analyze::call_graph::prism_v2::CallGraphOptions {
        go_modules: vec![(String::new(), "example.com/demo".to_string())],
        ..Default::default()
    };
    assert_eq!(
        call_targets(&structure, &opts, "func_run::main.go"),
        [
            "func_Helper::sub/sub.go",
            "method_Group_GET::main.go",
            "method_Store_Save::main.go",
            "method_Store_load::main.go",
        ]
    );
    assert_eq!(
        call_targets(&structure, &opts, "method_Store_Save::main.go"),
        ["method_Store_load::main.go"]
    );
}

#[test]
fn slimming_keeps_what_java_receivers_need() {
    let structure = slimmed(&[
        (
            "app/App.java",
            "package app;\nimport lib.Tool;\nimport java.util.List;\npublic class App extends Base {\n  private Tool tool;\n  void run(Tool param, List<Tool> list) {\n    tool.use();\n    param.use();\n    Tool local = new Tool();\n    local.use();\n    inherited();\n    Tool.make();\n    list.use();\n    var unknown = make();\n    unknown.use();\n  }\n  Object make() { return null; }\n}\nclass Base { void inherited() {} }\n",
        ),
        (
            "lib/Tool.java",
            "package lib;\npublic class Tool { public Tool() {} public void use() {} public static void make() {} }\n",
        ),
    ]);
    let opts = Default::default();
    assert_eq!(
        call_targets(&structure, &opts, "method_App_run::app/App.java"),
        [
            "ctor_Tool_Tool::lib/Tool.java",
            "method_App_make::app/App.java",
            "method_Base_inherited::app/App.java",
            "method_Tool_make::lib/Tool.java",
            "method_Tool_use::lib/Tool.java",
        ]
    );
}

// Slimming exists to bound memory: only names and declared types stay.
#[test]
fn slimming_keeps_nothing_else_for_go_and_java() {
    let structure = slimmed(&[
        (
            "main.go",
            "package main\n\ntype Store struct {\n\tname string `json:\"n\"`\n}\n\n// Save saves.\nfunc (s *Store) Save(n int) error {\n\tif n > 0 {\n\t\treturn nil\n\t}\n\tx := 1\n\t_ = x\n\treturn nil\n}\n",
        ),
        (
            "App.java",
            "public class App {\n  private int count = 0;\n  int run(int n) throws Exception { int x = n; if (x > 0) { return x; } return 0; }\n}\n",
        ),
    ]);
    for (file, fd) in &structure {
        assert!(fd.global_vars.is_empty() && fd.security_notes.is_empty());
        for class in &fd.classes {
            for attr in &class.attributes {
                assert_eq!(attr.value, None, "{file}: field value");
            }
            for f in &class.methods {
                assert!(f.signature.is_empty(), "{file}: signature");
                assert!(f.return_type.is_empty(), "{file}: return type");
                assert!(f.control_flow.branches.is_empty(), "{file}: control flow");
                assert!(f.params.iter().all(|p| p.type_annotation.is_empty()));
                for v in &f.variables {
                    assert!(
                        v.scope.is_empty()
                            && v.defined_at.is_none()
                            && v.transformations.is_empty()
                            && v.used_in.is_empty(),
                        "{file}: variable {}",
                        v.name
                    );
                }
                let go = f.lang_info.go.as_deref();
                assert!(go.is_none_or(|g| g.type_params.is_empty() && !g.returns_error));
                assert!(f.lang_info.java.is_none(), "{file}: java info");
            }
        }
    }
    // Other languages keep no variables at all.
    let other = slimmed(&[("app.py", "def run(a):\n    b = a\n    return b\n")]);
    assert!(other["app.py"].functions[0].variables.is_empty());
}
