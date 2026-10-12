// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
// Maintainer: Dawood (Nurysso) <nurysso [at] proton.me>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
//! # libeulix
//!
//! `libeulix` is the core parsing and analysis engine behind Eulix—designed to be
//! reused by the eulix_parser binary, eulix_lsp, eulix-cli(eulix) and any downstream tool requiring
//! deep code intelligence.
//!
//! ## The Spectrum Philosophy
//!
//! Just as a physical spectrum breaks light down into its constituent wavelengths,
//! **SPECTRUM** (Structured Parsing, Extraction, and Code-Traversal Routine for Universal Metadata)
//! is the design philosophy driving this library: decomposing a massive, multi-language codebase
//! into a clean, queryable spectrum of symbols, types,
//! relationships, and metadata.
//!
//! Source files are parsed into per-language AST structures, and engines like
//! **PRISM** resolve symbols across them to construct a unified, cross-referenced call graph.
//!
//! ## Core Modules
//!
//! - [`analyze`]       — Tree-sitter parsing, AST extraction, and **PRISM**
//!   (Polyglot Resolution via Inverted Symbol Map) for
//!   approximate relationship and call-graph analysis.
//! - [`grammar_files`] — Language grammar integrations and target definitions.
//! - [`struc`]         — The foundational knowledge base schema and shared data types.
//! - [`utils`]         — Shared, side-effect-free helper utilities.
//! ## Library Boundaries
//!
//! **Purity first.** Anything that prints to stdout, writes directly to disk,
//! or assumes a global CLI context lives in the binary crate (`eulix_parser`).
//! `libeulix` remains a pure, side-effect-free data transformation engine.

pub mod analyze;
pub mod grammar_files;
pub mod struc;
pub mod utils;
