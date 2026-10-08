use crate::spill::SpillIndex;
use crate::struc::kb_struct::Metadata;
use std::io::{BufWriter, Write};
use std::path::Path;

pub fn write_json_streaming<T: serde::Serialize>(
    path: &Path,
    value: &T,
    pretty: bool,
) -> std::io::Result<()> {
    let file = std::fs::File::create(path)?;
    let writer = BufWriter::with_capacity(256 * 1024, file);
    if pretty {
        serde_json::to_writer_pretty(writer, value)
    } else {
        serde_json::to_writer(writer, value)
    }
    .map_err(std::io::Error::other)
}

pub fn write_kb_from_spill(
    path: &Path,
    metadata: &Metadata,
    sp: &SpillIndex,
) -> std::io::Result<()> {
    let mut w = BufWriter::with_capacity(256 * 1024, std::fs::File::create(path)?);
    w.write_all(b"{\"metadata\":")?;
    serde_json::to_writer(&mut w, metadata).map_err(std::io::Error::other)?;
    w.write_all(b",\"structure\":{")?;
    if !sp.entries.is_empty() {
        let f = std::fs::File::open(&sp.path)?;
        #[allow(unsafe_code)]
        let mmap = unsafe { memmap2::Mmap::map(&f)? };
        for (i, (rel, off, len)) in sp.entries.iter().enumerate() {
            if i > 0 {
                w.write_all(b",")?;
            }
            serde_json::to_writer(&mut w, rel).map_err(std::io::Error::other)?;
            w.write_all(b":")?;
            let (a, b) = (*off as usize, *off as usize + *len as usize);
            w.write_all(&mmap[a..b])?;
        }
    }
    w.write_all(b"}}")?;
    w.flush()
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
