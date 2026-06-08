package query

import (
	"bufio"
	"fmt"
	"os"

	"github.com/bytedance/sonic"
)

// mmapThreshold: files smaller than this are read normally.
// mmap syscall overhead isn't worth it under ~32 MB.
const mmapThreshold = 32 << 20 // 32 MiB

// jsonBufSize is the read-buffer size used by decodeViaReader.

// sonicCopy is a sonic config that always copies strings out of the input
// buffer. This is required on the mmap path: the mapped region is unmapped
// before the caller uses the decoded value, so any decoded string that
// references the raw bytes directly would become a dangling pointer.
// CopyString: true adds a small allocation cost but is safe on all paths.
var sonicCopy = sonic.Config{CopyString: true}.Froze()

// decodeJSONFile decodes path into v using the fastest available strategy:
//
//  1. Files ≥ mmapThreshold: mmap the file so the OS controls paging, then
//     decode in-place with sonic (zero heap copy of the raw bytes, strings
//     copied out of the mapping before it is released).
//  2. Files < mmapThreshold, or if mmap fails: buffered reader + sonic decoder.
//
// sonic is 3-5× faster than encoding/json on large structs due to JIT field
// access; combined with mmap it avoids the transient "file bytes + parsed
// struct both live in RAM" peak that os.ReadFile + json.Unmarshal causes.
func decodeJSONFile(path string, v any) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}

	size := fi.Size()

	if size >= mmapThreshold {
		// Pass size so decodeViaMmap doesn't need a second stat.
		if err := decodeViaMmap(path, size, v); err == nil {
			return nil
		}
		// mmap failed (unsupported platform, permissions, etc.) — fall through.
	}

	return decodeViaReader(path, v)
}

// decodeViaReader is the fallback path: buffered reader + sonic streaming
// decoder. sonicCopy is used for consistency (and safety if this function is
// ever called after a partial mmap attempt).
func decodeViaReader(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return sonicCopy.
		NewDecoder(bufio.NewReaderSize(f, jsonBufSize)).
		Decode(v)
}

// sizeFitsInt returns an error when size would overflow int on this platform
// (relevant on 32-bit builds where int is 32 bits).
func sizeOverflows(size int64) bool {
	const maxInt = int64(^uint(0) >> 1)
	return size > maxInt
}

// errFileTooLarge is returned when a file exceeds the addressable range of
// the current platform (only reachable on 32-bit builds).
func errFileTooLarge(path string, size int64) error {
	return fmt.Errorf("file %s is %d bytes, which overflows int on this platform", path, size)
}
