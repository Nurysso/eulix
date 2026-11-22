// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use crate::spill::SpillIndex;
use libeulix::fb_io;
use libeulix::struc::kb_struct::Metadata;
use std::io::{BufWriter, Write};
use std::path::{Path, PathBuf};

// #![allow(clippy::module_inception)]

/// "test1/kb.json" -> "test1"
/// "test1/data/kb" -> "test1/data"
/// "kb.json"       -> "."   (no directory part, so the current dir)
pub fn output_dir(output: impl AsRef<Path>) -> PathBuf {
    let path = output.as_ref();
    match path.parent() {
        Some(p) if !p.as_os_str().is_empty() => p.to_path_buf(),
        _ => PathBuf::from("."),
    }
}

pub fn write_json_streaming<T: serde::Serialize>(
    path: &Path,
    value: &T,
    pretty: bool,
) -> std::io::Result<()> {
    write_atomic(path, |w| {
        if pretty {
            serde_json::to_writer_pretty(w, value)
        } else {
            serde_json::to_writer(w, value)
        }
        .map_err(std::io::Error::other)
    })
}

/// Writes `path` by filling a sibling `<name>.tmp` file and renaming it over
/// `path` once `fill` succeeds. A reader that already has the old file open or
/// mapped keeps seeing the old contents instead of a half-written file, and a
/// failed run leaves the previous output in place.
pub fn write_atomic<F>(path: &Path, fill: F) -> std::io::Result<()>
where
    F: FnOnce(&mut BufWriter<std::fs::File>) -> std::io::Result<()>,
{
    let tmp = tmp_path(path);
    let result = (|| {
        let mut w = BufWriter::with_capacity(256 * 1024, std::fs::File::create(&tmp)?);
        fill(&mut w)?;
        w.flush()?;
        drop(w);
        std::fs::rename(&tmp, path)
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&tmp);
    }
    result
}

/// Writes one FlatBuffers output. With `emit_json`, also writes the JSON copy
/// next to it (same stem, `.json`), built by `to_json` from the finished
/// bytes; without it, removes a stale copy left by an earlier run so nobody
/// reads JSON that no longer matches the binary file.
pub fn write_fb_output<E: std::fmt::Display>(
    path: &Path,
    bytes: Vec<u8>,
    to_json: fn(&[u8]) -> Result<serde_json::Value, E>,
    emit_json: bool,
) -> std::io::Result<()> {
    write_atomic(path, |w| w.write_all(&bytes))?;
    let json_path = path.with_extension("json");
    if emit_json {
        let value = to_json(&bytes)
            .map_err(|e| std::io::Error::other(format!("{}: {e}", path.display())))?;
        write_json_streaming(&json_path, &value, true)
    } else {
        match std::fs::remove_file(&json_path) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => Err(e),
            _ => Ok(()),
        }
    }
}

/// The checksum baseline is written by libeulix's file walker during
/// discovery. With `emit_json`, decode it and write `checksum.json` next to it;
/// without, remove a stale copy.
pub fn write_checksum_copy(dir: &Path, emit_json: bool) -> Result<(), Box<dyn std::error::Error>> {
    let json_path = dir.join("checksum.json");
    if !emit_json {
        return match std::fs::remove_file(&json_path) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => Err(e.into()),
            _ => Ok(()),
        };
    }
    let path = dir.join(libeulix::utils::file_walker::CHECKSUM_FILE);
    let buf = zstd::decode_all(&std::fs::read(&path)?[..])?;
    let value = fb_io::checksum_json(&buf).map_err(|e| format!("{}: {e}", path.display()))?;
    write_json_streaming(&json_path, &value, true)?;
    Ok(())
}

fn tmp_path(path: &Path) -> PathBuf {
    let mut name = path.file_name().unwrap_or_default().to_os_string();
    name.push(".tmp");
    path.with_file_name(name)
}

