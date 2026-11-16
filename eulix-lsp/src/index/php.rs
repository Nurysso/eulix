// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! PHP callees carry receivers and namespaces, unlike the short names in the
//! language-neutral index. Keep those identities until query time: resolving
//! while indexing would depend on scan order and leave stale inherited edges.

use super::{proximity_rank, CallRef, Def};
use crate::syntax::{DefKind, ParsedFile};
use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};

#[derive(Debug, Clone)]
struct Symbol {
    def: Def,
    parent: Option<String>,
}

#[derive(Debug, Clone)]
struct Call {
    site: CallRef,
    callee: String,
    namespace: String,
    class: Option<String>,
}

#[derive(Debug, Default)]
struct FileKeys {
    symbols: HashSet<String>,
    calls: HashSet<String>,
}

#[derive(Debug, Default)]
pub(super) struct PhpIndex {
    symbols: HashMap<String, Vec<Symbol>>,
    // Lowercase method name -> qualified symbol keys. A key can still hold
    // multiple definitions, so uniqueness must check both levels.
    methods: HashMap<String, HashSet<String>>,
    calls: HashMap<String, Vec<Call>>,
    files: HashMap<PathBuf, FileKeys>,
}

fn canonical(name: &str) -> String {
    name.trim_start_matches('\\').to_ascii_lowercase()
}

fn qualified(namespace: &str, name: &str) -> String {
    if namespace.is_empty() {
        canonical(name)
    } else {
        canonical(&format!("{namespace}\\{name}"))
    }
}

fn short_name(callee: &str) -> &str {
    callee.rsplit(['\\', '.', ':']).next().unwrap_or(callee)
}

fn key(kind: DefKind, name: &str) -> String {
    format!("{kind:?}:{}", canonical(name))
}

impl PhpIndex {
    pub(super) fn contains(&self, path: &Path) -> bool {
        self.files.contains_key(path)
    }

    pub(super) fn remove(&mut self, path: &Path) {
        if let Some(keys) = self.files.remove(path) {
            for k in keys.symbols {
                if let Some(symbols) = self.symbols.get_mut(&k) {
                    let method_name = symbols
                        .first()
                        .filter(|s| s.def.kind == DefKind::Method)
                        .map(|s| s.def.name.to_ascii_lowercase());
                    symbols.retain(|s| s.def.path != path);
                    if symbols.is_empty() {
                        self.symbols.remove(&k);
                        if let Some(name) = method_name {
                            if let Some(methods) = self.methods.get_mut(&name) {
                                methods.remove(&k);
                                if methods.is_empty() {
                                    self.methods.remove(&name);
                                }
                            }
                        }
                    }
                }
            }
            for k in keys.calls {
                if let Some(calls) = self.calls.get_mut(&k) {
                    calls.retain(|c| c.site.path != path);
                    if calls.is_empty() {
                        self.calls.remove(&k);
                    }
                }
            }
        }
    }

    pub(super) fn index(&mut self, path: &Path, parsed: &ParsedFile) {
        let mut keys = FileKeys::default();
        let mut add = |kind, name: &str, fqn: String, start, end, container, parent| {
            let k = key(kind, &fqn);
            if kind == DefKind::Method {
                self.methods
                    .entry(name.to_ascii_lowercase())
                    .or_default()
                    .insert(k.clone());
            }
            self.symbols.entry(k.clone()).or_default().push(Symbol {
                def: Def {
                    name: name.into(),
                    kind,
                    path: path.into(),
                    line_start: start,
                    line_end: end,
                    container,
                },
                parent,
            });
            keys.symbols.insert(k);
        };
        for f in &parsed.functions {
            add(
                DefKind::Function,
                &f.name,
                qualified(&f.php_namespace, &f.name),
                f.line_start,
                f.line_end,
                None,
                None,
            );
        }
        for c in &parsed.classes {
            let class = qualified(&c.php_namespace, &c.name);
            add(
                DefKind::Class,
                &c.name,
                class.clone(),
                c.line_start,
                c.line_end,
                None,
                c.php_parent.clone(),
            );
            for m in &c.methods {
                add(
                    DefKind::Method,
                    &m.name,
                    format!("{class}::{}", m.name),
                    m.line_start,
                    m.line_end,
                    Some(c.name.clone()),
                    None,
                );
            }
        }
        for (f, _) in parsed.all_callables() {
            let class = parsed
                .classes
                .iter()
                .filter(|c| c.line_start <= f.line_start && f.line_end <= c.line_end)
                .min_by_key(|c| c.line_end - c.line_start)
                .map(|c| qualified(&c.php_namespace, &c.name));
            for call in &f.calls {
                let k = short_name(&call.callee).to_ascii_lowercase();
                self.calls.entry(k.clone()).or_default().push(Call {
                    site: CallRef {
                        path: path.into(),
                        line: call.line,
                    },
                    callee: call.callee.clone(),
                    namespace: f.php_namespace.clone(),
                    class: class.clone(),
                });
                keys.calls.insert(k);
            }
        }
        self.files.insert(path.into(), keys);
    }

