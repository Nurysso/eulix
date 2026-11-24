// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
//
//! Go calls the syntax alone does not type: locals declared from a call,
//! calls through an interface, and receivers of unknown type.
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

fn kind(g: &CallGraph, from: &str, to: &str) -> Option<String> {
    g.edges
        .iter()
        .find(|e| e.from == from && e.to == to)
        .map(|e| e.edge_type.clone())
}

const PIPE: &str = "package demo\n\ntype Pipe struct{}\n\nfunc (p *Pipe) RunPreHooks() {}\nfunc (p *Pipe) next() *Pipe { return p }\n\nfunc newPipe() (*Pipe, error) { return nil, nil }\n";

// ─────────────────────────── locals typed from a call ───────────────────────────

#[test]
fn local_from_a_method_call_is_typed_by_its_result() {
    // The bifrost shape: `pipeline := bifrost.getPluginPipeline()`.
    let g = graph(&[
        ("pipe.go", PIPE),
        (
            "engine.go",
            "package demo\n\ntype Engine struct{}\n\nfunc (e *Engine) getPipe() *Pipe { return nil }\nfunc (e *Engine) release(p *Pipe) {}\n\nfunc (e *Engine) Handle() {\n\tpipe := e.getPipe()\n\tdefer e.release(pipe)\n\tpipe.RunPreHooks()\n}\n",
        ),
    ]);
    assert_eq!(
        kind(
            &g,
            "method_Engine_Handle::engine.go",
            "method_Pipe_RunPreHooks::pipe.go"
        )
        .as_deref(),
        Some("call")
    );
}

#[test]
fn local_takes_the_result_at_its_position() {
    let g = graph(&[
        ("pipe.go", PIPE),
        (
            "use.go",
            "package demo\n\nfunc first() {\n\tp, err := newPipe()\n\t_ = err\n\tp.RunPreHooks()\n}\n\nfunc chained() {\n\tp, _ := newPipe()\n\tq := p.next()\n\tq.RunPreHooks()\n}\n",
        ),
    ]);
    for caller in ["first", "chained"] {
        assert_eq!(
            kind(
                &g,
                &format!("func_{caller}::use.go"),
                "method_Pipe_RunPreHooks::pipe.go"
            )
            .as_deref(),
            Some("call"),
            "{caller}"
        );
    }
}

#[test]
fn local_declared_two_ways_is_not_typed() {
    // Which `p` the call sees depends on scopes; a guess could be wrong.
    let g = graph(&[
        ("pipe.go", PIPE),
        (
            "use.go",
            "package demo\n\ntype Other struct{}\n\nfunc other() *Other { return nil }\n\nfunc mixed(ok bool) {\n\tif ok {\n\t\tp := other()\n\t\t_ = p\n\t}\n\tp, _ := newPipe()\n\tp.RunPreHooks()\n}\n",
        ),
    ]);
    assert_ne!(
        kind(&g, "func_mixed::use.go", "method_Pipe_RunPreHooks::pipe.go").as_deref(),
        Some("call")
    );
}

// ───────────────────────────── interface dispatch ─────────────────────────────

const STORE: &str = "package demo\n\ntype Store interface {\n\tSave() error\n\tLoad() error\n}\n\ntype DB struct{}\n\nfunc (d *DB) Save() error { return nil }\nfunc (d *DB) Load() error { return nil }\n\n// Mem has Save but not Load: it is no Store.\ntype Mem struct{}\n\nfunc (m *Mem) Save() error { return nil }\n\ntype Svc struct {\n\tstore Store\n}\n\nfunc (s *Svc) Run() {\n\ts.store.Save()\n}\n";

#[test]
fn interface_call_reaches_each_implementer() {
    let g = graph(&[("store.go", STORE)]);
    assert_eq!(
        kind(&g, "method_Svc_Run::store.go", "ifacemethod_Save::store.go").as_deref(),
        Some("call")
    );
    assert_eq!(
        kind(&g, "method_Svc_Run::store.go", "method_DB_Save::store.go").as_deref(),
        Some("dispatch")
    );
    assert_eq!(
        kind(&g, "method_Svc_Run::store.go", "method_Mem_Save::store.go"),
        None
    );
}

#[test]
fn interface_with_many_implementers_gets_no_dispatch() {
    let mut src = String::from(
        "package demo\n\ntype Closer interface {\n\tShutdown() error\n}\n\ntype User struct {\n\tc Closer\n}\n\nfunc (u *User) Stop() { u.c.Shutdown() }\n",
    );
    for i in 0..7 {
        src.push_str(&format!(
            "\ntype T{i} struct{{}}\n\nfunc (t *T{i}) Shutdown() error {{ return nil }}\n"
        ));
    }
    let g = graph(&[("c.go", &src)]);
    assert!(
        !g.edges.iter().any(|e| e.edge_type == "dispatch"),
        "seven implementers link too much"
    );
}

