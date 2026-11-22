// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! FlatBuffers encoding for the files eulix-parser writes, and their JSON
//! copies.
//!
//! Every `encode_*` function returns a finished buffer with its file
//! identifier. Every `*_json` function builds the `--emit-json` copy by
//! reading that buffer back through the generated accessors, never from the
//! Rust values, so the copy shows exactly what the binary file holds. See
//! docs/Architecture/Binary-Knowledge-Base.md.

use crate::analyze::summary::ProjectSummary;
use crate::fb;
use crate::struc::kb_struct::{
    Attribute, CallGraphEdge, CallGraphNode, Class, EntryPoint, ExternalDependency, FileData,
    Function, IndicesView, Metadata, MetricsReport, Parameter, PatternInfo, Todo,
};
use flatbuffers::{FlatBufferBuilder, ForwardsUOffset, Vector, WIPOffset};
use serde_json::{json, Map, Value};
use std::collections::{BTreeMap, HashMap};

/// On-disk format of every FlatBuffers output. Readers reject any other
/// value. Keep in step with `KBFormatVersion` in eulix-cli.
pub const FORMAT_VERSION: u32 = 1;

type StrVec<'a> = WIPOffset<Vector<'a, ForwardsUOffset<&'a str>>>;

fn strs<'a>(fbb: &mut FlatBufferBuilder<'a>, items: &[String]) -> StrVec<'a> {
    let offs: Vec<_> = items.iter().map(|s| fbb.create_string(s)).collect();
    fbb.create_vector(&offs)
}

fn opt_str<'a>(fbb: &mut FlatBufferBuilder<'a>, s: &Option<String>) -> Option<WIPOffset<&'a str>> {
    s.as_deref().map(|s| fbb.create_string(s))
}

fn metadata<'a>(fbb: &mut FlatBufferBuilder<'a>, m: &Metadata) -> WIPOffset<fb::Metadata<'a>> {
    let args = fb::MetadataArgs {
        version: Some(fbb.create_string(&m.version)),
        git_hash: Some(fbb.create_string(&m.git_hash)),
        project_name: Some(fbb.create_string(&m.project_name)),
        project_hash: Some(fbb.create_string(&m.project_hash)),
        parsed_at: Some(fbb.create_string(&m.parsed_at)),
        languages: Some(strs(fbb, &m.languages)),
        total_files: m.total_files as u64,
        total_loc: m.total_loc as u64,
        total_functions: m.total_functions as u64,
        total_classes: m.total_classes as u64,
        total_methods: m.total_methods as u64,
    };
    fb::Metadata::create(fbb, &args)
}

fn pattern_info<'a>(
    fbb: &mut FlatBufferBuilder<'a>,
    p: &PatternInfo,
) -> WIPOffset<fb::PatternInfo<'a>> {
    let args = fb::PatternInfoArgs {
        naming_convention: Some(fbb.create_string(&p.naming_convention)),
        structure_type: Some(fbb.create_string(&p.structure_type)),
        architecture_style: opt_str(fbb, &p.architecture_style),
    };
    fb::PatternInfo::create(fbb, &args)
}

/// `BTreeMap` iterates in key order, which is the order `(key)` lookups need.
fn string_lists<'a>(
    fbb: &mut FlatBufferBuilder<'a>,
    map: &BTreeMap<String, Vec<String>>,
) -> WIPOffset<Vector<'a, ForwardsUOffset<fb::StringList<'a>>>> {
    let entries: Vec<_> = map
        .iter()
        .map(|(k, v)| {
            let args = fb::StringListArgs {
                key: Some(fbb.create_string(k)),
                values: Some(strs(fbb, v)),
            };
            fb::StringList::create(fbb, &args)
        })
        .collect();
    fbb.create_vector(&entries)
}

pub fn encode_entry_points(eps: &[EntryPoint]) -> Vec<u8> {
    let mut fbb = FlatBufferBuilder::new();
    let items: Vec<_> = eps
        .iter()
        .map(|e| {
            let args = fb::EntryPointArgs {
                entry_type: Some(fbb.create_string(&e.entry_type)),
                path: opt_str(&mut fbb, &e.path),
                function: Some(fbb.create_string(&e.function)),
                handler: Some(fbb.create_string(&e.handler)),
                file: Some(fbb.create_string(&e.file)),
                line: e.line as u64,
                methods: e.methods.as_deref().map(|m| strs(&mut fbb, m)),
            };
            fb::EntryPoint::create(&mut fbb, &args)
        })
        .collect();
    let entry_points = Some(fbb.create_vector(&items));
    let root = fb::EntryPoints::create(
        &mut fbb,
        &fb::EntryPointsArgs {
            format_version: FORMAT_VERSION,
            entry_points,
        },
    );
    fb::finish_entry_points_buffer(&mut fbb, root);
    fbb.finished_data().to_vec()
}

pub fn encode_external_deps(deps: &[ExternalDependency]) -> Vec<u8> {
    let mut fbb = FlatBufferBuilder::new();
    let items: Vec<_> = deps
        .iter()
        .map(|d| {
            let args = fb::ExternalDependencyArgs {
                name: Some(fbb.create_string(&d.name)),
                version: opt_str(&mut fbb, &d.version),
                source: Some(fbb.create_string(&d.source)),
                used_by: Some(strs(&mut fbb, &d.used_by)),
                import_count: d.import_count as u64,
            };
            fb::ExternalDependency::create(&mut fbb, &args)
        })
        .collect();
    let external_dependencies = Some(fbb.create_vector(&items));
    let root = fb::ExternalDeps::create(
        &mut fbb,
        &fb::ExternalDepsArgs {
            format_version: FORMAT_VERSION,
            external_dependencies,
        },
    );
    fb::finish_external_deps_buffer(&mut fbb, root);
    fbb.finished_data().to_vec()
}

pub fn encode_metrics(report: &MetricsReport<'_>) -> Vec<u8> {
    let mut fbb = FlatBufferBuilder::new();
    let md = metadata(&mut fbb, report.metadata);
    let items: Vec<_> = report
        .top_complex_functions
        .iter()
        .map(|f| {
            let args = fb::FunctionMetricArgs {
                name: Some(fbb.create_string(f.name)),
                file: Some(fbb.create_string(f.file)),
                complexity: f.complexity as u64,
                importance_score: f.importance_score,
                line_start: f.line_start as u64,
                line_end: f.line_end as u64,
            };
            fb::FunctionMetric::create(&mut fbb, &args)
        })
        .collect();
    let top_complex_functions = Some(fbb.create_vector(&items));
    let root = fb::Metrics::create(
        &mut fbb,
        &fb::MetricsArgs {
            format_version: FORMAT_VERSION,
            metadata: Some(md),
            top_complex_functions,
        },
    );
    fb::finish_metrics_buffer(&mut fbb, root);
    fbb.finished_data().to_vec()
}

