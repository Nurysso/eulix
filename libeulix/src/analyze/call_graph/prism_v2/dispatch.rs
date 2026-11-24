// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! Inferred "dispatch" edges across Go interfaces.
//!
//! A call through an interface value (`s.store.Save()` with a field
//! `store Store`) resolves to the interface's method declaration, which has
//! no body. The code that runs is a method of a type implementing the
//! interface, and the syntax graph has no edge to it: `callers` of
//! `DB.Save` misses every call made through `Store`, and `trace` stops at the
//! interface. Go types implement interfaces structurally, so an implementer
//! here is a project type whose methods include every method the interface
//! declares, by name. Each call to an interface method gets an edge to the
//! same-named method of each implementer, labelled "dispatch" so readers can
//! tell it was inferred.

use std::collections::{BTreeSet, HashMap};

use super::{RawEdge, Resolver, EDGE_CALL, EDGE_DISPATCH};

/// An interface with more implementers than this (`io.Writer`-like, or one
/// with a single common method such as `Close`) links too much to be useful.
pub(super) const MAX_IMPLEMENTERS: usize = 6;

/// Dispatch edges for the calls in `call_edges` that reach a Go interface
/// method.
pub(super) fn edges(r: &Resolver, call_edges: &[RawEdge]) -> Vec<RawEdge> {
    let go = |method: u32| r.languages[r.nodes[method as usize].file as usize] == "go";
    let is_iface_method = |method: u32| r.nodes[method as usize].id.starts_with("ifacemethod_");
    if !call_edges
        .iter()
        .any(|e| e.kind == EDGE_CALL && is_iface_method(e.to))
    {
        return Vec::new();
    }

    // Owner -> the names of its methods; concrete owners by method name.
    let mut names: HashMap<u32, BTreeSet<&str>> = HashMap::new();
    let mut owners_of: HashMap<&str, Vec<u32>> = HashMap::new();
    let mut ifaces: BTreeSet<u32> = BTreeSet::new();
    for &(owner, name, method) in r.methods {
        if !go(method) {
            continue;
        }
        names.entry(owner).or_default().insert(name);
        if is_iface_method(method) {
            ifaces.insert(owner);
        } else {
            owners_of.entry(name).or_default().push(owner);
        }
    }

    // Interface -> its implementers (none when there are too many).
    let mut implementers: HashMap<u32, Vec<u32>> = HashMap::new();
    for iface in ifaces {
        let wanted = &names[&iface];
        let Some(first) = wanted.iter().next() else {
            continue;
        };
        let mut found: Vec<u32> = owners_of
            .get(first)
            .into_iter()
            .flatten()
            .copied()
            .filter(|t| *t != iface && names.get(t).is_some_and(|have| wanted.is_subset(have)))
            .collect();
        found.sort_unstable();
        found.dedup();
        if !found.is_empty() && found.len() <= MAX_IMPLEMENTERS {
            implementers.insert(iface, found);
        }
    }

    let mut out = Vec::new();
    for e in call_edges
        .iter()
        .filter(|e| e.kind == EDGE_CALL && is_iface_method(e.to))
    {
        let target = &r.nodes[e.to as usize];
        let Some(iface) = target.owner.and_then(|o| r.node_map.get(o)) else {
            continue;
        };
        for &ty in implementers.get(iface).into_iter().flatten() {
            if let Some(method) = r.own_method(ty, target.name.as_ref()) {
                if method != e.from {
                    out.push(RawEdge {
                        from: e.from,
                        to: method,
                        line: e.line,
                        kind: EDGE_DISPATCH,
                        conditional: e.conditional,
                    });
                }
            }
        }
    }
    out.sort_unstable_by_key(|e| (e.from, e.to, e.line));
    out.dedup_by_key(|e| (e.from, e.to));
    out
}
