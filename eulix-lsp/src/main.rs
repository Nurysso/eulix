// Copyright 2025-2026 Dawood K. (Nurysso)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

//     http://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//! eulixlsp a language server over the `libeulix` knowledge base.

mod backend;
mod config;
mod convert;
mod diagnostics;
mod index;
mod position;
mod scan;
mod state;
mod syntax;

use tower_lsp::{LspService, Server};
use tracing_subscriber::EnvFilter;

#[tokio::main]
async fn main() {
    // stdout belongs to the JSON-RPC transport. Every log line goes to
    // stderr; a single stray `println!` corrupts the protocol stream.
    tracing_subscriber::fmt()
        .with_env_filter(
            EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| EnvFilter::new("info,eulixlsp=debug,eulixlsp=debug")),
        )
        .with_writer(std::io::stderr)
        .with_target(true)
        .init();

    let stdin = tokio::io::stdin();
    let stdout = tokio::io::stdout();
    let (service, socket) = LspService::new(Backend::new);
    Server::new(stdin, stdout, socket).serve(service).await;

    tracing::info!("server exited");
}

use backend::Backend;
