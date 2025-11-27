// GoRAGlite - Code-to-Code RAG Engine
// A pure Go vector search engine for code, powered by SQLite.
// Multi-layer vectorization: Structure + Lexical + Contextual
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
			fmt.Println("usage: goraglite search <code-or-file> [--db=<dbpath>] [--k=10] [--layer=final]")
			os.Exit(1)
		}
		query := os.Args[2]
		dbPath := getFlag("--db", "goraglite.db")
		k := getFlagInt("--k", 10)
		layer := getFlag("--layer", "final")
		runSearch(query, dbPath, k, layer)

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

	case "compare":
		if len(os.Args) < 4 {
			fmt.Println("usage: goraglite compare <chunk-id-1> <chunk-id-2> [--db=<dbpath>]")
			os.Exit(1)
		}
		id1 := getFlagInt64(os.Args[2], 0)
		id2 := getFlagInt64(os.Args[3], 0)
		dbPath := getFlag("--db", "goraglite.db")
		runCompare(id1, id2, dbPath)

	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`GoRAGlite - Code-to-Code RAG Engine (Multi-Layer, Multi-Language)

Usage:
  goraglite index <path>              Index code from directory (Go, SQL, Bash)
  goraglite search <code-or-file>     Search for similar code
  goraglite similar <chunk-id>        Find chunks similar to a given chunk
  goraglite compare <id1> <id2>       Compare two chunks layer by layer
  goraglite stats                     Show database statistics

Options:
  --db=<path>      Database file (default: goraglite.db)
  --k=<n>          Number of results (default: 10)
  --layer=<name>   Layer to search: structure, lexical, contextual, final (default: final)
  --lang=<name>    Language for search query: go, sql, bash (default: auto-detect)

Languages supported:
  go      Go source files (.go)
  sql     SQL files (.sql)
  bash    Shell scripts (.sh, .bash, .zsh, shebang)

Layers:
  structure   AST-based structural similarity (code shape, patterns)
  lexical     Identifier-based similarity (naming, vocabulary, domain)
  contextual  Call graph relationships (who calls whom)
  final       Blended vector (structure 45% + lexical 30% + contextual 25%)

Examples:
  goraglite index ./myproject
  goraglite search "func (u *User) Validate() error {}"
  goraglite search "SELECT * FROM users WHERE id = ?" --lang=sql
  goraglite search ./deploy.sh --layer=structure
  goraglite similar 42
  goraglite compare 1 2`)
}

