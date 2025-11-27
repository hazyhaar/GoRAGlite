// Package search provides vector similarity search.
package search

import (
	"math"
	"sort"

	"github.com/hazylab/goraglite/internal/db"
)

// Result represents a search result.
type Result struct {
	ChunkID    int64
	Score      float32 // Cosine similarity (higher = more similar)
	Chunk      *db.Chunk
	LayerScore map[string]float32 // Score per layer for explainability
}

// Searcher performs vector similarity search.
type Searcher struct {
	db *db.DB
}

// NewSearcher creates a new searcher.
func NewSearcher(database *db.DB) *Searcher {
	return &Searcher{db: database}
}

// Search finds the k most similar chunks to the query vector.
func (s *Searcher) Search(queryVec []float32, layer string, k int) ([]Result, error) {
	ids, vecs, err := s.db.GetAllVectors(layer)
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		return nil, nil
	}

	// Calculate similarities
	results := make([]Result, len(ids))
	for i, vec := range vecs {
		results[i] = Result{
			ChunkID: ids[i],
			Score:   CosineSimilarity(queryVec, vec),
		}
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Take top k
	if k > len(results) {
		k = len(results)
	}
	results = results[:k]

	// Load chunk data
	for i := range results {
		chunk, err := s.db.GetChunk(results[i].ChunkID)
		if err == nil {
			results[i].Chunk = chunk
		}
	}

	return results, nil
}

// SearchMultiLayer performs search across multiple layers and combines results.
func (s *Searcher) SearchMultiLayer(queryVecs map[string][]float32, weights map[string]float32, k int) ([]Result, error) {
	// Collect results from each layer
	layerResults := make(map[string][]Result)

	for layer, vec := range queryVecs {
		results, err := s.Search(vec, layer, k*3) // Get more candidates
		if err != nil {
			continue
		}
		layerResults[layer] = results
	}

	// Combine scores
	scoreMap := make(map[int64]*Result)

	for layer, results := range layerResults {
		weight := weights[layer]
		if weight == 0 {
			weight = 1.0 / float32(len(queryVecs))
		}

		for _, r := range results {
			if existing, ok := scoreMap[r.ChunkID]; ok {
				existing.Score += r.Score * weight
				if existing.LayerScore == nil {
					existing.LayerScore = make(map[string]float32)
				}
				existing.LayerScore[layer] = r.Score
			} else {
				newResult := Result{
					ChunkID:    r.ChunkID,
					Score:      r.Score * weight,
					Chunk:      r.Chunk,
					LayerScore: map[string]float32{layer: r.Score},
				}
				scoreMap[r.ChunkID] = &newResult
			}
		}
	}

	// Convert to slice and sort
	combined := make([]Result, 0, len(scoreMap))
	for _, r := range scoreMap {
		combined = append(combined, *r)
	}

	sort.Slice(combined, func(i, j int) bool {
		return combined[i].Score > combined[j].Score
	})

	if k > len(combined) {
		k = len(combined)
	}

	return combined[:k], nil
}

// CosineSimilarity calculates cosine similarity between two vectors.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		// Pad shorter vector
		if len(a) < len(b) {
			a = pad(a, len(b))
		} else {
			b = pad(b, len(a))
		}
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}

func pad(vec []float32, size int) []float32 {
	result := make([]float32, size)
	copy(result, vec)
	return result
}

// L2Distance calculates Euclidean distance between two vectors.
func L2Distance(a, b []float32) float32 {
	if len(a) != len(b) {
		if len(a) < len(b) {
			a = pad(a, len(b))
		} else {
			b = pad(b, len(a))
		}
	}

	var sum float64
	for i := range a {
		diff := float64(a[i]) - float64(b[i])
		sum += diff * diff
	}

	return float32(math.Sqrt(sum))
}

// DotProduct calculates dot product between two vectors.
func DotProduct(a, b []float32) float32 {
	if len(a) != len(b) {
		if len(a) < len(b) {
			a = pad(a, len(b))
		} else {
			b = pad(b, len(a))
		}
	}

	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}

	return float32(dot)
}
