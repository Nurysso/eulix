// Eulix - Turn your codebase into a searchable book.
// Copyright (C) 2026 Dawood Khan

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

package main

import (
	"fmt"
	"log"
	"os"

	a "eulix/internal/assets"
	"eulix/internal/cli"
)

func init() {
	if a.IsInitialized() {
		return // Skip setup if already initialized
	}

	root, err := a.EulixRoot()
	if err != nil {
		log.Fatalf("eulix: %v", err)
	}

	if err := a.VerifyOrExtract(root); err != nil {
		log.Fatalf("eulix: %v", err)
	}

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

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
