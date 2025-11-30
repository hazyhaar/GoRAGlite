package extract

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"unicode"
)

// CodeExtractor extracts code segments with language-aware parsing.
type CodeExtractor struct {
	name    string
	version string
	parsers map[string]CodeParser
}

// CodeParser parses a specific language.
type CodeParser interface {
	Parse(content []byte) ([]CodeBlock, error)
}

// CodeBlock represents a parsed code block.
type CodeBlock struct {
	Type      string // function, class, method, import, comment, block
	Name      string
	Content   string
	StartLine int
	EndLine   int
	Level     int // nesting level
	Parent    string
	Language  string
	Metadata  map[string]string
}

// NewCodeExtractor creates a new code extractor.
func NewCodeExtractor() *CodeExtractor {
	ext := &CodeExtractor{
		name:    "code",
		version: "1.0.0",
		parsers: make(map[string]CodeParser),
	}

	// Register language parsers
	ext.parsers["go"] = &GoParser{}
	ext.parsers["python"] = &PythonParser{}
	ext.parsers["javascript"] = &JavaScriptParser{}
	ext.parsers["typescript"] = &JavaScriptParser{typescript: true}
	ext.parsers["bash"] = &BashParser{}
	ext.parsers["sql"] = &SQLParser{}
	ext.parsers["html"] = &HTMLParser{}
	ext.parsers["markdown"] = &MarkdownParser{}

	return ext
}

func (e *CodeExtractor) Name() string    { return e.name }
func (e *CodeExtractor) Version() string { return e.version }

func (e *CodeExtractor) SupportedTypes() []string {
	return []string{
		"text/x-go",
		"text/x-python",
		"text/javascript",
		"text/typescript",
		"application/javascript",
		"text/x-sh",
		"application/x-sh",
		"text/x-sql",
		"text/html",
		"text/markdown",
	}
}

func (e *CodeExtractor) Extract(ctx context.Context, content []byte, config json.RawMessage) ([]Segment, error) {
	var cfg struct {
		Language string `json:"language"`
	}
	if config != nil {
		json.Unmarshal(config, &cfg)
	}

	parser, ok := e.parsers[cfg.Language]
	if !ok {
		// Fallback to generic line-based parsing
		return e.genericParse(content, cfg.Language)
	}

	blocks, err := parser.Parse(content)
	if err != nil {
		return e.genericParse(content, cfg.Language)
	}

	return blocksToSegments(blocks, cfg.Language)
}

func (e *CodeExtractor) genericParse(content []byte, language string) ([]Segment, error) {
	var segments []Segment
	scanner := bufio.NewScanner(bytes.NewReader(content))

	var currentBlock strings.Builder
	blockStart := 1
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Detect block boundaries (empty lines)
		if trimmed == "" {
			if currentBlock.Len() > 0 {
				segments = append(segments, createCodeSegment(
					currentBlock.String(), "block", "", language, blockStart, lineNum-1,
				))
				currentBlock.Reset()
				blockStart = lineNum + 1
			}
			continue
		}

		currentBlock.WriteString(line)
		currentBlock.WriteString("\n")
	}

	// Last block
	if currentBlock.Len() > 0 {
		segments = append(segments, createCodeSegment(
			currentBlock.String(), "block", "", language, blockStart, lineNum,
		))
	}

	return segments, nil
}

func blocksToSegments(blocks []CodeBlock, language string) ([]Segment, error) {
	var segments []Segment
	for _, b := range blocks {
		segments = append(segments, createCodeSegment(
			b.Content, b.Type, b.Name, language, b.StartLine, b.EndLine,
		))
	}
	return segments, nil
}

func createCodeSegment(content, blockType, name, language string, startLine, endLine int) Segment {
	hash := sha256.Sum256([]byte(content))
	return Segment{
		ID:          hex.EncodeToString(hash[:16]),
		SegmentType: "code",
		Content:     content,
		Position:    startLine,
		Metadata: Metadata{
			Style:     blockType,
			Language:  language,
			LineStart: startLine,
			LineEnd:   endLine,
		},
	}
}

// GoParser parses Go source code using the go/ast package.
type GoParser struct{}

