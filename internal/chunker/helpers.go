// Package chunker provides code chunking by language.
// helpers.go - Shared helper functions for chunkers.
package chunker

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// readFileContent reads a file and returns its content.
func readFileContent(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// ChunkFileFunc is a function type for chunking a single file.
type ChunkFileFunc func(path string) ([]*Chunk, error)

// chunkDirByExtension walks a directory and chunks files matching extensions.
func chunkDirByExtension(root string, extensions []string, chunkFile ChunkFileFunc) ([]*Chunk, error) {
	var allChunks []*Chunk

	extMap := make(map[string]bool)
	for _, ext := range extensions {
		extMap[ext] = true
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden dirs and vendor
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}

		// Check extension
		ext := strings.ToLower(filepath.Ext(path))
		if !extMap[ext] {
			// Also check shebang for shell scripts
			if !isShellScript(path) {
				return nil
			}
		}

		chunks, err := chunkFile(path)
		if err != nil {
			// Log but don't fail
			return nil
		}

		allChunks = append(allChunks, chunks...)
		return nil
	})

	return allChunks, err
}

// isShellScript checks if a file is a shell script by shebang.
func isShellScript(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 128)
	n, err := f.Read(buf)
	if err != nil || n < 2 {
		return false
	}

	line := string(buf[:n])
	if !strings.HasPrefix(line, "#!") {
		return false
	}

	firstLine := strings.Split(line, "\n")[0]
	return strings.Contains(firstLine, "sh") || strings.Contains(firstLine, "bash") || strings.Contains(firstLine, "zsh")
}

// DetectLanguage detects the language of a file by extension or content.
func DetectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".go":
		return "go"
	case ".sql":
		return "sql"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".py":
		return "python"
	case ".js", ".jsx", ".ts", ".tsx":
		return "javascript"
	case ".java":
		return "java"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".xml":
		return "xml"
	case ".md", ".markdown":
		return "markdown"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cpp", ".hpp", ".cc", ".cxx":
		return "cpp"
	default:
		// Check shebang
		if isShellScript(path) {
			return "bash"
		}
		return "unknown"
	}
}

// Chunker is the interface for all language chunkers.
type Chunker interface {
	ChunkFile(path string) ([]*Chunk, error)
	ChunkDir(root string) ([]*Chunk, error)
}

// GetChunker returns the appropriate chunker for a language.
func GetChunker(language string) Chunker {
	switch language {
	case "go":
		return NewGoChunker()
	case "sql":
		return NewSQLChunker()
	case "bash", "sh", "shell":
		return NewBashChunker()
	default:
		return nil
	}
}

// MultiChunker chunks files based on their detected language.
type MultiChunker struct {
	chunkers map[string]Chunker
}

// NewMultiChunker creates a multi-language chunker.
func NewMultiChunker() *MultiChunker {
	return &MultiChunker{
		chunkers: map[string]Chunker{
			"go":   NewGoChunker(),
			"sql":  NewSQLChunker(),
			"bash": NewBashChunker(),
		},
	}
}

// ChunkFile chunks a file using the appropriate chunker.
func (m *MultiChunker) ChunkFile(path string) ([]*Chunk, error) {
	lang := DetectLanguage(path)
	chunker := m.chunkers[lang]
	if chunker == nil {
		return nil, nil // Skip unsupported languages
	}
	return chunker.ChunkFile(path)
}

// ChunkDir chunks all supported files in a directory.
func (m *MultiChunker) ChunkDir(root string) ([]*Chunk, error) {
	var allChunks []*Chunk

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden dirs and vendor
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}

		chunks, err := m.ChunkFile(path)
		if err != nil {
			// Log but don't fail
			return nil
		}

		allChunks = append(allChunks, chunks...)
		return nil
	})

	return allChunks, err
}
