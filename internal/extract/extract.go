// Package extract provides file extraction capabilities.
// Go = I/O, extractors read binary formats and produce text segments.
package extract

import (
	"context"
	"encoding/json"
	"fmt"
)

// Segment represents an extracted text segment.
type Segment struct {
	ID           string   `json:"id"`
	FileID       string   `json:"file_id"`
	SegmentType  string   `json:"segment_type"` // text, table, image_ocr, metadata, code
	Content      string   `json:"content"`
	Page         *int     `json:"page,omitempty"`
	Position     int      `json:"position"`
	BBox         *BBox    `json:"bbox,omitempty"`
	Confidence   float64  `json:"confidence,omitempty"`
	Metadata     Metadata `json:"metadata,omitempty"`
}

// BBox represents a bounding box.
type BBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Metadata holds extraction metadata.
type Metadata struct {
	Style     string `json:"style,omitempty"`
	Level     int    `json:"level,omitempty"`
	Language  string `json:"language,omitempty"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
}

// Extractor is the interface for all extractors.
type Extractor interface {
	// Name returns the extractor name.
	Name() string
	// Version returns the extractor version.
	Version() string
	// SupportedTypes returns MIME types this extractor handles.
	SupportedTypes() []string
	// Extract extracts segments from content.
	Extract(ctx context.Context, content []byte, config json.RawMessage) ([]Segment, error)
}

// Registry holds registered extractors.
type Registry struct {
	extractors map[string]Extractor
	mimeMap    map[string]string // mime_type -> extractor_name
}

// NewRegistry creates a new extractor registry.
func NewRegistry() *Registry {
	return &Registry{
		extractors: make(map[string]Extractor),
		mimeMap:    make(map[string]string),
	}
}

// Register registers an extractor.
func (r *Registry) Register(ext Extractor) {
	r.extractors[ext.Name()] = ext
	for _, mimeType := range ext.SupportedTypes() {
		r.mimeMap[mimeType] = ext.Name()
	}
}

// Get returns an extractor by name.
func (r *Registry) Get(name string) (Extractor, bool) {
	ext, ok := r.extractors[name]
	return ext, ok
}

// GetForMime returns an extractor for a MIME type.
func (r *Registry) GetForMime(mimeType string) (Extractor, bool) {
	name, ok := r.mimeMap[mimeType]
	if !ok {
		return nil, false
	}
	return r.Get(name)
}

// List returns all registered extractors.
func (r *Registry) List() []Extractor {
	var list []Extractor
	for _, ext := range r.extractors {
		list = append(list, ext)
	}
	return list
}

// ExtractAll extracts from content using the appropriate extractor.
func (r *Registry) ExtractAll(ctx context.Context, mimeType string, content []byte, config json.RawMessage) ([]Segment, error) {
	ext, ok := r.GetForMime(mimeType)
	if !ok {
		return nil, fmt.Errorf("no extractor for mime type: %s", mimeType)
	}
	return ext.Extract(ctx, content, config)
}
