// Package vector implements vectorization without external APIs.
// Uses feature hashing and TF-IDF computed in SQL + Go.
package vector

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Vector is a dense float32 vector.
type Vector []float32

// New creates a zero vector of given dimensions.
func New(dimensions int) Vector {
	return make(Vector, dimensions)
}

// FromBytes deserializes a vector from bytes.
func FromBytes(data []byte) Vector {
	if len(data)%4 != 0 {
		return nil
	}
	v := make(Vector, len(data)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return v
}

// Bytes serializes the vector to bytes.
func (v Vector) Bytes() []byte {
	data := make([]byte, len(v)*4)
	for i, val := range v {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(val))
	}
	return data
}

// Norm returns the L2 norm of the vector.
func (v Vector) Norm() float32 {
	var sum float32
	for _, val := range v {
		sum += val * val
	}
	return float32(math.Sqrt(float64(sum)))
}

// Normalize normalizes the vector to unit length.
func (v Vector) Normalize() Vector {
	norm := v.Norm()
	if norm == 0 {
		return v
	}
	result := make(Vector, len(v))
	for i, val := range v {
		result[i] = val / norm
	}
	return result
}

// Dot computes the dot product with another vector.
func (v Vector) Dot(other Vector) float32 {
	if len(v) != len(other) {
		return 0
	}
	var sum float32
	for i := range v {
		sum += v[i] * other[i]
	}
	return sum
}

// CosineSimilarity computes cosine similarity with another vector.
func (v Vector) CosineSimilarity(other Vector) float32 {
	if len(v) != len(other) {
		return 0
	}
	dot := v.Dot(other)
	normA := v.Norm()
	normB := other.Norm()
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (normA * normB)
}

// Add adds another vector.
func (v Vector) Add(other Vector) Vector {
	if len(v) != len(other) {
		return v
	}
	result := make(Vector, len(v))
	for i := range v {
		result[i] = v[i] + other[i]
	}
	return result
}

// Scale multiplies by a scalar.
func (v Vector) Scale(s float32) Vector {
	result := make(Vector, len(v))
	for i := range v {
		result[i] = v[i] * s
	}
	return result
}

// FeatureHasher creates vectors via feature hashing.
type FeatureHasher struct {
	Dimensions int
}

// NewFeatureHasher creates a new feature hasher.
func NewFeatureHasher(dimensions int) *FeatureHasher {
	return &FeatureHasher{Dimensions: dimensions}
}

// Hash hashes a feature name to an index.
func (h *FeatureHasher) Hash(feature string) int {
	hasher := fnv.New32a()
	hasher.Write([]byte(feature))
	return int(hasher.Sum32() % uint32(h.Dimensions))
}

// Sign returns +1 or -1 based on a secondary hash.
func (h *FeatureHasher) Sign(feature string) float32 {
	hasher := fnv.New32()
	hasher.Write([]byte(feature))
	if hasher.Sum32()%2 == 0 {
		return 1.0
	}
	return -1.0
}

// HashFeatures converts features to a vector.
func (h *FeatureHasher) HashFeatures(features map[string]float64) Vector {
	v := New(h.Dimensions)
	for name, value := range features {
		idx := h.Hash(name)
		sign := h.Sign(name)
		v[idx] += sign * float32(value)
	}
	return v.Normalize()
}

// TFIDFVectorizer creates TF-IDF vectors.
type TFIDFVectorizer struct {
	Dimensions  int
	MinDF       int     // minimum document frequency
	MaxDF       float64 // maximum document frequency (as ratio)
	Vocabulary  map[string]int
	IDF         map[string]float64
	DocCount    int
}

// NewTFIDFVectorizer creates a new TF-IDF vectorizer.
func NewTFIDFVectorizer(dimensions int) *TFIDFVectorizer {
	return &TFIDFVectorizer{
		Dimensions: dimensions,
		MinDF:      2,
		MaxDF:      0.8,
		Vocabulary: make(map[string]int),
		IDF:        make(map[string]float64),
	}
}

// Fit builds the vocabulary and IDF from a corpus.
func (v *TFIDFVectorizer) Fit(documents []string) {
	// Count document frequency for each term
	df := make(map[string]int)
	v.DocCount = len(documents)

	for _, doc := range documents {
		seen := make(map[string]bool)
		tokens := Tokenize(doc)
		for _, token := range tokens {
			if !seen[token] {
				df[token]++
				seen[token] = true
			}
		}
	}

	// Filter by min/max DF and build vocabulary
	hasher := NewFeatureHasher(v.Dimensions)
	maxCount := int(float64(v.DocCount) * v.MaxDF)

	for term, count := range df {
		if count >= v.MinDF && count <= maxCount {
			idx := hasher.Hash(term)
			v.Vocabulary[term] = idx
			// IDF = log(N / df)
			v.IDF[term] = math.Log(float64(v.DocCount) / float64(count))
		}
	}
}

// Transform converts a document to a TF-IDF vector.
func (v *TFIDFVectorizer) Transform(document string) Vector {
	vec := New(v.Dimensions)
	tokens := Tokenize(document)

	// Count term frequency
	tf := make(map[string]int)
	for _, token := range tokens {
		tf[token]++
	}

	// Compute TF-IDF
	hasher := NewFeatureHasher(v.Dimensions)
	totalTerms := float64(len(tokens))

	for term, count := range tf {
		idf, ok := v.IDF[term]
		if !ok {
			continue
		}
		idx := hasher.Hash(term)
		sign := hasher.Sign(term)
		// TF = count / total_terms
		tfValue := float64(count) / totalTerms
		tfidf := tfValue * idf
		vec[idx] += sign * float32(tfidf)
	}

	return vec.Normalize()
}

