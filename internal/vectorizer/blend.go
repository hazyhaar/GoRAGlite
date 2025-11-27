// Package vectorizer provides code vectorization algorithms.
// blend.go - Multi-layer vector blending with dynamic weights.
package vectorizer

import (
	"encoding/json"
	"math"

	"github.com/hazylab/goraglite/internal/chunker"
)

// Layer represents a vectorization layer.
type Layer struct {
	Name   string
	Weight float32
	Dims   int
}

// BlendConfig defines how to combine multiple vector layers.
type BlendConfig struct {
	Layers      []Layer
	OutputDims  int
	Normalize   bool
	Method      string // "weighted", "concat", "attention"
}

// DefaultBlendConfig returns the default blend configuration.
func DefaultBlendConfig() BlendConfig {
	return BlendConfig{
		Layers: []Layer{
			{Name: "structure", Weight: 0.6, Dims: 256},
			{Name: "lexical", Weight: 0.4, Dims: 128},
		},
		OutputDims: 256,
		Normalize:  true,
		Method:     "weighted",
	}
}

// Blender combines multiple vector layers into a final vector.
type Blender struct {
	Config     BlendConfig
	Structure  *StructureVectorizer
	Lexical    *LexicalVectorizer
}

// NewBlender creates a new multi-layer blender.
func NewBlender(config BlendConfig) *Blender {
	return &Blender{
		Config:    config,
		Structure: NewStructureVectorizer(256),
		Lexical:   NewLexicalVectorizer(128),
	}
}

// Vectorize generates all layer vectors and blends them.
func (b *Blender) Vectorize(chunk *chunker.Chunk) (map[string][]float32, []float32) {
	layers := make(map[string][]float32)

	// Generate each layer
	layers["structure"] = b.Structure.Vectorize(chunk)
	layers["lexical"] = b.Lexical.Vectorize(chunk)

	// Blend based on method
	var final []float32
	switch b.Config.Method {
	case "concat":
		final = b.blendConcat(layers)
	case "attention":
		final = b.blendAttention(layers)
	default:
		final = b.blendWeighted(layers)
	}

	if b.Config.Normalize {
		normalize(final)
	}

	return layers, final
}

// blendWeighted combines layers using fixed weights.
func (b *Blender) blendWeighted(layers map[string][]float32) []float32 {
	output := make([]float32, b.Config.OutputDims)

	for _, layer := range b.Config.Layers {
		vec, ok := layers[layer.Name]
		if !ok {
			continue
		}

		// Project to output dims if needed
		projected := project(vec, b.Config.OutputDims)

		// Add weighted contribution
		for i := range output {
			output[i] += projected[i] * layer.Weight
		}
	}

	return output
}

// blendConcat concatenates all layers (with projection).
func (b *Blender) blendConcat(layers map[string][]float32) []float32 {
	// Calculate total input dims
	totalDims := 0
	for _, layer := range b.Config.Layers {
		if vec, ok := layers[layer.Name]; ok {
			totalDims += len(vec)
		}
	}

	// Concatenate
	concat := make([]float32, 0, totalDims)
	for _, layer := range b.Config.Layers {
		if vec, ok := layers[layer.Name]; ok {
			concat = append(concat, vec...)
		}
	}

	// Project to output dims
	return project(concat, b.Config.OutputDims)
}

// blendAttention uses self-attention-like mechanism to weight layers.
func (b *Blender) blendAttention(layers map[string][]float32) []float32 {
	output := make([]float32, b.Config.OutputDims)

	// Calculate "attention scores" based on vector norms (energy)
	scores := make(map[string]float32)
	var totalScore float32

	for _, layer := range b.Config.Layers {
		vec, ok := layers[layer.Name]
		if !ok {
			continue
		}
		// Use L2 norm as "confidence" score
		norm := l2Norm(vec)
		scores[layer.Name] = norm * layer.Weight
		totalScore += scores[layer.Name]
	}

	// Normalize scores (softmax-like)
	if totalScore > 0 {
		for name := range scores {
			scores[name] /= totalScore
		}
	}

	// Blend with attention weights
	for _, layer := range b.Config.Layers {
		vec, ok := layers[layer.Name]
		if !ok {
			continue
		}

		weight := scores[layer.Name]
		projected := project(vec, b.Config.OutputDims)

		for i := range output {
			output[i] += projected[i] * weight
		}
	}

	return output
}

