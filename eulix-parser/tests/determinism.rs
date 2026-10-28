// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use std::fs;
use std::path::Path;
use std::process::Command;

const VOLATILE: [&str; 3] = ["\"parsed_at\"", "\"project_hash\"", "\"git_hash\""];

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

fn run_parser(root: &Path, out: &Path, threads: &str) {
    fs::create_dir_all(out).unwrap();
    let status = Command::new(env!("CARGO_BIN_EXE_eulix_parser"))
        .arg("-r")
        .arg(root)
        .arg("-o")
        .arg(out.join("kb.json"))
        .args(["-t", threads, "-p", "2"])
        .status()
        .unwrap();
    assert!(status.success());
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
    let (one, eight) = (tmp.path().join("t1"), tmp.path().join("t8"));
    run_parser(&root, &one, "1");
    run_parser(&root, &eight, "8");

    let mut names: Vec<_> = fs::read_dir(&one)
        .unwrap()
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .filter(|n| n.ends_with(".json"))
        .collect();
    names.sort();
    assert_eq!(names.len(), 8, "{names:?}");

    for name in names {
        let a = blank_volatile(fs::read_to_string(one.join(&name)).unwrap());
        let b = blank_volatile(fs::read_to_string(eight.join(&name)).unwrap());
        assert!(a == b, "{name} differs between -t 1 and -t 8");
    }
}