// Tokenize splits text into tokens.
func Tokenize(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// NGrams generates n-grams from tokens.
func NGrams(tokens []string, n int) []string {
	if len(tokens) < n {
		return nil
	}
	var ngrams []string
	for i := 0; i <= len(tokens)-n; i++ {
		ngram := strings.Join(tokens[i:i+n], "_")
		ngrams = append(ngrams, ngram)
	}
	return ngrams
}

// StructureVectorizer creates vectors from structural features.
type StructureVectorizer struct {
	Dimensions int
	hasher     *FeatureHasher
}

// NewStructureVectorizer creates a new structure vectorizer.
func NewStructureVectorizer(dimensions int) *StructureVectorizer {
	return &StructureVectorizer{
		Dimensions: dimensions,
		hasher:     NewFeatureHasher(dimensions),
	}
}

// StructureFeatures holds structural features of text.
type StructureFeatures struct {
	TokenCount      int
	CharCount       int
	LineCount       int
	WordCount       int
	AvgWordLength   float64
	UppercaseRatio  float64
	DigitRatio      float64
	PunctuationRatio float64
	HasCode         bool
	HasList         bool
	HasHeading      bool
	Language        string
}

// Extract extracts structural features from text.
func (v *StructureVectorizer) Extract(text string) StructureFeatures {
	lines := strings.Split(text, "\n")
	words := strings.Fields(text)
	chars := []rune(text)

	var uppercase, digits, punctuation int
	for _, r := range chars {
		if unicode.IsUpper(r) {
			uppercase++
		}
		if unicode.IsDigit(r) {
			digits++
		}
		if unicode.IsPunct(r) {
			punctuation++
		}
	}

	charCount := len(chars)
	wordCount := len(words)

	var avgWordLength float64
	if wordCount > 0 {
		totalWordChars := 0
		for _, w := range words {
			totalWordChars += len(w)
		}
		avgWordLength = float64(totalWordChars) / float64(wordCount)
	}

	// Detect features
	hasCode := strings.Contains(text, "func ") || strings.Contains(text, "def ") ||
		strings.Contains(text, "class ") || strings.Contains(text, "```")
	hasList := strings.Contains(text, "- ") || strings.Contains(text, "* ")
	hasHeading := strings.HasPrefix(strings.TrimSpace(text), "#")

	return StructureFeatures{
		TokenCount:       charCount / 4, // rough approximation
		CharCount:        charCount,
		LineCount:        len(lines),
		WordCount:        wordCount,
		AvgWordLength:    avgWordLength,
		UppercaseRatio:   float64(uppercase) / float64(max(charCount, 1)),
		DigitRatio:       float64(digits) / float64(max(charCount, 1)),
		PunctuationRatio: float64(punctuation) / float64(max(charCount, 1)),
		HasCode:          hasCode,
		HasList:          hasList,
		HasHeading:       hasHeading,
	}
}

// Vectorize converts structural features to a vector.
func (v *StructureVectorizer) Vectorize(features StructureFeatures) Vector {
	featureMap := map[string]float64{
		"token_count":       float64(features.TokenCount) / 1000.0,
		"char_count":        float64(features.CharCount) / 5000.0,
		"line_count":        float64(features.LineCount) / 100.0,
		"word_count":        float64(features.WordCount) / 500.0,
		"avg_word_length":   features.AvgWordLength / 10.0,
		"uppercase_ratio":   features.UppercaseRatio,
		"digit_ratio":       features.DigitRatio,
		"punctuation_ratio": features.PunctuationRatio,
		"has_code":          boolToFloat(features.HasCode),
		"has_list":          boolToFloat(features.HasList),
		"has_heading":       boolToFloat(features.HasHeading),
	}

	return v.hasher.HashFeatures(featureMap)
}

func boolToFloat(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}

// BlendVectorizer combines multiple vectors with weights.
type BlendVectorizer struct {
	Weights map[string]float32
}

// NewBlendVectorizer creates a new blend vectorizer.
func NewBlendVectorizer(weights map[string]float32) *BlendVectorizer {
	return &BlendVectorizer{Weights: weights}
}

// Blend combines vectors with configured weights.
func (v *BlendVectorizer) Blend(vectors map[string]Vector) Vector {
	if len(vectors) == 0 {
		return nil
	}

	// Get dimensions from first vector
	var dimensions int
	for _, vec := range vectors {
		dimensions = len(vec)
		break
	}

	result := New(dimensions)
	var totalWeight float32

	for name, vec := range vectors {
		weight, ok := v.Weights[name]
		if !ok {
			weight = 1.0 / float32(len(vectors))
		}
		totalWeight += weight

		for i := range result {
			result[i] += vec[i] * weight
		}
	}

	// Normalize by total weight
	if totalWeight > 0 {
		for i := range result {
			result[i] /= totalWeight
		}
	}

	return result.Normalize()
}

// SearchIndex is a simple brute-force vector index.
type SearchIndex struct {
	Vectors map[string]Vector
	IDs     []string
}

// NewSearchIndex creates a new search index.
func NewSearchIndex() *SearchIndex {
	return &SearchIndex{
		Vectors: make(map[string]Vector),
	}
}

// Add adds a vector to the index.
func (idx *SearchIndex) Add(id string, vec Vector) {
	idx.Vectors[id] = vec
	idx.IDs = append(idx.IDs, id)
}

// SearchResult holds a search result.
type SearchResult struct {
	ID    string
	Score float32
}

// Search finds the top-k most similar vectors.
func (idx *SearchIndex) Search(query Vector, topK int) []SearchResult {
	var results []SearchResult

	for id, vec := range idx.Vectors {
		score := query.CosineSimilarity(vec)
		results = append(results, SearchResult{ID: id, Score: score})
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > topK {
		results = results[:topK]
	}

	return results
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
