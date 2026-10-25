// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

pub mod call_graph;
pub mod dependencies;
pub mod entry_points;
pub mod indices;
pub mod metrics;
pub mod patterns;
pub mod pipeline;
pub mod summary;

#[allow(deprecated)]
pub use pipeline::analyze_and_build;