pub fn encode_summary(s: &ProjectSummary) -> Vec<u8> {
    let mut fbb = FlatBufferBuilder::new();
    let project_name = Some(fbb.create_string(&s.project_name));
    let languages = Some(strs(&mut fbb, &s.languages));
    let categories = Some(string_lists(&mut fbb, &s.categories));
    let key_features = Some(strs(&mut fbb, &s.key_features));
    let entry_points = Some(strs(&mut fbb, &s.entry_points));
    let dep_args = fb::DependencyInfoArgs {
        stdlib: Some(strs(&mut fbb, &s.dependencies.stdlib)),
        third_party: Some(strs(&mut fbb, &s.dependencies.third_party)),
    };
    let dependencies = Some(fb::DependencyInfo::create(&mut fbb, &dep_args));
    let patterns = Some(pattern_info(&mut fbb, &s.patterns));
    let root = fb::Summary::create(
        &mut fbb,
        &fb::SummaryArgs {
            format_version: FORMAT_VERSION,
            project_name,
            total_files: s.total_files as u64,
            total_loc: s.total_loc as u64,
            languages,
            categories,
            key_features,
            entry_points,
            dependencies,
            patterns,
        },
    );
    fb::finish_summary_buffer(&mut fbb, root);
    fbb.finished_data().to_vec()
}

pub fn encode_patterns(p: &PatternInfo) -> Vec<u8> {
    let mut fbb = FlatBufferBuilder::new();
    let patterns = Some(pattern_info(&mut fbb, p));
    let root = fb::Patterns::create(
        &mut fbb,
        &fb::PatternsArgs {
            format_version: FORMAT_VERSION,
            patterns,
        },
    );
    fb::finish_patterns_buffer(&mut fbb, root);
    fbb.finished_data().to_vec()
}

/// A FlatBuffer cannot pass 2 GiB (32-bit offsets); the builder panics when
/// it would. Turn that into an error naming the file instead of a crash.
#[derive(Debug)]
pub struct EncodeError(pub String);

impl std::fmt::Display for EncodeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for EncodeError {}

fn guarded(kind: &str, f: impl FnOnce() -> Vec<u8>) -> Result<Vec<u8>, EncodeError> {
    std::panic::catch_unwind(std::panic::AssertUnwindSafe(f)).map_err(|p| {
        let why = p
            .downcast_ref::<&str>()
            .map(|s| s.to_string())
            .or_else(|| p.downcast_ref::<String>().cloned())
            .unwrap_or_default();
        EncodeError(format!(
            "{kind} is too large for one FlatBuffer (2 GiB limit): {why}"
        ))
    })
}

/// kb_call_graph.fb. `tags` maps node id to tags and `file_categories` maps
/// file to category; both iterate in key order, as `(key)` lookups need.
pub fn encode_call_graph(
    nodes: &[CallGraphNode],
    edges: &[CallGraphEdge],
    tags: &BTreeMap<&str, &[String]>,
    file_categories: &BTreeMap<&str, &str>,
) -> Result<Vec<u8>, EncodeError> {
    guarded("call graph", || {
        let mut fbb = FlatBufferBuilder::with_capacity(1 << 20);
        let mut sh = Shared::new();
        let node_offs: Vec<_> = nodes
            .iter()
            .map(|n| {
                let args = fb::CallGraphNodeArgs {
                    id: Some(sh.get(&mut fbb, &n.id)),
                    node_type: Some(sh.get(&mut fbb, &n.node_type)),
                    file: Some(sh.get(&mut fbb, &n.file)),
                    is_entry_point: n.is_entry_point,
                    call_count_estimate: n.call_count_estimate as u64,
                };
                fb::CallGraphNode::create(&mut fbb, &args)
            })
            .collect();
        let nodes = Some(fbb.create_vector(&node_offs));
        let edge_offs: Vec<_> = edges
            .iter()
            .map(|e| {
                let args = fb::CallGraphEdgeArgs {
                    from: Some(sh.get(&mut fbb, &e.from)),
                    to: Some(sh.get(&mut fbb, &e.to)),
                    edge_type: Some(sh.get(&mut fbb, &e.edge_type)),
                    conditional: e.conditional,
                    call_site_line: e.call_site_line as u64,
                };
                fb::CallGraphEdge::create(&mut fbb, &args)
            })
            .collect();
        let edges = Some(fbb.create_vector(&edge_offs));
        let tag_offs: Vec<_> = tags
            .iter()
            .map(|(id, ts)| {
                let key = Some(sh.get(&mut fbb, id));
                let vals: Vec<_> = ts.iter().map(|t| sh.get(&mut fbb, t)).collect();
                let values = Some(fbb.create_vector(&vals));
                fb::StringList::create(&mut fbb, &fb::StringListArgs { key, values })
            })
            .collect();
        let tags = Some(fbb.create_vector(&tag_offs));
        let cat_offs: Vec<_> = file_categories
            .iter()
            .map(|(file, cat)| {
                let args = fb::FileCategoryArgs {
                    key: Some(sh.get(&mut fbb, file)),
                    category: Some(sh.get(&mut fbb, cat)),
                };
                fb::FileCategory::create(&mut fbb, &args)
            })
            .collect();
        let file_categories = Some(fbb.create_vector(&cat_offs));
        let root = fb::CallGraph::create(
            &mut fbb,
            &fb::CallGraphArgs {
                format_version: FORMAT_VERSION,
                nodes,
                edges,
                tags,
                file_categories,
            },
        );
        fb::finish_call_graph_buffer(&mut fbb, root);
        fbb.finished_data().to_vec()
    })
}

/// Writes each distinct string once per buffer.
///
/// `FlatBufferBuilder::create_shared_string` keeps its pool in a sorted `Vec`
/// and inserts into it, which is quadratic in the number of distinct strings:
/// on Kubernetes the index took 2.4 s to encode with it. A hash map makes
/// this linear.
struct Shared<'s, 'a> {
    seen: HashMap<&'s str, WIPOffset<&'a str>>,
}

impl<'s, 'a> Shared<'s, 'a> {
    fn new() -> Self {
        Self {
            seen: HashMap::new(),
        }
    }

