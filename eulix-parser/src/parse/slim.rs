// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use libeulix::struc::kb_struct::*;

/// PRISM v2 resolves PHP by qualified name and walks classes the way PHP
/// does, so a PHP declaration keeps its namespace, a class its `extends` and
/// traits, and a method whether it is `static`; the rest of `lang_info` goes.
fn strip_lang_info(info: &mut LanguageSpecificInfo) {
    let php = info.php.take().map(|p| PhpInfo {
        namespace: p.namespace,
        extends: p.extends,
        traits: p.traits,
        is_static: p.is_static,
        ..Default::default()
    });
    *info = LanguageSpecificInfo::default();
    info.php = php.map(Box::new);
}

pub fn strip_fn(f: &mut Function) {
    f.control_flow = ControlFlow::default();
    f.exceptions = ExceptionInfo::default();
    strip_lang_info(&mut f.lang_info);
    f.called_by = Vec::new();
    for c in &mut f.calls {
        c.args = Vec::new();
        c.context = String::new();
        c.defined_in = None;
    }
    f.calls.shrink_to_fit();
}

pub fn strip_emitted(fd: &mut FileData) {
    fn strip_fn(f: &mut Function) {
        f.signature = String::new();
        f.return_type = String::new();
        f.variables = Vec::new();
        f.params.truncate(1); // analyzer only reads params[0] (self detection)
        f.params.shrink_to_fit();
    }
    fd.todos = Vec::new();
    fd.functions.iter_mut().for_each(strip_fn);
    for c in &mut fd.classes {
        c.attributes = Vec::new();
        strip_lang_info(&mut c.lang_info);
        c.methods.iter_mut().for_each(strip_fn);
    }
}

pub fn slim(fd: &mut FileData) {
    fd.global_vars = Vec::new();
    fd.security_notes = Vec::new();
    // PRISM v2 resolves PHP calls through `include`/`require`, so PHP keeps
    // its imports until the call graph is built.
    if fd.language != "php" {
        fd.imports = Vec::new();
        fd.imports.shrink_to_fit();
    }
    fd.functions.iter_mut().for_each(strip_fn);
    for c in &mut fd.classes {
        c.methods.iter_mut().for_each(strip_fn);
        c.methods.shrink_to_fit();
    }
    fd.functions.shrink_to_fit();
    fd.classes.shrink_to_fit();
}
