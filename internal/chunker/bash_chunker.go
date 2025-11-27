// Package chunker provides code chunking by language.
// bash_chunker.go - Bash/Shell script parser and chunker.
package chunker

import (
	"regexp"
	"strings"
)

// BashChunker chunks Bash/Shell script files.
type BashChunker struct {
	// Patterns for parsing
	funcPattern     *regexp.Regexp
	aliasPattern    *regexp.Regexp
	exportPattern   *regexp.Regexp
	variablePattern *regexp.Regexp
}

// NewBashChunker creates a new Bash chunker.
func NewBashChunker() *BashChunker {
	return &BashChunker{
		funcPattern:     regexp.MustCompile(`(?m)^(\w+)\s*\(\s*\)\s*\{`),
		aliasPattern:    regexp.MustCompile(`(?m)^alias\s+(\w+)=`),
		exportPattern:   regexp.MustCompile(`(?m)^export\s+(\w+)=`),
		variablePattern: regexp.MustCompile(`(?m)^(\w+)=`),
	}
}

// ChunkFile parses a Bash file and returns semantic chunks.
func (c *BashChunker) ChunkFile(path string) ([]*Chunk, error) {
	content, err := readFileContent(path)
	if err != nil {
		return nil, err
	}

	return c.ChunkContent(path, content)
}

