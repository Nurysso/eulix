// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use crate::spill::{Spill, SpillLoc};
use flatbuffers::FlatBufferBuilder;
use libeulix::fb;
use libeulix::fb_io::FORMAT_VERSION;
use libeulix::struc::kb_struct::FileData;
use libeulix::utils::syntax::SyntaxHealth;
use memmap2::Mmap;
use serde_json::json;
use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

// 2: emit spill records are kb.fb FileRecord buffers (were JSON fragments).
// 3: the index is a FlatBuffer (schema/parse_cache.fbs), index.<gen>.fb.zst.
// 4: slim spill records are FlexBuffers (were serde JSON).
const FORMAT: u32 = 4;
const INDEX_EXT: &str = "fb.zst";
/// Index name of caches written before format 3; their files are removed.
const LEGACY_INDEX_EXT: &str = "json.zst";
// Coarse filesystems tick in steps of up to 2s, so an edit this close to the
// cached run can leave size and mtime unchanged.
const RACY_NANOS: i64 = 2_000_000_000;

#[derive(Clone, Debug, PartialEq)]
pub struct Cached {
    pub size: u64,
    pub mtime: i64,
    pub hash: u128,
    pub syntax: SyntaxHealth,
    pub raw_imports: Vec<(String, String, String)>,
    pub emit: SpillLoc,
    pub slim: SpillLoc,
}

#[derive(Debug, PartialEq)]
struct Index {
    key: String,
    started_at: i64,
    files: BTreeMap<String, Cached>,
}

pub struct ParseCache {
    dir: PathBuf,
    key: Option<String>,
    now: i64,
    gen: i64,
    old_gen: Option<i64>,
    started_at: i64,
    files: BTreeMap<String, Cached>,
    emit: Option<Mmap>,
    slim: Option<Mmap>,
}

impl ParseCache {
    pub fn open(dir: PathBuf, version: &str, git_hash: &str) -> std::io::Result<Self> {
        fs::create_dir_all(&dir)?;
        let now = nanos(SystemTime::now());
        let old_gen = fs::read_dir(&dir)?
            .filter_map(|e| {
                let name = e.ok()?.file_name().into_string().ok()?;
                name.ends_with(&format!(".{INDEX_EXT}"))
                    .then(|| gen_of(&name))
                    .flatten()
            })
            .max();
        let mut cache = Self {
            key: cache_key(version, git_hash),
            now,
            gen: now.max(old_gen.map_or(0, |g| g + 1)),
            old_gen,
            started_at: 0,
            files: BTreeMap::new(),
            emit: None,
            slim: None,
            dir,
        };
        if let Some(g) = old_gen {
            cache.load(g);
        }
        Ok(cache)
    }

    fn load(&mut self, gen: i64) -> Option<()> {
        let raw = fs::read(file(&self.dir, "index", gen, INDEX_EXT)).ok()?;
        let idx = decode_index(&zstd::decode_all(&raw[..]).ok()?)?;
        if Some(&idx.key) != self.key.as_ref() {
            return None;
        }
        let map = |kind| {
            let f = fs::File::open(file(&self.dir, kind, gen, "spill")).ok()?;
            #[allow(unsafe_code)]
            unsafe { Mmap::map(&f) }.ok()
        };
        self.emit = Some(map("emit")?);
        self.slim = Some(map("slim")?);
        self.started_at = idx.started_at;
        self.files = idx.files;
        Some(())
    }

    pub fn spill(&self, kind: &str) -> std::io::Result<Spill> {
        Spill::create(file(&self.dir, kind, self.gen, "spill"))
    }

    pub fn lookup(&self, rel: &str) -> Option<&Cached> {
        self.files.get(rel)
    }

    pub fn fresh(&self, c: &Cached, size: u64, mtime: i64) -> bool {
        c.size == size && c.mtime == mtime && mtime > 0 && mtime + RACY_NANOS < self.started_at
    }

