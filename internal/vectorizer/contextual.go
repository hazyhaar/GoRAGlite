// Package vectorizer provides code vectorization algorithms.
// contextual.go - Contextual layer: call graph and dependency relationships.
package vectorizer

import (
	"hash/fnv"
	"math"
	"sort"
	"strings"

	"github.com/hazylab/goraglite/internal/chunker"
)

// ContextualVectorizer creates vectors from code relationships.
// It captures which functions call each other, shared imports, and co-occurrence patterns.
type ContextualVectorizer struct {
	Dims int // Vector dimensions (default: 128)

	// Corpus-level data (built during indexing)
	CallGraph      map[string][]string // caller -> callees
	ReverseGraph   map[string][]string // callee -> callers
	ImportGraph    map[string][]string // chunk -> imports
	CoOccurrence   map[string]int      // "funcA|funcB" -> count (same file)
	FunctionToFile map[string]string   // function name -> file path
	ChunkCount     int
}

// NewContextualVectorizer creates a new contextual vectorizer.
func NewContextualVectorizer(dims int) *ContextualVectorizer {
	if dims <= 0 {
		dims = 128
	}
	return &ContextualVectorizer{
		Dims:           dims,
		CallGraph:      make(map[string][]string),
		ReverseGraph:   make(map[string][]string),
		ImportGraph:    make(map[string][]string),
		CoOccurrence:   make(map[string]int),
		FunctionToFile: make(map[string]string),
	}
}

// BuildCorpus builds the corpus-level graphs from all chunks.
// Must be called before vectorization.
func (v *ContextualVectorizer) BuildCorpus(chunks []*chunker.Chunk) {
	v.ChunkCount = len(chunks)

	// Group chunks by file for co-occurrence
	fileChunks := make(map[string][]*chunker.Chunk)

	for _, chunk := range chunks {
		name := chunk.Name
		if name == "" {
			continue
		}

		// Build call graph
		v.CallGraph[name] = chunk.Calls
		for _, callee := range chunk.Calls {
			v.ReverseGraph[callee] = append(v.ReverseGraph[callee], name)
		}

		// Build import graph
		v.ImportGraph[name] = chunk.Imports

		// Track file location
		v.FunctionToFile[name] = chunk.FilePath

		// Group by file
		fileChunks[chunk.FilePath] = append(fileChunks[chunk.FilePath], chunk)
	}

	// Build co-occurrence (functions in same file)
	for _, chunks := range fileChunks {
		for i := 0; i < len(chunks); i++ {
			for j := i + 1; j < len(chunks); j++ {
				key := coOccurrenceKey(chunks[i].Name, chunks[j].Name)
				v.CoOccurrence[key]++
			}
		}
	}
}

// Vectorize creates a contextual vector for a chunk.
func (v *ContextualVectorizer) Vectorize(chunk *chunker.Chunk) []float32 {
	vec := make([]float32, v.Dims)

	if chunk.Name == "" {
		return vec
	}

	// Feature Group 1: Outgoing calls (who this function calls)
	v.addOutgoingCallFeatures(vec, chunk, 0, v.Dims/4)

	// Feature Group 2: Incoming calls (who calls this function)
	v.addIncomingCallFeatures(vec, chunk, v.Dims/4, v.Dims/4)

	// Feature Group 3: Import patterns
	v.addImportFeatures(vec, chunk, v.Dims/2, v.Dims/4)

	// Feature Group 4: Centrality and graph metrics
	v.addGraphMetrics(vec, chunk, 3*v.Dims/4, v.Dims/4)

	// Normalize
	normalizeVec(vec)

	return vec
}