// VectorizeWithContext generates vectors with contextual information.
func (b *Blender) VectorizeWithContext(chunk *chunker.Chunk, context *BlendContext) (map[string][]float32, []float32) {
	layers, _ := b.Vectorize(chunk)

	// Apply contextual weights
	weights := b.computeContextualWeights(layers, context)

	// Blend with dynamic weights
	output := make([]float32, b.Config.OutputDims)
	for name, vec := range layers {
		weight := weights[name]
		projected := project(vec, b.Config.OutputDims)
		for i := range output {
			output[i] += projected[i] * weight
		}
	}

	if b.Config.Normalize {
		normalize(output)
	}

	return layers, output
}

// BlendContext provides context for dynamic weight computation.
type BlendContext struct {
	QueryType     string             // "structural", "semantic", "mixed"
	LayerScores   map[string]float32 // Previous search scores per layer
	UserWeights   map[string]float32 // User-specified weights
}

// computeContextualWeights computes dynamic weights based on context.
func (b *Blender) computeContextualWeights(layers map[string][]float32, context *BlendContext) map[string]float32 {
	weights := make(map[string]float32)

	// Start with base weights
	for _, layer := range b.Config.Layers {
		weights[layer.Name] = layer.Weight
	}

	if context == nil {
		return weights
	}

	// Apply user weights if provided
	if context.UserWeights != nil {
		for name, w := range context.UserWeights {
			weights[name] = w
		}
	}

	// Adjust based on query type
	switch context.QueryType {
	case "structural":
		weights["structure"] *= 1.5
		weights["lexical"] *= 0.5
	case "semantic":
		weights["structure"] *= 0.5
		weights["lexical"] *= 1.5
	}

	// Adjust based on previous layer scores (feedback loop)
	if context.LayerScores != nil {
		for name, score := range context.LayerScores {
			if score > 0.8 {
				weights[name] *= 1.2 // Boost high-performing layers
			} else if score < 0.3 {
				weights[name] *= 0.8 // Reduce low-performing layers
			}
		}
	}

	// Normalize weights to sum to 1
	var total float32
	for _, w := range weights {
		total += w
	}
	if total > 0 {
		for name := range weights {
			weights[name] /= total
		}
	}

	return weights
}

// GetWeightsJSON returns the current blend weights as JSON.
func (b *Blender) GetWeightsJSON() string {
	weights := make(map[string]float32)
	for _, layer := range b.Config.Layers {
		weights[layer.Name] = layer.Weight
	}
	data, _ := json.Marshal(weights)
	return string(data)
}

// Helper functions

// project projects a vector to target dimensions.
// Uses simple linear projection (averaging or padding).
func project(vec []float32, targetDims int) []float32 {
	if len(vec) == targetDims {
		return vec
	}

	result := make([]float32, targetDims)

	if len(vec) < targetDims {
		// Pad with zeros (already done by make)
		copy(result, vec)
	} else {
		// Downsample by averaging bins
		binSize := float32(len(vec)) / float32(targetDims)
		for i := 0; i < targetDims; i++ {
			start := int(float32(i) * binSize)
			end := int(float32(i+1) * binSize)
			if end > len(vec) {
				end = len(vec)
			}

			var sum float32
			for j := start; j < end; j++ {
				sum += vec[j]
			}
			result[i] = sum / float32(end-start)
		}
	}

	return result
}

func l2Norm(vec []float32) float32 {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	return float32(math.Sqrt(sum))
}

// CrossCorrelation computes correlation between two layers.
func CrossCorrelation(a, b []float32) float32 {
	if len(a) != len(b) {
		// Project to same size
		minLen := len(a)
		if len(b) < minLen {
			minLen = len(b)
		}
		a = project(a, minLen)
		b = project(b, minLen)
	}

	// Compute Pearson correlation
	var sumA, sumB, sumAB, sumA2, sumB2 float64
	n := float64(len(a))

	for i := range a {
		sumA += float64(a[i])
		sumB += float64(b[i])
		sumAB += float64(a[i]) * float64(b[i])
		sumA2 += float64(a[i]) * float64(a[i])
		sumB2 += float64(b[i]) * float64(b[i])
	}

	numerator := n*sumAB - sumA*sumB
	denominator := math.Sqrt((n*sumA2 - sumA*sumA) * (n*sumB2 - sumB*sumB))

	if denominator == 0 {
		return 0
	}

	return float32(numerator / denominator)
}