// ─────────────────────────── receivers of unknown type ───────────────────────────

const HOOKS: &str = "package demo\n\ntype Hooks struct{}\n\nfunc (h *Hooks) RunLLMPreHooks() {}\nfunc (h *Hooks) Close() {}\n";

#[test]
fn unique_distinctive_name_on_an_unknown_receiver_is_inferred() {
    let g = graph(&[
        ("hooks.go", HOOKS),
        (
            "use.go",
            "package demo\n\nimport \"github.com/valyala/fasthttp\"\n\nvar registry = lookup()\n\nfunc lookup() interface{} { return nil }\n\nfunc global() { registry.RunLLMPreHooks() }\nfunc common() { registry.Close() }\nfunc typed(ctx *fasthttp.RequestCtx) { ctx.RunLLMPreHooks() }\nfunc fromPackage() {\n\tc := fasthttp.Acquire()\n\tc.RunLLMPreHooks()\n}\n",
        ),
    ]);
    let hook = "method_Hooks_RunLLMPreHooks::hooks.go";
    assert_eq!(
        kind(&g, "func_global::use.go", hook).as_deref(),
        Some("inferred")
    );
    // A common name, or a receiver whose type is known to be outside the
    // project, is never guessed.
    assert_eq!(
        kind(&g, "func_common::use.go", "method_Hooks_Close::hooks.go"),
        None
    );
    assert_eq!(kind(&g, "func_typed::use.go", hook), None);
    assert_eq!(kind(&g, "func_fromPackage::use.go", hook), None);
}

#[test]
fn name_defined_twice_is_not_inferred() {
    let g = graph(&[
        ("hooks.go", HOOKS),
        (
            "more.go",
            "package demo\n\ntype Other struct{}\n\nfunc (o *Other) RunLLMPreHooks() {}\n\nvar registry interface{}\n\nfunc global() { registry.RunLLMPreHooks() }\n",
        ),
    ]);
    assert!(!g
        .edges
        .iter()
        .any(|e| e.from == "func_global::more.go" && e.edge_type == "inferred"));
}

// ───────────────────────────── several Go modules ─────────────────────────────

#[test]
fn import_of_a_sibling_module_is_project_code() {
    // bifrost: transports/ imports core/ (aliased) and framework/, each with
    // its own go.mod. Calls across them used to count as external.
    use libeulix::analyze::call_graph::prism_v2::{build_with, CallGraphOptions};
    let sources = [
        (
            "core/bifrost.go",
            "package bifrost\n\ntype Bifrost struct{}\n\nfunc (b *Bifrost) UpdateProvider(p string) error { return nil }\n",
        ),
        (
            "framework/configstore/store.go",
            "package configstore\n\ntype ConfigStore interface {\n\tUpdateProvider(p string) error\n\tPing() error\n}\n\ntype RDBConfigStore struct{}\n\nfunc (s *RDBConfigStore) UpdateProvider(p string) error { return nil }\nfunc (s *RDBConfigStore) Ping() error { return nil }\n",
        ),
        (
            "transports/lib/config.go",
            "package lib\n\nimport (\n\tbifrost \"github.com/x/core\"\n\t\"github.com/x/framework/configstore\"\n\t\"github.com/other/dep\"\n)\n\ntype Config struct {\n\tclient      *bifrost.Bifrost\n\tConfigStore configstore.ConfigStore\n}\n\nfunc (c *Config) UpdateProviderConfig(p string) {\n\tc.ConfigStore.UpdateProvider(p)\n\tc.client.UpdateProvider(p)\n\tdep.UpdateProvider(p)\n}\n",
        ),
    ];
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
        go_modules: vec![
            ("core".into(), "github.com/x/core".into()),
            ("framework".into(), "github.com/x/framework".into()),
            ("transports".into(), "github.com/x/transports/v2".into()),
        ],
        ..Default::default()
    };
    let g = build_with(&files, &opts);
    let from = "method_Config_UpdateProviderConfig::transports/lib/config.go";
    assert_eq!(
        kind(&g, from, "method_Bifrost_UpdateProvider::core/bifrost.go").as_deref(),
        Some("call")
    );
    assert_eq!(
        kind(
            &g,
            from,
            "ifacemethod_UpdateProvider::framework/configstore/store.go"
        )
        .as_deref(),
        Some("call")
    );
    assert_eq!(
        kind(
            &g,
            from,
            "method_RDBConfigStore_UpdateProvider::framework/configstore/store.go"
        )
        .as_deref(),
        Some("dispatch")
    );
    // A module outside the project stays external.
    assert_eq!(g.edges.iter().filter(|e| e.from == from).count(), 3);
}
