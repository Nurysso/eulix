use std::path::Path;

use libeulix::grammar_files;
use libeulix::struc::kb_struct::FileData;
use rayon::prelude::*;
use rayon::{ThreadPool, ThreadPoolBuilder};

pub struct Engine {
    pool: ThreadPool,
}

impl Engine {
    pub fn threads(&self) -> usize {
        self.pool.current_num_threads()
    }
    pub fn with_threads(n: usize) -> Result<Self, rayon::ThreadPoolBuildError> {
        Ok(Self {
            pool: ThreadPoolBuilder::new().num_threads(n).build()?,
        })
    }

    /// The underlying pool, for callers that need to run their own
    /// parallel work inside it (e.g. `parse_directory`'s chunk pipeline).
    pub fn pool(&self) -> &ThreadPool {
        &self.pool
    }
}