func (p *GoParser) Parse(content []byte) ([]CodeBlock, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var blocks []CodeBlock
	lines := strings.Split(string(content), "\n")

	// Extract imports
	for _, imp := range file.Imports {
		pos := fset.Position(imp.Pos())
		end := fset.Position(imp.End())
		blocks = append(blocks, CodeBlock{
			Type:      "import",
			Name:      imp.Path.Value,
			Content:   getLines(lines, pos.Line, end.Line),
			StartLine: pos.Line,
			EndLine:   end.Line,
			Language:  "go",
		})
	}

	// Extract functions and types
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			pos := fset.Position(x.Pos())
			end := fset.Position(x.End())
			name := x.Name.Name
			if x.Recv != nil {
				// Method
				name = fmt.Sprintf("(%s).%s", getReceiverType(x.Recv), name)
			}
			blocks = append(blocks, CodeBlock{
				Type:      "function",
				Name:      name,
				Content:   getLines(lines, pos.Line, end.Line),
				StartLine: pos.Line,
				EndLine:   end.Line,
				Language:  "go",
			})

		case *ast.TypeSpec:
			pos := fset.Position(x.Pos())
			end := fset.Position(x.End())
			blockType := "type"
			if _, ok := x.Type.(*ast.StructType); ok {
				blockType = "struct"
			} else if _, ok := x.Type.(*ast.InterfaceType); ok {
				blockType = "interface"
			}
			blocks = append(blocks, CodeBlock{
				Type:      blockType,
				Name:      x.Name.Name,
				Content:   getLines(lines, pos.Line, end.Line),
				StartLine: pos.Line,
				EndLine:   end.Line,
				Language:  "go",
			})
		}
		return true
	})

	return blocks, nil
}

func getReceiverType(fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return ""
	}
	switch t := fl.List[0].Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return "*" + id.Name
		}
	}
	return ""
}

func getLines(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}

// PythonParser parses Python source code.
type PythonParser struct{}

func (p *PythonParser) Parse(content []byte) ([]CodeBlock, error) {
	var blocks []CodeBlock
	lines := strings.Split(string(content), "\n")

	// Regex patterns for Python
	funcPattern := regexp.MustCompile(`^(\s*)def\s+(\w+)\s*\(`)
	classPattern := regexp.MustCompile(`^(\s*)class\s+(\w+)`)
	importPattern := regexp.MustCompile(`^(from\s+\S+\s+)?import\s+`)

	var currentBlock *CodeBlock
	var blockIndent int

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimRightFunc(line, unicode.IsSpace)
		indent := len(line) - len(strings.TrimLeft(line, " \t"))

		// Check for new block start
		if match := funcPattern.FindStringSubmatch(line); match != nil {
			if currentBlock != nil {
				currentBlock.EndLine = lineNum - 1
				currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
				blocks = append(blocks, *currentBlock)
			}
			currentBlock = &CodeBlock{
				Type:      "function",
				Name:      match[2],
				StartLine: lineNum,
				Level:     indent,
				Language:  "python",
			}
			blockIndent = indent
			continue
		}

		if match := classPattern.FindStringSubmatch(line); match != nil {
			if currentBlock != nil {
				currentBlock.EndLine = lineNum - 1
				currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
				blocks = append(blocks, *currentBlock)
			}
			currentBlock = &CodeBlock{
				Type:      "class",
				Name:      match[2],
				StartLine: lineNum,
				Level:     indent,
				Language:  "python",
			}
			blockIndent = indent
			continue
		}

		if importPattern.MatchString(trimmed) {
			blocks = append(blocks, CodeBlock{
				Type:      "import",
				Content:   trimmed,
				StartLine: lineNum,
				EndLine:   lineNum,
				Language:  "python",
			})
			continue
		}

		// Check if we're exiting a block
		if currentBlock != nil && trimmed != "" && indent <= blockIndent {
			currentBlock.EndLine = lineNum - 1
			currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
			blocks = append(blocks, *currentBlock)
			currentBlock = nil
		}
	}

	// Handle last block
	if currentBlock != nil {
		currentBlock.EndLine = len(lines)
		currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
		blocks = append(blocks, *currentBlock)
	}

	return blocks, nil
}

