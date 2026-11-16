// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! Go and Java: receivers typed from declarations.
//!
//! Both languages state the type of every parameter and field, and of most
//! locals, so a receiver has a type PRISM can read without inferring one:
//! the method's own receiver (`s` in `func (s *Store)`, `this`), a variable
//! declared with a type, a field of a known type, a package or a class named
//! directly. Everything else (a call result, a range variable, `var`, a name
//! declared twice with two types) is of unknown type and gets no edge: there
//! is no fallback to "the only method of that name" (PRISM-3).
//!
//! Scopes follow the languages. A Go name is visible in its package
//! directory, or through an import that maps to exactly one directory. A
//! Java class is visible in its file, through an import, or in its package
//! directory. Neither path searches the whole project by name.
use super::*;
use crate::struc::kb_struct::{Class, Function};

/// Call targets for the scopes of this module are exact kinds.
const FUNCTION: Seek = Seek::Only(Kind::Function);
const CLASS: Seek = Seek::Only(Kind::Class);

fn is_path(s: &str) -> bool {
    !s.is_empty()
        && s.chars()
            .all(|c| c.is_alphanumeric() || matches!(c, '_' | '$' | '.'))
}

// ───────────────────────────── per-project facts ─────────────────────────────

/// Facts about Go and Java declarations, computed once per project.
#[derive(Default)]
pub(in super::super) struct Typed<'a> {
    /// Go: (package directory, type name) -> owner of its methods. A class
    /// node, or an index past the last node for a named type that has
    /// methods but no node of its own (`type Chain []int`).
    go_types: Map<(u32, &'a str), u32>,
    /// Class node -> declaration, for the types of its fields.
    decls: Map<u32, &'a Class>,
    /// Java: nested class -> the class it is declared in.
    outer: Map<u32, u32>,
}

/// `method_{Type}_{name}::{path}` -> `Type`, without type arguments. The Go
/// parser keeps a method apart from its type when they are in two files, or
/// when the type is not a struct.
fn go_receiver_type<'a>(id: &'a str, name: &str) -> Option<&'a str> {
    let scoped = id.strip_prefix("method_")?.rsplit_once("::")?.0;
    let ty = scoped.strip_suffix(name)?.strip_suffix('_')?;
    ty.split('[').next().filter(|t| !t.is_empty())
}

impl<'a> Typed<'a> {
    /// Also returns the methods Go declares outside their type, as
    /// (owner, name, method node) for the class-method index.
    pub(in super::super) fn build(
        files: &[(&'a str, &'a FileData)],
        nodes: &'a [Node<'a>],
        node_map: &Map<&'a str, u32>,
        file_dir: &[u32],
    ) -> (Self, Vec<(u32, &'a str, u32)>) {
        let mut typed = Typed::default();
        let mut methods = Vec::new();
        let lang = |file: u32| files[file as usize].1.language.as_str();
        if !files
            .iter()
            .any(|f| matches!(f.1.language.as_str(), "go" | "java"))
        {
            return (typed, methods);
        }
        let dir = |n: &Node| file_dir[n.file as usize];
        for (i, n) in nodes.iter().enumerate() {
            if n.kind == Kind::Class && lang(n.file) == "go" {
                typed
                    .go_types
                    .entry((dir(n), n.name.as_ref()))
                    .or_insert(i as u32);
            }
        }
        let mut next = nodes.len() as u32;
        for (i, n) in nodes.iter().enumerate() {
            if n.kind != Kind::Method || n.owner.is_some() || lang(n.file) != "go" {
                continue;
            }
            let (Cow::Borrowed(name), Some(ty)) = (&n.name, go_receiver_type(n.id, &n.name)) else {
                continue;
            };
            let owner = *typed.go_types.entry((dir(n), ty)).or_insert_with(|| {
                next += 1;
                next - 1
            });
            methods.push((owner, *name, i as u32));
        }
        for (f, (_, fd)) in files.iter().enumerate() {
            if !matches!(fd.language.as_str(), "go" | "java") {
                continue;
            }
            // (qualified name, node) of the classes this file declares.
            let mut declared: Vec<(&str, u32)> = Vec::new();
            for class in &fd.classes {
                let Some(&i) = node_map.get(class.id.as_str()) else {
                    continue;
                };
                if nodes[i as usize].file != f as u32 {
                    continue;
                }
                typed.decls.entry(i).or_insert(class);
                // `class_Outer.Inner::path`
                let qualified = class
                    .id
                    .rsplit_once("::")
                    .and_then(|(n, _)| n.split_once('_'));
                if let Some((_, qualified)) = qualified.filter(|_| fd.language == "java") {
                    declared.push((qualified, i));
                }
            }
            for &(qualified, i) in &declared {
                let enclosing = qualified
                    .rsplit_once('.')
                    .and_then(|(outer, _)| declared.iter().find(|d| d.0 == outer));
                if let Some(&(_, outer)) = enclosing {
                    typed.outer.insert(i, outer);
                }
            }
        }
        (typed, methods)
    }
}