    fn lookup(&self, kind: DefKind, name: &str, from: &Path) -> Option<&Symbol> {
        self.symbols.get(&key(kind, name))?.iter().min_by_key(|s| {
            (
                proximity_rank(&s.def.path, from, None),
                s.def.path.components().count(),
                &s.def.path,
                s.def.line_start,
            )
        })
    }

    fn method(&self, class: &str, name: &str, from: &Path) -> Option<Def> {
        let mut current = canonical(class);
        let mut seen = HashSet::new();
        while seen.insert(current.clone()) {
            if let Some(s) = self.lookup(DefKind::Method, &format!("{current}::{name}"), from) {
                return Some(s.def.clone());
            }
            current = canonical(
                self.lookup(DefKind::Class, &current, from)?
                    .parent
                    .as_deref()?,
            );
        }
        None // malformed inheritance cycle
    }

    fn resolve(&self, call: &Call) -> Option<Def> {
        let name = call.callee.as_str();
        let from = &call.site.path;
        if let Some(class) = name.strip_prefix("new ") {
            // Class identity also works for implicit constructors, and keeps
            // references on the class declaration useful.
            return self
                .lookup(DefKind::Class, class, from)
                .map(|s| s.def.clone());
        }
        if let Some((receiver, method)) = name.split_once("::").or_else(|| name.rsplit_once('.')) {
            let class = match receiver.to_ascii_lowercase().as_str() {
                "this" | "self" | "static" => call.class.clone()?,
                "parent" => self
                    .lookup(DefKind::Class, call.class.as_deref()?, from)?
                    .parent
                    .clone()?,
                _ if receiver.starts_with('$') => {
                    // Unknown receiver: only a unique workspace method is
                    // safe. Consult its name bucket, never all symbols.
                    let keys = self.methods.get(&method.to_ascii_lowercase())?;
                    if keys.len() != 1 {
                        return None;
                    }
                    let candidates = self.symbols.get(keys.iter().next()?)?;
                    return (candidates.len() == 1).then(|| candidates[0].def.clone());
                }
                _ => receiver.into(),
            };
            return self.method(&class, method, from);
        }
        let symbol = if name.starts_with('\\') {
            self.lookup(DefKind::Function, name, from)
        } else if name.contains('\\') {
            // Later namespace regions in {main}: the parser encodes an
            // unqualified call as Ns\\f, retaining PHP's global fallback.
            self.lookup(DefKind::Function, name, from)
                .or_else(|| self.lookup(DefKind::Function, short_name(name), from))
        } else {
            self.lookup(DefKind::Function, &qualified(&call.namespace, name), from)
                .or_else(|| self.lookup(DefKind::Function, name, from))
        };
        symbol.map(|s| s.def.clone())
    }

    pub(super) fn resolve_call(&self, path: &Path, line: usize, callee: &str) -> Option<Def> {
        let call = self
            .calls
            .get(&short_name(callee).to_ascii_lowercase())?
            .iter()
            .find(|c| c.site.path == path && c.site.line == line && c.callee == callee)?;
        self.resolve(call)
    }

    pub(super) fn symbol_at(
        &self,
        path: &Path,
        line: usize,
        name: &str,
        at_call: bool,
    ) -> Option<Def> {
        let keys = self.files.get(path)?;
        let name = short_name(name);
        if !at_call {
            // Attributes precede the declaration name; its source token can
            // be inside the recorded span rather than on line_start.
            let mut local = keys
                .symbols
                .iter()
                .filter_map(|k| self.symbols.get(k))
                .flatten()
                .filter(|s| {
                    s.def.path == path
                        && s.def.line_start <= line
                        && line <= s.def.line_end
                        && s.def.name.eq_ignore_ascii_case(name)
                });
            let first = local.next()?;
            return local.next().is_none().then(|| first.def.clone());
        }
        // Alias/new calls can have a source spelling different from their
        // qualified callee. If the line has one target, prepare still works.
        let calls = keys
            .calls
            .iter()
            .filter_map(|k| self.calls.get(k))
            .flatten()
            .filter(|c| c.site.path == path && c.site.line == line)
            .collect::<Vec<_>>();
        let matching = calls
            .iter()
            .filter(|c| short_name(&c.callee).eq_ignore_ascii_case(name))
            .collect::<Vec<_>>();
        let candidates = if matching.is_empty() && calls.len() == 1 {
            calls
        } else {
            matching.into_iter().copied().collect()
        };
        let mut targets = candidates.into_iter().map(|c| self.resolve(c));
        let first = targets.next()??;
        targets.all(|t| t.as_ref() == Some(&first)).then_some(first)
    }

    pub(super) fn sites_to(&self, target: &Def) -> Vec<CallRef> {
        let mut sites = self
            .calls
            .get(&target.name.to_ascii_lowercase())
            .into_iter()
            .flatten()
            .filter(|c| self.resolve(c).as_ref() == Some(target))
            .map(|c| c.site.clone())
            .collect::<Vec<_>>();
        sites.sort();
        sites.dedup();
        sites
    }
}