/// Writes kb.fb: the size-prefixed header (metadata and each file's
/// path, offset and length), padding, then every file's FileRecord copied
/// straight out of the spill, each starting at a multiple of 8 within the
/// records region. Layout: schema/kb_header.fbs.
///
/// With `emit_json`, also writes the JSON copy (`<stem>.json`) one record at a
/// time, decoded from the same bytes; without it, removes a stale copy.
pub fn write_kb_fb(
    path: &Path,
    metadata: &Metadata,
    sp: &SpillIndex,
    emit_json: bool,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut entries = Vec::with_capacity(sp.entries.len());
    let mut offset = 0u64;
    for (rel, _, len) in &sp.entries {
        entries.push(fb_io::KbEntry {
            path: rel,
            offset,
            length: *len,
        });
        offset += u64::from(*len) + fb_io::kb_pad(*len as usize) as u64;
    }
    debug_assert!(
        sp.entries.windows(2).all(|w| w[0].0 < w[1].0),
        "spill entries must be sorted"
    );
    let header = fb_io::encode_kb_header(metadata, &entries)?;

    let mmap = if sp.entries.is_empty() {
        None
    } else {
        let f = std::fs::File::open(&sp.path)?;
        #[allow(unsafe_code)]
        let m = unsafe { memmap2::Mmap::map(&f)? };
        Some(m)
    };
    let record = |off: u64, len: u32| -> &[u8] {
        let m = mmap.as_ref().expect("entries imply a spill mapping");
        &m[off as usize..off as usize + len as usize]
    };

    const ZEROS: [u8; fb_io::KB_RECORD_ALIGN] = [0; fb_io::KB_RECORD_ALIGN];
    write_atomic(path, |w| {
        w.write_all(&header)?;
        w.write_all(&ZEROS[..fb_io::kb_pad(header.len())])?;
        for (_, off, len) in &sp.entries {
            w.write_all(record(*off, *len))?;
            w.write_all(&ZEROS[..fb_io::kb_pad(*len as usize)])?;
        }
        Ok(())
    })?;

    let json_path = path.with_extension("json");
    if !emit_json {
        return match std::fs::remove_file(&json_path) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => Err(e.into()),
            _ => Ok(()),
        };
    }
    // The copy shows exactly what kb.fb holds: the header's fields and, per
    // file, its entry and decoded record. Built record by record so the whole
    // knowledge base is never in memory as JSON.
    let head = fb_io::kb_header(&header)?;
    write_atomic(&json_path, |w| {
        let fail =
            |e: fb_io::DecodeError| std::io::Error::other(format!("{}: {e}", path.display()));
        w.write_all(b"{\n  \"format_version\": ")?;
        serde_json::to_writer(&mut *w, &head.json["format_version"])
            .map_err(std::io::Error::other)?;
        w.write_all(b",\n  \"metadata\": ")?;
        let pretty = |w: &mut BufWriter<std::fs::File>, v: &serde_json::Value, indent: &[u8]| {
            let fmt = serde_json::ser::PrettyFormatter::with_indent(b"  ");
            let mut buf = Vec::new();
            let mut ser = serde_json::Serializer::with_formatter(&mut buf, fmt);
            serde::Serialize::serialize(v, &mut ser).map_err(std::io::Error::other)?;
            // Re-indent the nested value so the whole file reads as one document.
            for (i, line) in buf.split(|&b| b == b'\n').enumerate() {
                if i > 0 {
                    w.write_all(b"\n")?;
                    w.write_all(indent)?;
                }
                w.write_all(line)?;
            }
            Ok::<(), std::io::Error>(())
        };
        pretty(w, &head.json["metadata"], b"  ")?;
        w.write_all(b",\n  \"files\": [")?;
        for (i, (rel, off, len)) in sp.entries.iter().enumerate() {
            let rec = fb_io::file_record_json(record(*off, *len)).map_err(fail)?;
            let entry = &head.files[i];
            let v = serde_json::json!({
                "path": rel,
                "offset": entry.1,
                "length": entry.2,
                "record": rec,
            });
            w.write_all(if i == 0 { b"\n    " } else { b",\n    " })?;
            pretty(w, &v, b"    ")?;
        }
        w.write_all(if sp.entries.is_empty() {
            b"]\n}\n"
        } else {
            b"\n  ]\n}\n"
        })
    })?;
    Ok(())
}

