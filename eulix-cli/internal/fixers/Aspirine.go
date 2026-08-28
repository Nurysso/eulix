//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package Fixers makes an atempt to fix files present in .eulix
// Incase they are corrupted

// This file is responsible for Asprine command and makes a attempt
// to fix generated files
package fixers

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// AspirineOptions holds configuration for the Aspirine rebuild process
type AspirineOptions struct {
	NoBackup  bool
	Force     bool
	FixTarget string
}

// Aspirine validates embeddings.bin and vectors.bin, checks them against
// kb.json / kb_index.json, and repairs header corruption when possible.
func Aspirine(eulixDir string, opts AspirineOptions) error {
	if eulixDir == "" {
		eulixDir = ".eulix"
	}

	if _, err := os.Stat(eulixDir); os.IsNotExist(err) {
		fmt.Printf("☓ Directory not found: %s\n", eulixDir)
		fmt.Println("\nMake sure you've run 'eulix analyze' first to generate the knowledge base.")
		return fmt.Errorf("directory not found: %s", eulixDir)
	}

	fmt.Println("-- Aspirine — KB Binary Repair Tool --")
	embBinPath := filepath.Join(eulixDir, "embeddings.bin")
	vecBinPath := filepath.Join(eulixDir, "vectors.bin")
	kbPath := filepath.Join(eulixDir, "kb.json")
	indexPath := filepath.Join(eulixDir, "kb_index.json")

	fmt.Println("1. Reading binary files...")

	embData, embHdr, embPayloadOff, err := readBinFull(embBinPath)
	if err != nil {
		fmt.Printf("   ☓ embeddings.bin: %v\n", err)
		return fmt.Errorf("failed to read embeddings.bin: %w", err)
	}
	fmt.Printf("   ✓ embeddings.bin — model: %q  count: %d  dim: %d  magic: 0x%08X  ver: %d\n",
		embHdr.ModelName, embHdr.Count, embHdr.Dim, embHdr.Magic, embHdr.Version)

	vecData, vecHdr, vecPayloadOff, err := readBinFull(vecBinPath)
	if err != nil {
		fmt.Printf("   ☓ vectors.bin: %v\n", err)
		return fmt.Errorf("failed to read vectors.bin: %w", err)
	}
	fmt.Printf("   ✓ vectors.bin    — model: %q  count: %d  dim: %d  magic: 0x%08X  ver: %d\n",
		vecHdr.ModelName, vecHdr.Count, vecHdr.Dim, vecHdr.Magic, vecHdr.Version)

	fmt.Println("\n2. Validating payload sizes...")
	embOK := validatePayload("embeddings.bin", embData, embPayloadOff, embHdr)
	vecOK := validatePayload("vectors.bin", vecData, vecPayloadOff, vecHdr)

	fmt.Println("\n3. Running binary diagnostics...")
	_, embErr := printBinDiagnostic("embeddings.bin", embBinPath)
	_, vecErr := printBinDiagnostic("vectors.bin", vecBinPath)

	fmt.Println("\n4. Cross-file header consistency...")
	consistent := true

	if embHdr.Count != vecHdr.Count {
		fmt.Printf("   ⚠  Count mismatch: embeddings=%d  vectors=%d\n", embHdr.Count, vecHdr.Count)
		consistent = false
	} else {
		fmt.Printf("   ✓ Chunk counts match (%d)\n", embHdr.Count)
	}

	if embHdr.Dim != vecHdr.Dim {
		fmt.Printf("   ⚠  Dim mismatch: embeddings=%d  vectors=%d\n", embHdr.Dim, vecHdr.Dim)
		consistent = false
	} else {
		fmt.Printf("   ✓ Dimensions match (%d)\n", embHdr.Dim)
	}

	if embHdr.ModelName != vecHdr.ModelName {
		fmt.Printf("   ⚠  Model mismatch: embeddings=%q  vectors=%q\n",
			embHdr.ModelName, vecHdr.ModelName)
		consistent = false
	} else {
		fmt.Printf("   ✓ Model names match (%s)\n", embHdr.ModelName)
	}

	if embHdr.Magic != vecHdr.Magic {
		fmt.Printf("   ⚠  Magic mismatch: embeddings=0x%08X  vectors=0x%08X\n",
			embHdr.Magic, vecHdr.Magic)
		consistent = false
	}

	fmt.Println("\n5. Cross-checking against KB JSON metadata...")
	funcCount, typeCount, ierr := checkIndex(indexPath)
	if ierr == nil {
		fmt.Printf("   kb_index.json — functions: %d  types: %d\n", funcCount, typeCount)
		if embErr == nil {
			crossCheckRatio("embeddings.bin", int(embHdr.Count), funcCount)
		}
		if vecErr == nil {
			crossCheckRatio("vectors.bin", int(vecHdr.Count), funcCount)
		}
	} else {
		fmt.Printf("   !  kb_index.json not readable (%v); skipping cross-check\n", ierr)
	}

	if kb, kerr := loadKB(kbPath); kerr == nil {
		fmt.Printf("   kb.json — total_functions (metadata): %d\n", kb.Metadata.TotalFunctions)
	} else {
		fmt.Printf("   !  kb.json not readable (%v)\n", kerr)
	}

	fmt.Println("\n6. Checking vector payloads for NaN/Inf...")
	spotCheck("embeddings.bin", embData, embPayloadOff, embHdr)
	spotCheck("vectors.bin", vecData, vecPayloadOff, vecHdr)

	fmt.Println("\n7. File sizes:")
	files := []string{"kb.json", "kb_call_graph.json", "kb_index.json", "embeddings.bin", "vectors.bin"}
	for _, file := range files {
		path := filepath.Join(eulixDir, file)
		if info, serr := os.Stat(path); serr == nil {
			sizeMB := float64(info.Size()) / (1024 * 1024)
			fmt.Printf("   %-20s %.2f MB\n", file, sizeMB)
		} else {
			fmt.Printf("   %-20s NOT FOUND\n", file)
		}
	}

	allGood := embOK && vecOK && consistent && embErr == nil && vecErr == nil
	if allGood {
		fmt.Println("\n✓ Both binary files look healthy — no repair needed.")
		return nil
	}

	fmt.Println("\n8. Executing repair plan...")

	if !consistent && opts.FixTarget == "" && !opts.Force {
		fmt.Println("   ☓ Files are inconsistent and no --fix-target specified.")
		fmt.Println("      Re-run with --fix-target=embeddings or --fix-target=vectors")
		fmt.Println("      to nominate which file's header is the authoritative source.")
		return fmt.Errorf("inconsistent bin files; specify --fix-target to repair")
	}

	var authHdr binHeader
	var rewritePath string
	var rewriteData []byte
	var rewritePayloadOff int

	switch opts.FixTarget {
	case "embeddings":
		authHdr = embHdr
		rewritePath = vecBinPath
		rewriteData = vecData
		rewritePayloadOff = vecPayloadOff
	case "vectors":
		authHdr = vecHdr
		rewritePath = embBinPath
		rewriteData = embData
		rewritePayloadOff = embPayloadOff
	default:
		if err := selfHeal(embBinPath, embData, embHdr, embPayloadOff, opts); err != nil {
			fmt.Printf("   ☓ embeddings.bin self-heal failed: %v\n", err)
		}
		if err := selfHeal(vecBinPath, vecData, vecHdr, vecPayloadOff, opts); err != nil {
			fmt.Printf("   ☓ vectors.bin self-heal failed: %v\n", err)
		}
		fmt.Println("\n✓ Repair attempted. Re-run Aspirine to verify.")
		return nil
	}

	fmt.Printf("   Authority: %s header → rewriting %s\n", opts.FixTarget, filepath.Base(rewritePath))
	return rewriteHeader(rewritePath, rewriteData, rewritePayloadOff, authHdr, opts)
}

