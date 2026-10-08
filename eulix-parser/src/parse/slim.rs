use crate::struc::kb_struct::*;

#[allow(dead_code)]
pub fn slim1(fd: &mut FileData) {
    fd.global_vars = Vec::new();
    fd.security_notes = Vec::new();
    let strip = |f: &mut Function| {
        f.control_flow = ControlFlow::default();
        f.exceptions = ExceptionInfo::default();
        f.lang_info = LanguageSpecificInfo::default();
        f.called_by = Vec::new();
        for c in &mut f.calls {
            c.args = Vec::new();
            c.context = String::new();
        }
        f.calls.shrink_to_fit();
    };
    fd.functions.iter_mut().for_each(strip);
    for c in &mut fd.classes {
        c.methods.iter_mut().for_each(strip);
        c.methods.shrink_to_fit();
    }
    fd.functions.shrink_to_fit();
    fd.classes.shrink_to_fit();
}

pub fn strip_fn(f: &mut Function) {
    f.control_flow = ControlFlow::default();
    f.exceptions = ExceptionInfo::default();
    f.lang_info = LanguageSpecificInfo::default();
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
        c.lang_info = LanguageSpecificInfo::default();
        c.methods.iter_mut().for_each(strip_fn);
    }
}

pub fn slim(fd: &mut FileData) {
    fd.global_vars = Vec::new();
    fd.security_notes = Vec::new();
    fd.imports = Vec::new();
    fd.imports.shrink_to_fit();
    fd.functions.iter_mut().for_each(strip_fn);
    for c in &mut fd.classes {
        c.methods.iter_mut().for_each(strip_fn);
        c.methods.shrink_to_fit();
    }
    fd.functions.shrink_to_fit();
    fd.classes.shrink_to_fit();
}
