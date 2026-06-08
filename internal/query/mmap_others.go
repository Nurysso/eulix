//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query manages query routing and retrival for EULIX.

//go:build !unix && !windows

/*
Prevents mmap to happen on unsupported os
*/

package query

import "errors"

var errNoMmap = errors.New("mmap not supported on this platform")

func decodeViaMmap(_ string, _ int64, _ any) error {
	return errNoMmap
}