    fn get(&mut self, fbb: &mut FlatBufferBuilder<'a>, s: &'s str) -> WIPOffset<&'a str> {
        *self.seen.entry(s).or_insert_with(|| fbb.create_string(s))
    }
}

/// A `(key)`-sorted StringList vector from any map of string lists, with every
/// string shared (stored once per buffer).
fn shared_string_lists<'s, 'a, K, V, S>(
    fbb: &mut FlatBufferBuilder<'a>,
    shared: &mut Shared<'s, 'a>,
    map: &'s BTreeMap<K, V>,
) -> WIPOffset<Vector<'a, ForwardsUOffset<fb::StringList<'a>>>>
where
    K: AsRef<str>,
    V: AsRef<[S]>,
    S: AsRef<str> + 's,
{
    let entries: Vec<_> = map
        .iter()
        .map(|(k, vs)| {
            let key = Some(shared.get(fbb, k.as_ref()));
            let offs: Vec<_> = vs
                .as_ref()
                .iter()
                .map(|v| shared.get(fbb, v.as_ref()))
                .collect();
            let values = Some(fbb.create_vector(&offs));
            fb::StringList::create(fbb, &fb::StringListArgs { key, values })
        })
        .collect();
    fbb.create_vector(&entries)
}

/// kb_index.fb.
pub fn encode_indices(ix: &IndicesView<'_>) -> Result<Vec<u8>, EncodeError> {
    guarded("index", || {
        let mut fbb = FlatBufferBuilder::with_capacity(1 << 20);
        let mut sh = Shared::new();
        let functions_by_name = Some(shared_string_lists(
            &mut fbb,
            &mut sh,
            &ix.functions_by_name,
        ));
        let functions_calling = Some(shared_string_lists(
            &mut fbb,
            &mut sh,
            &ix.functions_calling,
        ));
        let functions_by_tag = Some(shared_string_lists(&mut fbb, &mut sh, &ix.functions_by_tag));
        let types_by_name = Some(shared_string_lists(&mut fbb, &mut sh, &ix.types_by_name));
        let files_by_category = Some(shared_string_lists(
            &mut fbb,
            &mut sh,
            &ix.files_by_category,
        ));
        let root = fb::Indices::create(
            &mut fbb,
            &fb::IndicesArgs {
                format_version: FORMAT_VERSION,
                functions_by_name,
                functions_calling,
                functions_by_tag,
                types_by_name,
                files_by_category,
            },
        );
        fb::finish_indices_buffer(&mut fbb, root);
        fbb.finished_data().to_vec()
    })
}

// ---------- kb.fb: header + one record per source file ----------

/// Records start at multiples of this, relative to the records region.
pub const KB_RECORD_ALIGN: usize = 8;

/// Bytes of zero padding that bring `n` up to a multiple of `KB_RECORD_ALIGN`.
pub fn kb_pad(n: usize) -> usize {
    (KB_RECORD_ALIGN - n % KB_RECORD_ALIGN) % KB_RECORD_ALIGN
}

fn params<'a>(
    fbb: &mut FlatBufferBuilder<'a>,
    ps: &[Parameter],
) -> WIPOffset<Vector<'a, ForwardsUOffset<fb::Parameter<'a>>>> {
    let offs: Vec<_> = ps
        .iter()
        .map(|p| {
            let args = fb::ParameterArgs {
                name: Some(fbb.create_string(&p.name)),
                type_annotation: Some(fbb.create_string(&p.type_annotation)),
                default_value: opt_str(fbb, &p.default_value),
            };
            fb::Parameter::create(fbb, &args)
        })
        .collect();
    fbb.create_vector(&offs)
}

fn function<'a>(fbb: &mut FlatBufferBuilder<'a>, f: &Function) -> WIPOffset<fb::Function<'a>> {
    let args = fb::FunctionArgs {
        id: Some(fbb.create_string(&f.id)),
        name: Some(fbb.create_string(&f.name)),
        signature: Some(fbb.create_string(&f.signature)),
        params: Some(params(fbb, &f.params)),
        return_type: Some(fbb.create_string(&f.return_type)),
        docstring: Some(fbb.create_string(&f.docstring)),
        line_start: f.line_start as u64,
        line_end: f.line_end as u64,
    };
    fb::Function::create(fbb, &args)
}

fn functions<'a>(
    fbb: &mut FlatBufferBuilder<'a>,
    fs: &[Function],
) -> WIPOffset<Vector<'a, ForwardsUOffset<fb::Function<'a>>>> {
    let offs: Vec<_> = fs.iter().map(|f| function(fbb, f)).collect();
    fbb.create_vector(&offs)
}

fn attributes<'a>(
    fbb: &mut FlatBufferBuilder<'a>,
    attrs: &[Attribute],
) -> WIPOffset<Vector<'a, ForwardsUOffset<fb::Attribute<'a>>>> {
    let offs: Vec<_> = attrs
        .iter()
        .map(|a| {
            let args = fb::AttributeArgs {
                name: Some(fbb.create_string(&a.name)),
                type_annotation: Some(fbb.create_string(&a.type_annotation)),
                value: opt_str(fbb, &a.value),
            };
            fb::Attribute::create(fbb, &args)
        })
        .collect();
    fbb.create_vector(&offs)
}

fn class<'a>(fbb: &mut FlatBufferBuilder<'a>, c: &Class) -> WIPOffset<fb::Class<'a>> {
    let args = fb::ClassArgs {
        id: Some(fbb.create_string(&c.id)),
        name: Some(fbb.create_string(&c.name)),
        bases: Some(strs(fbb, &c.bases)),
        docstring: Some(fbb.create_string(&c.docstring)),
        line_start: c.line_start as u64,
        line_end: c.line_end as u64,
        methods: Some(functions(fbb, &c.methods)),
        attributes: Some(attributes(fbb, &c.attributes)),
        decorators: Some(strs(fbb, &c.decorators)),
    };
    fb::Class::create(fbb, &args)
}

fn todos<'a>(
    fbb: &mut FlatBufferBuilder<'a>,
    ts: &[Todo],
) -> WIPOffset<Vector<'a, ForwardsUOffset<fb::Todo<'a>>>> {
    let offs: Vec<_> = ts
        .iter()
        .map(|t| {
            let args = fb::TodoArgs {
                line: t.line as u64,
                text: Some(fbb.create_string(&t.text)),
                priority: Some(fbb.create_string(&t.priority)),
            };
            fb::Todo::create(fbb, &args)
        })
        .collect();
    fbb.create_vector(&offs)
}

