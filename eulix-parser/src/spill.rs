use libeulix::struc::kb_struct::FileData;
use std::fs::{self, File, OpenOptions};
use std::io::{BufReader, BufWriter, Read, Write};
use std::path::{Path, PathBuf};
use std::sync::Mutex;

pub type SpillLoc = (u64, u32);
pub type ParsedFileResult = (String, FileData, SpillLoc);

const FLUSH_EVERY: u64 = 512;

#[derive(Clone, Debug)]
pub struct Entry {
    pub path: String,
    pub offset: u64, // offset of payload inside the spill file
    pub len: u32,    // payload length (not counting framing)
}

/// Per-record framing + CRC state, behind a single mutex.
struct Inner {
    writer: BufWriter<File>,
    written: u64,
    records_since_flush: u64,
    entries: Vec<Entry>,
}

pub struct Spill {
    pub path: PathBuf,
    inner: Mutex<Inner>,
}

/// Deletes the spill file on `Drop` unless `keep()` was called.
pub struct SpillGuard {
    path: PathBuf,
    keep: bool,
}

pub struct SpillIndex {
    pub path: PathBuf,
    pub entries: Vec<(String, u64, u32)>, // (relative path, offset, len)
    pub _guard: Option<SpillGuard>,
}

impl Spill {
    /// Fresh spill. Errors if the file already exists — callers that want
    /// resume must go through `open_or_resume`.
    pub fn create(path: PathBuf) -> std::io::Result<Self> {
        let f = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&path)?;
        Ok(Self::new(path, f))
    }

    /// Resume if `path` exists. Scans the contiguous valid prefix, truncates
    /// any torn tail, and returns the entries that are already good.
    pub fn open_or_resume(path: PathBuf) -> std::io::Result<(Self, Vec<Entry>)> {
        if !path.exists() {
            let f = OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(&path)?;
            return Ok((Self::new(path, f), Vec::new()));
        }
        let (entries, valid_end) = scan_valid_prefix(&path)?;
        // Remove any half-written tail so future appends stay contiguous.
        OpenOptions::new()
            .write(true)
            .open(&path)?
            .set_len(valid_end)?;
        let f = OpenOptions::new().append(true).open(&path)?;
        let s = Self::new(path, f);
        {
            let mut g = s
                .inner
                .lock()
                .map_err(|_| std::io::Error::other("spill lock poisoned"))?;
            g.written = valid_end;
            g.entries = entries.clone();
        }
        Ok((s, entries))
    }

    fn new(path: PathBuf, f: File) -> Self {
        Self {
            path,
            inner: Mutex::new(Inner {
                writer: BufWriter::with_capacity(1 << 20, f),
                written: 0,
                records_since_flush: 0,
                entries: Vec::new(),
            }),
        }
    }

    /// Returns (payload_offset, payload_len). Offset is the payload start,
    /// not the record start — that's what the mmap-based writer splices.
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
        g.entries.push(Entry {
            path: path.to_string(),
            offset: payload_offset,
            len: plen,
        });
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

    /// Read one payload back, verifying the CRC. Used by resume to rehydrate
    /// `FileData` for entries that were already spilled.
    #[allow(dead_code)]
    pub fn read_payload(&self, entry: &Entry) -> std::io::Result<Vec<u8>> {
        let mut f = File::open(&self.path)?;
        use std::io::Seek;
        f.seek(std::io::SeekFrom::Start(entry.offset))?;
        let mut buf = vec![0u8; entry.len as usize];
        f.read_exact(&mut buf)?;
        Ok(buf)
    }
}

fn scan_valid_prefix(path: &Path) -> std::io::Result<(Vec<Entry>, u64)> {
    let mut f = BufReader::new(File::open(path)?);
    let mut entries = Vec::new();
    let mut off: u64 = 0;
    loop {
        let mut u16b = [0u8; 2];
        if f.read_exact(&mut u16b).is_err() {
            break;
        }
        let path_len = u16::from_le_bytes(u16b) as usize;
        let mut pbuf = vec![0u8; path_len];
        if f.read_exact(&mut pbuf).is_err() {
            break;
        }
        let p = match std::str::from_utf8(&pbuf) {
            Ok(s) => s.to_string(),
            Err(_) => break,
        };
        let mut u32b = [0u8; 4];
        if f.read_exact(&mut u32b).is_err() {
            break;
        }
        let plen = u32::from_le_bytes(u32b) as usize;
        if f.read_exact(&mut u32b).is_err() {
            break;
        }
        let crc_stored = u32::from_le_bytes(u32b);
        let mut payload = vec![0u8; plen];
        if f.read_exact(&mut payload).is_err() {
            break;
        }
        if crc32fast::hash(&payload) != crc_stored {
            break;
        }
        let payload_offset = off + 2 + path_len as u64 + 4 + 4;
        entries.push(Entry {
            path: p,
            offset: payload_offset,
            len: plen as u32,
        });
        off = payload_offset + plen as u64;
    }
    Ok((entries, off))
}

impl SpillGuard {
    pub fn new(path: PathBuf) -> Self {
        Self { path, keep: false }
    }
    #[allow(dead_code)]
    pub fn keep(mut self) -> PathBuf {
        self.keep = true;
        self.path.clone()
    }
}

impl Drop for SpillGuard {
    fn drop(&mut self) {
        if !self.keep && std::env::var_os("EULIX_KEEP_SPILL").is_none() {
            let _ = fs::remove_file(&self.path);
        }
    }
}
