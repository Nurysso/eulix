//go:build windows

package checksum

import (
	"io/fs"
	"syscall"
)

// HiddenByAttr reports whether the entry carries the NTFS hidden attribute.
// WalkDir's d.Info() on Windows already returns the attribute data, so this
// costs no extra syscall.
func HiddenByAttr(d fs.DirEntry) bool {
	info, err := d.Info()
	if err != nil {
		return false
	}
	sys, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	return attrsHidden(sys.FileAttributes)
}

// hiddenPath is HiddenByAttr for a path without a DirEntry. Errors count as not
// hidden; the later read reports them.
func hiddenPath(path string) bool {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := syscall.GetFileAttributes(p)
	return err == nil && attrsHidden(attrs)
}
