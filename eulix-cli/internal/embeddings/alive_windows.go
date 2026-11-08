// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package embeddings

import (
	"os"
	"syscall"
)

// processRunning asks the process handle directly. Windows has no signal 0:
// Process.Signal rejects everything except Kill.
func processRunning(p *os.Process) bool {
	running := false
	err := p.WithHandle(func(handle uintptr) {
		event, err := syscall.WaitForSingleObject(syscall.Handle(handle), 0)
		running = err == nil && event == syscall.WAIT_TIMEOUT
	})
	return err == nil && running
}
