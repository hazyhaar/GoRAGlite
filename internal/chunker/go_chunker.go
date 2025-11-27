// Package chunker provides code chunking by language.
package chunker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Chunk represents a semantic unit of code.
type Chunk struct {
	FilePath  string
	Language  string
	Type      string // "function", "method", "type", "const", "var", "interface"
	Name      string
	Signature string
	Content   string
	StartLine int
	EndLine   int
	Hash      string

	// AST metadata for vectorization
	ASTNodes  []ASTNode
	Imports   []string
	Calls     []string // Function calls within this chunk
	Fields    []string // Struct fields or method receivers
}

// ASTNode represents a simplified AST node for vectorization.
type ASTNode struct {
	Path  string // e.g., "FuncDecl/Body/IfStmt/BinaryExpr"
	Type  string // Node type
	Depth int
}

// GoChunker chunks Go source files.
type GoChunker struct {
	fset *token.FileSet
}

// NewGoChunker creates a new Go chunker.
func NewGoChunker() *GoChunker {
	return &GoChunker{
		fset: token.NewFileSet(),
	}
}

// ChunkFile parses a Go file and returns semantic chunks.
func (c *GoChunker) ChunkFile(path string) ([]*Chunk, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	file, err := parser.ParseFile(c.fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var chunks []*Chunk
	srcLines := strings.Split(string(src), "\n")

	// Collect imports
	var imports []string
	for _, imp := range file.Imports {
		if imp.Path != nil {
			imports = append(imports, strings.Trim(imp.Path.Value, `"`))
		}
	}

	// Process declarations
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			chunk := c.chunkFunc(d, path, srcLines, imports)
			if chunk != nil {
				chunks = append(chunks, chunk)
			}

		case *ast.GenDecl:
			typeChunks := c.chunkGenDecl(d, path, srcLines, imports)
			chunks = append(chunks, typeChunks...)
		}
	}

	return chunks, nil
}

// ChunkDir recursively chunks all Go files in a directory.
func (c *GoChunker) ChunkDir(root string) ([]*Chunk, error) {
	var allChunks []*Chunk

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden dirs and vendor
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only .go files, skip tests for now
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		chunks, err := c.ChunkFile(path)
		if err != nil {
			// Log but don't fail on parse errors
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", path, err)
			return nil
		}

		allChunks = append(allChunks, chunks...)
		return nil
	})

	return allChunks, err
}

func (c *GoChunker) chunkFunc(fn *ast.FuncDecl, path string, srcLines []string, imports []string) *Chunk {
	if fn.Body == nil {
		return nil // Interface method or external
	}

	startPos := c.fset.Position(fn.Pos())
	endPos := c.fset.Position(fn.End())

	// Extract content
	content := extractLines(srcLines, startPos.Line, endPos.Line)

	// Determine type (function vs method)
	chunkType := "function"
	var receiver string
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		chunkType = "method"
		receiver = exprToString(fn.Recv.List[0].Type)
	}

	// Build signature
	sig := buildFuncSignature(fn)

	// Collect AST nodes for vectorization
	var nodes []ASTNode
	var calls []string
	ast.Inspect(fn, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		nodes = append(nodes, ASTNode{
			Type:  fmt.Sprintf("%T", n),
			Depth: nodeDepth(fn, n),
		})

		// Track function calls
		if call, ok := n.(*ast.CallExpr); ok {
			if ident := callName(call); ident != "" {
				calls = append(calls, ident)
			}
		}
		return true
	})

	// Build AST paths
	nodes = buildASTPaths(fn, nodes)

	chunk := &Chunk{
		FilePath:  path,
		Language:  "go",
		Type:      chunkType,
		Name:      fn.Name.Name,
		Signature: sig,
		Content:   content,
		StartLine: startPos.Line,
		EndLine:   endPos.Line,
		ASTNodes:  nodes,
		Imports:   imports,
		Calls:     calls,
	}

	if receiver != "" {
		chunk.Fields = []string{receiver}
	}

	chunk.Hash = hashContent(chunk.FilePath, chunk.Content)
	return chunk
}

func (c *GoChunker) chunkGenDecl(decl *ast.GenDecl, path string, srcLines []string, imports []string) []*Chunk {
	var chunks []*Chunk

	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			chunk := c.chunkTypeSpec(s, decl, path, srcLines, imports)
			if chunk != nil {
				chunks = append(chunks, chunk)
			}

		case *ast.ValueSpec:
			// Group const/var specs
			if len(s.Names) > 0 {
				chunk := c.chunkValueSpec(s, decl, path, srcLines, imports)
				if chunk != nil {
					chunks = append(chunks, chunk)
				}
			}
		}
	}

	return chunks
}