    pub fn reuse(
        &self,
        rel: &str,
        c: &Cached,
        size: u64,
        mtime: i64,
        emit: &Spill,
        slim: &Spill,
    ) -> Option<(FileData, Cached)> {
        let e = record(self.emit.as_ref()?, c.emit)?;
        let s = record(self.slim.as_ref()?, c.slim)?;
        let mut fd = decode_slim(s)?;
        fd.syntax = c.syntax;
        let cached = Cached {
            size,
            mtime,
            raw_imports: c.raw_imports.clone(),
            emit: emit.append(rel, e).ok()?,
            slim: slim.append(rel, s).ok()?,
            ..*c
        };
        Some((fd, cached))
    }

    /// Spills must be flushed first. Deletes generations up to the one this
    /// run read; newer ones may belong to a concurrent run.
    /// With `emit_json`, also writes `index.json`, the JSON copy of the index
    /// this run wrote; without, removes a stale one.
    pub fn commit(self, files: BTreeMap<String, Cached>, emit_json: bool) -> std::io::Result<()> {
        let Self {
            dir,
            key,
            now,
            gen,
            old_gen,
            emit,
            slim,
            ..
        } = self;
        drop((emit, slim));
        let idx = Index {
            key: key.unwrap_or_default(),
            started_at: now,
            files,
        };
        let path = file(&dir, "index", gen, INDEX_EXT);
        let tmp = path.with_extension("tmp");
        let buf = encode_index(&idx);
        fs::write(&tmp, zstd::encode_all(&buf[..], 3)?)?;
        fs::rename(&tmp, &path)?;
        // Generations this run read, and any cache from before format 3, go.
        let legacy: Vec<i64> = fs::read_dir(&dir)?
            .flatten()
            .filter_map(|e| {
                let name = e.file_name().to_string_lossy().into_owned();
                name.ends_with(&format!(".{LEGACY_INDEX_EXT}"))
                    .then(|| gen_of(&name))
                    .flatten()
            })
            .collect();
        for e in fs::read_dir(&dir)?.flatten() {
            let Some(g) = gen_of(&e.file_name().to_string_lossy()) else {
                continue;
            };
            if old_gen.is_some_and(|old| g <= old) || legacy.contains(&g) {
                let _ = fs::remove_file(e.path());
            }
        }
        let json_path = dir.join("index.json");
        if emit_json {
            let v = index_json(&idx);
            fs::write(
                &json_path,
                serde_json::to_vec_pretty(&v).map_err(std::io::Error::other)?,
            )?;
        } else if let Err(e) = fs::remove_file(&json_path) {
            if e.kind() != std::io::ErrorKind::NotFound {
                return Err(e);
            }
        }
        Ok(())
    }
}

pub fn mtime(m: &fs::Metadata) -> i64 {
    m.modified().map_or(0, nanos)
}

fn nanos(t: SystemTime) -> i64 {
    t.duration_since(UNIX_EPOCH)
        .map_or(0, |d| d.as_nanos() as i64)
}

fn cache_key(version: &str, git_hash: &str) -> Option<String> {
    let exe = fs::metadata(std::env::current_exe().ok()?).ok()?;
    Some(format!(
        "{FORMAT}:{version}:{git_hash}:{}:{}",
        exe.len(),
        mtime(&exe)
    ))
}

fn file(dir: &Path, kind: &str, gen: i64, ext: &str) -> PathBuf {
    dir.join(format!("{kind}.{gen}.{ext}"))
}

fn gen_of(name: &str) -> Option<i64> {
    name.split('.').nth(1)?.parse().ok()
}

/// A slimmed `FileData` for the slim spill, as FlexBuffers: the schemaless
/// sibling of FlatBuffers. These records mirror libeulix's internal types
/// (about 270 fields) and only this crate reads them, so serde keeps them in
/// step with every struct change instead of a hand-kept schema.
pub fn encode_slim(fd: &FileData) -> Result<Vec<u8>, flexbuffers::SerializationError> {
    flexbuffers::to_vec(fd)
}

fn decode_slim(buf: &[u8]) -> Option<FileData> {
    flexbuffers::from_slice(buf).ok()
}

