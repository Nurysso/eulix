//go:build !unix && !windows

package query

import "errors"

var errNoMmap = errors.New("mmap not supported on this platform")

func decodeViaMmap(_ string, _ int64, _ any) error {
	return errNoMmap
}