// ───────────────────────────────── imports ─────────────────────────────────

/// Directories of the project, for Go import paths.
pub(in super::super) struct GoPackages<'a> {
    /// `0..files`, so that the files of one directory are a slice of it.
    pub(in super::super) ids: &'a [u32],
    /// File paths, sorted by (directory, path).
    pub(in super::super) paths: &'a [&'a str],
    /// (directory of a `go.mod`, module path).
    pub(in super::super) modules: &'a [(String, String)],
}

impl<'a> GoPackages<'a> {
    /// The module a file belongs to: the nearest `go.mod` above it.
    fn module_of(&self, path: &str) -> Option<(&'a str, &'a str)> {
        let dir = dir_of(path);
        self.modules
            .iter()
            .map(|(root, module)| (clean_path(root).trim_end_matches('/'), module.as_str()))
            .filter(|(root, _)| {
                root.is_empty()
                    || dir
                        .strip_prefix(root)
                        .is_some_and(|rest| rest.is_empty() || rest.starts_with('/'))
            })
            .max_by_key(|(root, _)| root.len())
    }

    /// The files directly in `dir`.
    fn dir(&self, dir: &str) -> Target<'a> {
        let lo = self.paths.partition_point(|p| dir_of(p) < dir);
        let hi = lo + self.paths[lo..].partition_point(|p| dir_of(p) <= dir);
        (lo < hi).then(|| &self.ids[lo..hi])
    }
}

/// `import "a/b/pkg"` binds `pkg` (or an alias) to the directory of that
/// package. An import from outside the module is bound to `None`, so
/// `fmt.Println` and `errors.New` never reach a project function.
pub(super) fn go_imports<'a>(
    fd: &'a FileData,
    index: &'a ModuleIndex<'a>,
    path: &str,
    go: &GoPackages<'a>,
) -> FileImports<'a> {
    let mut out = FileImports::default();
    let module = go.module_of(path);
    for imp in &fd.imports {
        let binding = match imp.items.first().map(String::as_str) {
            // A blank import binds nothing, and a dot import binds the
            // package's names, which are not known here.
            Some("_" | ".") => continue,
            Some(alias) => alias,
            None => {
                let mut segments = imp.module.rsplit('/');
                let last = segments.next().unwrap_or(&imp.module);
                // `example.com/mod/v2` is package `mod`.
                let major = last
                    .strip_prefix('v')
                    .is_some_and(|n| !n.is_empty() && n.bytes().all(|b| b.is_ascii_digit()));
                segments.next().filter(|_| major).unwrap_or(last)
            }
        };
        let target = match module {
            Some((root, name)) => imp
                .module
                .strip_prefix(name)
                .filter(|rest| rest.is_empty() || rest.starts_with('/'))
                .and_then(|rest| {
                    let rest = rest.trim_matches('/');
                    match (root.is_empty(), rest.is_empty()) {
                        (true, _) => go.dir(rest),
                        (false, true) => go.dir(root),
                        (false, false) => go.dir(&format!("{root}/{rest}")),
                    }
                }),
            // Without a `go.mod`, a path of several segments may still name
            // a project directory; a single segment is the standard library.
            None if imp.import_type == "stdlib" || !imp.module.contains('/') => None,
            None => resolve_module(&imp.module, index),
        };
        out.namespaces.entry(binding).or_insert(target);
    }
    out
}

