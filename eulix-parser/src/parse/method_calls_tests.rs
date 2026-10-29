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
