pub mod call_graph;
pub mod dependencies;
pub mod entry_points;
pub mod indices;
pub mod metrics;
pub mod patterns;
pub mod pipeline;
pub mod summary;

pub use pipeline::analyze_and_build;
