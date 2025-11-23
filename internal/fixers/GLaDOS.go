package fixers

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GLaDOS checks for knowledge base outputs and checks for embeddings size and other errors
func GLaDOS(eulixDir string) error {
	// If no directory specified, default to .eulix
	if eulixDir == "" {
		eulixDir = ".eulix"
	}

	// Check if directory exists
	if _, err := os.Stat(eulixDir); os.IsNotExist(err) {
		fmt.Printf("❌ Directory not found: %s\n", eulixDir)
		fmt.Println("\nMake sure you've run 'eulix analyze' first to generate the knowledge base.")
		return fmt.Errorf("directory not found: %s", eulixDir)
	}

	fmt.Println("🔍 KB Diagnostic Tool")
	fmt.Println("================================")
	fmt.Printf("Analyzing: %s\n\n", eulixDir)

	// 1. Check kb.json (codebase structure)
	fmt.Println("1. Checking kb.json (codebase structure)...")
	kbPath := filepath.Join(eulixDir, "kb.json")
	kb, err := loadKB(kbPath)
	if err != nil {
		fmt.Printf("❌ Failed to load kb.json: %v\n", err)
	} else {
		fmt.Printf("✅ Loaded KB for project: %s\n", kb.Metadata.ProjectName)
		fmt.Printf("   Languages: %v\n", kb.Metadata.Languages)
		fmt.Printf("   Total files: %d\n", kb.Metadata.TotalFiles)
		fmt.Printf("   Total LOC: %d\n", kb.Metadata.TotalLOC)
		fmt.Printf("   Functions: %d, Classes: %d, Methods: %d\n",
			kb.Metadata.TotalFunctions, kb.Metadata.TotalClasses, kb.Metadata.TotalMethods)
		fmt.Printf("   Entry points: %d\n", len(kb.EntryPoints))
		fmt.Printf("   External dependencies: %d\n", len(kb.ExternalDeps))

		// Show indices
		fmt.Printf("   Index stats:\n")
		fmt.Printf("     - Functions indexed: %d\n", len(kb.Indices.FunctionsByName))
		fmt.Printf("     - Types indexed: %d\n", len(kb.Indices.TypesByName))
		fmt.Printf("     - Call graph nodes: %d\n", len(kb.CallGraph.Nodes))
		fmt.Printf("     - Call graph edges: %d\n", len(kb.CallGraph.Edges))
	}

	// 2. Check embeddings.json
	fmt.Println("\n2. Checking embeddings.json...")
	embJsonPath := filepath.Join(eulixDir, "embeddings.json")
	embFile, chunks, err := loadEmbeddingsJSON(embJsonPath)
	if err != nil {
		fmt.Printf("❌ Failed to load embeddings.json: %v\n", err)
	} else {
		fmt.Printf("✅ Loaded embeddings file\n")
		fmt.Printf("   Model: %s\n", embFile.Model)
		fmt.Printf("   Dimension: %d\n", embFile.Dimension)
		fmt.Printf("   Total chunks: %d\n", embFile.TotalChunks)
		fmt.Printf("   Actual embeddings: %d\n", len(chunks))

		if embFile.TotalChunks != len(chunks) {
			fmt.Printf("   ⚠️  WARNING: total_chunks (%d) != actual count (%d)\n",
				embFile.TotalChunks, len(chunks))
		}

		// Check if embeddings are present
		hasVectors := false
		if len(chunks) > 0 && len(chunks[0].Embedding) > 0 {
			hasVectors = true
			fmt.Printf("   ✅ Embeddings contain %d-dimensional vectors\n", len(chunks[0].Embedding))
		} else {
			fmt.Println("   ⚠️  No embedding vectors found in JSON")
		}

		// Show sample chunks
		fmt.Println("\n   📋 Sample chunks:")
		for i := 0; i < 3 && i < len(chunks); i++ {
			chunk := chunks[i]
			fmt.Printf("\n   Chunk %d:\n", i+1)
			fmt.Printf("     ID: %s\n", chunk.ID)
			fmt.Printf("     Type: %s\n", chunk.ChunkType)
			fmt.Printf("     File: %s (lines %d-%d)\n",
				chunk.Metadata.FilePath, chunk.Metadata.LineStart, chunk.Metadata.LineEnd)
			fmt.Printf("     Name: %s (complexity: %d)\n",
				chunk.Metadata.Name, chunk.Metadata.Complexity)
			fmt.Printf("     Content: %s...\n", truncate(chunk.Content, 80))
			if hasVectors {
				fmt.Printf("     Vector: [%.3f, %.3f, ...] (%d dims)\n",
					chunk.Embedding[0], chunk.Embedding[1], len(chunk.Embedding))
			}
		}

		// 3. Analyze chunk types
		fmt.Println("\n3. Chunk type distribution:")
		typeCount := make(map[string]int)
		for _, chunk := range chunks {
			typeCount[chunk.ChunkType]++
		}
		for chunkType, count := range typeCount {
			fmt.Printf("   %s: %d (%.1f%%)\n",
				chunkType, count, float64(count)/float64(len(chunks))*100)
		}

		// 4. Check for empty content
		fmt.Println("\n4. Data quality checks:")
		emptyCount := 0
		shortCount := 0
		for _, chunk := range chunks {
			if len(chunk.Content) == 0 {
				emptyCount++
			} else if len(chunk.Content) < 50 {
				shortCount++
			}
		}
		if emptyCount > 0 {
			fmt.Printf("   ⚠️  %d chunks with empty content\n", emptyCount)
		} else {
			fmt.Println("   ✅ No empty chunks")
		}
		if shortCount > 0 {
			fmt.Printf("   ⚠️  %d chunks with very short content (<50 chars)\n", shortCount)
		}

		// 5. Test symbol search
		fmt.Println("\n5. Testing symbol search...")
		fmt.Println("\n5. its fine if all are not found its just a test")
		testSymbols := []string{"main", "DownloadManager", "init", "setup", "handle", "auth"}
		for _, symbol := range testSymbols {
			found := findChunksWithSymbol(chunks, symbol)
			if len(found) > 0 {
				fmt.Printf("   ✅ '%s' found in %d chunk(s)\n", symbol, len(found))
			} else {
				fmt.Printf("   ❌ '%s' not found\n", symbol)
			}
		}
	}

	// 6. Check embeddings.bin
	fmt.Println("\n6. Checking embeddings.bin...")
	embBinPath := filepath.Join(eulixDir, "embeddings.bin")
	numEmb, dim, err := checkEmbeddingsBin(embBinPath)
	if err != nil {
		fmt.Printf("❌ Failed to load embeddings.bin: %v\n", err)
	} else {
		fmt.Printf("✅ Loaded binary embeddings\n")
		fmt.Printf("   Count: %d embeddings\n", numEmb)
		fmt.Printf("   Dimension: %d\n", dim)

		// Compare with JSON
		if len(chunks) > 0 {
			if numEmb != len(chunks) {
				fmt.Printf("   ⚠️  WARNING: Binary has %d but JSON has %d embeddings\n",
					numEmb, len(chunks))
			} else {
				fmt.Println("   ✅ Binary count matches JSON count")
			}

			if embFile != nil && dim != embFile.Dimension {
				fmt.Printf("   ⚠️  WARNING: Binary dim (%d) != JSON dim (%d)\n",
					dim, embFile.Dimension)
			} else {
				fmt.Println("   ✅ Dimensions match")
			}
		}
	}

	// 7. Check kb_index.json
	fmt.Println("\n7. Checking kb_index.json...")
	indexPath := filepath.Join(eulixDir, "kb_index.json")
	funcCount, typeCount, err := checkIndex(indexPath)
	if err != nil {
		fmt.Printf("❌ Failed to load kb_index.json: %v\n", err)
	} else {
		fmt.Printf("✅ Loaded index\n")
		fmt.Printf("   Functions: %d\n", funcCount)
		fmt.Printf("   Types: %d\n", typeCount)
	}

	// 8. File sizes
	fmt.Println("\n8. File sizes:")
	files := []string{"kb.json", "embeddings.json", "embeddings.bin", "kb_index.json", "kb_call_graph.json"}
	for _, file := range files {
		path := filepath.Join(eulixDir, file)
		if info, err := os.Stat(path); err == nil {
			sizeMB := float64(info.Size()) / (1024 * 1024)
			fmt.Printf("   %s: %.2f MB\n", file, sizeMB)
		} else {
			fmt.Printf("   %s: NOT FOUND\n", file)
		}
	}
	fmt.Println("\n✅ Diagnostic complete!")

	return nil
}

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