/// Java imports that are not a plain class binding.
#[derive(Default)]
pub(in super::super) struct JavaImports<'a> {
    /// `import a.b.*`: the files of every such package or class, sorted.
    on_demand: Vec<u32>,
    /// `import static a.b.C.m`: `m` -> (`C`, the file of `C`).
    static_members: Map<&'a str, (&'a str, Target<'a>)>,
    /// `import static a.b.C.*`: (`C`, the file of `C`).
    static_on_demand: Vec<(&'a str, Target<'a>)>,
}

/// `import a.b.C` binds `C` to the file of that class. `java.*`, `javax.*`
/// and any package without files in the project are external (`None`).
pub(super) fn java_imports<'a>(fd: &'a FileData, index: &'a ModuleIndex<'a>) -> FileImports<'a> {
    let mut out = FileImports::default();
    let mut java = JavaImports::default();
    // `a.b.C` is the file `…/a/b/C.java`, `a.b` the directory `…/a/b`.
    let files = |path: &str| -> Target<'a> {
        if path.starts_with("java.") || path.starts_with("javax.") {
            return None;
        }
        index
            .get(path.replace('.', "/").as_str())
            .map(Vec::as_slice)
    };
    let split = |path: &'a str| path.rsplit_once('.').unwrap_or(("", path));
    for imp in &fd.imports {
        let is_static = imp.import_type.starts_with("static_");
        let path = imp.module.as_str();
        match (is_static, path.strip_suffix(".*")) {
            (false, None) => {
                let (package, name) = split(path);
                // A nested class is in the file of its outer class.
                let target = files(path).or_else(|| files(package));
                out.names.entry(name).or_insert(target);
            }
            (false, Some(package)) => java.on_demand.extend(files(package).into_iter().flatten()),
            (true, None) => {
                let (class, member) = split(path);
                java.static_members
                    .entry(member)
                    .or_insert((split(class).1, files(class)));
            }
            (true, Some(class)) => java.static_on_demand.push((split(class).1, files(class))),
        }
    }
    java.on_demand.sort_unstable();
    java.on_demand.dedup();
    out.java = Some(Box::new(java));
    out
}

// ──────────────────────────────── resolution ────────────────────────────────

