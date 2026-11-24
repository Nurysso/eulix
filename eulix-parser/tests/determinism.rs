// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use std::fs;
use std::path::Path;
use std::process::Command;

const VOLATILE: [&str; 4] = [
    "\"parsed_at\"",
    "\"project_hash\"",
    "\"git_hash\"",
    "\"last_analyzed\"",
];

fn write_fixture(root: &Path) {
    for i in 0..300 {
        let dir = root.join(format!("pkg{}", i % 7));
        fs::create_dir_all(&dir).unwrap();
        let py = format!(
            "import os\nimport requests\nfrom pkg{j}.m{j} import run\n\n\
             @app.route(\"/r{i}\")\ndef handler_{i}(x):\n    \"\"\"Handle route {i}.\"\"\"\n    return run() + helper(x)\n\n\
             def run():\n    return os.getcwd()\n\n\
             def helper(x):\n    if x:\n        return run()\n    return requests.get(x)\n\n\
             class Model{i}:\n    def save(self):\n        return run()\n",
            j = (i + 1) % 7,
        );
        fs::write(dir.join(format!("m{i}.py")), py).unwrap();
        if i % 10 == 0 {
            fs::write(
                dir.join(format!("r{i}.rs")),
                "use std::collections::HashMap;\npub fn run() -> i32 { helper() }\nfn helper() -> i32 { 1 }\n",
            )
            .unwrap();
            fs::write(
                dir.join(format!("j{i}.js")),
                "import _ from \"lodash\";\nexport function run() { return helper(); }\nfunction helper() { return 1; }\n",
            )
            .unwrap();
        }
    }
}

fn write_go_fixture(root: &Path) {
    let methods: String = (0..30)
        .map(|i| {
            format!("func (t T{i}) M{i}(a int) int {{\n\tb := a\n\tc := b\n\treturn c\n}}\n\n")
        })
        .collect();
    fs::write(root.join("orphans.go"), format!("package p\n\n{methods}")).unwrap();
}

// Go and Java calls that resolve through typed receivers, imports and the
// module path: many files per package, so that chunks split inside one.
fn write_typed_receiver_fixture(root: &Path) {
    fs::write(root.join("go.mod"), "module example.com/demo\n").unwrap();
    for i in 0..60 {
        let dir = root.join(format!("gopkg{}", i % 5));
        fs::create_dir_all(&dir).unwrap();
        let go = format!(
            "package gopkg{k}\n\nimport (\n\t\"fmt\"\n\n\tnext \"example.com/demo/gopkg{j}\"\n)\n\n\
             type Store{i} struct {{\n\tBase\n\tpeer *Base\n}}\n\n\
             func (s *Store{i}) Save() {{\n\ts.load()\n\ts.Shared()\n\ts.peer.Shared()\n\tnext.Helper()\n\tfmt.Println()\n}}\n\
             func (s *Store{i}) load() {{ Helper() }}\n\n\
             func use{i}(m *Store{i}) {{\n\tm.Save()\n\tlocal := &Store{i}{{}}\n\tlocal.load()\n\tunknown := pick{i}()\n\tunknown.Save()\n}}\n\
             func pick{i}() *Store{i} {{ return nil }}\n",
            k = i % 5,
            j = (i + 1) % 5,
        );
        fs::write(dir.join(format!("s{i}.go")), go).unwrap();
        if i < 5 {
            fs::write(
                dir.join("base.go"),
                format!("package gopkg{i}\n\ntype Base struct{{}}\n\nfunc (b *Base) Shared() {{}}\nfunc Helper() {{}}\n"),
            )
            .unwrap();
        }
        let jdir = root.join(format!("java/com/demo/p{}", i % 5));
        fs::create_dir_all(&jdir).unwrap();
        let java = format!(
            "package com.demo.p{k};\n\nimport com.demo.p{j}.Tool{n};\nimport java.util.List;\n\n\
             public class Svc{i} extends Tool{i} {{\n  private Tool{n} tool;\n\
             \n  void run(Tool{n} other, List<String> names) {{\n    tool.use();\n    other.use();\n    this.own();\n    use();\n    new Tool{n}().use();\n    Tool{n}.make();\n    names.size();\n  }}\n\
             \n  void own() {{}}\n}}\n",
            k = i % 5,
            j = (i + 1) % 5,
            n = (i + 1) % 60,
        );
        fs::write(jdir.join(format!("Svc{i}.java")), java).unwrap();
        fs::write(
            jdir.join(format!("Tool{i}.java")),
            format!(
                "package com.demo.p{k};\n\npublic class Tool{i} {{\n  public Tool{i}() {{}}\n  public void use() {{}}\n  public static void make() {{}}\n}}\n",
                k = i % 5
            ),
        )
        .unwrap();
    }
}

fn run_parser(root: &Path, out: &Path, threads: &str) -> String {
    fs::create_dir_all(out).unwrap();
    let output = Command::new(env!("CARGO_BIN_EXE_eulix_parser"))
        .arg("-r")
        .arg(root)
        .arg("-o")
        .arg(out.join("kb.json"))
        .args(["-t", threads, "-p", "2", "-v", "--emit-json"])
        .output()
        .unwrap();
    assert!(output.status.success());
    String::from_utf8(output.stdout).unwrap()
}

