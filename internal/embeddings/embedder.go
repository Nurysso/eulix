package embeddings

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	// "unsafe"
)

// Embedder wraps the Rust eulix_embed binary for embedding generation
// This ensures consistent embeddings with the KB generation pipeline
type Embedder struct {
	binaryPath string
	model      string
	backend    string
	dimension  int
}

// QueryEmbedder is an alias for Embedder to maintain compatibility
type QueryEmbedder = Embedder

// QueryEmbeddingResult represents the JSON output from eulix_embed
type QueryEmbeddingResult struct {
	Query     string    `json:"query"`
	Model     string    `json:"model"`
	Dimension int       `json:"dimension"`
	Embedding []float32 `json:"embedding"`
}

// NewEmbedder creates a new embedder backed by the Rust eulix_embed binary
// Parameters:
//   - modelName: HuggingFace model name (e.g., "BAAI/bge-small-en-v1.5")
//   - backend: execution backend ("cpu", "cuda", "auto") - currently only used for reference
//   - dimension: expected embedding dimension
func NewEmbedder(modelName, backend string, dimension int) (*Embedder, error) {
	// Try to find eulix_embed binary in common locations
	binaryPath, err := findEulixBinary()
	if err != nil {
		return nil, fmt.Errorf("eulix_embed binary not found: %w\n\n",err)
	}

	// Test the binary works
	cmd := exec.Command(binaryPath, "--version")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("eulix_embed binary test failed, --version didn't worked :/, try running eulix_embed manually ): %w", err)
	}

	return &Embedder{
		binaryPath: binaryPath,
		model:      modelName,
		backend:    backend,
		dimension:  dimension,
	}, nil
}

// VectorWeaver creates a new query embedder
func VectorWeaver(binaryPath, model string) *Embedder {
	return &Embedder{
		binaryPath: binaryPath,
		model:      model,
		dimension:  384, // Default dimension, will be updated from response
	}
}

// Embed generates an embedding vector for the given text using the Rust binary
// This is the primary method for generating embeddings
func (e *Embedder) Embed(text string) ([]float32, error) {
	return e.EmbedQueryBinary(text)
}

// EmbedQuery generates an embedding using JSON output (for debugging)
func (e *Embedder) EmbedQuery(query string) ([]float32, error) {
	cmd := exec.Command(
		e.binaryPath,
		"query",
		"-q", query,
		"-m", e.model,
		"-f", "json",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("eulix_embed failed: %w\nstderr: %s", err, stderr.String())
	}

	var result QueryEmbeddingResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("failed to parse embedding result: %w", err)
	}

	// Update dimension if needed
	if e.dimension != result.Dimension {
		e.dimension = result.Dimension
	}

	return result.Embedding, nil
}

// EmbedQueryBinary generates an embedding using binary output (faster, recommended)
func (e *Embedder) EmbedQueryBinary(query string) ([]float32, error) {
	cmd := exec.Command(
		e.binaryPath,
		"query",
		"-q", query,
		"-m", e.model,
		"-f", "binary",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("eulix_embed failed: %w\nstderr: %s", err, stderr.String())
	}

	data := stdout.Bytes()
	if len(data) < 4 {
		return nil, fmt.Errorf("invalid binary output: too short")
	}

	// Read dimension (first 4 bytes, little-endian)
	dimension := binary.LittleEndian.Uint32(data[0:4])

	expectedSize := 4 + int(dimension)*4
	if len(data) != expectedSize {
		return nil, fmt.Errorf("invalid binary output: expected %d bytes, got %d", expectedSize, len(data))
	}

	// Update dimension if needed
	if e.dimension != int(dimension) {
		e.dimension = int(dimension)
	}

	// Read float32 values (little-endian)
	embedding := make([]float32, dimension)
	offset := 4
	for i := 0; i < int(dimension); i++ {
		bits := binary.LittleEndian.Uint32(data[offset : offset+4])
		embedding[i] = math.Float32frombits(bits)
		offset += 4
	}

	return embedding, nil
}

// GetDimension returns the embedding dimension
func (e *Embedder) GetDimension() int {
	return e.dimension
}

// GetModel returns the model name
func (e *Embedder) GetModel() string {
	return e.model
}

// Close cleans up resources (no-op for command-based embedder)
func (e *Embedder) Close() error {
	return nil
}

// Helper function to find the eulix_embed binary
func findEulixBinary() (string, error) {
	// Common locations to search
	locations := []string{
		"./eulix_embed",
		"./target/release/eulix_embed",
		"../eulix_embed",
		"../target/release/eulix_embed",
		"../../eulix_embed",
		"../../target/release/eulix_embed",
		"/usr/local/bin/eulix_embed",
		"eulix_embed", // In PATH
	}

	for _, path := range locations {
		if _, err := exec.LookPath(path); err == nil {
			return path, nil
		}
	}

	// Try using 'which' or 'where' command
	var cmd *exec.Cmd
	cmd = exec.Command("which", "eulix_embed")

	if output, err := cmd.Output(); err == nil && len(output) > 0 {
		return string(bytes.TrimSpace(output)), nil
	}

	return "", fmt.Errorf("eulix_embed binary not found in any common location")
}

// CosineSimilarity calculates cosine similarity between two vectors
// Both vectors should be normalized (L2 norm = 1)
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0.0
	}

	var dotProduct float64
	for i := range a {
		dotProduct += float64(a[i] * b[i])
	}

	return dotProduct
}

// NormalizeVector performs L2 normalization on a vector
// Returns a new normalized vector
func NormalizeVector(vec []float32) []float32 {
	var norm float32
	for _, v := range vec {
		norm += v * v
	}
	norm = float32(math.Sqrt(float64(norm)))

	if norm == 0 {
		return vec
	}

	normalized := make([]float32, len(vec))
	for i, v := range vec {
		normalized[i] = v / norm
	}

	return normalized
}

// BatchEmbed generates embeddings for multiple texts
// Returns a slice of embeddings in the same order as input texts
func (e *Embedder) BatchEmbed(texts []string) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))

	for i, text := range texts {
		emb, err := e.Embed(text)
		if err != nil {
			return nil, fmt.Errorf("failed to embed text %d: %w", i, err)
		}
		embeddings[i] = emb
	}

	return embeddings, nil
}

// VerifyConsistency checks if the embedder produces consistent results
// Useful for testing and validation
func (e *Embedder) VerifyConsistency(testText string) error {
	// Generate embedding twice
	emb1, err := e.Embed(testText)
	if err != nil {
		return fmt.Errorf("first embedding failed: %w", err)
	}

	emb2, err := e.Embed(testText)
	if err != nil {
		return fmt.Errorf("second embedding failed: %w", err)
	}

	// Check if dimensions match
	if len(emb1) != len(emb2) {
		return fmt.Errorf("dimension mismatch: %d vs %d", len(emb1), len(emb2))
	}

	// Check if embeddings are identical (should be deterministic)
	for i := range emb1 {
		if math.Abs(float64(emb1[i]-emb2[i])) > 1e-6 {
			return fmt.Errorf("embeddings not consistent at index %d: %f vs %f", i, emb1[i], emb2[i])
		}
	}

	return nil
}

// GetModelInfo returns information about the model
func (e *Embedder) GetModelInfo() map[string]interface{} {
	return map[string]interface{}{
		"model":      e.model,
		"backend":    e.backend,
		"dimension":  e.dimension,
		"binary":     e.binaryPath,
	}
}
