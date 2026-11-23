// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mmap

// MapFile exposes mapFile to the external tests, which must see a broken
// mapping that WithFileBytes would hide behind its read fallback.
var MapFile = mapFile