#[allow(dead_code)]
pub fn write_json_file(path: &Path, json: &str) -> std::io::Result<()> {
    let f = std::fs::File::create(path)?;
    let len = json.len();
    let buffer_size = if len > 100 * 1024 * 1024 {
        8 * 1024 * 1024
    } else if len > 10 * 1024 * 1024 {
        2 * 1024 * 1024
    } else {
        512 * 1024
    };
    let mut w = BufWriter::with_capacity(buffer_size, f);
    w.write_all(json.as_bytes())?;
    w.flush()?;
    Ok(())
}

#[cfg(test)]
mod atomic_tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn replaces_file_and_leaves_no_tmp() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("kb_x.json");
        std::fs::write(&path, b"old").unwrap();
        write_atomic(&path, |w| w.write_all(b"new")).unwrap();
        assert_eq!(std::fs::read(&path).unwrap(), b"new");
        assert!(!tmp_path(&path).exists());
    }

    #[test]
    fn failed_fill_keeps_old_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("kb_x.json");
        std::fs::write(&path, b"old").unwrap();
        let err = write_atomic(&path, |w| {
            w.write_all(b"partial")?;
            Err(std::io::Error::other("boom"))
        });
        assert!(err.is_err());
        assert_eq!(std::fs::read(&path).unwrap(), b"old");
        assert!(!tmp_path(&path).exists());
    }

    #[cfg(unix)]
    #[test]
    fn open_reader_keeps_old_contents() {
        use std::io::Read;
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("kb_x.json");
        std::fs::write(&path, b"old").unwrap();
        let mut reader = std::fs::File::open(&path).unwrap();
        write_atomic(&path, |w| w.write_all(b"new contents")).unwrap();
        let mut seen = String::new();
        reader.read_to_string(&mut seen).unwrap();
        assert_eq!(seen, "old");
    }
}

#[cfg(test)]
mod kb_layout_tests {
    use super::*;
    use std::io::{Seek, SeekFrom};

    fn meta() -> Metadata {
        Metadata {
            version: "t".into(),
            git_hash: "g".into(),
            project_name: "big".into(),
            project_hash: "h".into(),
            parsed_at: "now".into(),
            languages: vec![],
            total_files: 3,
            total_loc: 0,
            total_functions: 0,
            total_classes: 0,
            total_methods: 0,
        }
    }

    /// kb.fb must work past 2 GiB, the limit of one FlatBuffer: offsets in
    /// the header are 64-bit and each record is its own buffer. Writes about
    /// 2.3 GB under target/, so it is ignored by default:
    /// `cargo test --release kb_fb_past_two_gib -- --ignored`.
    #[test]
    #[ignore]
    fn kb_fb_past_two_gib() {
        let dir = tempfile::tempdir_in(concat!(env!("CARGO_MANIFEST_DIR"), "/target")).unwrap();
        let spill_path = dir.path().join("emit.spill");
        let big: u32 = 1_150_000_000; // two of these pass 2^31
        let tail = b"last-record-marker".to_vec();
        {
            let mut f = std::fs::File::create(&spill_path).unwrap();
            f.set_len(2 * u64::from(big)).unwrap(); // sparse zeros
            f.seek(SeekFrom::End(0)).unwrap();
            f.write_all(&tail).unwrap();
        }
        let sp = SpillIndex {
            path: spill_path,
            entries: vec![
                ("a.py".into(), 0, big),
                ("b.py".into(), u64::from(big), big),
                ("c.py".into(), 2 * u64::from(big), tail.len() as u32),
            ],
        };
        let out = dir.path().join("kb.fb");
        write_kb_fb(&out, &meta(), &sp, false).unwrap();

        let f = std::fs::File::open(&out).unwrap();
        #[allow(unsafe_code)]
        let m = unsafe { memmap2::Mmap::map(&f).unwrap() };
        assert!(m.len() as u64 > 1 << 31);
        let h = fb_io::kb_header(&m).unwrap();
        assert_eq!(h.files.len(), 3);
        let (path, off, len) = &h.files[2];
        assert_eq!(path, "c.py");
        assert!(
            h.records_start as u64 + off > 1 << 31,
            "last record must sit past 2 GiB"
        );
        let at = h.records_start + *off as usize;
        assert_eq!(&m[at..at + *len as usize], tail.as_slice());
        assert_eq!(h.json["metadata"]["project_name"], "big");
    }
}