func loadEmbeddingsJSON(path string) (*EmbeddingsFile, []KBChunk, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	var embFile EmbeddingsFile
	if err := json.Unmarshal(data, &embFile); err != nil {
		return nil, nil, err
	}

	return &embFile, embFile.Embeddings, nil
}

func checkEmbeddingsBin(path string) (int, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}

	if len(data) < 8 {
		return 0, 0, fmt.Errorf("invalid embeddings file: too short")
	}

	numEmbeddings := binary.LittleEndian.Uint32(data[0:4])
	dimension := binary.LittleEndian.Uint32(data[4:8])

	return int(numEmbeddings), int(dimension), nil
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

func findChunksWithSymbol(chunks []KBChunk, symbol string) []KBChunk {
	var found []KBChunk
	symbolLower := strings.ToLower(symbol)

	for _, chunk := range chunks {
		// Check in metadata name
		if strings.Contains(strings.ToLower(chunk.Metadata.Name), symbolLower) {
			found = append(found, chunk)
			continue
		}

		// Check in chunk ID
		if strings.Contains(strings.ToLower(chunk.ID), symbolLower) {
			found = append(found, chunk)
			continue
		}

		// Check in content (slower)
		if strings.Contains(strings.ToLower(chunk.Content), symbolLower) {
			found = append(found, chunk)
		}
	}

	return found
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