// JavaScriptParser parses JavaScript/TypeScript source code.
type JavaScriptParser struct {
	typescript bool
}

func (p *JavaScriptParser) Parse(content []byte) ([]CodeBlock, error) {
	var blocks []CodeBlock
	lines := strings.Split(string(content), "\n")

	// Regex patterns
	funcPattern := regexp.MustCompile(`^(\s*)(async\s+)?function\s+(\w+)`)
	arrowFuncPattern := regexp.MustCompile(`^(\s*)(const|let|var)\s+(\w+)\s*=\s*(async\s+)?\(`)
	classPattern := regexp.MustCompile(`^(\s*)(export\s+)?(default\s+)?class\s+(\w+)`)
	importPattern := regexp.MustCompile(`^import\s+`)
	exportPattern := regexp.MustCompile(`^export\s+(default\s+)?`)

	lang := "javascript"
	if p.typescript {
		lang = "typescript"
	}

	var braceCount int
	var currentBlock *CodeBlock

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Count braces for block detection
		braceCount += strings.Count(line, "{") - strings.Count(line, "}")

		// Imports
		if importPattern.MatchString(trimmed) {
			blocks = append(blocks, CodeBlock{
				Type:      "import",
				Content:   trimmed,
				StartLine: lineNum,
				EndLine:   lineNum,
				Language:  lang,
			})
			continue
		}

		// Functions
		if match := funcPattern.FindStringSubmatch(line); match != nil {
			if currentBlock != nil && braceCount <= 0 {
				currentBlock.EndLine = lineNum - 1
				currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
				blocks = append(blocks, *currentBlock)
			}
			currentBlock = &CodeBlock{
				Type:      "function",
				Name:      match[3],
				StartLine: lineNum,
				Language:  lang,
			}
			continue
		}

		// Arrow functions
		if match := arrowFuncPattern.FindStringSubmatch(line); match != nil {
			if currentBlock != nil && braceCount <= 0 {
				currentBlock.EndLine = lineNum - 1
				currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
				blocks = append(blocks, *currentBlock)
			}
			currentBlock = &CodeBlock{
				Type:      "function",
				Name:      match[3],
				StartLine: lineNum,
				Language:  lang,
			}
			continue
		}

		// Classes
		if match := classPattern.FindStringSubmatch(line); match != nil {
			if currentBlock != nil && braceCount <= 0 {
				currentBlock.EndLine = lineNum - 1
				currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
				blocks = append(blocks, *currentBlock)
			}
			currentBlock = &CodeBlock{
				Type:      "class",
				Name:      match[4],
				StartLine: lineNum,
				Language:  lang,
			}
			continue
		}

		// Check block end
		if currentBlock != nil && braceCount == 0 && strings.Contains(line, "}") {
			currentBlock.EndLine = lineNum
			currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
			blocks = append(blocks, *currentBlock)
			currentBlock = nil
		}
	}

	if currentBlock != nil {
		currentBlock.EndLine = len(lines)
		currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
		blocks = append(blocks, *currentBlock)
	}

	return blocks, nil
}

// BashParser parses Bash/Shell scripts.
type BashParser struct{}

