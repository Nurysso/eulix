// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package checksum

// checksum.fb.zst: a zstd-compressed FlatBuffer (schema/checksum.fbs).
// libeulix's file walker writes it during `eulix analyze`; the detector
// reads it. encodeChecksum mirrors libeulix fb_io::encode_checksum and is
// used by Save, which only tests call.

import (
	"errors"
	"fmt"
	"sort"
	"time"

	flatbuffers "github.com/google/flatbuffers/go"

	"eulix/internal/fb"
	"eulix/internal/utils"
)

var errNotChecksum = errors.New("not a eulix checksum file; run 'eulix analyze'")

func decodeChecksum(buf []byte) (c *Checksum, err error) {
	if len(buf) < 8 || !fb.ChecksumBufferHasIdentifier(buf) {
		return nil, errNotChecksum
	}
	defer func() {
		if r := recover(); r != nil {
			c, err = nil, fmt.Errorf("checksum: corrupt file (%v); run 'eulix analyze'", r)
		}
	}()
	root := fb.GetRootAsChecksum(buf, 0)
	if v := root.FormatVersion(); v != utils.KBFormatVersion {
		return nil, fmt.Errorf("checksum: format version %d, want %d: %w", v, utils.KBFormatVersion, errNotChecksum)
	}
	c = &Checksum{
		ProjectPath:     string(root.ProjectPath()),
		TotalFiles:      int(root.TotalFiles()),
		Hash:            string(root.Hash()),
		Files:           make(map[string]FileEntry, root.FilesLength()),
		AnalysisVersion: string(root.AnalysisVersion()),
	}
	if t, err := time.Parse(time.RFC3339Nano, string(root.LastAnalyzed())); err == nil {
		c.LastAnalyzed = t
	}
	var f fb.ChecksumFile
	for i := 0; i < root.FilesLength(); i++ {
		root.Files(&f, i)
		c.Files[string(f.Path())] = FileEntry{Hash: string(f.Hash()), Size: f.Size(), ModTime: f.ModTime()}
	}
	return c, nil
}

func encodeChecksum(c *Checksum) []byte {
	paths := make([]string, 0, len(c.Files))
	for p := range c.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	b := flatbuffers.NewBuilder(1024)
	offs := make([]flatbuffers.UOffsetT, len(paths))
	for i, p := range paths {
		e := c.Files[p]
		path, hash := b.CreateString(p), b.CreateString(e.Hash)
		fb.ChecksumFileStart(b)
		fb.ChecksumFileAddPath(b, path)
		fb.ChecksumFileAddHash(b, hash)
		fb.ChecksumFileAddSize(b, e.Size)
		fb.ChecksumFileAddModTime(b, e.ModTime)
		offs[i] = fb.ChecksumFileEnd(b)
	}
	fb.ChecksumStartFilesVector(b, len(offs))
	for i := len(offs) - 1; i >= 0; i-- {
		b.PrependUOffsetT(offs[i])
	}
	files := b.EndVector(len(offs))
	project, hash := b.CreateString(c.ProjectPath), b.CreateString(c.Hash)
	last, version := b.CreateString(c.LastAnalyzed.Format(time.RFC3339Nano)), b.CreateString(c.AnalysisVersion)
	fb.ChecksumStart(b)
	fb.ChecksumAddFormatVersion(b, utils.KBFormatVersion)
	fb.ChecksumAddProjectPath(b, project)
	fb.ChecksumAddTotalFiles(b, uint64(len(paths)))
	fb.ChecksumAddHash(b, hash)
	fb.ChecksumAddFiles(b, files)
	fb.ChecksumAddLastAnalyzed(b, last)
	fb.ChecksumAddAnalysisVersion(b, version)
	fb.FinishChecksumBuffer(b, fb.ChecksumEnd(b))
	return b.FinishedBytes()
}

// WriteBaseline writes c as <projectPath>/.eulix/checksum.fb.zst, the file the
// parser writes during analysis. For tests in other packages.
func WriteBaseline(projectPath string, c *Checksum) error {
	return newDetector(projectPath).Save(c)
}
