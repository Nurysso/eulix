//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
pub mod c;
pub mod cpp;
pub mod go;
pub mod java;
pub mod javascript;
pub mod language;
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
        lang => Err(format!("Unsupported language: {:?}", lang)),
    }
}