// Blanks the value of each volatile key in place, so every other byte is still compared.
fn blank_volatile(mut s: String) -> String {
    for key in VOLATILE {
        let mut from = 0;
        while let Some(i) = s[from..].find(key) {
            let after = from + i + key.len();
            let open = after + s[after..].find('"').unwrap();
            let close = open + 1 + s[open + 1..].find('"').unwrap();
            s.replace_range(open + 1..close, "");
            from = open + 2;
        }
    }
    s
}

#[test]
fn output_is_identical_across_thread_counts() {
    let tmp = tempfile::tempdir().unwrap();
    let root = tmp.path().join("proj");
    write_fixture(&root);
    write_go_fixture(&root);
    write_typed_receiver_fixture(&root);
    let (one, eight) = (tmp.path().join("t1"), tmp.path().join("t8"));
    run_parser(&root, &one, "1");
    run_parser(&root, &eight, "8");
    assert_same_outputs(&one, &eight);
}

#[test]
fn incremental_run_matches_a_fresh_full_parse() {
    let tmp = tempfile::tempdir().unwrap();
    let root = tmp.path().join("proj");
    write_fixture(&root);
    write_go_fixture(&root);
    write_typed_receiver_fixture(&root);
    fs::write(
        root.join("app.php"),
        "<?php\nrequire_once __DIR__ . '/lib.php';\nfunction main() { return helper(); }\n",
    )
    .unwrap();
    fs::write(
        root.join("lib.php"),
        "<?php\nfunction helper() { return 1; }\n",
    )
    .unwrap();

    let (inc, full) = (tmp.path().join("inc"), tmp.path().join("full"));
    assert!(run_parser(&root, &inc, "4").contains("Reused 0 cached files"));

    fs::write(
        root.join("pkg0/m0.py"),
        "def handler_0(x):\n    return helper(x)\n",
    )
    .unwrap();
    fs::write(
        root.join("lib.php"),
        "<?php\nfunction helper() { return extra(); }\nfunction extra() { return 2; }\n",
    )
    .unwrap();
    fs::remove_file(root.join("pkg1/m1.py")).unwrap();
    fs::write(
        root.join("pkg2/added.rs"),
        "pub fn added() -> i32 { run() }\n",
    )
    .unwrap();

    // 548 files (185 of them the typed-receiver fixture): one added, two
    // modified, the rest unchanged.
    let stdout = run_parser(&root, &inc, "4");
    assert!(stdout.contains("Reused 545 cached files"), "{stdout}");
    run_parser(&root, &full, "4");
    assert_same_outputs(&inc, &full);
}

/// Binary outputs whose content has no run-specific fields, so the bytes
/// themselves must match. The others (kb.fb, kb_metrics.fb) carry `Metadata`
/// (timestamps, hashes) and are compared through their JSON copies with those
/// values blanked.
const STABLE_FB: [&str; 6] = [
    "kb_call_graph.fb",
    "kb_index.fb",
    "kb_entry_points.fb",
    "kb_external_deps.fb",
    "kb_patterns.fb",
    "kb_summary.fb",
];

fn assert_same_outputs(a: &Path, b: &Path) {
    let list = |ext: &str| {
        let mut names: Vec<_> = fs::read_dir(a)
            .unwrap()
            .map(|e| e.unwrap().file_name().into_string().unwrap())
            .filter(|n| n.ends_with(ext))
            .collect();
        names.sort();
        names
    };
    let json = list(".json");
    assert_eq!(json.len(), 9, "{json:?}"); // 8 outputs and the checksum copy
    let fbs = list(".fb");
    assert_eq!(fbs.len(), 8, "{fbs:?}");

    for name in json {
        let x = blank_volatile(fs::read_to_string(a.join(&name)).unwrap());
        let y = blank_volatile(fs::read_to_string(b.join(&name)).unwrap());
        assert!(x == y, "{name} differs between {a:?} and {b:?}");
    }
    for name in STABLE_FB {
        assert!(
            fs::read(a.join(name)).unwrap() == fs::read(b.join(name)).unwrap(),
            "{name} bytes differ between {a:?} and {b:?}"
        );
    }
}

#[test]
fn json_copies_are_removed_without_emit_json() {
    let tmp = tempfile::tempdir().unwrap();
    let root = tmp.path().join("proj");
    write_fixture(&root);
    let out = tmp.path().join("out");
    run_parser(&root, &out, "2");
    assert!(out.join("kb_metrics.json").exists());

    let status = Command::new(env!("CARGO_BIN_EXE_eulix_parser"))
        .arg("-r")
        .arg(&root)
        .arg("-o")
        .arg(out.join("kb.json"))
        .args(["-t", "2", "-p", "2"])
        .status()
        .unwrap();
    assert!(status.success());
    for name in [
        "kb_summary",
        "kb_metrics",
        "kb_entry_points",
        "kb_external_deps",
        "kb_patterns",
        "kb_call_graph",
        "kb_index",
        "kb",
    ] {
        assert!(out.join(format!("{name}.fb")).exists(), "{name}.fb missing");
        assert!(
            !out.join(format!("{name}.json")).exists(),
            "stale {name}.json left behind"
        );
    }
}
