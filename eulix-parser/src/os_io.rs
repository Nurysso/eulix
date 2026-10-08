#[cfg(target_os = "linux")]
mod platform {
    use std::fs::File;
    use std::os::unix::io::AsRawFd;
    use std::path::Path;

    const MIN_PREFETCH_SIZE: u64 = 1024 * 1024;
    const MIN_CACHE_EVICT_SIZE: u64 = 10 * 1024 * 1024;

    pub fn prefetch(path: &Path) {
        if let Ok(metadata) = std::fs::metadata(path) {
            if metadata.len() > MIN_PREFETCH_SIZE {
                if let Ok(f) = std::fs::File::open(path) {
                    let fd = f.as_raw_fd();
                    let size = metadata.len().min(16 * 1024 * 1024);
                    #[allow(unsafe_code)]
                    unsafe {
                        libc::readahead(fd, 0, size as usize);
                    }
                }
            }
        }
    }

    pub fn hint_read_sequential(f: &File) {
        let fd = f.as_raw_fd();
        #[allow(unsafe_code)]
        unsafe {
            libc::posix_fadvise(fd, 0, 0, libc::POSIX_FADV_SEQUENTIAL);
        }
    }

    pub fn done_with_file(f: &File) {
        if let Ok(metadata) = f.metadata() {
            if metadata.len() > MIN_CACHE_EVICT_SIZE {
                let fd = f.as_raw_fd();
                #[allow(unsafe_code)]
                unsafe {
                    libc::posix_fadvise(fd, 0, 0, libc::POSIX_FADV_DONTNEED);
                }
            }
        }
    }

    #[allow(dead_code)]
    pub fn flush_output(_f: &File) {}

    #[allow(dead_code)]
    pub fn hint_write_sequential(_f: &File) {}
    #[allow(dead_code)]
    pub fn flush_and_drop(f: &std::fs::File) {
        if let Ok(metadata) = f.metadata() {
            if metadata.len() > MIN_CACHE_EVICT_SIZE {
                use std::os::unix::io::AsRawFd;
                #[allow(unsafe_code)]
                unsafe {
                    libc::posix_fadvise(f.as_raw_fd(), 0, 0, libc::POSIX_FADV_DONTNEED);
                }
            }
        }
    }
}

#[cfg(target_os = "macos")]
mod platform {
    use std::fs::File;
    use std::os::unix::io::AsRawFd;
    use std::path::Path;

    pub fn prefetch(_path: &Path) {}

    pub fn hint_read_sequential(f: &File) {
        let fd = f.as_raw_fd();
        unsafe {
            libc::fcntl(fd, libc::F_RDAHEAD, 1);
            libc::fcntl(fd, libc::F_NOCACHE, 1);
        }
    }

    pub fn done_with_file(_f: &File) {}

    pub fn flush_output(_f: &File) {}

    pub fn hint_write_sequential(_f: &File) {}

    pub fn flush_and_drop(_f: &std::fs::File) {}
}

#[cfg(target_os = "windows")]
mod platform {
    use std::fs::File;
    use std::path::Path;

    pub fn prefetch(_path: &Path) {}

    pub fn hint_read_sequential(_f: &File) {}

    pub fn done_with_file(_f: &File) {}

    pub fn flush_output(_f: &File) {}

    pub fn hint_write_sequential(_f: &File) {}

    pub fn flush_and_drop(_f: &std::fs::File) {}
}

#[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
mod platform {
    use std::fs::File;
    use std::path::Path;

    pub fn prefetch(_path: &Path) {}
    pub fn hint_read_sequential(_f: &File) {}
    pub fn done_with_file(_f: &File) {}
    pub fn flush_output(_f: &File) {}
    pub fn hint_write_sequential(_f: &File) {}
    pub fn flush_and_drop(_f: &std::fs::File) {}
}

pub use platform::*;
