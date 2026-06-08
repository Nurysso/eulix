//go:build unix

package query

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// decodeViaMmap maps path read-only into the process address space and
// decodes JSON directly from the mapping.  size must equal fi.Size() for the
// file — passing it in avoids a redundant stat syscall.
func decodeViaMmap(path string, size int64, v any) error {
	if sizeOverflows(size) {
		return errFileTooLarge(path, size)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if size == 0 {
		return fmt.Errorf("empty file: %s", path)
	}

	data, err := unix.Mmap(
		int(f.Fd()),
		0,
		int(size), // safe: overflow checked above
		unix.PROT_READ,
		unix.MAP_SHARED,
	)
	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}
	defer unix.Munmap(data)

	// MADV_SEQUENTIAL: tell the kernel we'll scan linearly (JSON parse order).
	// This triggers aggressive read-ahead and avoids random-access page faults.
	_ = unix.Madvise(data, unix.MADV_SEQUENTIAL)

	// sonicCopy copies all decoded strings out of data before we Munmap.
	// Using sonic.ConfigDefault (CopyString: false) here would leave decoded
	// string headers pointing into the now-unmapped region — use-after-free.
	return sonicCopy.Unmarshal(data, v)
}
