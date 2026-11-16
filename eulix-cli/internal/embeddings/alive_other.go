// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package embeddings

import (
	"os"
	"syscall"
)

// processRunning relies on signal 0, which checks liveness without delivering
// anything.
func processRunning(p *os.Process) bool {
	return p.Signal(syscall.Signal(0)) == nil
}