/// One file's record for kb.fb: the fields of the parser's
/// `FileDataSimpleView` (language, loc, functions, classes, todos).
pub fn encode_file_record(fd: &FileData) -> Vec<u8> {
    let mut fbb = FlatBufferBuilder::with_capacity(8 * 1024);
    let language = Some(fbb.create_string(&fd.language));
    let funcs = Some(functions(&mut fbb, &fd.functions));
    let class_offs: Vec<_> = fd.classes.iter().map(|c| class(&mut fbb, c)).collect();
    let classes = Some(fbb.create_vector(&class_offs));
    let todo_list = Some(todos(&mut fbb, &fd.todos));
    let root = fb::FileRecord::create(
        &mut fbb,
        &fb::FileRecordArgs {
            language,
            loc: fd.loc as u64,
            functions: funcs,
            classes,
            todos: todo_list,
        },
    );
    fb::finish_file_record_buffer(&mut fbb, root);
    fbb.finished_data().to_vec()
}

/// One header entry: a file's path and where its record sits in the records
/// region. `entries` must be sorted by path, as `(key)` lookups need.
pub struct KbEntry<'s> {
    pub path: &'s str,
    pub offset: u64,
    pub length: u32,
}

/// The size-prefixed header that opens kb.fb.
pub fn encode_kb_header(
    metadata_in: &Metadata,
    entries: &[KbEntry<'_>],
) -> Result<Vec<u8>, EncodeError> {
    guarded("knowledge base header", || {
        let mut fbb = FlatBufferBuilder::with_capacity(64 * 1024);
        let md = metadata(&mut fbb, metadata_in);
        let offs: Vec<_> = entries
            .iter()
            .map(|e| {
                let args = fb::FileEntryArgs {
                    path: Some(fbb.create_string(e.path)),
                    offset: e.offset,
                    length: e.length,
                };
                fb::FileEntry::create(&mut fbb, &args)
            })
            .collect();
        let files = Some(fbb.create_vector(&offs));
        let root = fb::KbHeader::create(
            &mut fbb,
            &fb::KbHeaderArgs {
                format_version: FORMAT_VERSION,
                metadata: Some(md),
                files,
            },
        );
        fb::finish_size_prefixed_kb_header_buffer(&mut fbb, root);
        fbb.finished_data().to_vec()
    })
}

// ---------- checksum.fb.zst ----------

/// One file's checksum entry. `files` passed to `encode_checksum` must be
/// sorted by path, as `(key)` lookups need.
pub struct ChecksumEntry<'s> {
    pub path: &'s str,
    pub hash: &'s str,
    pub size: i64,
    pub mod_time: i64,
}

/// The checksum baseline, before zstd compression.
pub fn encode_checksum(
    project_path: &str,
    hash: &str,
    files: &[ChecksumEntry<'_>],
    last_analyzed: &str,
    analysis_version: &str,
) -> Result<Vec<u8>, EncodeError> {
    guarded("checksum", || {
        let mut fbb = FlatBufferBuilder::with_capacity(64 * 1024);
        let offs: Vec<_> = files
            .iter()
            .map(|f| {
                let args = fb::ChecksumFileArgs {
                    path: Some(fbb.create_string(f.path)),
                    hash: Some(fbb.create_string(f.hash)),
                    size: f.size,
                    mod_time: f.mod_time,
                };
                fb::ChecksumFile::create(&mut fbb, &args)
            })
            .collect();
        let files_vec = Some(fbb.create_vector(&offs));
        let args = fb::ChecksumArgs {
            format_version: FORMAT_VERSION,
            project_path: Some(fbb.create_string(project_path)),
            total_files: files.len() as u64,
            hash: Some(fbb.create_string(hash)),
            files: files_vec,
            last_analyzed: Some(fbb.create_string(last_analyzed)),
            analysis_version: Some(fbb.create_string(analysis_version)),
        };
        let root = fb::Checksum::create(&mut fbb, &args);
        fb::finish_checksum_buffer(&mut fbb, root);
        fbb.finished_data().to_vec()
    })
}

// ---------- JSON copies, read back from the buffers ----------

/// Error for a buffer that is not the expected eulix output.
#[derive(Debug)]
pub struct DecodeError(pub String);

impl std::fmt::Display for DecodeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for DecodeError {}

/// The identifier is checked before verifying, so handing a reader the wrong
/// file names the real problem instead of a confusing verifier error.
fn ensure_id(kind: &str, has_id: bool) -> Result<(), DecodeError> {
    if has_id {
        Ok(())
    } else {
        Err(DecodeError(format!(
            "not a eulix {kind} file (wrong identifier)"
        )))
    }
}

fn check_version(kind: &str, version: u32) -> Result<(), DecodeError> {
    if version == FORMAT_VERSION {
        Ok(())
    } else {
        Err(DecodeError(format!(
            "{kind} has format version {version}, expected {FORMAT_VERSION}; run 'eulix analyze'"
        )))
    }
}

fn verr(kind: &str) -> impl Fn(flatbuffers::InvalidFlatbuffer) -> DecodeError + '_ {
    move |e| DecodeError(format!("corrupt {kind} file: {e}"))
}

/// An f32 as its shortest decimal form (0.6, not 0.6000000238418579), which is
/// what flatc prints for the same field.
fn f32_json(x: f32) -> Value {
    x.to_string()
        .parse::<f64>()
        .map(Value::from)
        .unwrap_or(Value::Null)
}

fn str_list(v: Option<Vector<'_, ForwardsUOffset<&str>>>) -> Value {
    Value::Array(v.into_iter().flatten().map(Value::from).collect())
}

/// Inserts `key` only when the field is present, the way flatc's JSON output
/// leaves out absent optional fields.
fn put_opt(obj: &mut Map<String, Value>, key: &str, v: Option<Value>) {
    if let Some(v) = v {
        obj.insert(key.to_string(), v);
    }
}

fn metadata_json(m: fb::Metadata<'_>) -> Value {
    json!({
        "version": m.version().unwrap_or_default(),
        "git_hash": m.git_hash().unwrap_or_default(),
        "project_name": m.project_name().unwrap_or_default(),
        "project_hash": m.project_hash().unwrap_or_default(),
        "parsed_at": m.parsed_at().unwrap_or_default(),
        "languages": str_list(m.languages()),
        "total_files": m.total_files(),
        "total_loc": m.total_loc(),
        "total_functions": m.total_functions(),
        "total_classes": m.total_classes(),
        "total_methods": m.total_methods(),
    })
}

