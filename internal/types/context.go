package types

// ContextChunk represents a piece of code with metadata
type ContextChunk struct {
	File       string
	StartLine  int
	EndLine    int
	Content    string
	Importance float64
}

// ContextWindow represents the full context for a query
type ContextWindow struct {
	Chunks      []ContextChunk
	TotalTokens int
	Sources     []string
}
