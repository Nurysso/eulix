//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package Fixers makes an atempt to fix files present in .corvux
// Incase they are corrupted

// This file is reponsible for Glasdos command that checks wether the
// analyze output was correct or not
package fixers

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// binHeader holds the decoded header common to embeddings.bin and vectors.bin.
type binHeader struct {
	Magic     uint32
	Version   uint32
	ModelName string
	Count     uint32
	Dim       uint32
}

// readBinHeader parses [4B magic][4B version][4B+str model_name][4B count][4B dim]
// from the start of data. Returns the header and the number of bytes consumed.
func readBinHeader(data []byte) (binHeader, int, error) {
	const minFixed = 4 + 4 + 4 // magic + version + name-length field
	if len(data) < minFixed {
		return binHeader{}, 0, fmt.Errorf("file too short for header (%d bytes)", len(data))
	}

	off := 0
	magic := binary.LittleEndian.Uint32(data[off:])
	off += 4
	version := binary.LittleEndian.Uint32(data[off:])
	off += 4

	nameLen := int(binary.LittleEndian.Uint32(data[off:]))
	off += 4
	if len(data) < off+nameLen+8 { // +8 for count+dim
		return binHeader{}, 0, fmt.Errorf("file too short: need %d bytes for name+count+dim, have %d",
			off+nameLen+8, len(data))
	}
	modelName := string(data[off : off+nameLen])
	off += nameLen

	count := binary.LittleEndian.Uint32(data[off:])
	off += 4
	dim := binary.LittleEndian.Uint32(data[off:])
	off += 4

	return binHeader{
		Magic:     magic,
		Version:   version,
		ModelName: modelName,
		Count:     count,
		Dim:       dim,
	}, off, nil
}

// checkBinFile reads a .bin file and returns its header.
// Retained for callers (e.g. Aspirine) that only need the header.
func checkBinFile(path string) (binHeader, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return binHeader{}, err
	}
	hdr, _, err := readBinHeader(data)
	return hdr, err
}

// checkBinFileFull reads a .bin file and returns the header, raw bytes, and
// the byte offset at which the payload begins.
func checkBinFileFull(path string) (hdr binHeader, data []byte, payloadOff int, err error) {
	data, err = os.ReadFile(path)
	if err != nil {
		return
	}
	hdr, payloadOff, err = readBinHeader(data)
	return
}

// scanBinPayload walks the entry-by-entry payload of embeddings.bin / vectors.bin
// and returns the inferred entry count and whether the file ended cleanly.
// Format per entry: [4B id_len][id bytes][dim*4B float32 vector]
func scanBinPayload(data []byte, payloadOff int, hdr binHeader) (inferredCount int, ok bool) {
	pos := payloadOff
	count := 0
	for pos < len(data) {
		if pos+4 > len(data) {
			return count, false
		}
		idLen := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4
		if pos+idLen > len(data) {
			return count, false
		}
		pos += idLen
		vecBytes := int(hdr.Dim) * 4
		if pos+vecBytes > len(data) {
			return count, false
		}
		pos += vecBytes
		count++
	}
	return count, pos == len(data)
}

// printBinDiagnostic prints header info and validates the payload for a single
// .bin file, using the actual [id_len][id][vector] entry layout rather than
// assuming a flat dense matrix.
func printBinDiagnostic(label, path string) (binHeader, error) {
	hdr, data, payloadOff, err := checkBinFileFull(path)
	if err != nil {
		fmt.Printf("   ☓ Failed to read %s: %v\n", label, err)
		return binHeader{}, err
	}

	fmt.Printf("   ✓ Loaded %s\n", label)
	fmt.Printf("      Magic:   0x%08X   Version: %d\n", hdr.Magic, hdr.Version)
	fmt.Printf("      Model:   %s\n", hdr.ModelName)
	fmt.Printf("      Count:   %d   Dim: %d\n", hdr.Count, hdr.Dim)

	inferred, ok := scanBinPayload(data, payloadOff, hdr)
	switch {
	case !ok && inferred != int(hdr.Count):
		fmt.Printf("   ⚠  Payload malformed: scanned %d complete entries before truncation (header says %d)\n",
			inferred, hdr.Count)
	case !ok:
		fmt.Printf("   ⚠  Payload truncated after %d entries (header says %d)\n",
			inferred, hdr.Count)
	case inferred != int(hdr.Count):
		fmt.Printf("   ⚠  Entry count mismatch: scanned %d entries, header says %d\n",
			inferred, hdr.Count)
	default:
		payloadBytes := len(data) - payloadOff
		fmt.Printf("   ✓ Payload OK — %d entries, %d payload bytes (avg %.0f bytes/entry incl. IDs)\n",
			inferred, payloadBytes, float64(payloadBytes)/float64(max(inferred, 1)))
	}
	return hdr, nil
}