fn pattern_info_json(p: fb::PatternInfo<'_>) -> Value {
    let mut o = Map::new();
    o.insert(
        "naming_convention".into(),
        p.naming_convention().unwrap_or_default().into(),
    );
    o.insert(
        "structure_type".into(),
        p.structure_type().unwrap_or_default().into(),
    );
    put_opt(
        &mut o,
        "architecture_style",
        p.architecture_style().map(Value::from),
    );
    Value::Object(o)
}

pub fn entry_points_json(buf: &[u8]) -> Result<Value, DecodeError> {
    ensure_id("entry points", fb::entry_points_buffer_has_identifier(buf))?;
    let root = fb::root_as_entry_points(buf).map_err(verr("entry points"))?;
    check_version("entry points", root.format_version())?;
    let items: Vec<Value> = root
        .entry_points()
        .into_iter()
        .flatten()
        .map(|e| {
            let mut o = Map::new();
            o.insert(
                "entry_type".into(),
                e.entry_type().unwrap_or_default().into(),
            );
            put_opt(&mut o, "path", e.path().map(Value::from));
            o.insert("function".into(), e.function().unwrap_or_default().into());
            o.insert("handler".into(), e.handler().unwrap_or_default().into());
            o.insert("file".into(), e.file().unwrap_or_default().into());
            o.insert("line".into(), e.line().into());
            put_opt(&mut o, "methods", e.methods().map(|m| str_list(Some(m))));
            Value::Object(o)
        })
        .collect();
    Ok(json!({ "format_version": root.format_version(), "entry_points": items }))
}

pub fn external_deps_json(buf: &[u8]) -> Result<Value, DecodeError> {
    ensure_id(
        "external deps",
        fb::external_deps_buffer_has_identifier(buf),
    )?;
    let root = fb::root_as_external_deps(buf).map_err(verr("external deps"))?;
    check_version("external deps", root.format_version())?;
    let items: Vec<Value> = root
        .external_dependencies()
        .into_iter()
        .flatten()
        .map(|d| {
            let mut o = Map::new();
            o.insert("name".into(), d.name().unwrap_or_default().into());
            put_opt(&mut o, "version", d.version().map(Value::from));
            o.insert("source".into(), d.source().unwrap_or_default().into());
            o.insert("used_by".into(), str_list(d.used_by()));
            o.insert("import_count".into(), d.import_count().into());
            Value::Object(o)
        })
        .collect();
    Ok(json!({ "format_version": root.format_version(), "external_dependencies": items }))
}

pub fn metrics_json(buf: &[u8]) -> Result<Value, DecodeError> {
    ensure_id("metrics", fb::metrics_buffer_has_identifier(buf))?;
    let root = fb::root_as_metrics(buf).map_err(verr("metrics"))?;
    check_version("metrics", root.format_version())?;
    let items: Vec<Value> = root
        .top_complex_functions()
        .into_iter()
        .flatten()
        .map(|f| {
            json!({
                "name": f.name().unwrap_or_default(),
                "file": f.file().unwrap_or_default(),
                "complexity": f.complexity(),
                "importance_score": f32_json(f.importance_score()),
                "line_start": f.line_start(),
                "line_end": f.line_end(),
            })
        })
        .collect();
    let mut o = Map::new();
    o.insert("format_version".into(), root.format_version().into());
    put_opt(&mut o, "metadata", root.metadata().map(metadata_json));
    o.insert("top_complex_functions".into(), Value::Array(items));
    Ok(Value::Object(o))
}

pub fn summary_json(buf: &[u8]) -> Result<Value, DecodeError> {
    ensure_id("summary", fb::summary_buffer_has_identifier(buf))?;
    let s = fb::root_as_summary(buf).map_err(verr("summary"))?;
    check_version("summary", s.format_version())?;
    let categories: Vec<Value> = s
        .categories()
        .into_iter()
        .flatten()
        .map(|c| json!({ "key": c.key(), "values": str_list(c.values()) }))
        .collect();
    let mut o = Map::new();
    o.insert("format_version".into(), s.format_version().into());
    o.insert(
        "project_name".into(),
        s.project_name().unwrap_or_default().into(),
    );
    o.insert("total_files".into(), s.total_files().into());
    o.insert("total_loc".into(), s.total_loc().into());
    o.insert("languages".into(), str_list(s.languages()));
    o.insert("categories".into(), Value::Array(categories));
    o.insert("key_features".into(), str_list(s.key_features()));
    o.insert("entry_points".into(), str_list(s.entry_points()));
    put_opt(
        &mut o,
        "dependencies",
        s.dependencies().map(
            |d| json!({ "stdlib": str_list(d.stdlib()), "third_party": str_list(d.third_party()) }),
        ),
    );
    put_opt(&mut o, "patterns", s.patterns().map(pattern_info_json));
    Ok(Value::Object(o))
}

pub fn patterns_json(buf: &[u8]) -> Result<Value, DecodeError> {
    ensure_id("patterns", fb::patterns_buffer_has_identifier(buf))?;
    let root = fb::root_as_patterns(buf).map_err(verr("patterns"))?;
    check_version("patterns", root.format_version())?;
    let mut o = Map::new();
    o.insert("format_version".into(), root.format_version().into());
    put_opt(&mut o, "patterns", root.patterns().map(pattern_info_json));
    Ok(Value::Object(o))
}

/// Verifier options for the large outputs: the defaults cap a buffer at
/// about one million tables, which a big repository's call graph passes.
fn large_opts() -> flatbuffers::VerifierOptions {
    flatbuffers::VerifierOptions {
        max_tables: usize::MAX,
        max_apparent_size: usize::MAX,
        ..Default::default()
    }
}