// ChunkContent chunks Bash content directly.
func (c *BashChunker) ChunkContent(path, content string) ([]*Chunk, error) {
	var chunks []*Chunk
	lines := strings.Split(content, "\n")

	// First pass: find functions
	funcChunks := c.extractFunctions(content, path, lines)
	chunks = append(chunks, funcChunks...)

	// Second pass: find top-level constructs (aliases, exports, main blocks)
	otherChunks := c.extractTopLevel(content, path, lines, funcChunks)
	chunks = append(chunks, otherChunks...)

	// If no chunks found, treat the whole content as a snippet
	if len(chunks) == 0 && strings.TrimSpace(content) != "" {
		nodes := c.parseFunctionBody(content)
		chunk := &Chunk{
			FilePath:  path,
			Language:  "bash",
			Type:      "snippet",
			Name:      "snippet",
			Content:   content,
			StartLine: 1,
			EndLine:   len(lines),
			ASTNodes:  nodes,
			Calls:     c.extractCalls(content),
			Fields:    c.extractVariables(content),
		}
		chunk.Hash = hashContent(path, content)
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// extractFunctions finds all function definitions.
func (c *BashChunker) extractFunctions(content, path string, lines []string) []*Chunk {
	var chunks []*Chunk

	// Pattern for function definitions: name() { or function name {
	funcPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?m)^(\w+)\s*\(\s*\)\s*\{`),
		regexp.MustCompile(`(?m)^function\s+(\w+)\s*(?:\(\s*\))?\s*\{`),
	}

	for _, pattern := range funcPatterns {
		matches := pattern.FindAllStringSubmatchIndex(content, -1)
		for _, match := range matches {
			if len(match) < 4 {
				continue
			}

			funcName := content[match[2]:match[3]]
			startIdx := match[0]
			startLine := strings.Count(content[:startIdx], "\n") + 1

			// Find matching closing brace
			endIdx := c.findMatchingBrace(content, match[1]-1)
			if endIdx == -1 {
				endIdx = len(content)
			}
			endLine := strings.Count(content[:endIdx], "\n") + 1

			funcContent := content[startIdx:endIdx]

			// Parse function body for AST nodes
			nodes := c.parseFunctionBody(funcContent)
			calls := c.extractCalls(funcContent)
			vars := c.extractVariables(funcContent)

			chunk := &Chunk{
				FilePath:  path,
				Language:  "bash",
				Type:      "function",
				Name:      funcName,
				Content:   funcContent,
				StartLine: startLine,
				EndLine:   endLine,
				ASTNodes:  nodes,
				Calls:     calls,
				Fields:    vars,
			}
			chunk.Hash = hashContent(path, funcContent)
			chunks = append(chunks, chunk)
		}
	}

	return chunks
}

// extractTopLevel finds aliases, exports, and main script blocks.
func (c *BashChunker) extractTopLevel(content, path string, lines []string, funcChunks []*Chunk) []*Chunk {
	var chunks []*Chunk

	// Build a set of lines covered by functions
	coveredLines := make(map[int]bool)
	for _, fc := range funcChunks {
		for i := fc.StartLine; i <= fc.EndLine; i++ {
			coveredLines[i] = true
		}
	}

	// Find aliases
	aliasMatches := c.aliasPattern.FindAllStringSubmatchIndex(content, -1)
	for _, match := range aliasMatches {
		if len(match) < 4 {
			continue
		}
		lineNum := strings.Count(content[:match[0]], "\n") + 1
		if coveredLines[lineNum] {
			continue
		}

		aliasName := content[match[2]:match[3]]
		lineContent := lines[lineNum-1]
		endLine := lineNum

		// Handle multi-line aliases
		for strings.HasSuffix(strings.TrimSpace(lines[endLine-1]), "\\") && endLine < len(lines) {
			endLine++
		}

		aliasContent := strings.Join(lines[lineNum-1:endLine], "\n")

		chunk := &Chunk{
			FilePath:  path,
			Language:  "bash",
			Type:      "alias",
			Name:      aliasName,
			Content:   aliasContent,
			StartLine: lineNum,
			EndLine:   endLine,
			ASTNodes:  []ASTNode{{Type: "AliasDecl", Depth: 1}},
		}
		chunk.Hash = hashContent(path, lineContent)
		chunks = append(chunks, chunk)
	}

	// Find main script blocks (if/for/while/case at top level)
	mainBlocks := c.findMainBlocks(content, lines, coveredLines)
	for _, block := range mainBlocks {
		block.FilePath = path
		block.Language = "bash"
		block.Hash = hashContent(path, block.Content)
		chunks = append(chunks, block)
	}

	return chunks
}

// findMainBlocks finds top-level control structures.
func (c *BashChunker) findMainBlocks(content string, lines []string, coveredLines map[int]bool) []*Chunk {
	var chunks []*Chunk

	// Patterns for control structures
	controlPatterns := []struct {
		start   *regexp.Regexp
		end     string
		nodeTyp string
	}{
		{regexp.MustCompile(`(?m)^if\s+`), "fi", "IfBlock"},
		{regexp.MustCompile(`(?m)^for\s+`), "done", "ForLoop"},
		{regexp.MustCompile(`(?m)^while\s+`), "done", "WhileLoop"},
		{regexp.MustCompile(`(?m)^until\s+`), "done", "UntilLoop"},
		{regexp.MustCompile(`(?m)^case\s+`), "esac", "CaseBlock"},
	}

	for _, cp := range controlPatterns {
		matches := cp.start.FindAllStringIndex(content, -1)
		for _, match := range matches {
			startLine := strings.Count(content[:match[0]], "\n") + 1

			// Skip if inside a function
			if coveredLines[startLine] {
				continue
			}

			// Find the end keyword
			endLine := c.findEndKeyword(lines, startLine-1, cp.end)
			if endLine == -1 {
				continue
			}

			blockContent := strings.Join(lines[startLine-1:endLine], "\n")
			nodes := c.parseControlBlock(blockContent)

			chunk := &Chunk{
				Type:      "block",
				Name:      cp.nodeTyp + "_line" + string(rune('0'+startLine%10)),
				Content:   blockContent,
				StartLine: startLine,
				EndLine:   endLine,
				ASTNodes:  nodes,
				Calls:     c.extractCalls(blockContent),
				Fields:    c.extractVariables(blockContent),
			}
			chunks = append(chunks, chunk)
		}
	}

	return chunks
}

// findMatchingBrace finds the matching closing brace.
func (c *BashChunker) findMatchingBrace(content string, startIdx int) int {
	depth := 0
	inString := false
	stringChar := rune(0)
	escaped := false

	for i := startIdx; i < len(content); i++ {
		ch := rune(content[i])

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' {
			escaped = true
			continue
		}

		if !inString {
			if ch == '"' || ch == '\'' {
				inString = true
				stringChar = ch
			} else if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
				if depth == 0 {
					return i + 1
				}
			}
		} else {
			if ch == stringChar {
				inString = false
			}
		}
	}

	return -1
}

// findEndKeyword finds the line number of an end keyword.
func (c *BashChunker) findEndKeyword(lines []string, startLine int, endKeyword string) int {
	depth := 1
	startKeywords := map[string]bool{"if": true, "for": true, "while": true, "until": true, "case": true}

	for i := startLine + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])

		// Check for nested starts
		for kw := range startKeywords {
			if strings.HasPrefix(line, kw+" ") || strings.HasPrefix(line, kw+"\t") {
				depth++
				break
			}
		}

		// Check for end
		if line == endKeyword || strings.HasPrefix(line, endKeyword+" ") || strings.HasPrefix(line, endKeyword+";") {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}

	return -1
}

// parseFunctionBody extracts AST nodes from a function body.
func (c *BashChunker) parseFunctionBody(content string) []ASTNode {
	nodes := []ASTNode{
		{Type: "FunctionDecl", Depth: 1},
	}

	// Control flow
	if regexp.MustCompile(`(?m)\bif\b`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "IfStmt", Depth: 2})
	}
	if regexp.MustCompile(`(?m)\belif\b`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "ElifStmt", Depth: 2})
	}
	if regexp.MustCompile(`(?m)\belse\b`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "ElseStmt", Depth: 2})
	}
	if regexp.MustCompile(`(?m)\bfor\b`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "ForLoop", Depth: 2})
	}
	if regexp.MustCompile(`(?m)\bwhile\b`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "WhileLoop", Depth: 2})
	}
	if regexp.MustCompile(`(?m)\buntil\b`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "UntilLoop", Depth: 2})
	}
	if regexp.MustCompile(`(?m)\bcase\b`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "CaseStmt", Depth: 2})
	}

	// Operators and constructs
	if strings.Contains(content, "|") {
		nodes = append(nodes, ASTNode{Type: "Pipeline", Depth: 2})
	}
	if strings.Contains(content, "&&") {
		nodes = append(nodes, ASTNode{Type: "AndList", Depth: 2})
	}
	if strings.Contains(content, "||") {
		nodes = append(nodes, ASTNode{Type: "OrList", Depth: 2})
	}
	if strings.Contains(content, "$(") || strings.Contains(content, "`") {
		nodes = append(nodes, ASTNode{Type: "CommandSubst", Depth: 2})
	}
	if strings.Contains(content, "$((") {
		nodes = append(nodes, ASTNode{Type: "ArithmeticExpr", Depth: 2})
	}
	if regexp.MustCompile(`\$\{[^}]+\}`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "ParamExpansion", Depth: 2})
	}

	// Redirections
	if regexp.MustCompile(`[<>]`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "Redirection", Depth: 2})
	}
	if strings.Contains(content, "<<") {
		nodes = append(nodes, ASTNode{Type: "HereDoc", Depth: 2})
	}
	if strings.Contains(content, "2>&1") || strings.Contains(content, "&>") {
		nodes = append(nodes, ASTNode{Type: "StderrRedirect", Depth: 2})
	}

	// Arrays
	if regexp.MustCompile(`\w+\[`).MatchString(content) || regexp.MustCompile(`\$\{?\w+\[@\]`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "ArrayAccess", Depth: 2})
	}
	if regexp.MustCompile(`\(\s*[^)]+\s*\)`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "ArrayLiteral", Depth: 2})
	}

	// Special constructs
	if strings.Contains(content, "local ") {
		nodes = append(nodes, ASTNode{Type: "LocalVar", Depth: 2})
	}
	if strings.Contains(content, "return ") || strings.Contains(content, "return\n") {
		nodes = append(nodes, ASTNode{Type: "ReturnStmt", Depth: 2})
	}
	if strings.Contains(content, "exit ") || strings.Contains(content, "exit\n") {
		nodes = append(nodes, ASTNode{Type: "ExitStmt", Depth: 2})
	}
	if strings.Contains(content, "trap ") {
		nodes = append(nodes, ASTNode{Type: "TrapStmt", Depth: 2})
	}

	// Test constructs
	if regexp.MustCompile(`\[\s+`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "TestExpr", Depth: 2})
	}
	if regexp.MustCompile(`\[\[\s+`).MatchString(content) {
		nodes = append(nodes, ASTNode{Type: "ExtendedTest", Depth: 2})
	}

	return nodes
}

