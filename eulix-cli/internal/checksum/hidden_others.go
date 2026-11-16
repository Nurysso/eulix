//go:build !windows

package checksum

import "io/fs"

// HiddenByAttr is always false: only Windows has a hidden attribute.
func HiddenByAttr(fs.DirEntry) bool { return false }

func hiddenPath(string) bool { return false }