func (c *GoChunker) chunkTypeSpec(spec *ast.TypeSpec, decl *ast.GenDecl, path string, srcLines []string, imports []string) *Chunk {
	startPos := c.fset.Position(decl.Pos())
	endPos := c.fset.Position(decl.End())
	content := extractLines(srcLines, startPos.Line, endPos.Line)

	// Determine chunk type
	chunkType := "type"
	var fields []string

	switch t := spec.Type.(type) {
	case *ast.InterfaceType:
		chunkType = "interface"
		if t.Methods != nil {
			for _, m := range t.Methods.List {
				for _, name := range m.Names {
					fields = append(fields, name.Name)
				}
			}
		}
	case *ast.StructType:
		chunkType = "struct"
		if t.Fields != nil {
			for _, f := range t.Fields.List {
				for _, name := range f.Names {
					fields = append(fields, name.Name)
				}
			}
		}
	}

	// Collect AST nodes
	var nodes []ASTNode
	ast.Inspect(spec, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		nodes = append(nodes, ASTNode{
			Type:  fmt.Sprintf("%T", n),
			Depth: 0,
		})
		return true
	})

	chunk := &Chunk{
		FilePath:  path,
		Language:  "go",
		Type:      chunkType,
		Name:      spec.Name.Name,
		Content:   content,
		StartLine: startPos.Line,
		EndLine:   endPos.Line,
		ASTNodes:  nodes,
		Imports:   imports,
		Fields:    fields,
	}

	chunk.Hash = hashContent(chunk.FilePath, chunk.Content)
	return chunk
}

func (c *GoChunker) chunkValueSpec(spec *ast.ValueSpec, decl *ast.GenDecl, path string, srcLines []string, imports []string) *Chunk {
	startPos := c.fset.Position(decl.Pos())
	endPos := c.fset.Position(decl.End())
	content := extractLines(srcLines, startPos.Line, endPos.Line)

	chunkType := "var"
	if decl.Tok == token.CONST {
		chunkType = "const"
	}

	name := spec.Names[0].Name
	if len(spec.Names) > 1 {
		name = name + "..." // Multiple names in one decl
	}

	chunk := &Chunk{
		FilePath:  path,
		Language:  "go",
		Type:      chunkType,
		Name:      name,
		Content:   content,
		StartLine: startPos.Line,
		EndLine:   endPos.Line,
		Imports:   imports,
	}

	chunk.Hash = hashContent(chunk.FilePath, chunk.Content)
	return chunk
}

// Helper functions

func extractLines(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}

func hashContent(path, content string) string {
	h := sha256.New()
	h.Write([]byte(path))
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}

func buildFuncSignature(fn *ast.FuncDecl) string {
	var sb strings.Builder
	sb.WriteString("func ")

	// Receiver
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		sb.WriteString("(")
		sb.WriteString(exprToString(fn.Recv.List[0].Type))
		sb.WriteString(") ")
	}

	sb.WriteString(fn.Name.Name)
	sb.WriteString("(")

	// Parameters
	if fn.Type.Params != nil {
		var params []string
		for _, p := range fn.Type.Params.List {
			params = append(params, exprToString(p.Type))
		}
		sb.WriteString(strings.Join(params, ", "))
	}
	sb.WriteString(")")

	// Results
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		sb.WriteString(" ")
		if len(fn.Type.Results.List) > 1 {
			sb.WriteString("(")
		}
		var results []string
		for _, r := range fn.Type.Results.List {
			results = append(results, exprToString(r.Type))
		}
		sb.WriteString(strings.Join(results, ", "))
		if len(fn.Type.Results.List) > 1 {
			sb.WriteString(")")
		}
	}

	return sb.String()
}

func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprToString(e.X)
	case *ast.SelectorExpr:
		return exprToString(e.X) + "." + e.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprToString(e.Elt)
	case *ast.MapType:
		return "map[" + exprToString(e.Key) + "]" + exprToString(e.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func(...)"
	case *ast.ChanType:
		return "chan " + exprToString(e.Value)
	default:
		return "?"
	}
}

func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return exprToString(fn.X) + "." + fn.Sel.Name
	}
	return ""
}

func nodeDepth(root ast.Node, target ast.Node) int {
	depth := 0
	ast.Inspect(root, func(n ast.Node) bool {
		if n == target {
			return false
		}
		if n != nil {
			depth++
		}
		return true
	})
	return depth
}

func buildASTPaths(root ast.Node, nodes []ASTNode) []ASTNode {
	var result []ASTNode
	var path []string

	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
			return false
		}

		nodeType := strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast.")
		path = append(path, nodeType)

		result = append(result, ASTNode{
			Path:  strings.Join(path, "/"),
			Type:  nodeType,
			Depth: len(path),
		})

		return true
	})

	return result
}