pub fn call_graph_json(buf: &[u8]) -> Result<Value, DecodeError> {
    ensure_id("call graph", fb::call_graph_buffer_has_identifier(buf))?;
    let g = fb::root_as_call_graph_with_opts(&large_opts(), buf).map_err(verr("call graph"))?;
    check_version("call graph", g.format_version())?;
    let nodes: Vec<Value> = g
        .nodes()
        .into_iter()
        .flatten()
        .map(|n| {
            json!({
                "id": n.id().unwrap_or_default(),
                "node_type": n.node_type().unwrap_or_default(),
                "file": n.file().unwrap_or_default(),
                "is_entry_point": n.is_entry_point(),
                "call_count_estimate": n.call_count_estimate(),
            })
        })
        .collect();
    let edges: Vec<Value> = g
        .edges()
        .into_iter()
        .flatten()
        .map(|e| {
            json!({
                "from": e.from().unwrap_or_default(),
                "to": e.to().unwrap_or_default(),
                "edge_type": e.edge_type().unwrap_or_default(),
                "conditional": e.conditional(),
                "call_site_line": e.call_site_line(),
            })
        })
        .collect();
    let tags: Vec<Value> = g
        .tags()
        .into_iter()
        .flatten()
        .map(|t| json!({ "key": t.key(), "values": str_list(t.values()) }))
        .collect();
    let cats: Vec<Value> = g
        .file_categories()
        .into_iter()
        .flatten()
        .map(|c| json!({ "key": c.key(), "category": c.category().unwrap_or_default() }))
        .collect();
    Ok(json!({
        "format_version": g.format_version(),
        "nodes": nodes,
        "edges": edges,
        "tags": tags,
        "file_categories": cats,
    }))
}

fn string_lists_json(v: Option<Vector<'_, ForwardsUOffset<fb::StringList<'_>>>>) -> Value {
    Value::Array(
        v.into_iter()
            .flatten()
            .map(|e| json!({ "key": e.key(), "values": str_list(e.values()) }))
            .collect(),
    )
}

pub fn indices_json(buf: &[u8]) -> Result<Value, DecodeError> {
    ensure_id("index", fb::indices_buffer_has_identifier(buf))?;
    let ix = fb::root_as_indices_with_opts(&large_opts(), buf).map_err(verr("index"))?;
    check_version("index", ix.format_version())?;
    Ok(json!({
        "format_version": ix.format_version(),
        "functions_by_name": string_lists_json(ix.functions_by_name()),
        "functions_calling": string_lists_json(ix.functions_calling()),
        "functions_by_tag": string_lists_json(ix.functions_by_tag()),
        "types_by_name": string_lists_json(ix.types_by_name()),
        "files_by_category": string_lists_json(ix.files_by_category()),
    }))
}

fn opt_json(v: Option<&str>) -> Option<Value> {
    v.map(Value::from)
}

fn function_json(f: fb::Function<'_>) -> Value {
    let params: Vec<Value> = f
        .params()
        .into_iter()
        .flatten()
        .map(|p| {
            let mut o = Map::new();
            o.insert("name".into(), p.name().unwrap_or_default().into());
            o.insert(
                "type_annotation".into(),
                p.type_annotation().unwrap_or_default().into(),
            );
            put_opt(&mut o, "default_value", opt_json(p.default_value()));
            Value::Object(o)
        })
        .collect();
    json!({
        "id": f.id().unwrap_or_default(),
        "name": f.name().unwrap_or_default(),
        "signature": f.signature().unwrap_or_default(),
        "params": params,
        "return_type": f.return_type().unwrap_or_default(),
        "docstring": f.docstring().unwrap_or_default(),
        "line_start": f.line_start(),
        "line_end": f.line_end(),
    })
}

/// The JSON copy of one FileRecord buffer.
pub fn file_record_json(buf: &[u8]) -> Result<Value, DecodeError> {
    ensure_id("file record", fb::file_record_buffer_has_identifier(buf))?;
    let r = fb::root_as_file_record_with_opts(&large_opts(), buf).map_err(verr("file record"))?;
    let functions: Vec<Value> = r
        .functions()
        .into_iter()
        .flatten()
        .map(function_json)
        .collect();
    let classes: Vec<Value> = r
        .classes()
        .into_iter()
        .flatten()
        .map(|c| {
            let attrs: Vec<Value> = c
                .attributes()
                .into_iter()
                .flatten()
                .map(|a| {
                    let mut o = Map::new();
                    o.insert("name".into(), a.name().unwrap_or_default().into());
                    o.insert(
                        "type_annotation".into(),
                        a.type_annotation().unwrap_or_default().into(),
                    );
                    put_opt(&mut o, "value", opt_json(a.value()));
                    Value::Object(o)
                })
                .collect();
            json!({
                "id": c.id().unwrap_or_default(),
                "name": c.name().unwrap_or_default(),
                "bases": str_list(c.bases()),
                "docstring": c.docstring().unwrap_or_default(),
                "line_start": c.line_start(),
                "line_end": c.line_end(),
                "methods": c.methods().into_iter().flatten().map(function_json).collect::<Vec<_>>(),
                "attributes": attrs,
                "decorators": str_list(c.decorators()),
            })
        })
        .collect();
    let todos: Vec<Value> = r
        .todos()
        .into_iter()
        .flatten()
        .map(|t| {
            json!({
                "line": t.line(),
                "text": t.text().unwrap_or_default(),
                "priority": t.priority().unwrap_or_default(),
            })
        })
        .collect();
    Ok(json!({
        "language": r.language().unwrap_or_default(),
        "loc": r.loc(),
        "functions": functions,
        "classes": classes,
        "todos": todos,
    }))
}

/// Decoded kb.fb header: the JSON copy's top-level fields and each file's
/// `(path, offset, length)`, plus where the records region starts.
pub struct KbHeaderInfo {
    pub json: Value,
    pub files: Vec<(String, u64, u32)>,
    pub records_start: usize,
}

/// Reads the size-prefixed header at the start of kb.fb.
pub fn kb_header(buf: &[u8]) -> Result<KbHeaderInfo, DecodeError> {
    if buf.len() < 8 {
        return Err(DecodeError("knowledge base file is too short".into()));
    }
    let n = u32::from_le_bytes([buf[0], buf[1], buf[2], buf[3]]) as usize;
    let end = 4usize
        .checked_add(n)
        .filter(|&e| e <= buf.len())
        .ok_or_else(|| DecodeError("knowledge base header runs past the end of the file".into()))?;
    let head = &buf[..end];
    ensure_id(
        "knowledge base",
        fb::kb_header_size_prefixed_buffer_has_identifier(head),
    )?;
    let h = fb::size_prefixed_root_as_kb_header_with_opts(&large_opts(), head)
        .map_err(verr("knowledge base header"))?;
    check_version("knowledge base", h.format_version())?;
    let files: Vec<(String, u64, u32)> = h
        .files()
        .into_iter()
        .flatten()
        .map(|e| {
            (
                e.path().to_string(),
                e.offset(),
                e.length(),
            )
        })
        .collect();
    let mut o = Map::new();
    o.insert("format_version".into(), h.format_version().into());
    put_opt(&mut o, "metadata", h.metadata().map(metadata_json));
    Ok(KbHeaderInfo {
        json: Value::Object(o),
        files,
        records_start: end + kb_pad(end),
    })
}

