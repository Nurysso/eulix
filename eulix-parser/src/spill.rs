// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use std::fs::{File, OpenOptions};
use std::io::{BufWriter, Write};
use std::path::{Path, PathBuf};
use std::sync::Mutex;

pub type SpillLoc = (u64, u32);

const FLUSH_EVERY: u64 = 512;

/// Per-record framing + CRC state, behind a single mutex.
struct Inner {
    writer: BufWriter<File>,
    written: u64,
    records_since_flush: u64,
}

pub struct Spill {
    pub path: PathBuf,
    inner: Mutex<Inner>,
}

pub struct SpillIndex {
    pub path: PathBuf,
    pub entries: Vec<(String, u64, u32)>, // (relative path, offset, len)
}

impl Spill {
    /// Fresh spill. Errors if the file already exists.
    pub fn create(path: PathBuf) -> std::io::Result<Self> {
        let f = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&path)?;
        Ok(Self {
            path,
            inner: Mutex::new(Inner {
                writer: BufWriter::with_capacity(1 << 20, f),
                written: 0,
                records_since_flush: 0,
            }),
        })
    }

    /// Returns (payload_offset, payload_len). Offset is the payload start,
    /// not the record start — that's what the mmap-based writer splices.
    /// The payload's CRC sits in the 4 bytes just before it.
    pub fn append(&self, path: &str, payload: &[u8]) -> std::io::Result<SpillLoc> {
        let pb = path.as_bytes();
        if pb.len() > u16::MAX as usize {
            return Err(std::io::Error::other("spill path too long"));
        }
        // CRC is computed *outside* the lock.
        let crc = crc32fast::hash(payload);
        let plen = payload.len() as u32;

        let mut g = self
            .inner
            .lock()
            .map_err(|_| std::io::Error::other("spill lock poisoned"))?;

        let record_start = g.written;
        g.writer.write_all(&(pb.len() as u16).to_le_bytes())?;
        g.writer.write_all(pb)?;
        g.writer.write_all(&plen.to_le_bytes())?;
        g.writer.write_all(&crc.to_le_bytes())?;
        let payload_offset = record_start + 2 + pb.len() as u64 + 4 + 4;
        g.writer.write_all(payload)?;
        g.written = payload_offset + plen as u64;
        g.records_since_flush += 1;
        if g.records_since_flush >= FLUSH_EVERY {
            g.writer.flush()?;
            g.records_since_flush = 0;
        }
        Ok((payload_offset, plen))
    }

    pub fn flush(&self) -> std::io::Result<()> {
        let mut g = self
            .inner
            .lock()
            .map_err(|_| std::io::Error::other("spill lock poisoned"))?;
        g.writer.flush()?;
        g.records_since_flush = 0;
        Ok(())
    }

    pub fn path(&self) -> &Path {
        &self.path
    }
}
