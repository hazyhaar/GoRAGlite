// Package vectorizer provides code vectorization algorithms.
package vectorizer

import (
	"hash/fnv"
	"math"
	"strings"

	"github.com/hazylab/goraglite/internal/chunker"
)

// StructureVectorizer creates vectors from AST structure.
// It uses feature hashing (the "hashing trick") to convert AST paths
// into fixed-dimension vectors without needing a vocabulary.
type StructureVectorizer struct {
	Dims     int     // Vector dimensions (default: 256)
	MaxDepth int     // Max AST depth to consider (default: 10)
	Seed     uint32  // Hash seed for reproducibility
	UseIDF   bool    // Weight by inverse document frequency
	idfCache map[string]float32
}

// NewStructureVectorizer creates a new structure vectorizer.
func NewStructureVectorizer(dims int) *StructureVectorizer {
	if dims <= 0 {
		dims = 256
	}
	return &StructureVectorizer{
		Dims:     dims,
		MaxDepth: 10,
		Seed:     42,
		idfCache: make(map[string]float32),
	}
}

// Vectorize converts a chunk's AST structure into a vector.
func (v *StructureVectorizer) Vectorize(chunk *chunker.Chunk) []float32 {
	vec := make([]float32, v.Dims)

	// Feature 1: AST path histogram (main structural fingerprint)
	v.addASTPathFeatures(vec, chunk.ASTNodes)

	// Feature 2: Chunk type encoding
	v.addChunkTypeFeatures(vec, chunk.Type)

	// Feature 3: Structural metrics
	v.addStructuralMetrics(vec, chunk)

	// Feature 4: Call pattern features
	v.addCallFeatures(vec, chunk.Calls)

	// Normalize to unit vector
	normalize(vec)

	return vec
}

// addASTPathFeatures adds hashed AST path features.
// This is the core of structural vectorization.
func (v *StructureVectorizer) addASTPathFeatures(vec []float32, nodes []chunker.ASTNode) {
	// Count path occurrences
	pathCounts := make(map[string]int)
	typeCounts := make(map[string]int)

	for _, node := range nodes {
		if node.Depth <= v.MaxDepth {
			pathCounts[node.Path]++
			typeCounts[node.Type]++
		}
	}

	// Hash paths into vector (feature hashing)
	for path, count := range pathCounts {
		idx := v.hashToIndex(path)
		sign := v.hashToSign(path)
		vec[idx] += sign * float32(count)
	}

	// Add type histogram in separate subspace
	typeOffset := v.Dims / 4 // Use 1/4 of dims for type histogram
	for typ, count := range typeCounts {
		idx := typeOffset + (v.hashToIndexRaw(typ) % (v.Dims / 4))
		vec[idx] += float32(count)
	}
}

// addChunkTypeFeatures encodes the chunk type (function, struct, interface, etc.)
func (v *StructureVectorizer) addChunkTypeFeatures(vec []float32, chunkType string) {
	// Reserve last 16 dims for chunk type one-hot-ish encoding
	typeMap := map[string]int{
		"function":  0,
		"method":    1,
		"struct":    2,
		"interface": 3,
		"type":      4,
		"const":     5,
		"var":       6,
	}

	offset := v.Dims - 16
	if idx, ok := typeMap[chunkType]; ok {
		vec[offset+idx] = 1.0
	}
}

// addStructuralMetrics adds numeric structural features.
func (v *StructureVectorizer) addStructuralMetrics(vec []float32, chunk *chunker.Chunk) {
	// Use dims [Dims-32 : Dims-16] for metrics
	offset := v.Dims - 32

	// Lines of code (normalized)
	loc := float32(chunk.EndLine - chunk.StartLine + 1)
	vec[offset+0] = sigmoid(loc / 50.0) // Normalize around 50 lines

	// AST node count (normalized)
	nodeCount := float32(len(chunk.ASTNodes))
	vec[offset+1] = sigmoid(nodeCount / 100.0)

	// Max depth
	maxDepth := 0
	for _, n := range chunk.ASTNodes {
		if n.Depth > maxDepth {
			maxDepth = n.Depth
		}
	}
	vec[offset+2] = float32(maxDepth) / float32(v.MaxDepth)

	// Number of imports used
	vec[offset+3] = sigmoid(float32(len(chunk.Imports)) / 10.0)

	// Number of fields/receiver
	vec[offset+4] = sigmoid(float32(len(chunk.Fields)) / 5.0)

	// Cyclomatic-like complexity (count of branches)
	branchCount := 0
	for _, n := range chunk.ASTNodes {
		switch n.Type {
		case "IfStmt", "ForStmt", "RangeStmt", "SwitchStmt",
			"TypeSwitchStmt", "SelectStmt", "CaseClause":
			branchCount++
		}
	}
	vec[offset+5] = sigmoid(float32(branchCount) / 10.0)
}