// readBinFull reads a file, parses its header, and returns the raw bytes,
// the parsed header, and the byte offset where the payload begins.
func readBinFull(path string) ([]byte, binHeader, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, binHeader{}, 0, err
	}
	hdr, off, err := readBinHeader(data)
	if err != nil {
		return nil, binHeader{}, 0, err
	}
	return data, hdr, off, nil
}

func validatePayload(name string, data []byte, payloadOff int, hdr binHeader) bool {
	pos := payloadOff
	for i := uint32(0); i < hdr.Count; i++ {
		// Read id_len prefix
		if pos+4 > len(data) {
			fmt.Printf("   ⚠  %s: truncated at entry %d while reading id_len (byte %d)\n",
				name, i, pos)
			return false
		}
		idLen := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4

		if pos+idLen > len(data) {
			fmt.Printf("   ⚠  %s: truncated at entry %d while reading id (%d bytes, byte %d)\n",
				name, i, idLen, pos)
			return false
		}
		pos += idLen

		// Skip vector
		vecBytes := int(hdr.Dim) * 4
		if pos+vecBytes > len(data) {
			fmt.Printf("   ⚠  %s: truncated at entry %d while reading vector (byte %d)\n",
				name, i, pos)
			return false
		}
		pos += vecBytes
	}

	trailing := len(data) - pos
	if trailing != 0 {
		fmt.Printf("   ⚠  %s: %d unexpected trailing bytes after %d entries\n",
			name, trailing, hdr.Count)
		return false
	}

	fmt.Printf("   ✓ %s payload OK (%d entries, %d bytes)\n",
		name, hdr.Count, len(data)-payloadOff)
	return true
}

