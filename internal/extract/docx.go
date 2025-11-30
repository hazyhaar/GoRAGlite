package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// DOCXExtractor extracts text from DOCX files.
type DOCXExtractor struct {
	name    string
	version string
}

// NewDOCXExtractor creates a new DOCX extractor.
func NewDOCXExtractor() *DOCXExtractor {
	return &DOCXExtractor{
		name:    "docx",
		version: "1.0.0",
	}
}

func (e *DOCXExtractor) Name() string    { return e.name }
func (e *DOCXExtractor) Version() string { return e.version }

func (e *DOCXExtractor) SupportedTypes() []string {
	return []string{
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/msword",
	}
}

func (e *DOCXExtractor) Extract(ctx context.Context, content []byte, config json.RawMessage) ([]Segment, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("open docx: %w", err)
	}

	// Find document.xml
	var docFile *zip.File
	for _, f := range reader.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}

	if docFile == nil {
		return nil, fmt.Errorf("document.xml not found")
	}

	rc, err := docFile.Open()
	if err != nil {
		return nil, fmt.Errorf("open document.xml: %w", err)
	}
	defer rc.Close()

	xmlContent, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read document.xml: %w", err)
	}

	return e.parseDocumentXML(xmlContent)
}

// OOXML structures
type document struct {
	Body body `xml:"body"`
}

type body struct {
	Paragraphs []paragraph `xml:"p"`
	Tables     []table     `xml:"tbl"`
}

type paragraph struct {
	Properties paragraphProperties `xml:"pPr"`
	Runs       []run               `xml:"r"`
}

type paragraphProperties struct {
	Style     *styleRef `xml:"pStyle"`
	OutlineLevel *outlineLvl `xml:"outlineLvl"`
}

type styleRef struct {
	Val string `xml:"val,attr"`
}

type outlineLvl struct {
	Val int `xml:"val,attr"`
}

type run struct {
	Properties runProperties `xml:"rPr"`
	Text       []text        `xml:"t"`
}

type runProperties struct {
	Bold   *struct{} `xml:"b"`
	Italic *struct{} `xml:"i"`
}

type text struct {
	Content string `xml:",chardata"`
	Space   string `xml:"space,attr"`
}

type table struct {
	Rows []tableRow `xml:"tr"`
}

type tableRow struct {
	Cells []tableCell `xml:"tc"`
}

type tableCell struct {
	Paragraphs []paragraph `xml:"p"`
}

func (e *DOCXExtractor) parseDocumentXML(xmlContent []byte) ([]Segment, error) {
	// Remove namespace prefixes for simpler parsing
	cleaned := e.cleanNamespaces(xmlContent)

	var doc document
	if err := xml.Unmarshal(cleaned, &doc); err != nil {
		// Fallback to regex-based extraction
		return e.fallbackExtract(xmlContent)
	}

	return e.convertToSegments(&doc)
}

func (e *DOCXExtractor) cleanNamespaces(content []byte) []byte {
	// Remove common Word XML namespaces
	s := string(content)
	s = regexp.MustCompile(`<w:`).ReplaceAllString(s, `<`)
	s = regexp.MustCompile(`</w:`).ReplaceAllString(s, `</`)
	s = regexp.MustCompile(`xmlns:w="[^"]*"`).ReplaceAllString(s, ``)
	return []byte(s)
}

func (e *DOCXExtractor) convertToSegments(doc *document) ([]Segment, error) {
	var segments []Segment
	position := 0

	for _, p := range doc.Body.Paragraphs {
		text := e.extractParagraphText(&p)
		if strings.TrimSpace(text) == "" {
			continue
		}

		segType := "text"
		style := ""
		level := 0

		if p.Properties.Style != nil {
			style = p.Properties.Style.Val
			if strings.HasPrefix(strings.ToLower(style), "heading") {
				segType = "heading"
			}
		}

		if p.Properties.OutlineLevel != nil {
			level = p.Properties.OutlineLevel.Val + 1
			segType = "heading"
		}

		hash := sha256.Sum256([]byte(text))
		segments = append(segments, Segment{
			ID:          hex.EncodeToString(hash[:16]),
			SegmentType: segType,
			Content:     text,
			Position:    position,
			Metadata: Metadata{
				Style: style,
				Level: level,
			},
		})
		position++
	}

	// Extract tables
	for _, tbl := range doc.Body.Tables {
		text := e.extractTableText(&tbl)
		if strings.TrimSpace(text) == "" {
			continue
		}

		hash := sha256.Sum256([]byte(text))
		segments = append(segments, Segment{
			ID:          hex.EncodeToString(hash[:16]),
			SegmentType: "table",
			Content:     text,
			Position:    position,
		})
		position++
	}

	return segments, nil
}

func (e *DOCXExtractor) extractParagraphText(p *paragraph) string {
	var builder strings.Builder

	for _, run := range p.Runs {
		for _, t := range run.Text {
			builder.WriteString(t.Content)
		}
	}

	return builder.String()
}

func (e *DOCXExtractor) extractTableText(tbl *table) string {
	var rows []string

	for _, row := range tbl.Rows {
		var cells []string
		for _, cell := range row.Cells {
			var cellText []string
			for _, p := range cell.Paragraphs {
				text := e.extractParagraphText(&p)
				if text != "" {
					cellText = append(cellText, text)
				}
			}
			cells = append(cells, strings.Join(cellText, " "))
		}
		rows = append(rows, strings.Join(cells, " | "))
	}

	return strings.Join(rows, "\n")
}

func (e *DOCXExtractor) fallbackExtract(content []byte) ([]Segment, error) {
	// Simple regex-based text extraction
	textPattern := regexp.MustCompile(`<w:t[^>]*>([^<]+)</w:t>`)
	matches := textPattern.FindAllSubmatch(content, -1)

	var builder strings.Builder
	for _, match := range matches {
		if len(match) > 1 {
			builder.Write(match[1])
			builder.WriteString(" ")
		}
	}

	text := strings.TrimSpace(builder.String())
	if text == "" {
		return nil, nil
	}

	// Split into paragraphs by double spaces (rough heuristic)
	paragraphs := strings.Split(text, "  ")

	var segments []Segment
	for i, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		hash := sha256.Sum256([]byte(p))
		segments = append(segments, Segment{
			ID:          hex.EncodeToString(hash[:16]),
			SegmentType: "text",
			Content:     p,
			Position:    i,
		})
	}

	return segments, nil
}
