// Package vectorizer provides code vectorization algorithms.
// lexical.go - Lexical layer: TF-IDF on identifiers and subwords.
package vectorizer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"hash/fnv"
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/hazylab/goraglite/internal/chunker"
)

// LexicalVectorizer creates vectors from code identifiers and naming patterns.
// It captures the "vocabulary" of the code - what things are called.
type LexicalVectorizer struct {
	Dims        int                // Vector dimensions
	MinTokenLen int                // Minimum token length
	IDF         map[string]float32 // Inverse document frequency cache
	DocCount    int                // Total documents for IDF
}

// NewLexicalVectorizer creates a new lexical vectorizer.
func NewLexicalVectorizer(dims int) *LexicalVectorizer {
	if dims <= 0 {
		dims = 128
	}
	return &LexicalVectorizer{
		Dims:        dims,
		MinTokenLen: 2,
		IDF:         make(map[string]float32),
	}
}

// Vectorize converts a chunk's identifiers into a vector.
func (v *LexicalVectorizer) Vectorize(chunk *chunker.Chunk) []float32 {
	vec := make([]float32, v.Dims)

	// Extract all identifiers from the code
	identifiers := v.extractIdentifiers(chunk.Content)

	// Tokenize into subwords
	tokens := v.tokenize(identifiers)

	// Count token frequencies (TF)
	tf := make(map[string]int)
	for _, tok := range tokens {
		tf[tok]++
	}

	// Apply TF-IDF weighting and hash into vector
	totalTokens := float32(len(tokens))
	if totalTokens == 0 {
		totalTokens = 1
	}

	for tok, count := range tf {
		// Term frequency (normalized)
		tfScore := float32(count) / totalTokens

		// IDF score (if available, otherwise use 1.0)
		idfScore := float32(1.0)
		if idf, ok := v.IDF[tok]; ok {
			idfScore = idf
		}

		// TF-IDF weight
		weight := tfScore * idfScore

		// Hash token to vector indices (feature hashing with sign trick)
		idx := v.hashToIndex(tok)
		sign := v.hashToSign(tok)
		vec[idx] += sign * weight
	}

	// Add special features for naming conventions
	v.addNamingFeatures(vec, identifiers)

	// Add domain vocabulary features
	v.addDomainFeatures(vec, tokens)

	// Normalize
	normalize(vec)

	return vec
}

// BuildIDF builds IDF scores from a corpus of chunks.
func (v *LexicalVectorizer) BuildIDF(chunks []*chunker.Chunk) {
	docFreq := make(map[string]int)
	v.DocCount = len(chunks)

	for _, chunk := range chunks {
		identifiers := v.extractIdentifiers(chunk.Content)
		tokens := v.tokenize(identifiers)

		// Count unique tokens per document
		seen := make(map[string]bool)
		for _, tok := range tokens {
			if !seen[tok] {
				docFreq[tok]++
				seen[tok] = true
			}
		}
	}

	// Calculate IDF: log(N / df)
	for tok, df := range docFreq {
		v.IDF[tok] = float32(math.Log(float64(v.DocCount+1) / float64(df+1)))
	}
}

// extractIdentifiers extracts all identifiers from Go code.
func (v *LexicalVectorizer) extractIdentifiers(code string) []string {
	var identifiers []string

	fset := token.NewFileSet()
	// Wrap in package if needed
	src := code
	if !strings.Contains(code, "package") {
		src = "package x\n" + code
	}

	file, err := parser.ParseFile(fset, "", src, parser.AllErrors)
	if err != nil {
		// Fallback: extract with regex
		return v.extractIdentifiersRegex(code)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name != "_" && !isKeyword(x.Name) {
				identifiers = append(identifiers, x.Name)
			}
		}
		return true
	})

	return identifiers
}

// extractIdentifiersRegex fallback extraction using regex.
func (v *LexicalVectorizer) extractIdentifiersRegex(code string) []string {
	re := regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)
	matches := re.FindAllString(code, -1)

	var identifiers []string
	for _, m := range matches {
		if !isKeyword(m) && len(m) >= v.MinTokenLen {
			identifiers = append(identifiers, m)
		}
	}
	return identifiers
}

// tokenize splits identifiers into subwords.
// camelCase -> [camel, case], snake_case -> [snake, case]
func (v *LexicalVectorizer) tokenize(identifiers []string) []string {
	var tokens []string

	for _, ident := range identifiers {
		subwords := splitIdentifier(ident)
		for _, sw := range subwords {
			sw = strings.ToLower(sw)
			if len(sw) >= v.MinTokenLen {
				tokens = append(tokens, sw)
			}
		}
	}

	return tokens
}

