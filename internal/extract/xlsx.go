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
	"strconv"
	"strings"
)

// XLSXExtractor extracts text from Excel files.
type XLSXExtractor struct {
	name    string
	version string
}

// NewXLSXExtractor creates a new XLSX extractor.
func NewXLSXExtractor() *XLSXExtractor {
	return &XLSXExtractor{
		name:    "xlsx",
		version: "1.0.0",
	}
}

func (e *XLSXExtractor) Name() string    { return e.name }
func (e *XLSXExtractor) Version() string { return e.version }

func (e *XLSXExtractor) SupportedTypes() []string {
	return []string{
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.ms-excel",
	}
}

func (e *XLSXExtractor) Extract(ctx context.Context, content []byte, config json.RawMessage) ([]Segment, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}

	// Load shared strings
	sharedStrings, err := e.loadSharedStrings(reader)
	if err != nil {
		// Continue without shared strings
		sharedStrings = []string{}
	}

	// Find all sheet files
	var segments []Segment
	position := 0

	for _, f := range reader.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheetSegments, err := e.extractSheet(f, sharedStrings, position)
			if err != nil {
				continue
			}
			segments = append(segments, sheetSegments...)
			position += len(sheetSegments)
		}
	}

	return segments, nil
}

// SharedStrings XML structure
type sst struct {
	Strings []si `xml:"si"`
}

type si struct {
	T string `xml:"t"`
	R []r    `xml:"r"`
}

type r struct {
	T string `xml:"t"`
}

func (e *XLSXExtractor) loadSharedStrings(reader *zip.Reader) ([]string, error) {
	var ssFile *zip.File
	for _, f := range reader.File {
		if f.Name == "xl/sharedStrings.xml" {
			ssFile = f
			break
		}
	}

	if ssFile == nil {
		return nil, fmt.Errorf("sharedStrings.xml not found")
	}

	rc, err := ssFile.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	var ss sst
	if err := xml.Unmarshal(content, &ss); err != nil {
		return nil, err
	}

	var strings []string
	for _, s := range ss.Strings {
		if s.T != "" {
			strings = append(strings, s.T)
		} else {
			// Rich text
			var builder strings.Builder
			for _, run := range s.R {
				builder.WriteString(run.T)
			}
			strings = append(strings, builder.String())
		}
	}

	return strings, nil
}

// Worksheet XML structure
type worksheet struct {
	SheetData sheetData `xml:"sheetData"`
}

type sheetData struct {
	Rows []row `xml:"row"`
}

type row struct {
	R     int    `xml:"r,attr"` // row number
	Cells []cell `xml:"c"`
}

type cell struct {
	R string `xml:"r,attr"` // cell reference (A1, B2, etc.)
	T string `xml:"t,attr"` // type: s=shared string, n=number, b=bool, etc.
	V string `xml:"v"`      // value
	F string `xml:"f"`      // formula
}

func (e *XLSXExtractor) extractSheet(f *zip.File, sharedStrings []string, startPos int) ([]Segment, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	var ws worksheet
	if err := xml.Unmarshal(content, &ws); err != nil {
		return e.fallbackExtract(content, f.Name, startPos)
	}

	return e.convertToSegments(&ws, sharedStrings, f.Name, startPos)
}

func (e *XLSXExtractor) convertToSegments(ws *worksheet, sharedStrings []string, sheetName string, startPos int) ([]Segment, error) {
	var segments []Segment

	// Extract sheet name from path
	sheetNum := strings.TrimPrefix(sheetName, "xl/worksheets/sheet")
	sheetNum = strings.TrimSuffix(sheetNum, ".xml")

	// Process rows
	for _, row := range ws.SheetData.Rows {
		var cells []string
		for _, cell := range row.Cells {
			value := e.getCellValue(&cell, sharedStrings)
			if value != "" {
				// Include cell reference for context
				cells = append(cells, fmt.Sprintf("%s:%s", cell.R, value))
			}
		}

		if len(cells) == 0 {
			continue
		}

		rowText := strings.Join(cells, " | ")
		hash := sha256.Sum256([]byte(rowText))

		segments = append(segments, Segment{
			ID:          hex.EncodeToString(hash[:16]),
			SegmentType: "table",
			Content:     rowText,
			Position:    startPos + row.R,
			Metadata: Metadata{
				Style:     fmt.Sprintf("sheet%s", sheetNum),
				LineStart: row.R,
				LineEnd:   row.R,
			},
		})
	}

	return segments, nil
}

func (e *XLSXExtractor) getCellValue(cell *cell, sharedStrings []string) string {
	switch cell.T {
	case "s": // Shared string
		idx, err := strconv.Atoi(cell.V)
		if err != nil || idx >= len(sharedStrings) {
			return cell.V
		}
		return sharedStrings[idx]
	case "b": // Boolean
		if cell.V == "1" {
			return "TRUE"
		}
		return "FALSE"
	default:
		return cell.V
	}
}

func (e *XLSXExtractor) fallbackExtract(content []byte, sheetName string, startPos int) ([]Segment, error) {
	// Simple regex extraction
	valuePattern := regexp.MustCompile(`<v>([^<]+)</v>`)
	matches := valuePattern.FindAllSubmatch(content, -1)

	if len(matches) == 0 {
		return nil, nil
	}

	var values []string
	for _, match := range matches {
		if len(match) > 1 {
			values = append(values, string(match[1]))
		}
	}

	text := strings.Join(values, " | ")
	hash := sha256.Sum256([]byte(text))

	return []Segment{
		{
			ID:          hex.EncodeToString(hash[:16]),
			SegmentType: "table",
			Content:     text,
			Position:    startPos,
			Metadata: Metadata{
				Style: sheetName,
			},
		},
	}, nil
}