func (p *BashParser) Parse(content []byte) ([]CodeBlock, error) {
	var blocks []CodeBlock
	lines := strings.Split(string(content), "\n")

	funcPattern := regexp.MustCompile(`^(\w+)\s*\(\)\s*\{?`)
	funcPattern2 := regexp.MustCompile(`^function\s+(\w+)`)

	var currentBlock *CodeBlock
	var braceCount int

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Skip comments
		if strings.HasPrefix(trimmed, "#") {
			if strings.HasPrefix(trimmed, "#!/") {
				blocks = append(blocks, CodeBlock{
					Type:      "shebang",
					Content:   trimmed,
					StartLine: lineNum,
					EndLine:   lineNum,
					Language:  "bash",
				})
			}
			continue
		}

		braceCount += strings.Count(line, "{") - strings.Count(line, "}")

		// Function definitions
		if match := funcPattern.FindStringSubmatch(trimmed); match != nil {
			if currentBlock != nil {
				currentBlock.EndLine = lineNum - 1
				currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
				blocks = append(blocks, *currentBlock)
			}
			currentBlock = &CodeBlock{
				Type:      "function",
				Name:      match[1],
				StartLine: lineNum,
				Language:  "bash",
			}
			continue
		}

		if match := funcPattern2.FindStringSubmatch(trimmed); match != nil {
			if currentBlock != nil {
				currentBlock.EndLine = lineNum - 1
				currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
				blocks = append(blocks, *currentBlock)
			}
			currentBlock = &CodeBlock{
				Type:      "function",
				Name:      match[1],
				StartLine: lineNum,
				Language:  "bash",
			}
			continue
		}

		if currentBlock != nil && braceCount == 0 && strings.Contains(line, "}") {
			currentBlock.EndLine = lineNum
			currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
			blocks = append(blocks, *currentBlock)
			currentBlock = nil
		}
	}

	if currentBlock != nil {
		currentBlock.EndLine = len(lines)
		currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
		blocks = append(blocks, *currentBlock)
	}

	return blocks, nil
}

// SQLParser parses SQL statements.
type SQLParser struct{}

func (p *SQLParser) Parse(content []byte) ([]CodeBlock, error) {
	var blocks []CodeBlock
	lines := strings.Split(string(content), "\n")

	// Patterns for SQL statements
	createTable := regexp.MustCompile(`(?i)^CREATE\s+(TABLE|VIEW|INDEX|FUNCTION|PROCEDURE|TRIGGER)`)
	selectStmt := regexp.MustCompile(`(?i)^SELECT\s+`)
	insertStmt := regexp.MustCompile(`(?i)^INSERT\s+`)
	updateStmt := regexp.MustCompile(`(?i)^UPDATE\s+`)
	deleteStmt := regexp.MustCompile(`(?i)^DELETE\s+`)

	var currentBlock *CodeBlock
	var inStatement bool

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)

		// Skip comments
		if strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		// Detect statement type
		var stmtType string
		if createTable.MatchString(trimmed) {
			stmtType = "create"
		} else if selectStmt.MatchString(trimmed) {
			stmtType = "select"
		} else if insertStmt.MatchString(trimmed) {
			stmtType = "insert"
		} else if updateStmt.MatchString(trimmed) {
			stmtType = "update"
		} else if deleteStmt.MatchString(trimmed) {
			stmtType = "delete"
		}

		if stmtType != "" && !inStatement {
			currentBlock = &CodeBlock{
				Type:      stmtType,
				StartLine: lineNum,
				Language:  "sql",
			}
			inStatement = true
		}

		// Check for statement end
		if inStatement && strings.HasSuffix(upper, ";") {
			if currentBlock != nil {
				currentBlock.EndLine = lineNum
				currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
				blocks = append(blocks, *currentBlock)
				currentBlock = nil
			}
			inStatement = false
		}
	}

	if currentBlock != nil {
		currentBlock.EndLine = len(lines)
		currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
		blocks = append(blocks, *currentBlock)
	}

	return blocks, nil
}

// HTMLParser parses HTML documents.
type HTMLParser struct{}

