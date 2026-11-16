// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use rayon::{ThreadPool, ThreadPoolBuilder};

// The libeulix tree walkers recurse once per syntax level, so a long
// expression chain (generated or minified code) needs far more than the
// 2 MiB rayon gives each worker by default; past that the whole process
// aborts. The extra stack is only touched by files that go that deep.
const WORKER_STACK_SIZE: usize = 64 * 1024 * 1024;

pub struct Engine {
    pool: ThreadPool,
}

impl Engine {
    pub fn threads(&self) -> usize {
        self.pool.current_num_threads()
    }
    pub fn with_threads(n: usize) -> Result<Self, rayon::ThreadPoolBuildError> {
        Ok(Self {
            pool: ThreadPoolBuilder::new()
                .num_threads(n)
                .stack_size(WORKER_STACK_SIZE)
                .build()?,
        })
    }

    /// The underlying pool, for callers that need to run their own
    /// parallel work inside it (e.g. `parse_directory`'s chunk pipeline).
    pub fn pool(&self) -> &ThreadPool {
        &self.pool
    }
}

#[cfg(test)]
mod tests {
    use super::Engine;
    use crate::parse::directory::parse_directory;
    use std::fs;
    use tempfile::TempDir;

    #[test]
    fn a_long_expression_chain_does_not_overflow_the_worker_stack() {
        // A release build on the default 2 MiB stack aborts at about
        // 2,600 terms; debug builds use bigger frames and abort sooner.
        let repo = TempDir::new().unwrap();
        let out = TempDir::new().unwrap();
        let chain = vec!["x"; 10_000].join(" + ");
        fs::write(
            repo.path().join("deep.py"),
            format!("def f(x):\n    return {chain}\n"),
        )
        .unwrap();

        let engine = Engine::with_threads(1).unwrap();
        let root = repo.path().to_string_lossy().to_string();
        let (kb, stats, _spill) = parse_directory(
            &root,
            "all",
            None,
            false,
            "0.8.1",
            "test",
            out.path(),
            &engine,
        )
        .unwrap();

        assert_eq!(kb.structure["deep.py"].functions[0].name, "f");
        assert!(stats.failed.is_empty());
    }
}