// addOutgoingCallFeatures encodes what this chunk calls.
func (v *ContextualVectorizer) addOutgoingCallFeatures(vec []float32, chunk *chunker.Chunk, offset, dims int) {
	calls := chunk.Calls
	if len(calls) == 0 {
		return
	}

	// Direct calls
	uniqueCalls := make(map[string]int)
	for _, call := range calls {
		uniqueCalls[call]++
	}

	for call, count := range uniqueCalls {
		idx := offset + (hashString(call) % (dims / 2))
		vec[idx] += float32(count)
	}

	// Call categories (stdlib, internal, external)
	var stdlibCalls, internalCalls, externalCalls int
	for call := range uniqueCalls {
		if isStdlibCall(call) {
			stdlibCalls++
		} else if v.isInternalCall(call) {
			internalCalls++
		} else {
			externalCalls++
		}
	}

	catOffset := offset + dims/2
	vec[catOffset+0] = sigmoidFloat(float32(stdlibCalls) / 5.0)
	vec[catOffset+1] = sigmoidFloat(float32(internalCalls) / 5.0)
	vec[catOffset+2] = sigmoidFloat(float32(externalCalls) / 5.0)
	vec[catOffset+3] = sigmoidFloat(float32(len(uniqueCalls)) / 10.0)

	// Call diversity
	if len(calls) > 0 {
		vec[catOffset+4] = float32(len(uniqueCalls)) / float32(len(calls))
	}
}

// addIncomingCallFeatures encodes who calls this chunk.
func (v *ContextualVectorizer) addIncomingCallFeatures(vec []float32, chunk *chunker.Chunk, offset, dims int) {
	callers := v.ReverseGraph[chunk.Name]
	if len(callers) == 0 {
		// Also check with receiver prefix for methods
		if chunk.Type == "method" && len(chunk.Fields) > 0 {
			methodName := chunk.Fields[0] + "." + chunk.Name
			callers = v.ReverseGraph[methodName]
		}
	}

	if len(callers) == 0 {
		return
	}

	// Hash callers
	for _, caller := range callers {
		idx := offset + (hashString(caller) % (dims / 2))
		vec[idx] += 1.0
	}

	// Caller metrics
	metricOffset := offset + dims/2
	vec[metricOffset+0] = sigmoidFloat(float32(len(callers)) / 10.0) // In-degree
	vec[metricOffset+1] = float32(len(callers)) / float32(max(v.ChunkCount, 1))

	// Caller diversity (unique files)
	callerFiles := make(map[string]bool)
	for _, caller := range callers {
		if file, ok := v.FunctionToFile[caller]; ok {
			callerFiles[file] = true
		}
	}
	vec[metricOffset+2] = sigmoidFloat(float32(len(callerFiles)) / 5.0)
}

// addImportFeatures encodes import patterns.
func (v *ContextualVectorizer) addImportFeatures(vec []float32, chunk *chunker.Chunk, offset, dims int) {
	imports := chunk.Imports
	if len(imports) == 0 {
		return
	}

	// Categorize imports
	categories := map[string]int{
		"net":     0,
		"http":    0,
		"io":      0,
		"os":      0,
		"fmt":     0,
		"strings": 0,
		"sync":    0,
		"context": 0,
		"json":    0,
		"sql":     0,
		"testing": 0,
		"crypto":  0,
	}

	for _, imp := range imports {
		for cat := range categories {
			if strings.Contains(imp, cat) {
				categories[cat]++
			}
		}
	}

	i := 0
	for cat, count := range categories {
		if i >= dims/2 {
			break
		}
		idx := offset + (hashString("import:"+cat) % (dims / 2))
		vec[idx] += float32(count)
		i++
	}

	// Import metrics
	metricOffset := offset + dims/2
	vec[metricOffset+0] = sigmoidFloat(float32(len(imports)) / 10.0)

	// Stdlib vs external ratio
	var stdlibImports int
	for _, imp := range imports {
		if !strings.Contains(imp, ".") {
			stdlibImports++
		}
	}
	if len(imports) > 0 {
		vec[metricOffset+1] = float32(stdlibImports) / float32(len(imports))
	}
}

