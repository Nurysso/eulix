// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
//
// Maintainer: Dawood (Nurysso) <nurysso [at] proton.me>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Eulix is the command-line orchestrator and terminal interface for the Eulix code-intelligence platform.
// It coordinates static analysis (eulix_parser), semantic embedding generation (eulix_embed),
// multi-tier hybrid retrieval, context window assembly, and LLM inference.

package main

import (
	"fmt"
	"log"
	"os"

	a "eulix/internal/assets"
	"eulix/internal/cli"
)

func init() {
	root, err := a.EulixRoot()
	if err != nil {
		log.Fatalf("eulix: %v", err)
	}

	// ALWAYS verify assets, regardless of initialization status
	if err := a.VerifyOrExtract(root); err != nil {
		log.Fatalf("eulix: %v", err)
	}

	// Only run setup if not already initialized
	if !a.IsInitialized() {
		if err := a.CheckUv(); err != nil {
			log.Fatalf("eulix: %v", err)
		}

		if err := a.CheckVenv(root); err != nil {
			log.Fatalf("eulix: %v", err)
		}

		if err := a.GlobalInitialized(root); err != nil {
			log.Fatalf("failed to write .initialized file: %v", err)
		}

		if err := a.InstallEmbedDeps(root); err != nil {
			log.Fatalf("eulix: %v", err)
		}
	}
}

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
