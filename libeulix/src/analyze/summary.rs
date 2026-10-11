use crate::struc::kb_struct::{FileData, KnowledgeBase, PatternInfo};
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet};

#[derive(Debug, Default, Serialize, Deserialize)]
pub struct ProjectSummary {
    pub project_name: String,
    pub total_files: usize,
    pub total_loc: usize,
    pub languages: Vec<String>,
    pub categories: HashMap<String, Vec<String>>,
    pub key_features: Vec<String>,
    pub entry_points: Vec<String>,
    pub dependencies: DependencyInfo,
    pub patterns: PatternInfo,
}

#[derive(Debug, Default, Serialize, Deserialize)]
pub struct DependencyInfo {
    pub stdlib: Vec<String>,
    pub third_party: Vec<String>,
}

pub fn generate_summary(kb: &KnowledgeBase) -> ProjectSummary {
    ProjectSummary {
        project_name: kb.metadata.project_name.clone(),
        total_files: kb.metadata.total_files,
        total_loc: kb.metadata.total_loc,
        languages: kb.metadata.languages.clone(),
        categories: categorize_files(&kb.structure),
        key_features: extract_key_features(kb),
        entry_points: kb
            .entry_points
            .iter()
            .map(|ep| format!("{}:{}", ep.file, ep.line))
            .collect(),
        dependencies: DependencyInfo {
            stdlib: kb
                .external_dependencies
                .iter()
                .filter(|d| d.source == "stdlib")
                .map(|d| d.name.clone())
                .collect(),
            third_party: kb
                .external_dependencies
                .iter()
                .filter(|d| d.source == "external")
                .map(|d| d.name.clone())
                .collect(),
        },
        patterns: kb.patterns.clone(),
    }
}

pub fn categorize_files(structure: &HashMap<String, FileData>) -> HashMap<String, Vec<String>> {
    let mut categories: HashMap<String, Vec<String>> = HashMap::new();

    for (filepath, filedata) in structure {
        let category = classify_file(filepath, filedata);
        categories
            .entry(category)
            .or_default()
            .push(filepath.to_string());
    }

    categories
}

pub fn classify_file(path: &str, data: &FileData) -> String {
    let path_lower = path.to_lowercase();

    // Path-based checks are tried before scanning function names because
    // they're both cheaper and more reliable — a file literally named
    // `auth/login.py` is stronger evidence than a function that happens
    // to mention "hash."
    if path_lower.contains("test") {
        return "Tests".to_string();
    }
    if path_lower.contains("auth") || path_lower.contains("login") {
        return "Authentication".to_string();
    }
    if path_lower.contains("api") || path_lower.contains("endpoint") || path_lower.contains("route")
    {
        return "API".to_string();
    }
    if path_lower.contains("util") || path_lower.contains("helper") {
        return "Utilities".to_string();
    }
    if path_lower.contains("model") || path_lower.contains("entity") {
        return "Data Models".to_string();
    }
    if path_lower.contains("ui") || path_lower.contains("view") {
        return "User Interface".to_string();
    }

    // Falls back to scanning function names for security-flavored
    // keywords only when the path itself gave no signal — catches
    // security-relevant code that isn't isolated into its own
    // directory (common in smaller projects).
    for func in &data.functions {
        let name_lower = func.name.to_lowercase();
        if name_lower.contains("crypt")
            || name_lower.contains("hash")
            || name_lower.contains("encrypt")
        {
            return "Security".to_string();
        }
    }

    "Other".to_string()
}

pub fn extract_key_features(kb: &KnowledgeBase) -> Vec<String> {
    // A HashSet dedupes near-identical docstring openers that show up
    // repeatedly across a codebase (boilerplate docstrings, copy-pasted
    // templates), so the feature list isn't dominated by repeats.
    let mut features = HashSet::new();

    for filedata in kb.structure.values() {
        for func in &filedata.functions {
            // Length threshold (>20 chars) filters out placeholder or
            // near-empty docstrings that wouldn't read as a real
            // "feature" description anyway.
            if !func.docstring.is_empty() && func.docstring.len() > 20 {
                let sentences: Vec<&str> = func.docstring.split('.').collect();
                if let Some(first) = sentences.first() {
                    let trimmed = first.trim();
                    if !trimmed.is_empty() {
                        features.insert(trimmed.to_string());
                    }
                }
            }
        }

        for cls in &filedata.classes {
            if !cls.docstring.is_empty() && cls.docstring.len() > 20 {
                let sentences: Vec<&str> = cls.docstring.split('.').collect();
                if let Some(first) = sentences.first() {
                    let trimmed = first.trim();
                    if !trimmed.is_empty() {
                        features.insert(trimmed.to_string());
                    }
                }
            }
        }
    }

    // Capped at 10 because this feeds a human-facing summary — an
    // unbounded list would defeat the point of a quick overview.
    features.into_iter().take(10).collect()
}
