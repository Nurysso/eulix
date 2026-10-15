// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

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
    key_features(&kb.structure)
}

fn key_features(structure: &HashMap<String, FileData>) -> Vec<String> {
    // Capped at 10 because this feeds a human-facing summary — an
    // unbounded list would defeat the point of a quick overview.
    const MAX_FEATURES: usize = 10;

    // Files are walked in path order so the same project always yields the
    // same list: HashMap and HashSet iteration order changes on every run.
    let mut paths: Vec<&String> = structure.keys().collect();
    paths.sort();

    // The set dedupes near-identical docstring openers that show up
    // repeatedly across a codebase (boilerplate docstrings, copy-pasted
    // templates), so the feature list isn't dominated by repeats.
    let mut seen = HashSet::new();
    let mut features = Vec::new();

    for path in paths {
        let filedata = &structure[path];
        let docstrings = filedata
            .functions
            .iter()
            .map(|func| func.docstring.as_str())
            .chain(filedata.classes.iter().map(|cls| cls.docstring.as_str()));

        for docstring in docstrings {
            let Some(opener) = docstring_opener(docstring) else {
                continue;
            };
            if seen.insert(opener.clone()) {
                features.push(opener);
                if features.len() == MAX_FEATURES {
                    return features;
                }
            }
        }
    }

    features
}

/// The first sentence of a docstring's first paragraph, with its whitespace
/// collapsed, or `None` when it does not read as a feature description.
fn docstring_opener(docstring: &str) -> Option<String> {
    // Length threshold (>20 chars) filters out placeholder or near-empty
    // docstrings that wouldn't read as a real "feature" description anyway.
    if docstring.len() <= 20 {
        return None;
    }

    let paragraph = docstring.trim().split("\n\n").next()?;
    let sentence = paragraph.split('.').next()?;
    let opener = sentence.split_whitespace().collect::<Vec<_>>().join(" ");

    // A docblock that opens with a tag (`@param`, `@see`, `{@inheritdoc}`
    // in PHPDoc, JSDoc or Javadoc) has no prose summary to show.
    if opener.is_empty() || opener.starts_with('@') || opener.starts_with("{@") {
        return None;
    }

    Some(opener)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::struc::kb_struct::{Class, Function};

    fn file(function_docs: &[&str], class_docs: &[&str]) -> FileData {
        FileData {
            functions: function_docs
                .iter()
                .map(|doc| Function {
                    docstring: doc.to_string(),
                    ..Default::default()
                })
                .collect(),
            classes: class_docs
                .iter()
                .map(|doc| Class {
                    docstring: doc.to_string(),
                    ..Default::default()
                })
                .collect(),
            ..Default::default()
        }
    }

    #[test]
    fn key_features_follow_path_order_and_stop_at_ten() {
        let mut structure = HashMap::new();
        for i in (0..12).rev() {
            let doc = format!("Feature number {i:02} of the project. More text.");
            structure.insert(format!("src/f{i:02}.py"), file(&[&doc], &[]));
        }

        let features = key_features(&structure);

        let expected: Vec<String> = (0..10)
            .map(|i| format!("Feature number {i:02} of the project"))
            .collect();
        assert_eq!(features, expected);
    }

    #[test]
    fn key_features_skip_tags_and_repeats() {
        let mut structure = HashMap::new();
        structure.insert(
            "a.php".to_string(),
            file(
                &[
                    "@see Other::method()\n\n@param string $x",
                    "{@inheritdoc} and nothing else here",
                    "Registers the routes of the application\n\n@param Router $router",
                ],
                &["Registers the routes of the application. Again."],
            ),
        );
        structure.insert(
            "b.js".to_string(),
            file(&["Formats a date\n   for display. Returns a string."], &[]),
        );

        assert_eq!(
            key_features(&structure),
            vec![
                "Registers the routes of the application".to_string(),
                "Formats a date for display".to_string(),
            ]
        );
    }
}
