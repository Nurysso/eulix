//go:build windows

package mcp

import (
	"io/fs"
	"syscall"
)

// hiddenByAttr reports whether the entry carries the NTFS hidden attribute.
// WalkDir's d.Info() on Windows already returns the attribute data, so this
// costs no extra syscall.
func hiddenByAttr(d fs.DirEntry) bool {
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
