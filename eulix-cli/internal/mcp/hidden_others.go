//go:build !windows

package mcp

import "io/fs"

func hiddenByAttr(fs.DirEntry) bool { return false }