func runIndex(path string, dbPath string) {
	fmt.Printf("🔍 Indexing code from: %s\n", path)
	start := time.Now()

	// Open database
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Create multi-language chunker
	multiChunker := chunker.NewMultiChunker()

	// Create multi-layer blender
	blender := vectorizer.NewBlender(vectorizer.DefaultBlendConfig())

	// Chunk the codebase
	var chunks []*chunker.Chunk
	var chunkerErr error

	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if info.IsDir() {
		chunks, chunkerErr = multiChunker.ChunkDir(path)
	} else {
		chunks, chunkerErr = multiChunker.ChunkFile(path)
	}

	if chunkerErr != nil {
		fmt.Fprintf(os.Stderr, "error chunking: %v\n", chunkerErr)
		os.Exit(1)
	}

	fmt.Printf("📦 Found %d chunks\n", len(chunks))

	// Build corpus-level features (IDF, call graph, etc.)
	fmt.Printf("📊 Building corpus features (IDF, call graph)...\n")
	blender.BuildCorpus(chunks)

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

		// Vectorize all layers
		layers, finalVec := blender.Vectorize(chunk)

		// Store each layer vector
		for layerName, vec := range layers {
			if err := database.InsertVector(chunkID, layerName, vec); err != nil {
				fmt.Fprintf(os.Stderr, "warning: insert %s vector: %v\n", layerName, err)
			}
		}

		// Store final blended vector
		if err := database.InsertVector(chunkID, "final", finalVec); err != nil {
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
	fmt.Printf("🧬 Layers: structure (256d) + lexical (128d) + contextual (128d) → final (256d)\n")
}

func runSearch(query string, dbPath string, k int, layer string) {
	// Open database
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Detect language from flag or file extension
	lang := getFlag("--lang", "")

	// Check if query is a file
	var queryCode string
	var queryPath string
	if _, err := os.Stat(query); err == nil {
		content, err := os.ReadFile(query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading file: %v\n", err)
			os.Exit(1)
		}
		queryCode = string(content)
		queryPath = query
		if lang == "" {
			lang = chunker.DetectLanguage(query)
		}
		fmt.Printf("🔍 Searching for code similar to: %s (%s)\n", query, lang)
	} else {
		queryCode = query
		fmt.Printf("🔍 Searching for: %s\n", truncate(query, 60))
		// Auto-detect language from content if not specified
		if lang == "" {
			lang = detectLanguageFromContent(queryCode)
		}
		fmt.Printf("   Language: %s\n", lang)
	}

	fmt.Printf("   Layer: %s\n", layer)

	// Parse query based on language
	var queryChunks []*chunker.Chunk

	switch lang {
	case "sql":
		sqlChunker := chunker.NewSQLChunker()
		queryChunks, err = sqlChunker.ChunkContent("query.sql", queryCode)
	case "bash", "sh", "shell":
		bashChunker := chunker.NewBashChunker()
		queryChunks, err = bashChunker.ChunkContent("query.sh", queryCode)
	default:
		// Default to Go
		goChunker := chunker.NewGoChunker()
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

		queryChunks, err = goChunker.ChunkFile(tmpFile)
		if queryPath != "" {
			os.Remove(tmpFile)
		}
	}

	if err != nil || len(queryChunks) == 0 {
		fmt.Fprintf(os.Stderr, "error parsing query as %s code: %v\n", lang, err)
		fmt.Println("Hint: query should be valid code for the detected/specified language")
		os.Exit(1)
	}

	// Create blender and vectorize query
	blender := vectorizer.NewBlender(vectorizer.DefaultBlendConfig())
	layers, finalVec := blender.Vectorize(queryChunks[0])

	// Select query vector based on layer
	var queryVec []float32
	switch layer {
	case "structure":
		queryVec = layers["structure"]
	case "lexical":
		queryVec = layers["lexical"]
	case "contextual":
		queryVec = layers["contextual"]
		if queryVec == nil {
			// Contextual not available for query, use final
			queryVec = finalVec
		}
	default:
		queryVec = finalVec
	}

	// Search
	searcher := search.NewSearcher(database)
	results, err := searcher.Search(queryVec, layer, k)
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

func runCompare(id1, id2 int64, dbPath string) {
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	chunk1, err := database.GetChunk(id1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: chunk %d not found\n", id1)
		os.Exit(1)
	}
	chunk2, err := database.GetChunk(id2)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: chunk %d not found\n", id2)
		os.Exit(1)
	}

	fmt.Printf("🔬 Comparing chunks:\n")
	fmt.Printf("   #%d: [%s] %s\n", id1, chunk1.ChunkType, chunk1.Name)
	fmt.Printf("   #%d: [%s] %s\n", id2, chunk2.ChunkType, chunk2.Name)
	fmt.Printf("\n")

	layers := []string{"structure", "lexical", "contextual", "final"}

	for _, layer := range layers {
		vec1, err1 := database.GetVector(id1, layer)
		vec2, err2 := database.GetVector(id2, layer)

		if err1 != nil || err2 != nil {
			continue
		}

		score := search.CosineSimilarity(vec1, vec2)
		bar := strings.Repeat("█", int(score*20))
		pad := strings.Repeat("░", 20-int(score*20))

		fmt.Printf("   %-12s %s%s %.4f\n", layer+":", bar, pad, score)
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
	fmt.Printf("   Vectors:  %d (per layer)\n", vectors)
	fmt.Printf("   Layers:   structure (256d), lexical (128d), contextual (128d), final (256d)\n")
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

// detectLanguageFromContent tries to detect the language from code content.
func detectLanguageFromContent(code string) string {
	upper := strings.ToUpper(code)

	// SQL patterns
	sqlKeywords := []string{"SELECT ", "INSERT ", "UPDATE ", "DELETE ", "CREATE TABLE", "ALTER TABLE", "DROP TABLE", "FROM ", "WHERE ", "JOIN "}
	sqlScore := 0
	for _, kw := range sqlKeywords {
		if strings.Contains(upper, kw) {
			sqlScore++
		}
	}
	if sqlScore >= 2 {
		return "sql"
	}

	// Bash patterns
	bashPatterns := []string{"#!/bin/", "#!/usr/bin/env", "if [", "for ", "while ", "done", "fi", "esac", "then", "${", "$(",}
	bashScore := 0
	for _, p := range bashPatterns {
		if strings.Contains(code, p) {
			bashScore++
		}
	}
	if bashScore >= 2 || strings.HasPrefix(code, "#!") {
		return "bash"
	}

	// Go patterns
	goPatterns := []string{"func ", "package ", "import ", "type ", "struct {", "interface {", ":= ", "go ", "defer "}
	goScore := 0
	for _, p := range goPatterns {
		if strings.Contains(code, p) {
			goScore++
		}
	}
	if goScore >= 2 {
		return "go"
	}

	// Default to Go
	return "go"
}