impl Resolver<'_> {
    /// `name` in the caller's language, among `files` or, without them, in
    /// the caller's directory.
    fn scoped(&self, name: &str, file: u32, files: Option<&[u32]>, seek: Seek) -> Option<u32> {
        let cands = self.by_name.get(name)?.as_slice();
        let file_of = |c: u32| self.nodes[c as usize].file;
        let same_language =
            |c: &u32| self.languages[file_of(*c) as usize] == self.languages[file as usize];
        let found = match files {
            Some(&[one]) => {
                let lo = cands.partition_point(|&c| file_of(c) < one);
                let hi = lo + cands[lo..].partition_point(|&c| file_of(c) <= one);
                let it = cands[lo..hi].iter().copied().filter(same_language);
                self.pick(it, seek, false, file, false)
            }
            Some(files) => {
                let it = cands
                    .iter()
                    .copied()
                    .filter(|&c| files.binary_search(&file_of(c)).is_ok())
                    .filter(same_language);
                self.pick(it, seek, false, file, false)
            }
            None => {
                let dir = self.file_dir[file as usize];
                let dir_of = |c: u32| self.file_dir[file_of(c) as usize];
                let lo = cands.partition_point(|&c| dir_of(c) < dir);
                let hi = lo + cands[lo..].partition_point(|&c| dir_of(c) <= dir);
                let it = cands[lo..hi].iter().copied().filter(same_language);
                self.pick(it, seek, false, file, false)
            }
        };
        found.0
    }

    /// The base of a Go or Java class, where one is declared in the project.
    pub(in super::super) fn typed_base(&self, base: &str, file: u32) -> Option<u32> {
        if self.languages[file as usize] == "java" {
            return self.java_type(base, file);
        }
        // Only a type with a node can be a parent in the graph.
        self.go_type(base, file)
            .filter(|&owner| (owner as usize) < self.nodes.len())
    }

    // ─────────────────────────────────── Go ───────────────────────────────────

    /// The owner of the methods of `T`, `*T`, `pkg.T` or `T[K]`. Slices,
    /// maps, channels and function types have no methods of their own.
    fn go_type(&self, text: &str, file: u32) -> Option<u32> {
        let named = text.trim().trim_start_matches('*');
        let named = named.split('[').next().filter(|t| is_path(t))?;
        let (dir, name) = match named.split_once('.') {
            Some((package, name)) => {
                let files = (*self.imports[file as usize].namespaces.get(package)?)?;
                (self.file_dir[*files.first()? as usize], name)
            }
            None => (self.file_dir[file as usize], named),
        };
        self.typed.go_types.get(&(dir, name)).copied()
    }

    /// The type of a field declared by the struct `owner`. An embedded field
    /// has no name of its own and is called after its type (`e.Group`).
    fn go_field(&self, owner: u32, field: &str) -> Option<u32> {
        let attr = self.typed.decls.get(&owner)?.attributes.iter().find(|a| {
            if a.name.is_empty() {
                a.type_annotation.rsplit('.').next() == Some(field)
            } else {
                a.name == field
            }
        })?;
        self.go_type(&attr.type_annotation, self.nodes[owner as usize].file)
    }

    pub(in super::super) fn go_call(
        &self,
        call: &FunctionCall,
        file: u32,
        func: &Function,
    ) -> Option<u32> {
        let name = call.callee.as_str();
        let variable = |name: &str| func.variables.iter().find(|v| v.name == name);
        let Some(receiver) = call.receiver.as_deref() else {
            // A local (a closure, a function value) hides a package function.
            if variable(name).is_some() {
                return None;
            }
            // An unqualified name is declared in the package: this file,
            // then its directory. A bare call never runs a method.
            return self
                .scoped(name, file, Some(&[file]), FUNCTION)
                .or_else(|| self.scoped(name, file, None, FUNCTION));
        };
        let receiver = receiver.trim();
        if !is_path(receiver) {
            return None; // a call result, an index, a conversion, ...
        }
        let mut segments = receiver.split('.');
        let root = segments.next()?;
        // `s` in `func (s *Store) Save()`
        let own = func
            .lang_info
            .go
            .as_deref()
            .filter(|go| go.receiver_name.as_deref() == Some(root))
            .and_then(|go| go.receiver_type.as_deref());
        let mut owner = match (own, variable(root)) {
            (Some(own), None) => self.go_type(own, file)?,
            // Declared again in the body: usable only if the type agrees.
            (Some(own), Some(v)) => {
                let owner = self.go_type(own, file)?;
                if self.go_type(v.var_type.as_deref()?, file)? != owner {
                    return None;
                }
                owner
            }
            (None, Some(v)) => self.go_type(v.var_type.as_deref()?, file)?,
            (None, None) => {
                // `pkg.Func()`, or `pkg.Type.Method(x)`. Any other root is a
                // package-level variable, whose type is not recorded.
                let files = (*self.imports[file as usize].namespaces.get(root)?)?;
                let Some(ty) = segments.next() else {
                    return self.scoped(name, file, Some(files), FUNCTION);
                };
                let dir = self.file_dir[*files.first()? as usize];
                *self.typed.go_types.get(&(dir, ty))?
            }
        };
        for field in segments {
            owner = self.go_field(owner, field)?;
        }
        self.receiver_method(owner, name)
    }

    // ────────────────────────────────── Java ──────────────────────────────────

    fn is_constructor(&self, method: u32) -> bool {
        self.nodes[method as usize].id.starts_with("ctor_")
    }

    /// A method of `class` or of an ancestor; never a constructor, which
    /// shares the index but is not inherited or called by name.
    fn java_method(&self, class: u32, name: &str) -> Option<u32> {
        self.receiver_method(class, name)
            .filter(|&m| !self.is_constructor(m))
    }

    /// What `new C(...)` runs: the declared constructor, else the class.
    fn java_constructor(&self, class: u32) -> u32 {
        self.own_method(class, &self.nodes[class as usize].name)
            .filter(|&m| self.is_constructor(m))
            .unwrap_or(class)
    }

    /// A class declared inside `class`.
    fn java_nested(&self, class: u32, name: &str) -> Option<u32> {
        let file = self.nodes[class as usize].file;
        self.by_name.get(name)?.iter().copied().find(|c| {
            let n = &self.nodes[*c as usize];
            n.kind == Kind::Class && n.file == file && self.typed.outer.get(c) == Some(&class)
        })
    }

    /// A simple class name, in Java's order: this file, a single-type
    /// import, the package, then on-demand imports. A class imported from
    /// outside the project hides a project class of the same name.
    fn java_class(&self, name: &str, file: u32) -> Option<u32> {
        if let Some(class) = self.scoped(name, file, Some(&[file]), CLASS) {
            return Some(class);
        }
        let imports = &self.imports[file as usize];
        if let Some(target) = imports.names.get(name) {
            return self.scoped(name, file, Some((*target)?), CLASS);
        }
        if let Some(class) = self.scoped(name, file, None, CLASS) {
            return Some(class);
        }
        let on_demand = &imports.java.as_deref()?.on_demand;
        self.scoped(name, file, Some(on_demand), CLASS)
    }

    /// `Foo`, `Foo<T>`, `Outer.Inner`. Arrays, `var` and type variables
    /// name no project class.
    fn java_type(&self, text: &str, file: u32) -> Option<u32> {
        let named = text.split('<').next().map(str::trim)?;
        if named == "var" || !is_path(named) {
            return None;
        }
        let mut segments = named.split('.');
        let mut class = self.java_class(segments.next()?, file)?;
        for nested in segments {
            class = self.java_nested(class, nested)?;
        }
        Some(class)
    }

    /// The type of field `name` of `class` or of an ancestor. `Some(None)`
    /// is a field whose type is not a project class.
    fn java_field(&self, class: u32, name: &str) -> Option<Option<u32>> {
        let mut queue = VecDeque::from([class]);
        let mut visited: Vec<u32> = Vec::new();
        while let Some(c) = queue.pop_front() {
            if visited.contains(&c) || visited.len() >= MAX_CHAIN {
                continue;
            }
            visited.push(c);
            let field = self
                .typed
                .decls
                .get(&c)
                .and_then(|decl| decl.attributes.iter().find(|a| a.name == name));
            if let Some(field) = field {
                // The Java parser lists enum constants among the fields.
                return Some(if field.type_annotation == "enum_constant" {
                    Some(c)
                } else {
                    self.java_type(&field.type_annotation, self.nodes[c as usize].file)
                });
            }
            if let Some(parents) = self.parents.get(&c) {
                queue.extend(parents.iter().copied());
            }
        }
        None
    }

    /// The static type of a receiver, and whether it is `super`.
    fn java_receiver(
        &self,
        receiver: &str,
        file: u32,
        class: u32,
        func: &Function,
    ) -> Option<(u32, bool)> {
        if receiver == "super" {
            return Some((class, true));
        }
        // `new Foo(args)`, with nothing after the closing parenthesis.
        if let Some(created) = receiver.strip_prefix("new ") {
            let open = created.find('(')?;
            let mut depth = 0usize;
            let mut end = None;
            for (i, c) in created[open..].char_indices() {
                match c {
                    '(' => depth += 1,
                    ')' => {
                        depth -= 1;
                        if depth == 0 {
                            end = Some(open + i + 1);
                            break;
                        }
                    }
                    _ => {}
                }
            }
            return (end == Some(created.len()))
                .then(|| self.java_type(&created[..open], file))
                .flatten()
                .map(|class| (class, false));
        }
        // `((Foo) x)`: the cast states the type. The outer parentheses must
        // be one pair around the whole receiver.
        if let Some(cast) = receiver.strip_prefix("((") {
            let (ty, _) = cast.split_once(')')?;
            let mut depth = 0usize;
            let closes_at_end = receiver.char_indices().all(|(i, c)| {
                match c {
                    '(' => depth += 1,
                    ')' => depth = depth.saturating_sub(1),
                    _ => {}
                }
                depth > 0 || i + 1 == receiver.len()
            });
            return (closes_at_end && !ty.contains('('))
                .then(|| self.java_type(ty, file))
                .flatten()
                .map(|class| (class, false));
        }
        if !is_path(receiver) {
            return None; // a call result, an array element, ...
        }
        let mut segments = receiver.split('.');
        let root = segments.next()?;
        let mut owner = if root == "this" {
            class
        } else if let Some(v) = func.variables.iter().find(|v| v.name == root) {
            // A local or a parameter hides a field and a class.
            self.java_type(v.var_type.as_deref()?, file)?
        } else {
            // A field of this class or of an enclosing one, then a class.
            let mut scope = Some(class);
            let mut field = None;
            while let Some(c) = scope.filter(|_| field.is_none()) {
                field = self.java_field(c, root);
                scope = self.typed.outer.get(&c).copied();
            }
            match field {
                Some(ty) => ty?,
                None => self.java_class(root, file)?,
            }
        };
        // `Outer.this` is the instance of the class named before it.
        for segment in segments.filter(|s| *s != "this") {
            owner = match self.java_field(owner, segment) {
                Some(ty) => ty?,
                None => self.java_nested(owner, segment)?,
            };
        }
        Some((owner, false))
    }

    /// `m()` with no receiver: a method of this class, of an ancestor or of
    /// an enclosing class, then a static import. The parser records
    /// `new Name(...)` under the class name, which comes last.
    fn java_bare(&self, name: &str, file: u32, class: u32) -> Option<u32> {
        let mut scope = Some(class);
        while let Some(c) = scope {
            if let Some(method) = self.java_method(c, name) {
                return Some(method);
            }
            scope = self.typed.outer.get(&c).copied();
        }
        if let Some(java) = self.imports[file as usize].java.as_deref() {
            if let Some(&(owner, target)) = java.static_members.get(name) {
                let class = self.scoped(owner, file, Some(target?), CLASS)?;
                return self.java_method(class, name);
            }
            for &(owner, target) in &java.static_on_demand {
                let method = target
                    .and_then(|files| self.scoped(owner, file, Some(files), CLASS))
                    .and_then(|class| self.java_method(class, name));
                if method.is_some() {
                    return method;
                }
            }
        }
        self.java_class(name, file)
            .map(|c| self.java_constructor(c))
    }

    pub(in super::super) fn java_call(
        &self,
        call: &FunctionCall,
        file: u32,
        class: u32,
        func: &Function,
    ) -> Option<u32> {
        let name = call.callee.as_str();
        let Some(receiver) = call.receiver.as_deref() else {
            return match name {
                // `this(...)` and `super(...)` in a constructor.
                "this" => Some(self.java_constructor(class)),
                "super" => self
                    .parents
                    .get(&class)?
                    .iter()
                    .find(|&&p| !self.nodes[p as usize].id.starts_with("interface_"))
                    .map(|&p| self.java_constructor(p)),
                _ => self.java_bare(name, file, class),
            };
        };
        let (owner, is_super) = self.java_receiver(receiver.trim(), file, class, func)?;
        if is_super {
            return self
                .walk(owner, name, &self.parents)
                .filter(|&m| !self.is_constructor(m));
        }
        // `new Outer.Inner()` is recorded as `Inner` on the receiver `Outer`.
        self.java_method(owner, name).or_else(|| {
            self.java_nested(owner, name)
                .map(|c| self.java_constructor(c))
        })
    }
}