// GLaDOS checks for knowledge base outputs and validates file integrity.
func GLaDOS(corvuxDir string) error {
	if corvuxDir == "" {
		corvuxDir = ".corvux"
	}

	if _, err := os.Stat(corvuxDir); os.IsNotExist(err) {
		fmt.Printf("☓ Directory not found: %s\n", corvuxDir)
		fmt.Println("\nMake sure you've run 'corvux analyze' first to generate the knowledge base.")
		return fmt.Errorf("directory not found: %s", corvuxDir)
	}

	fmt.Println("🔍 KB Diagnostic Tool")
	fmt.Println("================================")
	fmt.Printf("Analyzing: %s\n\n", corvuxDir)

	// 1. kb.json
	fmt.Println("1. Checking kb.json (codebase structure)...")
	kbPath := filepath.Join(corvuxDir, "kb.json")
	kb, err := loadKB(kbPath)
	if err != nil {
		fmt.Printf("   ☓ Failed to load kb.json: %v\n", err)
	} else {
		fmt.Printf("   ✓ Loaded KB for project: %s\n", kb.Metadata.ProjectName)
		fmt.Printf("      Languages:  %v\n", kb.Metadata.Languages)
		fmt.Printf("      Files:      %d\n", kb.Metadata.TotalFiles)
		fmt.Printf("      LOC:        %d\n", kb.Metadata.TotalLOC)
		fmt.Printf("      Functions:  %d   Classes: %d   Methods: %d\n",
			kb.Metadata.TotalFunctions, kb.Metadata.TotalClasses, kb.Metadata.TotalMethods)
		fmt.Printf("      Entry pts:  %d\n", len(kb.EntryPoints))
		fmt.Printf("      Ext deps:   %d\n", len(kb.ExternalDeps))
		fmt.Printf("      Index — functions: %d   types: %d\n",
			len(kb.Indices.FunctionsByName), len(kb.Indices.TypesByName))
		fmt.Printf("      Call graph — nodes: %d   edges: %d\n",
			len(kb.CallGraph.Nodes), len(kb.CallGraph.Edges))
	}

	// 2. kb_call_graph.json
	fmt.Println("\n2. Checking call graphs...")
	cgPath := filepath.Join(corvuxDir, "kb_call_graph.json")
	cgNodes, cgEdges, err := checkCallGraph(cgPath)
	if err != nil {
		fmt.Printf("   ☓ Failed to load kb_call_graph.json: %v\n", err)
	} else {
		fmt.Printf("   ✓ Loaded call graph\n")
		fmt.Printf("      Nodes: %d   Edges: %d\n", cgNodes, cgEdges)
	}

	// 3. kb_index.json
	fmt.Println("\n3. Checking indexes...")
	indexPath := filepath.Join(corvuxDir, "kb_index.json")
	funcCount, typeCount2, err := checkIndex(indexPath)
	if err != nil {
		fmt.Printf("   ☓ Failed to load kb_index.json: %v\n", err)
	} else {
		fmt.Printf("   ✓ Loaded index\n")
		fmt.Printf("      Functions: %d   Types: %d\n", funcCount, typeCount2)
	}

	// 4. embeddings.bin
	fmt.Println("\n4. Checking embeddings.bin...")
	embBinPath := filepath.Join(corvuxDir, "embeddings.bin")
	embHdr, embErr := printBinDiagnostic("embeddings.bin", embBinPath)

	// 5. vectors.bin
	fmt.Println("\n5. Checking vectors.bin...")
	vecBinPath := filepath.Join(corvuxDir, "vectors.bin")
	vecHdr, vecErr := printBinDiagnostic("vectors.bin", vecBinPath)

	// 6. Cross-file consistency
	fmt.Println("\n6. Cross-file consistency checks:")
	if embErr == nil && vecErr == nil {
		if embHdr.Count != vecHdr.Count {
			fmt.Printf("   ⚠  Count mismatch: embeddings.bin=%d  vectors.bin=%d\n",
				embHdr.Count, vecHdr.Count)
		} else {
			fmt.Printf("   ✓ Chunk counts match (%d)\n", embHdr.Count)
		}
		if embHdr.Dim != vecHdr.Dim {
			fmt.Printf("   ⚠  Dim mismatch: embeddings.bin=%d  vectors.bin=%d\n",
				embHdr.Dim, vecHdr.Dim)
		} else {
			fmt.Printf("   ✓ Dimensions match (%d)\n", embHdr.Dim)
		}
		if embHdr.ModelName != vecHdr.ModelName {
			fmt.Printf("   ⚠  Model mismatch: embeddings.bin=%q  vectors.bin=%q\n",
				embHdr.ModelName, vecHdr.ModelName)
		} else {
			fmt.Printf("   ✓ Model names match (%s)\n", embHdr.ModelName)
		}
	}

	if kb != nil && embErr == nil {
		kbFuncs := len(kb.Indices.FunctionsByName)
		embCount := int(embHdr.Count)
		ratio := float64(embCount) / float64(max(kbFuncs, 1))
		if ratio < 0.1 || ratio > 100 {
			fmt.Printf("   ⚠  Unusual ratio: %d embeddings vs %d indexed functions\n",
				embCount, kbFuncs)
		} else {
			fmt.Printf("   ✓ Embedding count (%d) looks reasonable relative to %d indexed functions\n",
				embCount, kbFuncs)
		}
	}

	// 7. File sizes
	fmt.Println("\n7. File sizes:")
	files := []string{"kb.json", "kb_call_graph.json", "kb_index.json", "embeddings.bin", "vectors.bin"}
	for _, file := range files {
		path := filepath.Join(corvuxDir, file)
		if info, serr := os.Stat(path); serr == nil {
			sizeMB := float64(info.Size()) / (1024 * 1024)
			fmt.Printf("   %-20s %.2f MB\n", file, sizeMB)
		} else {
			fmt.Printf("   %-20s NOT FOUND\n", file)
		}
	}

	fmt.Println("\n✓ Diagnostic complete!")
	return nil
}

//  Loaders

func loadKB(path string) (*KBFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var kb KBFile
	if err := json.Unmarshal(data, &kb); err != nil {
		return nil, err
	}
	return &kb, nil
}

func checkCallGraph(path string) (nodes, edges int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	var cg CallGraph
	if err := json.Unmarshal(data, &cg); err != nil {
		return 0, 0, err
	}
	return len(cg.Nodes), len(cg.Edges), nil
}

func checkIndex(path string) (int, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	var index Indices
	if err := json.Unmarshal(data, &index); err != nil {
		return 0, 0, err
	}
	return len(index.FunctionsByName), len(index.TypesByName), nil
}

//  Helpers

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