// splitIdentifier splits camelCase and snake_case identifiers.
func splitIdentifier(s string) []string {
	var result []string
	var current strings.Builder

	for i, r := range s {
		if r == '_' {
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
			continue
		}

		if unicode.IsUpper(r) && i > 0 {
			// Check if this starts a new word
			prev := rune(s[i-1])
			if unicode.IsLower(prev) || prev == '_' {
				if current.Len() > 0 {
					result = append(result, current.String())
					current.Reset()
				}
			}
		}

		current.WriteRune(r)
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// addNamingFeatures adds features about naming conventions.
func (v *LexicalVectorizer) addNamingFeatures(vec []float32, identifiers []string) {
	if len(identifiers) == 0 {
		return
	}

	offset := v.Dims - 32 // Reserve last 32 dims for naming features

	var camelCount, snakeCount, allCapsCount, shortCount int
	var totalLen int

	for _, id := range identifiers {
		totalLen += len(id)

		if strings.Contains(id, "_") {
			snakeCount++
		} else if hasUpperCase(id) && hasLowerCase(id) {
			camelCount++
		}

		if strings.ToUpper(id) == id && len(id) > 1 {
			allCapsCount++
		}

		if len(id) <= 3 {
			shortCount++
		}
	}

	total := float32(len(identifiers))

	// Naming convention ratios
	vec[offset+0] = float32(camelCount) / total  // camelCase ratio
	vec[offset+1] = float32(snakeCount) / total  // snake_case ratio
	vec[offset+2] = float32(allCapsCount) / total // ALLCAPS ratio
	vec[offset+3] = float32(shortCount) / total   // Short names ratio

	// Average identifier length
	vec[offset+4] = sigmoid(float32(totalLen) / total / 10.0)

	// Identifier density
	vec[offset+5] = sigmoid(total / 20.0)
}

// addDomainFeatures adds features for domain-specific vocabulary.
func (v *LexicalVectorizer) addDomainFeatures(vec []float32, tokens []string) {
	offset := v.Dims - 64 // Use dims [-64:-32] for domain features

	// Domain vocabularies
	domains := map[string][]string{
		"http":    {"http", "request", "response", "handler", "server", "client", "url", "header", "cookie", "route", "api", "rest"},
		"db":      {"db", "database", "query", "sql", "row", "column", "table", "insert", "update", "delete", "select", "transaction", "commit"},
		"io":      {"file", "read", "write", "buffer", "stream", "reader", "writer", "open", "close", "path", "dir"},
		"error":   {"error", "err", "panic", "recover", "fatal", "warn", "log", "debug", "trace"},
		"async":   {"goroutine", "channel", "chan", "mutex", "lock", "unlock", "wait", "sync", "async", "concurrent"},
		"test":    {"test", "assert", "expect", "mock", "stub", "bench", "benchmark"},
		"crypto":  {"hash", "encrypt", "decrypt", "sign", "verify", "key", "token", "secret", "password"},
		"json":    {"json", "marshal", "unmarshal", "encode", "decode", "serialize", "parse"},
	}

	tokenSet := make(map[string]bool)
	for _, t := range tokens {
		tokenSet[t] = true
	}

	// Score each domain
	domainIdx := 0
	for _, vocab := range domains {
		matches := 0
		for _, word := range vocab {
			if tokenSet[word] {
				matches++
			}
		}
		if matches > 0 {
			vec[offset+domainIdx] = float32(matches) / float32(len(vocab))
		}
		domainIdx++
		if domainIdx >= 32 {
			break
		}
	}
}

func (v *LexicalVectorizer) hashToIndex(s string) int {
	h := fnv.New32a()
	h.Write([]byte(s))
	return int(h.Sum32()) % (v.Dims - 64) // Leave room for special features
}

func (v *LexicalVectorizer) hashToSign(s string) float32 {
	h := fnv.New32()
	h.Write([]byte(s))
	if h.Sum32()%2 == 0 {
		return 1.0
	}
	return -1.0
}

// Helper functions

func isKeyword(s string) bool {
	keywords := map[string]bool{
		"break": true, "case": true, "chan": true, "const": true,
		"continue": true, "default": true, "defer": true, "else": true,
		"fallthrough": true, "for": true, "func": true, "go": true,
		"goto": true, "if": true, "import": true, "interface": true,
		"map": true, "package": true, "range": true, "return": true,
		"select": true, "struct": true, "switch": true, "type": true,
		"var": true, "true": true, "false": true, "nil": true,
		"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
		"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
		"float32": true, "float64": true, "complex64": true, "complex128": true,
		"bool": true, "byte": true, "rune": true, "string": true, "error": true,
		"uintptr": true, "iota": true, "append": true, "cap": true, "close": true,
		"copy": true, "delete": true, "len": true, "make": true, "new": true,
		"panic": true, "print": true, "println": true, "real": true, "recover": true,
	}
	return keywords[s]
}

func hasUpperCase(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

func hasLowerCase(s string) bool {
	for _, r := range s {
		if unicode.IsLower(r) {
			return true
		}
	}
	return false
}
