use crate::kb_loader::KnowledgeBase;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Chunk {
    pub id: String,
    pub chunk_type: ChunkType,
    pub content: String,
    pub metadata: ChunkMetadata,
    pub tags: Vec<String>,
    pub importance_score: f32,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "lowercase")]
pub enum ChunkType {
    Function,
    Class,
    Method,
    File,
    EntryPoint,
    #[serde(other)]
    Other,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ChunkMetadata {
    pub file_path: Option<String>,
    pub language: Option<String>,
    pub line_start: Option<usize>,
    pub line_end: Option<usize>,
    pub name: String,
    pub complexity: Option<usize>,
}

/// Convert KB to chunks with different granularity options
pub fn chunk_knowledge_base(kb: &KnowledgeBase, max_size: usize) -> Vec<Chunk> {
    let mut chunks = Vec::new();

    // Chunk 1: Entry points (highest priority)
    for entry_point in &kb.entry_points {
        if let Some((file_path, func)) = kb.get_function(&entry_point.function) {
            let content = format_function_with_context(func, file_path, kb);
            chunks.push(Chunk {
                id: entry_point.function.clone(),
                chunk_type: ChunkType::EntryPoint,
                content: truncate_content(&content, max_size),
                metadata: ChunkMetadata {
                    file_path: Some(file_path.clone()),
                    language: Some(kb.structure[file_path].language.clone()),
                    line_start: Some(func.line_start),
                    line_end: Some(func.line_end),
                    name: func.name.clone(),
                    complexity: Some(func.complexity),
                },
                tags: generate_tags(func, &entry_point.entry_type),
                importance_score: 1.0, // Entry points are most important
            });
        }
    }

    // Chunk 2: Regular functions
    for (file_path, file_struct) in &kb.structure {
        for func in &file_struct.functions {
            // Skip if already added as entry point
            if chunks.iter().any(|c| c.id == func.id) {
                continue;
            }

            let content = format_function_with_context(func, file_path, kb);
            chunks.push(Chunk {
                id: func.id.clone(),
                chunk_type: ChunkType::Function,
                content: truncate_content(&content, max_size),
                metadata: ChunkMetadata {
                    file_path: Some(file_path.clone()),
                    language: Some(file_struct.language.clone()),
                    line_start: Some(func.line_start),
                    line_end: Some(func.line_end),
                    name: func.name.clone(),
                    complexity: Some(func.complexity),
                },
                tags: generate_tags(func, "function"),
                importance_score: func.importance_score,
            });
        }
    }

    // Chunk 3: Classes and methods
    for (file_path, file_struct) in &kb.structure {
        for class in &file_struct.classes {
            // Create chunk for class overview
            let class_content = format_class_overview(class, file_path);
            chunks.push(Chunk {
                id: class.id.clone(),
                chunk_type: ChunkType::Class,
                content: truncate_content(&class_content, max_size),
                metadata: ChunkMetadata {
                    file_path: Some(file_path.clone()),
                    language: Some(file_struct.language.clone()),
                    line_start: Some(class.line_start),
                    line_end: Some(class.line_end),
                    name: class.name.clone(),
                    complexity: None,
                },
                tags: vec!["class".to_string(), file_struct.language.clone()],
                importance_score: 0.7,
            });

            // Create chunks for each method
            for method in &class.methods {
                let method_content = format_method_with_class_context(method, class, file_path, kb);
                chunks.push(Chunk {
                    id: method.id.clone(),
                    chunk_type: ChunkType::Method,
                    content: truncate_content(&method_content, max_size),
                    metadata: ChunkMetadata {
                        file_path: Some(file_path.clone()),
                        language: Some(file_struct.language.clone()),
                        line_start: Some(method.line_start),
                        line_end: Some(method.line_end),
                        name: format!("{}.{}", class.name, method.name),
                        complexity: Some(method.complexity),
                    },
                    tags: generate_tags(method, "method"),
                    importance_score: method.importance_score,
                });
            }
        }
    }

    // Chunk 4: File-level summaries (optional, for context)
    for (file_path, file_struct) in &kb.structure {
        let file_summary = format_file_summary(file_path, file_struct, kb);
        if !file_summary.is_empty() {
            chunks.push(Chunk {
                id: format!("file:{}", file_path),
                chunk_type: ChunkType::File,
                content: truncate_content(&file_summary, max_size),
                metadata: ChunkMetadata {
                    file_path: Some(file_path.clone()),
                    language: Some(file_struct.language.clone()),
                    line_start: Some(1),
                    line_end: Some(file_struct.loc),
                    name: file_path.clone(),
                    complexity: None,
                },
                tags: vec!["file".to_string(), file_struct.language.clone()],
                importance_score: 0.5,
            });
        }
    }

    chunks
}

fn format_function_with_context(
    func: &crate::kb_loader::Function,
    file_path: &str,
    _kb: &KnowledgeBase,
) -> String {
    let mut content = String::new();

    // Header
    content.push_str(&format!("// File: {}\n", file_path));
    content.push_str(&format!("// Function: {}\n", func.name));
    if !func.docstring.is_empty() {
        content.push_str(&format!("// Description: {}\n", func.docstring));
    }
    content.push_str(&format!("// Lines: {}-{}\n", func.line_start, func.line_end));
    content.push_str(&format!("// Complexity: {}\n", func.complexity));
    content.push_str("\n");

    // Signature
    content.push_str(&format!("{}\n", func.signature));
    content.push_str("\n");

    // Parameters
    if !func.params.is_empty() {
        content.push_str("Parameters:\n");
        for param in &func.params {
            content.push_str(&format!(
                "  - {}: {} {}\n",
                param.name,
                param.type_annotation,
                param.default_value.as_ref().map(|v| format!("= {}", v)).unwrap_or_default()
            ));
        }
        content.push_str("\n");
    }

    // Return type
    if !func.return_type.is_empty() {
        content.push_str(&format!("Returns: {}\n\n", func.return_type));
    }

    // Calls made by this function
    if !func.calls.is_empty() {
        content.push_str("Calls:\n");
        for call in func.calls.iter().take(10) {
            content.push_str(&format!("  - {} (line {})\n", call.callee, call.line));
        }
        if func.calls.len() > 10 {
            content.push_str(&format!("  ... and {} more\n", func.calls.len() - 10));
        }
        content.push_str("\n");
    }

    // Called by
    if !func.called_by.is_empty() {
        content.push_str("Called by:\n");
        for caller in func.called_by.iter().take(5) {
            content.push_str(&format!("  - {} in {}\n", caller.function, caller.file));
        }
        if func.called_by.len() > 5 {
            content.push_str(&format!("  ... and {} more\n", func.called_by.len() - 5));
        }
        content.push_str("\n");
    }

    // Control flow summary
    if func.control_flow.complexity > 0 {
        content.push_str(&format!(
            "Control flow: {} branches, {} loops\n",
            func.control_flow.branches.len(),
            func.control_flow.loops.len()
        ));
    }

    // Exception handling
    if !func.exceptions.raises.is_empty() || !func.exceptions.handles.is_empty() {
        content.push_str("Exceptions:\n");
        if !func.exceptions.raises.is_empty() {
            content.push_str(&format!("  Raises: {}\n", func.exceptions.raises.join(", ")));
        }
        if !func.exceptions.handles.is_empty() {
            content.push_str(&format!("  Handles: {}\n", func.exceptions.handles.join(", ")));
        }
        content.push_str("\n");
    }

    content
}

fn format_method_with_class_context(
    method: &crate::kb_loader::Function,
    class: &crate::kb_loader::Class,
    file_path: &str,
    kb: &KnowledgeBase,
) -> String {
    let mut content = String::new();

    content.push_str(&format!("// File: {}\n", file_path));
    content.push_str(&format!("// Class: {}\n", class.name));
    content.push_str(&format!("// Method: {}\n", method.name));
    if !method.docstring.is_empty() {
        content.push_str(&format!("// Description: {}\n", method.docstring));
    }
    content.push_str("\n");

    // Class context
    if !class.bases.is_empty() {
        content.push_str(&format!("// Inherits from: {}\n", class.bases.join(", ")));
    }

    content.push_str("\n");
    content.push_str(&format_function_with_context(method, file_path, kb));

    content
}

fn format_class_overview(class: &crate::kb_loader::Class, file_path: &str) -> String {
    let mut content = String::new();

    content.push_str(&format!("// File: {}\n", file_path));
    content.push_str(&format!("// Class: {}\n", class.name));
    if !class.docstring.is_empty() {
        content.push_str(&format!("// Description: {}\n", class.docstring));
    }
    content.push_str(&format!("// Lines: {}-{}\n", class.line_start, class.line_end));
    content.push_str("\n");

    // Base classes
    if !class.bases.is_empty() {
        content.push_str(&format!("Inherits from: {}\n\n", class.bases.join(", ")));
    }

    // Attributes
    if !class.attributes.is_empty() {
        content.push_str("Attributes:\n");
        for attr in &class.attributes {
            content.push_str(&format!(
                "  - {}: {}\n",
                attr.name,
                attr.type_annotation
            ));
        }
        content.push_str("\n");
    }

    // Methods summary
    if !class.methods.is_empty() {
        content.push_str(&format!("Methods ({}):\n", class.methods.len()));
        for method in &class.methods {
            content.push_str(&format!("  - {}{}\n", method.name,
                if method.is_async { " (async)" } else { "" }));
        }
        content.push_str("\n");
    }

    content
}

fn format_file_summary(
    file_path: &str,
    file_struct: &crate::kb_loader::FileStructure,
    _kb: &KnowledgeBase,
) -> String {
    let mut content = String::new();

    content.push_str(&format!("File: {}\n", file_path));
    content.push_str(&format!("Language: {}\n", file_struct.language));
    content.push_str(&format!("Lines of code: {}\n\n", file_struct.loc));

    // Imports
    if !file_struct.imports.is_empty() {
        content.push_str("Imports:\n");
        for import in &file_struct.imports {
            content.push_str(&format!("  - {} ({})\n", import.module, import.import_type));
        }
        content.push_str("\n");
    }

    // Functions
    if !file_struct.functions.is_empty() {
        content.push_str(&format!("Functions: {}\n", file_struct.functions.len()));
        for func in file_struct.functions.iter().take(10) {
            content.push_str(&format!("  - {}\n", func.name));
        }
        if file_struct.functions.len() > 10 {
            content.push_str(&format!("  ... and {} more\n", file_struct.functions.len() - 10));
        }
        content.push_str("\n");
    }

    // Classes
    if !file_struct.classes.is_empty() {
        content.push_str(&format!("Classes: {}\n", file_struct.classes.len()));
        for class in &file_struct.classes {
            content.push_str(&format!("  - {}\n", class.name));
        }
        content.push_str("\n");
    }

    content
}

fn generate_tags(func: &crate::kb_loader::Function, base_tag: &str) -> Vec<String> {
    let mut tags = vec![base_tag.to_string()];

    // Add async tag
    if func.is_async {
        tags.push("async".to_string());
    }

    // Add tags from function
    tags.extend(func.tags.iter().cloned());

    // Add complexity-based tags
    if func.complexity > 10 {
        tags.push("complex".to_string());
    }

    // Add decorator-based tags
    for decorator in &func.decorators {
        if decorator.contains("api") || decorator.contains("route") {
            tags.push("api".to_string());
        }
        if decorator.contains("test") {
            tags.push("test".to_string());
        }
    }

    tags.sort();
    tags.dedup();
    tags
}

fn truncate_content(content: &str, max_size: usize) -> String {
    // Conservative estimate: 1 token ≈ 4 characters
    // BERT models have 512 token limit, so ~2000 chars is safe
    let safe_max = max_size.min(2000);

    if content.len() <= safe_max {
        content.to_string()
    } else {
        // Try to truncate at a newline for cleaner cuts
        let truncate_at = safe_max.saturating_sub(3);
        if let Some(newline_pos) = content[..truncate_at].rfind('\n') {
            format!("{}...", &content[..newline_pos])
        } else {
            format!("{}...", &content[..truncate_at])
        }
    }
}
