package fixers

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"
)

// AspirineOptions holds configuration for the Aspirine rebuild process
type AspirineOptions struct {
	NoBackup bool
	Force    bool
}

// Aspirine rebuilds kb and embeddings and fixes problems like what always for me
func Aspirine(eulixDir string, opts AspirineOptions) error {
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

	embJsonPath := filepath.Join(eulixDir, "embeddings.json")
	embBinPath := filepath.Join(eulixDir, "embeddings.bin")

	fmt.Println("🔧 Rebuilding embeddings.bin from embeddings.json")
	fmt.Println("==================================================\n")

	// 1. Load embeddings.json
	fmt.Println("1. Loading embeddings.json...")
	data, err := os.ReadFile(embJsonPath)
	if err != nil {
		fmt.Printf("❌ Failed to read embeddings.json: %v\n", err)
		fmt.Println("\n💡 Make sure embeddings.json exists in the .eulix directory")
		return fmt.Errorf("failed to read embeddings.json: %w", err)
	}

	var embFile EmbeddingsFile
	if err := json.Unmarshal(data, &embFile); err != nil {
		fmt.Printf("❌ Failed to parse embeddings.json: %v\n", err)
		fmt.Println("\n💡 The JSON file may be corrupted. Try regenerating it with:")
		fmt.Println("   eulix analyze")
		return fmt.Errorf("failed to parse embeddings.json: %w", err)
	}

	fmt.Printf("✅ Loaded embeddings metadata\n")
	fmt.Printf("   Model: %s\n", embFile.Model)
	fmt.Printf("   Dimension: %d\n", embFile.Dimension)
	fmt.Printf("   Total Chunks: %d\n", embFile.TotalChunks)
	fmt.Printf("   Actual Embeddings: %d\n", len(embFile.Embeddings))

	// 2. Validate embeddings
	fmt.Println("\n2. Validating embeddings...")
	if len(embFile.Embeddings) == 0 {
		fmt.Println("❌ No embeddings found in JSON file")
		fmt.Println("\n💡 Regenerate embeddings with:")
		fmt.Println("   eulix analyze")
		return fmt.Errorf("no embeddings found")
	}

	if embFile.Dimension <= 0 {
		fmt.Println("❌ Invalid dimension in metadata")
		return fmt.Errorf("invalid dimension: %d", embFile.Dimension)
	}

	// Check for dimension mismatches (common issue)
	if embFile.Dimension < 100 {
		fmt.Printf("⚠️  WARNING: Dimension %d seems suspiciously low\n", embFile.Dimension)
		fmt.Println("   Most embedding models use 384, 768, or 1536 dimensions")
		if !opts.Force {
			fmt.Println("   Use --force flag to continue anyway")
			return fmt.Errorf("dimension too low: %d", embFile.Dimension)
		}
	}

	// Check total chunks mismatch
	if embFile.TotalChunks != len(embFile.Embeddings) {
		fmt.Printf("⚠️  WARNING: Metadata says %d chunks but found %d embeddings\n",
			embFile.TotalChunks, len(embFile.Embeddings))
		if !opts.Force {
			fmt.Println("   Use --force flag to continue anyway")
			return fmt.Errorf("chunk count mismatch")
		}
	}

	// Check each embedding
	invalidCount := 0
	wrongDimCount := 0
	var firstInvalid string

	for i, chunk := range embFile.Embeddings {
		if len(chunk.Embedding) == 0 {
			if invalidCount == 0 {
				firstInvalid = chunk.ID
			}
			invalidCount++
		} else if len(chunk.Embedding) != embFile.Dimension {
			if wrongDimCount == 0 {
				fmt.Printf("⚠️  Chunk %d (%s) has wrong dimension: %d (expected %d)\n",
					i, chunk.ID, len(chunk.Embedding), embFile.Dimension)
			}
			wrongDimCount++
		}
	}

	if invalidCount > 0 {
		fmt.Printf("❌ Found %d embeddings with no vectors (first: %s)\n",
			invalidCount, firstInvalid)
		fmt.Println("\n💡 The embeddings file is incomplete. Regenerate it with:")
		fmt.Println("   eulix analyze")
		return fmt.Errorf("found %d invalid embeddings", invalidCount)
	}

	if wrongDimCount > 0 {
		fmt.Printf("❌ Found %d embeddings with wrong dimensions\n", wrongDimCount)
		if !opts.Force {
			fmt.Println("   Use --force flag to continue anyway")
			return fmt.Errorf("found %d embeddings with wrong dimensions", wrongDimCount)
		}
	}

	fmt.Printf("✅ All %d embeddings are valid (%d dimensions)\n",
		len(embFile.Embeddings), embFile.Dimension)

	// 3. Backup old embeddings.bin
	if !opts.NoBackup {
		fmt.Println("\n3. Backing up old embeddings.bin...")
		if _, err := os.Stat(embBinPath); err == nil {
			timestamp := fmt.Sprintf("%d", os.Getpid())
			backupPath := fmt.Sprintf("%s.backup.%s", embBinPath, timestamp)
			if err := os.Rename(embBinPath, backupPath); err != nil {
				fmt.Printf("⚠️  Failed to backup: %v\n", err)
			} else {
				fmt.Printf("✅ Backed up to: %s\n", backupPath)
			}
		} else {
			fmt.Println("ℹ️  No existing embeddings.bin found (will create new)")
		}
	} else {
		fmt.Println("\n3. Skipping backup (--no-backup flag set)")
	}

	// 4. Write new embeddings.bin
	fmt.Println("\n4. Writing new embeddings.bin...")
	file, err := os.Create(embBinPath)
	if err != nil {
		fmt.Printf("❌ Failed to create file: %v\n", err)
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Write header: num_embeddings (uint32) + dimension (uint32)
	numEmbeddings := uint32(len(embFile.Embeddings))
	dimension := uint32(embFile.Dimension)

	headerBuf := make([]byte, 8)
	binary.LittleEndian.PutUint32(headerBuf[0:4], numEmbeddings)
	binary.LittleEndian.PutUint32(headerBuf[4:8], dimension)

	if _, err := file.Write(headerBuf); err != nil {
		fmt.Printf("❌ Failed to write header: %v\n", err)
		return fmt.Errorf("failed to write header: %w", err)
	}

	fmt.Printf("✅ Wrote header: %d embeddings × %d dimensions\n",
		numEmbeddings, dimension)

	// Write each embedding vector
	vectorBuf := make([]byte, 4) // float32 = 4 bytes
	totalFloats := 0

	fmt.Println("   Writing vectors...")
	for i, chunk := range embFile.Embeddings {
		for _, val := range chunk.Embedding {
			binary.LittleEndian.PutUint32(vectorBuf, floatToUint32(val))
			if _, err := file.Write(vectorBuf); err != nil {
				fmt.Printf("❌ Failed to write embedding %d: %v\n", i, err)
				return fmt.Errorf("failed to write embedding %d: %w", i, err)
			}
			totalFloats++
		}

		// Progress indicator
		if (i+1)%100 == 0 || i == len(embFile.Embeddings)-1 {
			percent := float64(i+1) / float64(numEmbeddings) * 100
			fmt.Printf("   Progress: %d/%d (%.1f%%)\n", i+1, numEmbeddings, percent)
		}
	}

	fmt.Printf("✅ Wrote %d total floats (%d embeddings × %d dims)\n",
		totalFloats, numEmbeddings, dimension)

	// 5. Verify the new file
	fmt.Println("\n5. Verifying new embeddings.bin...")
	info, err := os.Stat(embBinPath)
	if err != nil {
		fmt.Printf("❌ Failed to stat file: %v\n", err)
		return fmt.Errorf("failed to stat file: %w", err)
	}

	expectedSize := 8 + (int(numEmbeddings) * int(dimension) * 4) // header + floats
	actualSize := int(info.Size())

	fmt.Printf("   Expected size: %d bytes (%.2f MB)\n",
		expectedSize, float64(expectedSize)/(1024*1024))
	fmt.Printf("   Actual size: %d bytes (%.2f MB)\n",
		actualSize, float64(actualSize)/(1024*1024))

	if actualSize != expectedSize {
		fmt.Printf("❌ Size mismatch! File may be corrupted.\n")
		fmt.Printf("   Difference: %d bytes\n", actualSize-expectedSize)
		return fmt.Errorf("size mismatch: expected %d, got %d", expectedSize, actualSize)
	}

	// Read back and verify header
	verifyData, err := os.ReadFile(embBinPath)
	if err != nil {
		fmt.Printf("❌ Failed to read back file: %v\n", err)
		return fmt.Errorf("failed to verify file: %w", err)
	}

	verifyNumEmb := binary.LittleEndian.Uint32(verifyData[0:4])
	verifyDim := binary.LittleEndian.Uint32(verifyData[4:8])

	if verifyNumEmb != numEmbeddings || verifyDim != dimension {
		fmt.Printf("❌ Header verification failed!\n")
		fmt.Printf("   Expected: %d × %d\n", numEmbeddings, dimension)
		fmt.Printf("   Got: %d × %d\n", verifyNumEmb, verifyDim)
		return fmt.Errorf("header verification failed")
	}

	fmt.Printf("✅ Header verified: %d embeddings × %d dimensions\n",
		verifyNumEmb, verifyDim)

	// 6. Summary
	sizeMB := float64(actualSize) / (1024 * 1024)
	fmt.Printf("\n════════════════════════════════════════\n")
	fmt.Printf("✅ Successfully rebuilt embeddings.bin!\n")
	fmt.Printf("════════════════════════════════════════\n")
	fmt.Printf("Location:   %s\n", embBinPath)
	fmt.Printf("Size:       %.2f MB\n", sizeMB)
	fmt.Printf("Format:     %d embeddings × %d dimensions\n", numEmbeddings, dimension)
	fmt.Printf("Model:      %s\n", embFile.Model)
	fmt.Printf("════════════════════════════════════════\n")
	fmt.Println("\n🎉 Your embeddings.bin is ready! Run 'eulix chat' to use it.")

	return nil
}

// Convert float32 to uint32 bits (for binary.LittleEndian.PutUint32)
func floatToUint32(f float32) uint32 {
	return *(*uint32)(unsafe.Pointer(&f))
}
