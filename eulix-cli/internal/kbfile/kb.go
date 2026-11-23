// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package kbfile

// kb.fb: a size-prefixed KbHeader (metadata and each file's path, offset
// and length) followed by one FileRecord buffer per source file. Layout:
// schema/kb_header.fbs.

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"eulix/internal/fb"
	"eulix/internal/query/retrieval/mmap"
	"eulix/internal/utils"
)

// KBFile is the knowledge base, relative to .eulix/.
const KBFile = "kb.fb"

// kbRecordAlign matches libeulix fb_io::KB_RECORD_ALIGN.
const kbRecordAlign = 8

func kbPad(n int64) int64 { return (kbRecordAlign - n%kbRecordAlign) % kbRecordAlign }

// recovered runs fn, turning a panic from a corrupt buffer into an error.
func recovered(path, what string, fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: corrupt %s (%v); run 'eulix analyze'", filepath.Base(path), what, r)
		}
	}()
	fn()
	return nil
}

type kbEntry struct {
	path   string
	offset int64
	length int64
}

// WalkKB reads kb.fb front to back. onMeta, if not nil, gets the metadata
// before any file; onFile gets each file's path and record in path order.
// Only one record is in memory at a time, and the file is read through a
// buffered reader rather than mapped (see mmap.OpenForSequentialRead), so a
// multi-gigabyte knowledge base costs little more than the chunks the caller
// keeps. An error from onFile stops the walk and is returned.
func WalkKB(path string, onMeta func(*utils.Metadata), onFile func(string, *utils.FileData) error) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	size := fi.Size()
	in, cleanup, err := mmap.OpenForSequentialRead(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	defer cleanup()
	r := bufio.NewReaderSize(in, 1<<16)

	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return fmt.Errorf("%s is not a eulix knowledge base file: %w", filepath.Base(path), ErrStale)
	}
	n := int64(binary.LittleEndian.Uint32(prefix[:]))
	if 4+n > size || n < 8 {
		// Old JSON kb files land here: "{\"me" read as a length.
		return fmt.Errorf("%s is not a eulix knowledge base file: %w", filepath.Base(path), ErrStale)
	}
	head := make([]byte, 4+n)
	copy(head, prefix[:])
	if _, err := io.ReadFull(r, head[4:]); err != nil {
		return fmt.Errorf("read %s header: %w", filepath.Base(path), err)
	}
	if !fb.SizePrefixedKbHeaderBufferHasIdentifier(head) {
		return fmt.Errorf("%s is not a eulix knowledge base file: %w", filepath.Base(path), ErrStale)
	}

	var meta *utils.Metadata
	var entries []kbEntry
	if err := recovered(path, "header", func() {
		h := fb.GetSizePrefixedRootAsKbHeader(head, 0)
		if v := h.FormatVersion(); v != utils.KBFormatVersion {
			err = fmt.Errorf("%s has format version %d, want %d: %w", filepath.Base(path), v, utils.KBFormatVersion, ErrStale)
			return
		}
		meta = metadata(h.Metadata(nil))
		entries = make([]kbEntry, h.FilesLength())
		var e fb.FileEntry
		for i := range entries {
			h.Files(&e, i)
			entries[i] = kbEntry{path: string(e.Path()), offset: int64(e.Offset()), length: int64(e.Length())}
		}
	}); err != nil {
		return err
	}
	if err != nil {
		return err
	}
	if onMeta != nil {
		onMeta(meta)
	}

	recordsStart := 4 + n + kbPad(4+n)
	if _, err := r.Discard(int(recordsStart - (4 + n))); err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	var pos int64 // position within the records region
	var buf []byte
	for _, e := range entries {
		if e.offset < pos || recordsStart+e.offset+e.length > size {
			return fmt.Errorf("%s: record for %s is out of place; run 'eulix analyze'", filepath.Base(path), e.path)
		}
		if _, err := r.Discard(int(e.offset - pos)); err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(path), err)
		}
		if int64(cap(buf)) < e.length {
			buf = make([]byte, e.length)
		}
		rec := buf[:e.length]
		if _, err := io.ReadFull(r, rec); err != nil {
			return fmt.Errorf("read %s record for %s: %w", filepath.Base(path), e.path, err)
		}
		pos = e.offset + e.length
		if len(rec) < 8 || !fb.FileRecordBufferHasIdentifier(rec) {
			return fmt.Errorf("%s: record for %s is corrupt; run 'eulix analyze'", filepath.Base(path), e.path)
		}
		var fd *utils.FileData
		if err := recovered(path, "record for "+e.path, func() { fd = fileRecord(fb.GetRootAsFileRecord(rec, 0)) }); err != nil {
			return err
		}
		if err := onFile(e.path, fd); err != nil {
			return err
		}
	}
	return nil
}