// addGraphMetrics adds graph-theoretic features.
func (v *ContextualVectorizer) addGraphMetrics(vec []float32, chunk *chunker.Chunk, offset, dims int) {
	name := chunk.Name

	// Out-degree (number of calls)
	outDegree := len(v.CallGraph[name])
	vec[offset+0] = sigmoidFloat(float32(outDegree) / 10.0)

	// In-degree (number of callers)
	inDegree := len(v.ReverseGraph[name])
	vec[offset+1] = sigmoidFloat(float32(inDegree) / 10.0)

	// Degree ratio (in/out balance)
	if outDegree > 0 {
		vec[offset+2] = float32(inDegree) / float32(inDegree+outDegree)
	}

	// Hub score (calls many important functions)
	hubScore := 0.0
	for _, callee := range v.CallGraph[name] {
		hubScore += float64(len(v.ReverseGraph[callee])) // Importance of callees
	}
	vec[offset+3] = sigmoidFloat(float32(hubScore) / 20.0)

	// Authority score (called by many important functions)
	authScore := 0.0
	for _, caller := range v.ReverseGraph[name] {
		authScore += float64(len(v.CallGraph[caller])) // Importance of callers
	}
	vec[offset+4] = sigmoidFloat(float32(authScore) / 20.0)

	// Co-occurrence features (functions in same file)
	var coOccurCount int
	var coOccurStrength float64
	for key, count := range v.CoOccurrence {
		if strings.Contains(key, name+"|") || strings.Contains(key, "|"+name) {
			coOccurCount++
			coOccurStrength += float64(count)
		}
	}
	vec[offset+5] = sigmoidFloat(float32(coOccurCount) / 10.0)
	vec[offset+6] = sigmoidFloat(float32(coOccurStrength) / 5.0)

	// Transitivity (do my callees call each other?)
	transitivity := v.computeTransitivity(name)
	vec[offset+7] = transitivity

	// Is leaf (no outgoing calls)
	if outDegree == 0 {
		vec[offset+8] = 1.0
	}

	// Is root (no incoming calls)
	if inDegree == 0 {
		vec[offset+9] = 1.0
	}

	// Neighborhood hash (signature of local graph structure)
	neighborhoodHash := v.computeNeighborhoodHash(name)
	vec[offset+10] = float32(neighborhoodHash%1000) / 1000.0
}

// computeTransitivity calculates local clustering coefficient.
func (v *ContextualVectorizer) computeTransitivity(name string) float32 {
	callees := v.CallGraph[name]
	if len(callees) < 2 {
		return 0
	}

	// Count edges between callees
	edges := 0
	for i := 0; i < len(callees); i++ {
		for j := i + 1; j < len(callees); j++ {
			// Check if callee i calls callee j or vice versa
			for _, c := range v.CallGraph[callees[i]] {
				if c == callees[j] {
					edges++
					break
				}
			}
			for _, c := range v.CallGraph[callees[j]] {
				if c == callees[i] {
					edges++
					break
				}
			}
		}
	}

	possibleEdges := len(callees) * (len(callees) - 1)
	if possibleEdges == 0 {
		return 0
	}
	return float32(edges) / float32(possibleEdges)
}

// computeNeighborhoodHash creates a hash of the local graph structure.
func (v *ContextualVectorizer) computeNeighborhoodHash(name string) int {
	// Combine hashes of callees and callers
	h := fnv.New32a()
	h.Write([]byte(name))

	callees := v.CallGraph[name]
	sort.Strings(callees)
	for _, c := range callees {
		h.Write([]byte(c))
	}

	callers := v.ReverseGraph[name]
	sort.Strings(callers)
	for _, c := range callers {
		h.Write([]byte(c))
	}

	return int(h.Sum32())
}

// isInternalCall checks if a call is to an internal function.
func (v *ContextualVectorizer) isInternalCall(call string) bool {
	_, exists := v.CallGraph[call]
	return exists
}

// Helper functions

func coOccurrenceKey(a, b string) string {
	if a < b {
		return a + "|" + b
	}
	return b + "|" + a
}

func hashString(s string) int {
	h := fnv.New32a()
	h.Write([]byte(s))
	return int(h.Sum32())
}

func isStdlibCall(call string) bool {
	stdlibPrefixes := []string{
		"fmt.", "strings.", "strconv.", "bytes.", "io.", "os.", "path.",
		"sync.", "context.", "time.", "math.", "sort.", "json.", "xml.",
		"http.", "net.", "sql.", "log.", "errors.", "regexp.", "reflect.",
		"runtime.", "testing.", "crypto.", "encoding.", "bufio.", "flag.",
	}
	for _, prefix := range stdlibPrefixes {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

func sigmoidFloat(x float32) float32 {
	return float32(1.0 / (1.0 + math.Exp(-float64(x))))
}

func normalizeVec(vec []float32) {
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
