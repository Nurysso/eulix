// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

pub mod c;
pub mod cpp;
pub mod go;
pub mod java;
pub mod javascript;
pub mod language;
pub mod php;
pub mod python;
pub mod rust;
pub mod typescript;
pub mod utils;

use crate::struc::kb_struct::FileData;
use language::Language;
use std::path::Path;

pub fn parse_source(path: &Path, src: &str) -> Result<(String, FileData), String> {
    match Language::detect(path) {
        Language::C => c::parse_source(path, src),
        Language::Cpp => cpp::parse_source(path, src),
        Language::Python => python::parse_source(path, src),
        Language::JavaScript => javascript::parse_source(path, src),
        Language::TypeScript => typescript::parse_source(path, src),
        Language::Go => go::parse_source(path, src),
        Language::Rust => rust::parse_source(path, src),
        Language::Java => java::parse_source(path, src),
        Language::Php => {
            // `.inc` is also C, C++ and assembler: only PHP code makes it PHP.
            let inc = path
                .extension()
                .is_some_and(|e| e.eq_ignore_ascii_case("inc"));
            if inc && !php::has_open_tag(src) {
                return Err("not PHP: a .inc file without a PHP opening tag".to_string());
            }
            php::parse_source(path, src)
        }
        lang => Err(format!("Unsupported language: {:?}", lang)),
    }
}