var errStopWalk = errors.New("stop")

// ReadKBMetadata reads only the knowledge base's metadata from its header.
func ReadKBMetadata(path string) (*utils.Metadata, error) {
	var meta *utils.Metadata
	err := WalkKB(path, func(m *utils.Metadata) { meta = m }, func(string, *utils.FileData) error { return errStopWalk })
	if err != nil && !errors.Is(err, errStopWalk) {
		return nil, err
	}
	return meta, nil
}

// ReadKB reads the whole knowledge base into memory. Prefer WalkKB when the
// records are only needed one at a time.
func ReadKB(path string) (*utils.KnowledgeBaseSimplifiedRef, error) {
	kb := &utils.KnowledgeBaseSimplifiedRef{Structure: map[string]utils.FileData{}}
	err := WalkKB(path, func(m *utils.Metadata) { kb.Metadata = m }, func(p string, fd *utils.FileData) error {
		kb.Structure[p] = *fd
		return nil
	})
	if err != nil {
		return nil, err
	}
	return kb, nil
}

func fileRecord(r *fb.FileRecord) *utils.FileData {
	fd := &utils.FileData{
		Language:  str(r.Language()),
		Loc:       int(r.Loc()),
		Functions: make([]utils.KBFunction, r.FunctionsLength()),
		Classes:   make([]utils.KBClass, r.ClassesLength()),
		Todos:     make([]utils.Todo, r.TodosLength()),
	}
	var f fb.Function
	for i := range fd.Functions {
		r.Functions(&f, i)
		fd.Functions[i] = function(&f)
	}
	var c fb.Class
	for i := range fd.Classes {
		r.Classes(&c, i)
		cls := utils.KBClass{
			ID:         str(c.Id()),
			Name:       str(c.Name()),
			Bases:      copyStrings(c.BasesLength(), c.Bases),
			Docstring:  str(c.Docstring()),
			LineStart:  int(c.LineStart()),
			LineEnd:    int(c.LineEnd()),
			Methods:    make([]utils.KBFunction, c.MethodsLength()),
			Attributes: make([]utils.Attribute, c.AttributesLength()),
			Decorators: copyStrings(c.DecoratorsLength(), c.Decorators),
		}
		var m fb.Function
		for j := range cls.Methods {
			c.Methods(&m, j)
			cls.Methods[j] = function(&m)
		}
		var a fb.Attribute
		for j := range cls.Attributes {
			c.Attributes(&a, j)
			cls.Attributes[j] = utils.Attribute{
				Name:           str(a.Name()),
				TypeAnnotation: str(a.TypeAnnotation()),
				Value:          optStr(a.Value()),
			}
		}
		fd.Classes[i] = cls
	}
	var t fb.Todo
	for i := range fd.Todos {
		r.Todos(&t, i)
		fd.Todos[i] = utils.Todo{Line: int(t.Line()), Text: str(t.Text()), Priority: str(t.Priority())}
	}
	return fd
}

func function(f *fb.Function) utils.KBFunction {
	out := utils.KBFunction{
		Params:     make([]utils.Parameter, f.ParamsLength()),
		ID:         str(f.Id()),
		Name:       str(f.Name()),
		Signature:  str(f.Signature()),
		ReturnType: str(f.ReturnType()),
		Docstring:  str(f.Docstring()),
		LineStart:  int(f.LineStart()),
		LineEnd:    int(f.LineEnd()),
	}
	var p fb.Parameter
	for i := range out.Params {
		f.Params(&p, i)
		out.Params[i] = utils.Parameter{
			Name:           str(p.Name()),
			TypeAnnotation: str(p.TypeAnnotation()),
			DefaultValue:   optStr(p.DefaultValue()),
		}
	}
	return out
}