// addCallFeatures adds features based on function calls.
func (v *StructureVectorizer) addCallFeatures(vec []float32, calls []string) {
	// Hash call patterns into a subspace
	callOffset := v.Dims / 2
	callDims := v.Dims / 8

	// Unique calls
	uniqueCalls := make(map[string]bool)
	for _, call := range calls {
		uniqueCalls[call] = true
	}

	// Hash each unique call
	for call := range uniqueCalls {
		idx := callOffset + (v.hashToIndexRaw(call) % callDims)
		vec[idx] += 1.0
	}

	// Add call count feature
	vec[v.Dims-33] = sigmoid(float32(len(calls)) / 20.0)
}

// hashToIndex returns a vector index for a string using FNV hash.
func (v *StructureVectorizer) hashToIndex(s string) int {
	h := fnv.New32a()
	h.Write([]byte{byte(v.Seed), byte(v.Seed >> 8)})
	h.Write([]byte(s))
	return int(h.Sum32()) % (v.Dims / 2) // First half for paths
}

// hashToIndexRaw returns raw index without seed offset.
func (v *StructureVectorizer) hashToIndexRaw(s string) int {
	h := fnv.New32a()
	h.Write([]byte(s))
	return int(h.Sum32())
}

// hashToSign returns +1 or -1 based on hash (for signed feature hashing).
func (v *StructureVectorizer) hashToSign(s string) float32 {
	h := fnv.New32()
	h.Write([]byte(s))
	if h.Sum32()%2 == 0 {
		return 1.0
	}
	return -1.0
}

// VectorizeQuery vectorizes a code query (another chunk).
// For code-to-code RAG, query is also code.
func (v *StructureVectorizer) VectorizeQuery(queryChunk *chunker.Chunk) []float32 {
	// Same as regular vectorization for code-to-code
	return v.Vectorize(queryChunk)
}

// VectorizeSnippet vectorizes a code snippet string.
func (v *StructureVectorizer) VectorizeSnippet(code string, language string) ([]float32, error) {
	if language != "go" {
		return nil, ErrUnsupportedLanguage
	}

	// Parse the snippet
	c := chunker.NewGoChunker()

	// Wrap in minimal valid Go for parsing
	wrapped := code
	if !strings.Contains(code, "package") {
		wrapped = "package query\n" + code
	}

	// Write to temp and parse
	chunks, err := parseSnippet(c, wrapped)
	if err != nil || len(chunks) == 0 {
		// Fallback: create a minimal chunk
		chunk := &chunker.Chunk{
			Language:  "go",
			Type:      "snippet",
			Content:   code,
			StartLine: 1,
			EndLine:   strings.Count(code, "\n") + 1,
		}
		return v.Vectorize(chunk), nil
	}

	// Return vector of first chunk
	return v.Vectorize(chunks[0]), nil
}

// parseSnippet attempts to parse a code snippet.
func parseSnippet(c *chunker.GoChunker, code string) ([]*chunker.Chunk, error) {
	// Create temp file for parsing
	// For now, use a simplified approach
	return nil, nil
}

// Helper functions

func normalize(vec []float32) {
	var norm float32
	for _, v := range vec {
		norm += v * v
	}
	if norm > 0 {
		norm = float32(math.Sqrt(float64(norm)))
		for i := range vec {
			vec[i] /= norm
		}
	}
}

func sigmoid(x float32) float32 {
	return float32(1.0 / (1.0 + math.Exp(-float64(x))))
}

// ErrUnsupportedLanguage is returned for unsupported languages.
var ErrUnsupportedLanguage = &UnsupportedLanguageError{}

type UnsupportedLanguageError struct{}

func (e *UnsupportedLanguageError) Error() string {
	return "unsupported language"
}