fn encode_index(idx: &Index) -> Vec<u8> {
    let mut fbb = FlatBufferBuilder::with_capacity(64 * 1024);
    let files: Vec<_> = idx
        .files
        .iter()
        .map(|(path, c)| {
            let imports: Vec<_> = c
                .raw_imports
                .iter()
                .map(|(module, file, import_type)| {
                    let args = fb::RawImportArgs {
                        module: Some(fbb.create_string(module)),
                        file: Some(fbb.create_string(file)),
                        import_type: Some(fbb.create_string(import_type)),
                    };
                    fb::RawImport::create(&mut fbb, &args)
                })
                .collect();
            let raw_imports = Some(fbb.create_vector(&imports));
            let args = fb::CachedFileArgs {
                path: Some(fbb.create_string(path)),
                size: c.size,
                mtime: c.mtime,
                hash_lo: c.hash as u64,
                hash_hi: (c.hash >> 64) as u64,
                error_nodes: c.syntax.error_nodes as u64,
                missing_nodes: c.syntax.missing_nodes as u64,
                first_error_line: c.syntax.first_error_line.map(|l| l as u64),
                raw_imports,
                emit_offset: c.emit.0,
                emit_length: c.emit.1,
                slim_offset: c.slim.0,
                slim_length: c.slim.1,
            };
            fb::CachedFile::create(&mut fbb, &args)
        })
        .collect();
    let files = Some(fbb.create_vector(&files));
    let key = Some(fbb.create_string(&idx.key));
    let root = fb::ParseCacheIndex::create(
        &mut fbb,
        &fb::ParseCacheIndexArgs {
            format_version: FORMAT_VERSION,
            key,
            started_at: idx.started_at,
            files,
        },
    );
    fb::finish_parse_cache_index_buffer(&mut fbb, root);
    fbb.finished_data().to_vec()
}

/// `None` for anything that is not a current index: the cache is then just
/// rebuilt, as for a missing one.
fn decode_index(buf: &[u8]) -> Option<Index> {
    if !fb::parse_cache_index_buffer_has_identifier(buf) {
        return None;
    }
    let opts = flatbuffers::VerifierOptions {
        max_tables: usize::MAX,
        max_apparent_size: usize::MAX,
        ..Default::default()
    };
    let root = fb::root_as_parse_cache_index_with_opts(&opts, buf).ok()?;
    if root.format_version() != FORMAT_VERSION {
        return None;
    }
    let files = root
        .files()
        .into_iter()
        .flatten()
        .map(|f| {
            let raw_imports = f
                .raw_imports()
                .into_iter()
                .flatten()
                .map(|i| {
                    (
                        i.module().unwrap_or_default().to_string(),
                        i.file().unwrap_or_default().to_string(),
                        i.import_type().unwrap_or_default().to_string(),
                    )
                })
                .collect();
            let cached = Cached {
                size: f.size(),
                mtime: f.mtime(),
                hash: u128::from(f.hash_lo()) | (u128::from(f.hash_hi()) << 64),
                syntax: SyntaxHealth {
                    error_nodes: f.error_nodes() as usize,
                    missing_nodes: f.missing_nodes() as usize,
                    first_error_line: f.first_error_line().map(|l| l as usize),
                },
                raw_imports,
                emit: (f.emit_offset(), f.emit_length()),
                slim: (f.slim_offset(), f.slim_length()),
            };
            (f.path().to_string(), cached)
        })
        .collect();
    Some(Index {
        key: root.key().unwrap_or_default().to_string(),
        started_at: root.started_at(),
        files,
    })
}

/// The `--emit-json` copy of the index, field for field as in the FlatBuffer.
fn index_json(idx: &Index) -> serde_json::Value {
    let files: Vec<_> = idx
        .files
        .iter()
        .map(|(path, c)| {
            let mut o = serde_json::Map::new();
            o.insert("path".into(), path.as_str().into());
            o.insert("size".into(), c.size.into());
            o.insert("mtime".into(), c.mtime.into());
            o.insert("hash_lo".into(), (c.hash as u64).into());
            o.insert("hash_hi".into(), ((c.hash >> 64) as u64).into());
            o.insert("error_nodes".into(), c.syntax.error_nodes.into());
            o.insert("missing_nodes".into(), c.syntax.missing_nodes.into());
            // An optional scalar: flatc prints it as null when absent.
            o.insert("first_error_line".into(), c.syntax.first_error_line.into());
            let imports: Vec<_> = c
                .raw_imports
                .iter()
                .map(|(m, f, t)| json!({"module": m, "file": f, "import_type": t}))
                .collect();
            o.insert("raw_imports".into(), imports.into());
            o.insert("emit_offset".into(), c.emit.0.into());
            o.insert("emit_length".into(), c.emit.1.into());
            o.insert("slim_offset".into(), c.slim.0.into());
            o.insert("slim_length".into(), c.slim.1.into());
            serde_json::Value::Object(o)
        })
        .collect();
    json!({
        "format_version": FORMAT_VERSION,
        "key": idx.key,
        "started_at": idx.started_at,
        "files": files,
    })
}

