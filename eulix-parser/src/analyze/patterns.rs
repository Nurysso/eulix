/// Detect common patterns
///
/// These are cheap, corpus-wide heuristics rather than an attempt at
/// exhaustive static analysis — the goal is a quick "here's roughly how
/// this project is organized" summary, not a certified classification.
// use crate::struc::kb_struct::PatternInfo;
use crate::struc::kb_struct::{KnowledgeBase, PatternInfo};

pub fn detect_patterns(kb: &KnowledgeBase) -> PatternInfo {
    let mut patterns = PatternInfo::default();

    // Majority vote over naming style, rather than flagging every
    // inconsistency, since most real codebases mix conventions
    // somewhat and what's useful here is the dominant style.
    let mut snake_case_count = 0;
    let mut camel_case_count = 0;

    for filedata in kb.structure.values() {
        for func in &filedata.functions {
            if func.name.contains('_') {
                snake_case_count += 1;
            } else if func.name.chars().any(|c| c.is_uppercase()) {
                camel_case_count += 1;
            }
        }
    }

    patterns.naming_convention = if snake_case_count > camel_case_count {
        "snake_case".to_string()
    } else {
        "camelCase".to_string()
    };

    let has_src_dir = kb.structure.keys().any(|p| p.starts_with("src/"));
    let has_lib_dir = kb.structure.keys().any(|p| p.starts_with("lib/"));
    let has_tests_dir = kb.structure.keys().any(|p| p.contains("test"));

    if has_src_dir && has_tests_dir {
        patterns.structure_type = "Standard (src/ + tests/)".to_string();
    } else if has_lib_dir {
        patterns.structure_type = "Library".to_string();
    } else {
        patterns.structure_type = "Flat".to_string()
    }

    patterns.architecture_style = detect_architecture(kb);

    patterns
}

fn detect_architecture(kb: &KnowledgeBase) -> Option<String> {
    let file_paths: Vec<&String> = kb.structure.keys().collect();

    // Layered/MVC/microservices are distinguished by directory-naming
    // conventions rather than actual dependency direction between
    // layers, since verifying real layering would require the full
    // call graph and cross-language import resolution — this is a
    // fast, best-effort signal instead.
    let has_api = file_paths
        .iter()
        .any(|p| p.contains("api") || p.contains("routes"));
    let has_service = file_paths
        .iter()
        .any(|p| p.contains("service") || p.contains("business"));
    let has_data = file_paths
        .iter()
        .any(|p| p.contains("model") || p.contains("repository") || p.contains("dao"));

    if has_api && has_service && has_data {
        return Some("layered".to_string());
    }

    let has_model = file_paths.iter().any(|p| p.contains("model"));
    let has_view = file_paths
        .iter()
        .any(|p| p.contains("view") || p.contains("template"));
    let has_controller = file_paths.iter().any(|p| p.contains("controller"));

    if has_model && has_view && has_controller {
        return Some("mvc".to_string());
    }

    // Threshold of >3 is arbitrary but intentional: one or two
    // "service"-named files is common in non-microservice projects too,
    // so a low bar would misclassify them.
    let service_count = file_paths.iter().filter(|p| p.contains("service")).count();
    if service_count > 3 {
        return Some("microservices".to_string());
    }

    None
}