func (p *HTMLParser) Parse(content []byte) ([]CodeBlock, error) {
	var blocks []CodeBlock
	lines := strings.Split(string(content), "\n")

	// Patterns for HTML elements
	tagPattern := regexp.MustCompile(`<(\w+)([^>]*)>`)
	scriptPattern := regexp.MustCompile(`(?i)<script[^>]*>`)
	stylePattern := regexp.MustCompile(`(?i)<style[^>]*>`)
	templatePattern := regexp.MustCompile(`(?i)<template[^>]*>`)

	var currentBlock *CodeBlock
	var inBlock string

	for i, line := range lines {
		lineNum := i + 1

		// Script blocks
		if scriptPattern.MatchString(line) && inBlock == "" {
			currentBlock = &CodeBlock{
				Type:      "script",
				StartLine: lineNum,
				Language:  "html",
			}
			inBlock = "script"
			continue
		}

		if strings.Contains(strings.ToLower(line), "</script>") && inBlock == "script" {
			if currentBlock != nil {
				currentBlock.EndLine = lineNum
				currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
				blocks = append(blocks, *currentBlock)
				currentBlock = nil
			}
			inBlock = ""
			continue
		}

		// Style blocks
		if stylePattern.MatchString(line) && inBlock == "" {
			currentBlock = &CodeBlock{
				Type:      "style",
				StartLine: lineNum,
				Language:  "html",
			}
			inBlock = "style"
			continue
		}

		if strings.Contains(strings.ToLower(line), "</style>") && inBlock == "style" {
			if currentBlock != nil {
				currentBlock.EndLine = lineNum
				currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
				blocks = append(blocks, *currentBlock)
				currentBlock = nil
			}
			inBlock = ""
			continue
		}

		// Template blocks (htmx)
		if templatePattern.MatchString(line) && inBlock == "" {
			currentBlock = &CodeBlock{
				Type:      "template",
				StartLine: lineNum,
				Language:  "html",
			}
			inBlock = "template"
			continue
		}

		if strings.Contains(strings.ToLower(line), "</template>") && inBlock == "template" {
			if currentBlock != nil {
				currentBlock.EndLine = lineNum
				currentBlock.Content = getLines(lines, currentBlock.StartLine, currentBlock.EndLine)
				blocks = append(blocks, *currentBlock)
				currentBlock = nil
			}
			inBlock = ""
			continue
		}
	}

	// If no specific blocks, treat as single block
	if len(blocks) == 0 {
		blocks = append(blocks, CodeBlock{
			Type:      "document",
			Content:   string(content),
			StartLine: 1,
			EndLine:   len(lines),
			Language:  "html",
		})
	}

	return blocks, nil
}

// MarkdownParser parses Markdown documents.
type MarkdownParser struct{}

func (p *MarkdownParser) Parse(content []byte) ([]CodeBlock, error) {
	var blocks []CodeBlock
	lines := strings.Split(string(content), "\n")

	headingPattern := regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	codeBlockStart := regexp.MustCompile("^```(\\w*)")
	codeBlockEnd := regexp.MustCompile("^```$")

	var currentSection *CodeBlock
	var currentCodeBlock *CodeBlock
	var inCodeBlock bool
	var codeBlockLang string

	for i, line := range lines {
		lineNum := i + 1

		// Code block handling
		if match := codeBlockStart.FindStringSubmatch(line); match != nil && !inCodeBlock {
			inCodeBlock = true
			codeBlockLang = match[1]
			currentCodeBlock = &CodeBlock{
				Type:      "code_block",
				StartLine: lineNum,
				Language:  codeBlockLang,
			}
			continue
		}

		if codeBlockEnd.MatchString(line) && inCodeBlock {
			if currentCodeBlock != nil {
				currentCodeBlock.EndLine = lineNum
				currentCodeBlock.Content = getLines(lines, currentCodeBlock.StartLine, currentCodeBlock.EndLine)
				blocks = append(blocks, *currentCodeBlock)
				currentCodeBlock = nil
			}
			inCodeBlock = false
			codeBlockLang = ""
			continue
		}

		if inCodeBlock {
			continue
		}

		// Headings as sections
		if match := headingPattern.FindStringSubmatch(line); match != nil {
			if currentSection != nil {
				currentSection.EndLine = lineNum - 1
				currentSection.Content = getLines(lines, currentSection.StartLine, currentSection.EndLine)
				blocks = append(blocks, *currentSection)
			}

			currentSection = &CodeBlock{
				Type:      "section",
				Name:      match[2],
				StartLine: lineNum,
				Level:     len(match[1]),
				Language:  "markdown",
			}
			continue
		}
	}

	// Handle unclosed blocks
	if currentCodeBlock != nil {
		currentCodeBlock.EndLine = len(lines)
		currentCodeBlock.Content = getLines(lines, currentCodeBlock.StartLine, currentCodeBlock.EndLine)
		blocks = append(blocks, *currentCodeBlock)
	}

	if currentSection != nil {
		currentSection.EndLine = len(lines)
		currentSection.Content = getLines(lines, currentSection.StartLine, currentSection.EndLine)
		blocks = append(blocks, *currentSection)
	}

	return blocks, nil
}