// spotCheck reads the first and last float32 vectors and flags NaN/Inf.
// Scans entry-by-entry to handle the [id_len][id][vector] layout.
func spotCheck(name string, data []byte, payloadOff int, hdr binHeader) {
	if hdr.Count == 0 || hdr.Dim == 0 {
		fmt.Printf("   ⚠  %s: empty — skipping spot check\n", name)
		return
	}

	type entry struct {
		id     string
		vecOff int
	}

	// Seek to the nth entry by scanning — we only need first and last
	seekEntry := func(target uint32) (entry, bool) {
		pos := payloadOff
		for i := uint32(0); i <= target; i++ {
			if pos+4 > len(data) {
				return entry{}, false
			}
			idLen := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
			pos += 4
			if pos+idLen > len(data) {
				return entry{}, false
			}
			id := string(data[pos : pos+idLen])
			pos += idLen
			vecOff := pos
			vecBytes := int(hdr.Dim) * 4
			if pos+vecBytes > len(data) {
				return entry{}, false
			}
			pos += vecBytes
			if i == target {
				return entry{id: id, vecOff: vecOff}, true
			}
		}
		return entry{}, false
	}

	checkVec := func(label string, e entry) {
		vecBytes := int(hdr.Dim) * 4
		bad := 0
		for j := 0; j+4 <= vecBytes; j += 4 {
			bits := binary.LittleEndian.Uint32(data[e.vecOff+j : e.vecOff+j+4])
			f := math.Float32frombits(bits)
			if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
				bad++
			}
		}
		if bad > 0 {
			fmt.Printf("   ⚠  %s %s vector (%s): %d NaN/Inf out of %d\n",
				name, label, e.id, bad, hdr.Dim)
		} else {
			fmt.Printf("   ✓ %s %s vector (%s): clean\n", name, label, e.id)
		}
	}

	if first, ok := seekEntry(0); ok {
		checkVec("first", first)
	} else {
		fmt.Printf("   ⚠  %s: could not seek to first entry\n", name)
	}

	if hdr.Count > 1 {
		if last, ok := seekEntry(hdr.Count - 1); ok {
			checkVec("last", last)
		} else {
			fmt.Printf("   ⚠  %s: could not seek to last entry\n", name)
		}
	}
}