/// The JSON copy of a decompressed checksum buffer.
pub fn checksum_json(buf: &[u8]) -> Result<Value, DecodeError> {
    ensure_id("checksum", fb::checksum_buffer_has_identifier(buf))?;
    let c = fb::root_as_checksum_with_opts(&large_opts(), buf).map_err(verr("checksum"))?;
    check_version("checksum", c.format_version())?;
    let files: Vec<Value> = c
        .files()
        .into_iter()
        .flatten()
        .map(|f| {
            json!({
                "path": f.path(),
                "hash": f.hash().unwrap_or_default(),
                "size": f.size(),
                "mod_time": f.mod_time(),
            })
        })
        .collect();
    Ok(json!({
        "format_version": c.format_version(),
        "project_path": c.project_path().unwrap_or_default(),
        "total_files": c.total_files(),
        "hash": c.hash().unwrap_or_default(),
        "files": files,
        "last_analyzed": c.last_analyzed().unwrap_or_default(),
        "analysis_version": c.analysis_version().unwrap_or_default(),
    }))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::analyze::summary::DependencyInfo;
    use crate::struc::kb_struct::FunctionMetric;

    fn md() -> Metadata {
        Metadata {
            version: "0.9.0".into(),
            git_hash: "abc".into(),
            project_name: "demo".into(),
            project_hash: "h".into(),
            parsed_at: "now".into(),
            languages: vec!["go".into(), "rust".into()],
            total_files: 3,
            total_loc: 120,
            total_functions: 7,
            total_classes: 1,
            total_methods: 2,
        }
    }

    #[test]
    fn entry_points_round_trip() {
        let eps = vec![
            EntryPoint {
                entry_type: "main".into(),
                path: None,
                function: "main".into(),
                handler: "main".into(),
                file: "main.go".into(),
                line: 5,
                methods: None,
            },
            EntryPoint {
                entry_type: "api_endpoint".into(),
                path: Some("/users".into()),
                function: "list".into(),
                handler: "list".into(),
                file: "api.py".into(),
                line: 9,
                methods: Some(vec!["GET".into(), "POST".into()]),
            },
        ];
        let buf = encode_entry_points(&eps);
        let v = entry_points_json(&buf).unwrap();
        assert_eq!(v["format_version"], 1);
        assert!(v["entry_points"][0].get("path").is_none());
        assert!(v["entry_points"][0].get("methods").is_none());
        assert_eq!(v["entry_points"][1]["path"], "/users");
        assert_eq!(v["entry_points"][1]["methods"], json!(["GET", "POST"]));
        assert_eq!(v["entry_points"][1]["line"], 9);
    }

    #[test]
    fn external_deps_round_trip() {
        let deps = vec![ExternalDependency {
            name: "serde".into(),
            version: Some("1.0".into()),
            source: "external".into(),
            used_by: vec!["a.rs".into(), "b.rs".into()],
            import_count: 2,
        }];
        let v = external_deps_json(&encode_external_deps(&deps)).unwrap();
        assert_eq!(
            v["external_dependencies"][0],
            json!({"name":"serde","version":"1.0","source":"external","used_by":["a.rs","b.rs"],"import_count":2})
        );
    }

    #[test]
    fn metrics_round_trip() {
        let m = md();
        let report = MetricsReport {
            metadata: &m,
            top_complex_functions: vec![FunctionMetric {
                name: "f",
                file: "x.rs",
                complexity: 12,
                importance_score: 0.5,
                line_start: 1,
                line_end: 40,
            }],
        };
        let v = metrics_json(&encode_metrics(&report)).unwrap();
        assert_eq!(v["metadata"]["languages"], json!(["go", "rust"]));
        assert_eq!(v["metadata"]["total_loc"], 120);
        assert_eq!(v["top_complex_functions"][0]["complexity"], 12);
        assert_eq!(v["top_complex_functions"][0]["importance_score"], 0.5);
    }

    #[test]
    fn summary_round_trip_and_sorted_categories() {
        let mut categories = BTreeMap::new();
        categories.insert("tests".to_string(), vec!["t.py".to_string()]);
        categories.insert(
            "core".to_string(),
            vec!["a.py".to_string(), "b.py".to_string()],
        );
        let s = ProjectSummary {
            project_name: "demo".into(),
            total_files: 3,
            total_loc: 10,
            languages: vec!["python".into()],
            categories,
            key_features: vec![],
            entry_points: vec!["a.py:1".into()],
            dependencies: DependencyInfo {
                stdlib: vec!["os".into()],
                third_party: vec![],
            },
            patterns: PatternInfo {
                naming_convention: "snake_case".into(),
                structure_type: "Flat".into(),
                architecture_style: None,
            },
        };
        let buf = encode_summary(&s);
        let v = summary_json(&buf).unwrap();
        assert_eq!(v["categories"][0]["key"], "core");
        assert_eq!(v["categories"][1]["key"], "tests");
        assert!(v["patterns"].get("architecture_style").is_none());
        let root = fb::root_as_summary(&buf).unwrap();
        let cats = root.categories().unwrap();
        assert!(cats
            .lookup_by_key("tests", |e, k| e.key_compare_with_value(k))
            .is_some());
    }

    #[test]
    fn patterns_round_trip() {
        let p = PatternInfo {
            naming_convention: "camelCase".into(),
            structure_type: "Flat".into(),
            architecture_style: Some("mvc".into()),
        };
        let v = patterns_json(&encode_patterns(&p)).unwrap();
        assert_eq!(v["patterns"]["architecture_style"], "mvc");
    }

    #[test]
    fn call_graph_round_trip_shares_strings() {
        let nodes: Vec<CallGraphNode> = (0..50)
            .map(|i| CallGraphNode {
                id: format!("pkg/a.go::f{i}"),
                node_type: "function".into(),
                file: "src/shared_file.go".into(),
                is_entry_point: i == 0,
                call_count_estimate: i,
            })
            .collect();
        let edges: Vec<CallGraphEdge> = (1..50)
            .map(|i| CallGraphEdge {
                from: "pkg/a.go::f0".into(),
                to: format!("pkg/a.go::f{i}"),
                edge_type: "call".into(),
                conditional: i % 2 == 0,
                call_site_line: i,
            })
            .collect();
        let tag_list = vec!["entry".to_string()];
        let mut tags = BTreeMap::new();
        tags.insert("pkg/a.go::f0", tag_list.as_slice());
        let mut cats = BTreeMap::new();
        cats.insert("src/shared_file.go", "core");
        let buf = encode_call_graph(&nodes, &edges, &tags, &cats).unwrap();
        let v = call_graph_json(&buf).unwrap();
        assert_eq!(v["nodes"].as_array().unwrap().len(), 50);
        assert_eq!(v["nodes"][0]["is_entry_point"], true);
        assert_eq!(v["edges"][1]["conditional"], true);
        assert_eq!(v["edges"][1]["to"], "pkg/a.go::f2");
        assert_eq!(
            v["tags"][0],
            json!({"key": "pkg/a.go::f0", "values": ["entry"]})
        );
        assert_eq!(
            v["file_categories"][0],
            json!({"key": "src/shared_file.go", "category": "core"})
        );
        // The repeated file name and "function"/"call" are stored once each.
        let pat = b"src/shared_file.go";
        let stored = buf.windows(pat.len()).filter(|w| w == pat).count();
        assert_eq!(stored, 1, "file path stored {stored} times");
    }

    #[test]
    fn indices_round_trip_sorted_and_shared() {
        let mut ix = IndicesView::default();
        ix.functions_by_name
            .insert("zeta", vec!["src/lib_main.rs:9".to_string()]);
        ix.functions_by_name.insert(
            "alpha",
            vec!["src/lib_main.rs:1".to_string(), "src/b.rs:4".to_string()],
        );
        ix.functions_calling
            .insert("alpha", vec!["caller_fn_name", "caller_fn_name"]);
        ix.files_by_category
            .insert("core", vec!["src/lib_main.rs", "src/b.rs"]);
        let buf = encode_indices(&ix).unwrap();
        let v = indices_json(&buf).unwrap();
        assert_eq!(v["functions_by_name"][0]["key"], "alpha");
        assert_eq!(
            v["functions_by_name"][1]["values"],
            json!(["src/lib_main.rs:9"])
        );
        assert_eq!(
            v["functions_calling"][0]["values"],
            json!(["caller_fn_name", "caller_fn_name"])
        );
        assert_eq!(v["types_by_name"], json!([]));
        let root = fb::root_as_indices(&buf).unwrap();
        let byname = root.functions_by_name().unwrap();
        assert!(byname
            .lookup_by_key("zeta", |e, k| e.key_compare_with_value(k))
            .is_some());
        let pat = b"caller_fn_name";
        assert_eq!(buf.windows(pat.len()).filter(|w| w == pat).count(), 1);
    }

    #[test]
    fn file_record_round_trip() {
        let f = Function {
            id: "func_run::a.py".into(),
            name: "run".into(),
            signature: "def run(x=1)".into(),
            params: vec![Parameter {
                name: "x".into(),
                type_annotation: String::new(),
                default_value: Some("1".into()),
            }],
            docstring: "Runs.".into(),
            line_start: 3,
            line_end: 9,
            ..Default::default()
        };
        let c = Class {
            id: "class_A::a.py".into(),
            name: "A".into(),
            bases: vec!["Base".into()],
            methods: vec![f.clone()],
            attributes: vec![Attribute {
                name: "n".into(),
                type_annotation: "int".into(),
                value: None,
            }],
            ..Default::default()
        };
        let fd = FileData {
            language: "python".into(),
            loc: 12,
            functions: vec![f],
            classes: vec![c],
            todos: vec![Todo {
                line: 2,
                text: "TODO: x".into(),
                priority: "low".into(),
            }],
            ..Default::default()
        };
        let v = file_record_json(&encode_file_record(&fd)).unwrap();
        assert_eq!(v["language"], "python");
        assert_eq!(
            v["functions"][0]["params"][0],
            json!({"name":"x","type_annotation":"","default_value":"1"})
        );
        assert_eq!(v["classes"][0]["methods"][0]["line_end"], 9);
        assert_eq!(
            v["classes"][0]["attributes"][0],
            json!({"name":"n","type_annotation":"int"})
        );
        assert_eq!(v["todos"][0]["priority"], "low");
    }

    #[test]
    fn kb_header_round_trip_and_layout() {
        let entries = [
            KbEntry {
                path: "a.py",
                offset: 0,
                length: 20,
            },
            KbEntry {
                path: "b.py",
                offset: 24,
                length: 7,
            },
        ];
        let mut buf = encode_kb_header(&md(), &entries).unwrap();
        let n = u32::from_le_bytes(buf[..4].try_into().unwrap()) as usize;
        assert_eq!(buf.len(), 4 + n);
        assert_eq!(&buf[8..12], b"EUKB");
        buf.extend(std::iter::repeat_n(0u8, kb_pad(buf.len()) + 31));
        let h = kb_header(&buf).unwrap();
        assert_eq!(h.records_start % KB_RECORD_ALIGN, 0);
        assert_eq!(
            h.files,
            vec![("a.py".to_string(), 0, 20), ("b.py".to_string(), 24, 7)]
        );
        assert_eq!(h.json["metadata"]["project_name"], "demo");
        assert!(kb_header(&buf[..10]).is_err());
    }

    #[test]
    fn checksum_round_trip() {
        let files = [
            ChecksumEntry { path: "a.go", hash: "00ff", size: 10, mod_time: -1 },
            ChecksumEntry { path: "b/c.py", hash: "abcd", size: 0, mod_time: 1_700_000_000_000_000_000 },
        ];
        let buf = encode_checksum("/repo", "h", &files, "2026-11-19T00:00:00Z", "0.9.0").unwrap();
        let v = checksum_json(&buf).unwrap();
        assert_eq!(v["total_files"], 2);
        assert_eq!(v["files"][1], json!({"path":"b/c.py","hash":"abcd","size":0,"mod_time":1_700_000_000_000_000_000_i64}));
        assert_eq!(v["files"][0]["mod_time"], -1);
        let root = fb::root_as_checksum(&buf).unwrap();
        assert!(root.files().unwrap().lookup_by_key("b/c.py", |e, k| e.key_compare_with_value(k)).is_some());
    }

    #[test]
    fn rejects_wrong_file_type() {
        let buf = encode_patterns(&PatternInfo::default());
        let err = entry_points_json(&buf).unwrap_err();
        assert!(err.0.contains("wrong identifier"), "{err}");
    }
}