fn record(m: &[u8], (off, len): SpillLoc) -> Option<&[u8]> {
    let off = off as usize;
    let crc = m.get(off.checked_sub(4)?..off)?;
    let payload = m.get(off..off + len as usize)?;
    (crc32fast::hash(payload).to_le_bytes() == crc).then_some(payload)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fresh_needs_same_meta_and_an_mtime_well_before_the_cached_run() {
        let dir = tempfile::tempdir().unwrap();
        let mut cache = ParseCache::open(dir.path().to_path_buf(), "0", "t").unwrap();
        cache.started_at = 10 * RACY_NANOS;
        let c = Cached {
            size: 5,
            mtime: RACY_NANOS,
            hash: 0,
            syntax: SyntaxHealth::default(),
            raw_imports: vec![],
            emit: (0, 0),
            slim: (0, 0),
        };
        assert!(cache.fresh(&c, 5, RACY_NANOS));
        assert!(!cache.fresh(&c, 6, RACY_NANOS));
        assert!(!cache.fresh(&c, 5, RACY_NANOS + 1));
        cache.started_at = 2 * RACY_NANOS;
        assert!(!cache.fresh(&c, 5, RACY_NANOS));
    }

    #[test]
    fn index_round_trip() {
        let mut files = BTreeMap::new();
        files.insert(
            "a.py".to_string(),
            Cached {
                size: 12,
                mtime: -5,
                hash: u128::MAX - 7,
                syntax: SyntaxHealth {
                    error_nodes: 2,
                    missing_nodes: 1,
                    first_error_line: Some(9),
                },
                raw_imports: vec![("os".into(), "a.py".into(), "stdlib".into())],
                emit: (8, 100),
                slim: (1 << 40, 7),
            },
        );
        files.insert(
            "b.go".to_string(),
            Cached {
                size: 0,
                mtime: 1,
                hash: 3,
                syntax: SyntaxHealth::default(),
                raw_imports: vec![],
                emit: (0, 0),
                slim: (0, 0),
            },
        );
        let idx = Index {
            key: "3:x".into(),
            started_at: 42,
            files,
        };
        let buf = encode_index(&idx);
        assert_eq!(decode_index(&buf), Some(idx));
        assert_eq!(decode_index(b"{\"key\":\"old json\"}"), None);
    }

    #[test]
    fn commit_then_open_reuses_the_index_and_drops_legacy_files() {
        let dir = tempfile::tempdir().unwrap();
        // A cache left by a release whose index was zstd JSON.
        fs::write(dir.path().join("index.5.json.zst"), b"old").unwrap();
        fs::write(dir.path().join("emit.5.spill"), b"old").unwrap();
        let cache = ParseCache::open(dir.path().to_path_buf(), "0", "t").unwrap();
        let gen = cache.gen;
        let mut files = BTreeMap::new();
        files.insert(
            "a.py".to_string(),
            Cached {
                size: 1,
                mtime: 2,
                hash: 3,
                syntax: SyntaxHealth::default(),
                raw_imports: vec![],
                emit: (4, 5),
                slim: (6, 7),
            },
        );
        for kind in ["emit", "slim"] {
            fs::write(file(dir.path(), kind, gen, "spill"), b"").unwrap();
        }
        cache.commit(files.clone(), true).unwrap();
        assert!(!dir.path().join("index.5.json.zst").exists());
        assert!(!dir.path().join("emit.5.spill").exists());
        let v: serde_json::Value =
            serde_json::from_slice(&fs::read(dir.path().join("index.json")).unwrap()).unwrap();
        assert_eq!(v["files"][0]["emit_length"], 5);
        let again = ParseCache::open(dir.path().to_path_buf(), "0", "t").unwrap();
        assert_eq!(again.files, files);
    }

    /// Every language's slimmed FileData must come back from FlexBuffers
    /// exactly, or reused files would feed different data to the analysis.
    #[test]
    fn slim_records_round_trip_for_every_language() {
        use crate::parse::slim::{slim, strip_emitted};
        use libeulix::grammar_files::parse_source;
        use std::path::Path;
        let sources = [
            ("a.py", "import os\n@dec\nasync def f(x: int = 1) -> int:\n    \"\"\"Doc.\"\"\"\n    if x:\n        return g(x)\n    try:\n        os.remove(x)\n    except OSError:\n        raise\n\nclass C(B):\n    n: int = 3\n    @staticmethod\n    def m(self):\n        self.f()\n"),
            ("a.rs", "use std::fmt;\n#[derive(Debug)]\npub struct S<'a, T> { x: &'a T }\nimpl<'a, T> S<'a, T> where T: Clone {\n    pub async unsafe fn run(&self) -> Result<(), E> { helper()?; println!(\"x\"); Ok(()) }\n}\n"),
            ("a.go", "package main\nimport \"fmt\"\ntype T struct{ n int }\nfunc (t *T) Run(xs ...int) error { go f(); defer g(); fmt.Println(t.n); return nil }\nfunc main() { var t T; _ = t.Run(1) }\n"),
            ("a.ts", "import { x } from './x';\nexport abstract class A<T> { private readonly n?: number; async m(): Promise<void> { await x(); } }\nexport const f = async (a: number) => a + 1;\n"),
            ("a.js", "const fs = require('fs');\nfunction* gen() { yield 1; }\nmodule.exports = { gen };\n(function () { fs.readFileSync('a'); })();\n"),
            ("a.java", "package p;\nimport java.util.List;\n@Deprecated\npublic final class A<T> extends B implements C {\n  private int n;\n  public static synchronized void m(String... a) throws Exception { helper(); }\n  public A() {}\n}\n"),
            ("a.php", "<?php\nnamespace N;\nuse X\\Y;\nclass A extends B { use T; public static function m($a = 1) { return helper($a); } }\nfunction f() { return 1; }\n"),
            ("a.c", "#include <stdio.h>\nstatic int add(int a, int b) { return a + b; }\nint main(void) { int (*fp)(int,int) = add; printf(\"%d\", fp(1,2)); return 0; }\n"),
            ("a.cpp", "#include <vector>\nnamespace n { template <typename T> class V : public B { public: virtual void m() const override { helper(); } }; }\nint main() { n::V<int> v; v.m(); }\n"),
        ];
        for (name, src) in sources {
            let (_, mut fd) = parse_source(Path::new(name), src).unwrap();
            // The full record first, so every field is exercised, not only
            // those that survive slimming.
            let full = decode_slim(&encode_slim(&fd).unwrap()).expect(name);
            assert_eq!(
                serde_json::to_value(&full).unwrap(),
                serde_json::to_value(&fd).unwrap(),
                "{name}: FlexBuffers round trip changed the full FileData"
            );
            slim(&mut fd);
            strip_emitted(&mut fd);
            let back = decode_slim(&encode_slim(&fd).unwrap()).expect(name);
            assert_eq!(
                serde_json::to_value(&back).unwrap(),
                serde_json::to_value(&fd).unwrap(),
                "{name}: FlexBuffers round trip changed the slim FileData"
            );
        }
    }

    #[test]
    fn record_rejects_a_corrupted_payload() {
        let dir = tempfile::tempdir().unwrap();
        let spill = Spill::create(dir.path().join("s")).unwrap();
        let loc = spill.append("a.py", b"payload").unwrap();
        spill.flush().unwrap();
        let mut bytes = fs::read(dir.path().join("s")).unwrap();
        assert_eq!(record(&bytes, loc), Some(&b"payload"[..]));
        bytes[loc.0 as usize] ^= 1;
        assert_eq!(record(&bytes, loc), None);
        assert_eq!(record(&bytes, (loc.0, loc.1 + 1)), None);
    }
}
