// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use crate::spill::{Spill, SpillLoc};
use libeulix::struc::kb_struct::FileData;
use libeulix::utils::syntax::SyntaxHealth;
use memmap2::Mmap;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

const FORMAT: u32 = 1;
// Coarse filesystems tick in steps of up to 2s, so an edit this close to the
// cached run can leave size and mtime unchanged.
const RACY_NANOS: i64 = 2_000_000_000;

#[derive(Serialize, Deserialize, Clone)]
pub struct Cached {
    pub size: u64,
    pub mtime: i64,
    pub hash: u128,
    pub syntax: SyntaxHealth,
    pub raw_imports: Vec<(String, String, String)>,
    pub emit: SpillLoc,
    pub slim: SpillLoc,
}

#[derive(Serialize, Deserialize)]
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
                name.ends_with(".json.zst").then(|| gen_of(&name)).flatten()
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
        let raw = fs::read(file(&self.dir, "index", gen, "json.zst")).ok()?;
        let idx: Index = serde_json::from_slice(&zstd::decode_all(&raw[..]).ok()?).ok()?;
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
        let mut fd: FileData = serde_json::from_slice(s).ok()?;
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
    pub fn commit(self, files: BTreeMap<String, Cached>) -> std::io::Result<()> {
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
        let path = file(&dir, "index", gen, "json.zst");
        let tmp = path.with_extension("tmp");
        fs::write(&tmp, zstd::encode_all(&serde_json::to_vec(&idx)?[..], 3)?)?;
        fs::rename(&tmp, &path)?;
        if let Some(old) = old_gen {
            for e in fs::read_dir(&dir)?.flatten() {
                if gen_of(&e.file_name().to_string_lossy()).is_some_and(|g| g <= old) {
                    let _ = fs::remove_file(e.path());
                }
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