// parseControlBlock extracts AST nodes from a control block.
func (c *BashChunker) parseControlBlock(content string) []ASTNode {
	nodes := c.parseFunctionBody(content)
	// Remove FunctionDecl as it's not a function
	if len(nodes) > 0 && nodes[0].Type == "FunctionDecl" {
		nodes[0].Type = "ControlBlock"
	}
	return nodes
}

// extractCalls extracts command calls from Bash content.
func (c *BashChunker) extractCalls(content string) []string {
	var calls []string
	seen := make(map[string]bool)

	// Common command patterns
	cmdPattern := regexp.MustCompile(`(?m)(?:^|\||;|&&|\|\||` + "`" + `|\$\()\s*([a-zA-Z_][a-zA-Z0-9_-]*)(?:\s|$|;|\||&|>|<)`)

	for _, match := range cmdPattern.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 {
			cmd := match[1]
			// Skip shell builtins and keywords
			if !isBashKeyword(cmd) && !seen[cmd] {
				seen[cmd] = true
				calls = append(calls, cmd)
			}
		}
	}

	// Function calls (simple name followed by arguments or newline)
	funcCallPattern := regexp.MustCompile(`(?m)^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s+[^=\(\)]`)
	for _, match := range funcCallPattern.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 {
			fn := match[1]
			if !isBashKeyword(fn) && !seen[fn] {
				seen[fn] = true
				calls = append(calls, fn)
			}
		}
	}

	return calls
}

