// GoRAGlite - Code-to-Code RAG Engine
// A pure Go vector search engine for code, powered by SQLite.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hazylab/goraglite/internal/chunker"
	"github.com/hazylab/goraglite/internal/db"
	"github.com/hazylab/goraglite/internal/search"
	"github.com/hazylab/goraglite/internal/vectorizer"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]

	switch cmd {
	case "index":
		if len(os.Args) < 3 {
			fmt.Println("usage: goraglite index <path> [--db=<dbpath>]")
			os.Exit(1)
		}
		path := os.Args[2]
		dbPath := getFlag("--db", "goraglite.db")
		runIndex(path, dbPath)

	case "search":
		if len(os.Args) < 3 {
			fmt.Println("usage: goraglite search <code-or-file> [--db=<dbpath>] [--k=10]")
			os.Exit(1)
		}
		query := os.Args[2]
		dbPath := getFlag("--db", "goraglite.db")
		k := getFlagInt("--k", 10)
		runSearch(query, dbPath, k)

	case "stats":
		dbPath := getFlag("--db", "goraglite.db")
		runStats(dbPath)

	case "similar":
		if len(os.Args) < 3 {
			fmt.Println("usage: goraglite similar <chunk-id> [--db=<dbpath>] [--k=5]")
			os.Exit(1)
		}
		chunkID := getFlagInt64(os.Args[2], 0)
		dbPath := getFlag("--db", "goraglite.db")
		k := getFlagInt("--k", 5)
		runSimilar(chunkID, dbPath, k)

	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`GoRAGlite - Code-to-Code RAG Engine

Usage:
  goraglite index <path>              Index Go code from directory
  goraglite search <code-or-file>     Search for similar code
  goraglite similar <chunk-id>        Find chunks similar to a given chunk
  goraglite stats                     Show database statistics

Options:
  --db=<path>    Database file (default: goraglite.db)
  --k=<n>        Number of results (default: 10)

Examples:
  goraglite index ./myproject
  goraglite search "func (u *User) Validate() error"
  goraglite search ./query.go
  goraglite similar 42`)
}

func runIndex(path string, dbPath string) {
	fmt.Printf("🔍 Indexing Go code from: %s\n", path)
	start := time.Now()

	// Open database
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Create chunker
	goChunker := chunker.NewGoChunker()

	// Create vectorizer
	vecStruct := vectorizer.NewStructureVectorizer(256)

	// Chunk the codebase
	var chunks []*chunker.Chunk
	var chunkerErr error

	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if info.IsDir() {
		chunks, chunkerErr = goChunker.ChunkDir(path)
	} else {
		chunks, chunkerErr = goChunker.ChunkFile(path)
	}

	if chunkerErr != nil {
		fmt.Fprintf(os.Stderr, "error chunking: %v\n", chunkerErr)
		os.Exit(1)
	}

	fmt.Printf("📦 Found %d chunks\n", len(chunks))

	// Index each chunk
	indexed := 0
	for _, chunk := range chunks {
		// Insert chunk
		dbChunk := &db.Chunk{
			FilePath:  chunk.FilePath,
			Language:  chunk.Language,
			ChunkType: chunk.Type,
			Name:      chunk.Name,
			Signature: chunk.Signature,
			Content:   chunk.Content,
			StartLine: chunk.StartLine,
			EndLine:   chunk.EndLine,
			Hash:      chunk.Hash,
		}

		chunkID, err := database.InsertChunk(dbChunk)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: insert chunk: %v\n", err)
			continue
		}

		// Vectorize structure
		vec := vecStruct.Vectorize(chunk)

		// Store vector
		if err := database.InsertVector(chunkID, "structure", vec); err != nil {
			fmt.Fprintf(os.Stderr, "warning: insert vector: %v\n", err)
			continue
		}

		// For now, structure = final (single layer MVP)
		if err := database.InsertVector(chunkID, "final", vec); err != nil {
			fmt.Fprintf(os.Stderr, "warning: insert final vector: %v\n", err)
			continue
		}

		indexed++
		if indexed%100 == 0 {
			fmt.Printf("  indexed %d chunks...\n", indexed)
		}
	}

	elapsed := time.Since(start)
	fmt.Printf("✅ Indexed %d chunks in %v\n", indexed, elapsed.Round(time.Millisecond))
	fmt.Printf("📁 Database: %s\n", dbPath)
}

