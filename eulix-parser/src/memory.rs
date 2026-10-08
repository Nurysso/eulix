pub fn default_thread_count() -> usize {
    #[cfg(feature = "num_cpus")]
    {
        num_cpus::get_physical().max(1)
    }
    #[cfg(not(feature = "num_cpus"))]
    {
        4
    }
}

/// Peak resident memory of the current process, in MB.
pub fn max_rss_mb() -> f64 {
    platform_max_rss_mb()
}

#[cfg(unix)]
fn platform_max_rss_mb() -> f64 {
    use libc::{getrusage, rusage, RUSAGE_SELF};
    use std::mem::zeroed;

    let mut usage: rusage = unsafe { zeroed() };

    if unsafe { getrusage(RUSAGE_SELF, &mut usage) } != 0 {
        return 0.0;
    }

    let raw = usage.ru_maxrss as f64;

    // macOS reports bytes; Linux and most other Unix systems report kilobytes.
    if cfg!(target_os = "macos") {
        raw / (1024.0 * 1024.0)
    } else {
        raw / 1024.0
    }
}

#[cfg(windows)]
fn platform_max_rss_mb() -> f64 {
    windows_mem_mb().unwrap_or(0.0)
}

#[cfg(windows)]
fn windows_mem_mb() -> Option<f64> {
    use std::ffi::c_void;
    use std::mem::{size_of, zeroed};

    #[repr(C)]
    struct ProcessMemoryCounters {
        cb: u32,
        page_fault_count: u32,
        peak_working_set_size: usize,
        working_set_size: usize,
        quota_peak_paged_pool_usage: usize,
        quota_paged_pool_usage: usize,
        quota_peak_non_paged_pool_usage: usize,
        quota_non_paged_pool_usage: usize,
        pagefile_usage: usize,
        peak_pagefile_usage: usize,
    }

    #[link(name = "kernel32")]
    extern "system" {
        fn GetCurrentProcess() -> *mut c_void;
        fn K32GetProcessMemoryInfo(
            process: *mut c_void,
            counters: *mut ProcessMemoryCounters,
            cb: u32,
        ) -> i32;
    }

    unsafe {
        let mut counters: ProcessMemoryCounters = zeroed();
        counters.cb = size_of::<ProcessMemoryCounters>() as u32;

        let ok = K32GetProcessMemoryInfo(GetCurrentProcess(), &mut counters, counters.cb);

        if ok != 0 {
            Some(counters.peak_working_set_size as f64 / (1024.0 * 1024.0))
        } else {
            None
        }
    }
}

#[cfg(not(any(unix, windows)))]
fn platform_max_rss_mb() -> f64 {
    0.0
}