// extractVariables extracts variable names from Bash content.
func (c *BashChunker) extractVariables(content string) []string {
	var vars []string
	seen := make(map[string]bool)

	// Variable assignments
	assignPattern := regexp.MustCompile(`(?m)\b([A-Za-z_][A-Za-z0-9_]*)=`)
	for _, match := range assignPattern.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 && !seen[match[1]] {
			seen[match[1]] = true
			vars = append(vars, match[1])
		}
	}

	// Local variables
	localPattern := regexp.MustCompile(`(?m)local\s+([A-Za-z_][A-Za-z0-9_]*)`)
	for _, match := range localPattern.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 && !seen[match[1]] {
			seen[match[1]] = true
			vars = append(vars, match[1])
		}
	}

	return vars
}

// isBashKeyword checks if a word is a Bash keyword.
func isBashKeyword(word string) bool {
	keywords := map[string]bool{
		"if": true, "then": true, "else": true, "elif": true, "fi": true,
		"for": true, "while": true, "until": true, "do": true, "done": true,
		"case": true, "esac": true, "in": true,
		"function": true, "return": true, "exit": true,
		"local": true, "export": true, "declare": true, "readonly": true,
		"source": true, "eval": true, "exec": true,
		"true": true, "false": true,
		"break": true, "continue": true,
		"shift": true, "set": true, "unset": true,
		"echo": true, "printf": true, "read": true,
		"cd": true, "pwd": true, "pushd": true, "popd": true,
		"test": true,
	}
	return keywords[word]
}

// ChunkDir chunks all Bash files in a directory.
func (c *BashChunker) ChunkDir(root string) ([]*Chunk, error) {
	return chunkDirByExtension(root, []string{".sh", ".bash", ".zsh"}, c.ChunkFile)
}