func runSearch(query string, dbPath string, k int) {
	// Open database
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Check if query is a file
	var queryCode string
	if _, err := os.Stat(query); err == nil {
		content, err := os.ReadFile(query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading file: %v\n", err)
			os.Exit(1)
		}
		queryCode = string(content)
		fmt.Printf("🔍 Searching for code similar to: %s\n", query)
	} else {
		queryCode = query
		fmt.Printf("🔍 Searching for: %s\n", truncate(query, 60))
	}

	// Parse query as Go code
	goChunker := chunker.NewGoChunker()

	// Create a temporary file for parsing
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, "goraglite_query.go")

	// Wrap query if needed
	wrappedCode := queryCode
	if !strings.Contains(queryCode, "package") {
		wrappedCode = "package query\n\n" + queryCode
	}

	if err := os.WriteFile(tmpFile, []byte(wrappedCode), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(tmpFile)

	queryChunks, err := goChunker.ChunkFile(tmpFile)
	if err != nil || len(queryChunks) == 0 {
		fmt.Fprintf(os.Stderr, "error parsing query as Go code: %v\n", err)
		fmt.Println("Hint: query should be valid Go code (function, type, etc.)")
		os.Exit(1)
	}

	// Vectorize first chunk from query
	vecStruct := vectorizer.NewStructureVectorizer(256)
	queryVec := vecStruct.Vectorize(queryChunks[0])

	// Search
	searcher := search.NewSearcher(database)
	results, err := searcher.Search(queryVec, "final", k)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error searching: %v\n", err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return
	}

	// Print results
	fmt.Printf("\n📋 Top %d results:\n\n", len(results))
	for i, r := range results {
		if r.Chunk == nil {
			continue
		}
		fmt.Printf("─────────────────────────────────────────────\n")
		fmt.Printf("#%d  Score: %.4f\n", i+1, r.Score)
		fmt.Printf("    %s:%d-%d\n", r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine)
		fmt.Printf("    [%s] %s\n", r.Chunk.ChunkType, r.Chunk.Name)
		if r.Chunk.Signature != "" {
			fmt.Printf("    %s\n", r.Chunk.Signature)
		}
		fmt.Printf("\n%s\n", indent(truncate(r.Chunk.Content, 500), "    "))
	}
}

func runSimilar(chunkID int64, dbPath string, k int) {
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Get the reference chunk's vector
	vec, err := database.GetVector(chunkID, "final")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: chunk %d not found: %v\n", chunkID, err)
		os.Exit(1)
	}

	// Get chunk info
	chunk, _ := database.GetChunk(chunkID)
	if chunk != nil {
		fmt.Printf("🔍 Finding chunks similar to: [%s] %s\n", chunk.ChunkType, chunk.Name)
	}

	// Search
	searcher := search.NewSearcher(database)
	results, err := searcher.Search(vec, "final", k+1) // +1 to skip self
	if err != nil {
		fmt.Fprintf(os.Stderr, "error searching: %v\n", err)
		os.Exit(1)
	}

	// Print results (skip the query chunk itself)
	fmt.Printf("\n📋 Similar chunks:\n\n")
	count := 0
	for _, r := range results {
		if r.ChunkID == chunkID {
			continue // Skip self
		}
		if r.Chunk == nil {
			continue
		}
		count++
		if count > k {
			break
		}
		fmt.Printf("#%d  Score: %.4f  [%s] %s\n", count, r.Score, r.Chunk.ChunkType, r.Chunk.Name)
		fmt.Printf("    %s:%d\n", r.Chunk.FilePath, r.Chunk.StartLine)
	}
}

func runStats(dbPath string) {
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	chunks, vectors, _ := database.Stats()

	fmt.Printf("📊 GoRAGlite Database Stats\n")
	fmt.Printf("   Database: %s\n", dbPath)
	fmt.Printf("   Chunks:   %d\n", chunks)
	fmt.Printf("   Vectors:  %d\n", vectors)
}

// Helper functions

func getFlag(name, defaultVal string) string {
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, name+"=") {
			return strings.TrimPrefix(arg, name+"=")
		}
	}
	return defaultVal
}

func getFlagInt(name string, defaultVal int) int {
	val := getFlag(name, "")
	if val == "" {
		return defaultVal
	}
	var n int
	fmt.Sscanf(val, "%d", &n)
	return n
}

func getFlagInt64(val string, defaultVal int64) int64 {
	var n int64
	fmt.Sscanf(val, "%d", &n)
	if n == 0 {
		return defaultVal
	}
	return n
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func indent(s string, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