// crossCheckRatio warns if the embedding count is wildly off vs indexed functions.
func crossCheckRatio(name string, embCount, kbFuncs int) {
	if kbFuncs == 0 {
		return
	}
	ratio := float64(embCount) / float64(kbFuncs)
	if ratio < 0.1 || ratio > 100 {
		fmt.Printf("   ⚠  %s: %d embeddings vs %d indexed functions (ratio %.2f — unusual)\n",
			name, embCount, kbFuncs, ratio)
	} else {
		fmt.Printf("   ✓ %s: %d embeddings vs %d indexed functions (ratio %.2f)\n",
			name, embCount, kbFuncs, ratio)
	}
}

// selfHeal infers the correct count from the payload length and rewrites the header.
func selfHeal(path string, data []byte, hdr binHeader, payloadOff int, opts AspirineOptions) error {
	if hdr.Dim == 0 {
		return fmt.Errorf("dim is 0; cannot scan entries")
	}

	// Scan entry-by-entry to count what's actually there
	pos := payloadOff
	count := uint32(0)
	for pos < len(data) {
		if pos+4 > len(data) {
			break
		}
		idLen := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4
		if pos+idLen > len(data) {
			break
		}
		pos += idLen
		vecBytes := int(hdr.Dim) * 4
		if pos+vecBytes > len(data) {
			break
		}
		pos += vecBytes
		count++
	}

	trailing := len(data) - pos
	if trailing != 0 {
		fmt.Printf("   ⚠  %s: %d trailing bytes not part of any complete entry\n",
			filepath.Base(path), trailing)
	}

	if count == hdr.Count {
		fmt.Printf("   ✓ %s header already correct (count=%d)\n", filepath.Base(path), hdr.Count)
		return nil
	}

	fmt.Printf("    %s: correcting count %d → %d\n", filepath.Base(path), hdr.Count, count)
	corrected := hdr
	corrected.Count = count
	return rewriteHeader(path, data, payloadOff, corrected, opts)
}

// rewriteHeader backs up the file (unless NoBackup) then writes a new header
// followed by the original payload unchanged.
func rewriteHeader(path string, data []byte, payloadOff int, hdr binHeader, opts AspirineOptions) error {
	if !opts.NoBackup {
		backupPath := fmt.Sprintf("%s.backup.%d", path, os.Getpid())
		if err := os.WriteFile(backupPath, data, 0644); err != nil {
			fmt.Printf("   ⚠  Could not create backup %s: %v\n", backupPath, err)
		} else {
			fmt.Printf("   ✓ Backed up original to %s\n", backupPath)
		}
	}

	newHeader := buildBinHeader(hdr)
	payload := data[payloadOff:]

	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("could not open %s for writing: %w", path, err)
	}
	func() { _ = out.Close() }()

	if _, err := out.Write(newHeader); err != nil {
		return fmt.Errorf("header write failed: %w", err)
	}
	if _, err := out.Write(payload); err != nil {
		return fmt.Errorf("payload write failed: %w", err)
	}

	fmt.Printf("   ✓ Rewrote %s — count=%d  dim=%d  model=%q\n",
		filepath.Base(path), hdr.Count, hdr.Dim, hdr.ModelName)
	return nil
}

// buildBinHeader serialises a binHeader back to bytes.
// Format: [4B magic][4B version][4B name_len][name bytes][4B count][4B dim]
func buildBinHeader(hdr binHeader) []byte {
	nameBytes := []byte(hdr.ModelName)
	buf := make([]byte, 4+4+4+len(nameBytes)+4+4)
	off := 0
	binary.LittleEndian.PutUint32(buf[off:], hdr.Magic)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], hdr.Version)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(len(nameBytes)))
	off += 4
	copy(buf[off:], nameBytes)
	off += len(nameBytes)
	binary.LittleEndian.PutUint32(buf[off:], hdr.Count)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], hdr.Dim)
	return buf
}
