package extract

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// PDFExtractor extracts text from PDF files using pdftotext.
type PDFExtractor struct {
	name    string
	version string
}

// NewPDFExtractor creates a new PDF extractor.
func NewPDFExtractor() *PDFExtractor {
	return &PDFExtractor{
		name:    "pdftotext",
		version: "0.86.1",
	}
}

func (e *PDFExtractor) Name() string    { return e.name }
func (e *PDFExtractor) Version() string { return e.version }

func (e *PDFExtractor) SupportedTypes() []string {
	return []string{"application/pdf"}
}

func (e *PDFExtractor) Extract(ctx context.Context, content []byte, config json.RawMessage) ([]Segment, error) {
	var cfg struct {
		Layout   bool   `json:"layout"`
		Encoding string `json:"encoding"`
	}
	cfg.Layout = true
	cfg.Encoding = "UTF-8"

	if config != nil {
		json.Unmarshal(config, &cfg)
	}

	// Try pdftotext first
	text, err := e.extractWithPdftotext(ctx, content, cfg.Layout)
	if err != nil {
		// Fallback to simple extraction
		text, err = e.extractSimple(content)
		if err != nil {
			return nil, fmt.Errorf("pdf extraction failed: %w", err)
		}
	}

	return e.parseText(text)
}

func (e *PDFExtractor) extractWithPdftotext(ctx context.Context, content []byte, layout bool) (string, error) {
	args := []string{"-"}
	if layout {
		args = append(args, "-layout")
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, "pdftotext", args...)
	cmd.Stdin = bytes.NewReader(content)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext: %w (stderr: %s)", err, stderr.String())
	}

	return stdout.String(), nil
}

func (e *PDFExtractor) extractSimple(content []byte) (string, error) {
	// Basic text extraction from PDF stream
	// This is a fallback for when pdftotext is not available

	var text strings.Builder

	// Look for text streams (very basic)
	re := regexp.MustCompile(`\(([^)]+)\)`)
	matches := re.FindAllStringSubmatch(string(content), -1)

	for _, match := range matches {
		if len(match) > 1 {
			text.WriteString(match[1])
			text.WriteString(" ")
		}
	}

	return text.String(), nil
}

func (e *PDFExtractor) parseText(text string) ([]Segment, error) {
	var segments []Segment
	lines := strings.Split(text, "\n")

	// Detect page breaks (form feed character or "Page X" patterns)
	pagePattern := regexp.MustCompile(`(?i)^(page\s+\d+|\f)`)

	var currentPage int = 1
	var currentBlock strings.Builder
	var blockStart int = 0
	var position int = 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Page break detection
		if pagePattern.MatchString(trimmed) || line == "\f" {
			if currentBlock.Len() > 0 {
				hash := sha256.Sum256([]byte(currentBlock.String()))
				segments = append(segments, Segment{
					ID:          hex.EncodeToString(hash[:16]),
					SegmentType: "text",
					Content:     strings.TrimSpace(currentBlock.String()),
					Page:        &currentPage,
					Position:    position,
					Metadata: Metadata{
						LineStart: blockStart,
						LineEnd:   i,
					},
				})
				position++
				currentBlock.Reset()
			}
			currentPage++
			blockStart = i + 1
			continue
		}

		// Empty line = paragraph break
		if trimmed == "" {
			if currentBlock.Len() > 0 {
				hash := sha256.Sum256([]byte(currentBlock.String()))
				segments = append(segments, Segment{
					ID:          hex.EncodeToString(hash[:16]),
					SegmentType: "text",
					Content:     strings.TrimSpace(currentBlock.String()),
					Page:        &currentPage,
					Position:    position,
					Metadata: Metadata{
						LineStart: blockStart,
						LineEnd:   i,
					},
				})
				position++
				currentBlock.Reset()
				blockStart = i + 1
			}
			continue
		}

		currentBlock.WriteString(trimmed)
		currentBlock.WriteString(" ")
	}

	// Last block
	if currentBlock.Len() > 0 {
		hash := sha256.Sum256([]byte(currentBlock.String()))
		segments = append(segments, Segment{
			ID:          hex.EncodeToString(hash[:16]),
			SegmentType: "text",
			Content:     strings.TrimSpace(currentBlock.String()),
			Page:        &currentPage,
			Position:    position,
			Metadata: Metadata{
				LineStart: blockStart,
				LineEnd:   len(lines),
			},
		})
	}

	return segments, nil
}
