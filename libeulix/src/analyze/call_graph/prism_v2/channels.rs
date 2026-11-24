// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! Inferred "async" edges across Go channels.
//!
//! A syntax call graph has no edge from `tryRequest`, which does
//! `pq.queue <- msg`, to `requestWorker`, which does `for msg := range
//! pq.queue` in a goroutine. Agents asking how a request is processed lose
//! the chain exactly there. This pass links every function that sends on a
//! field channel to the functions in the same package that receive from a
//! field of the same name. The edge is labelled "async" so readers can tell
//! it was inferred, not resolved.

use std::collections::{BTreeSet, HashMap};

use crate::struc::kb_struct::{FileData, Function};

use super::{RawEdge, EDGE_ASYNC};

/// A field name with more receivers than this in one package (`done`,
/// `stop`, `ch` shared by many select loops) links too much to be useful.
pub(super) const MAX_RECEIVERS: usize = 4;

/// Async edges for Go files. `files` and `file_dir` are in the call graph's
/// file order; `node_of` maps a function id to its node index.
pub(super) fn edges(
    files: &[(&str, &FileData)],
    file_dir: &[u32],
    node_of: &dyn Fn(&str) -> Option<u32>,
) -> Vec<RawEdge> {
    // (package dir, field) -> senders (node, line) / receivers (node)
    let mut sends: HashMap<(u32, &str), Vec<(u32, u32)>> = HashMap::new();
    let mut recvs: HashMap<(u32, &str), BTreeSet<u32>> = HashMap::new();
    let mut funcs: Vec<(&Function, u32)> = Vec::new();
    for (i, (_, fd)) in files.iter().enumerate() {
        if fd.language != "go" {
            continue;
        }
        let dir = file_dir[i];
        funcs.extend(fd.functions.iter().map(|f| (f, dir)));
        for c in &fd.classes {
            funcs.extend(c.methods.iter().map(|m| (m, dir)));
        }
    }
    for (f, dir) in funcs {
        let Some(go) = f.lang_info.go.as_deref() else {
            continue;
        };
        if go.chan_sends.is_empty() && go.chan_recvs.is_empty() {
            continue;
        }
        let Some(node) = node_of(&f.id) else {
            continue;
        };
        for op in &go.chan_sends {
            sends
                .entry((dir, op.field.as_str()))
                .or_default()
                .push((node, op.line as u32));
        }
        for op in &go.chan_recvs {
            recvs
                .entry((dir, op.field.as_str()))
                .or_default()
                .insert(node);
        }
    }

    let mut out = Vec::new();
    let mut keys: Vec<_> = sends.keys().copied().collect();
    keys.sort_unstable();
    for key in keys {
        let Some(rx) = recvs.get(&key) else {
            continue;
        };
        if rx.len() > MAX_RECEIVERS {
            continue;
        }
        for &(from, line) in &sends[&key] {
            for &to in rx {
                if from != to {
                    out.push(RawEdge {
                        from,
                        to,
                        line,
                        kind: EDGE_ASYNC,
                        conditional: false,
                    });
                }
            }
        }
    }
    out.sort_unstable_by_key(|e| (e.from, e.to, e.line));
    out.dedup_by_key(|e| (e.from, e.to));
    out
}
