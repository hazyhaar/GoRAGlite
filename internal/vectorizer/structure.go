// Package vectorizer provides code vectorization algorithms.
package vectorizer

import (
	"hash/fnv"
	"math"
	"strings"

	"github.com/hazylab/goraglite/internal/chunker"
)

// StructureVectorizer creates vectors from AST structure.
// Enhanced with n-grams, control flow patterns, and Go idioms detection.
type StructureVectorizer struct {
	Dims     int    // Vector dimensions (default: 256)
	MaxDepth int    // Max AST depth to consider (default: 10)
	Seed     uint32 // Hash seed for reproducibility
}

// NewStructureVectorizer creates a new structure vectorizer.
func NewStructureVectorizer(dims int) *StructureVectorizer {
	if dims <= 0 {
		dims = 256
	}
	return &StructureVectorizer{
		Dims:     dims,
		MaxDepth: 10,
		Seed:     42,
	}
}

// Vectorize converts a chunk's AST structure into a vector.
func (v *StructureVectorizer) Vectorize(chunk *chunker.Chunk) []float32 {
	vec := make([]float32, v.Dims)

	// Feature Group 1: AST path unigrams (basic structure)
	v.addASTUnigrams(vec, chunk.ASTNodes, 0, v.Dims/4)

	// Feature Group 2: AST bigrams (node sequences)
	v.addASTBigrams(vec, chunk.ASTNodes, v.Dims/4, v.Dims/4)

	// Feature Group 3: Control flow patterns (language-aware)
	v.addControlFlowFeatures(vec, chunk.ASTNodes, v.Dims/2, v.Dims/8)

	// Feature Group 4: Language-specific idioms & patterns
	switch chunk.Language {
	case "sql":
		v.addSQLIdiomFeatures(vec, chunk, v.Dims/2+v.Dims/8, v.Dims/8)
	case "bash", "sh", "shell":
		v.addBashIdiomFeatures(vec, chunk, v.Dims/2+v.Dims/8, v.Dims/8)
	default:
		v.addGoIdiomFeatures(vec, chunk, v.Dims/2+v.Dims/8, v.Dims/8)
	}

	// Feature Group 5: Structural metrics
	v.addStructuralMetrics(vec, chunk, v.Dims-48, 32)

	// Feature Group 6: Chunk type encoding (language-aware)
	v.addChunkTypeFeatures(vec, chunk.Type, v.Dims-16, 16)

	// Normalize to unit vector
	normalize(vec)

	return vec
}

// addASTUnigrams adds single AST node type frequencies.
func (v *StructureVectorizer) addASTUnigrams(vec []float32, nodes []chunker.ASTNode, offset, dims int) {
	typeCounts := make(map[string]int)

	for _, node := range nodes {
		if node.Depth <= v.MaxDepth {
			typeCounts[node.Type]++
		}
	}

	// Hash into vector with signed hashing
	for typ, count := range typeCounts {
		idx := offset + (v.hash(typ) % dims)
		sign := v.hashSign(typ)
		vec[idx] += sign * float32(count)
	}
}

// addASTBigrams adds consecutive AST node pair frequencies.
// Captures patterns like "IfStmt→BinaryExpr" or "ForStmt→RangeStmt"
func (v *StructureVectorizer) addASTBigrams(vec []float32, nodes []chunker.ASTNode, offset, dims int) {
	if len(nodes) < 2 {
		return
	}

	bigramCounts := make(map[string]int)

	for i := 0; i < len(nodes)-1; i++ {
		if nodes[i].Depth <= v.MaxDepth && nodes[i+1].Depth <= v.MaxDepth {
			bigram := nodes[i].Type + "→" + nodes[i+1].Type
			bigramCounts[bigram]++
		}
	}

	for bigram, count := range bigramCounts {
		idx := offset + (v.hash(bigram) % dims)
		sign := v.hashSign(bigram)
		vec[idx] += sign * float32(count)
	}
}

// addControlFlowFeatures captures control flow patterns.
func (v *StructureVectorizer) addControlFlowFeatures(vec []float32, nodes []chunker.ASTNode, offset, dims int) {
	// Extract control flow sequence
	var cfNodes []string
	for _, node := range nodes {
		switch node.Type {
		case "IfStmt", "ForStmt", "RangeStmt", "SwitchStmt",
			"TypeSwitchStmt", "SelectStmt", "ReturnStmt",
			"BranchStmt", "DeferStmt", "GoStmt":
			cfNodes = append(cfNodes, node.Type)
		}
	}

	// Control flow unigrams
	cfCounts := make(map[string]int)
	for _, cf := range cfNodes {
		cfCounts[cf]++
	}
	for cf, count := range cfCounts {
		idx := offset + (v.hash("cf:"+cf) % (dims / 2))
		vec[idx] += float32(count)
	}

	// Control flow bigrams (sequences)
	for i := 0; i < len(cfNodes)-1; i++ {
		pattern := cfNodes[i] + "→" + cfNodes[i+1]
		idx := offset + dims/2 + (v.hash("cfseq:"+pattern) % (dims / 2))
		vec[idx] += 1.0
	}

	// Special patterns
	patterns := map[string]int{
		"nested_if":       countPattern(cfNodes, []string{"IfStmt", "IfStmt"}),
		"loop_with_break": countPattern(cfNodes, []string{"ForStmt", "BranchStmt"}),
		"defer_pattern":   cfCounts["DeferStmt"],
		"goroutine":       cfCounts["GoStmt"],
		"early_return":    countEarlyReturns(nodes),
	}

	patternOffset := offset + dims - 8
	i := 0
	for _, count := range patterns {
		if i < 8 && count > 0 {
			vec[patternOffset+i] = sigmoid(float32(count) / 3.0)
		}
		i++
	}
}

// addGoIdiomFeatures detects Go-specific coding patterns.
func (v *StructureVectorizer) addGoIdiomFeatures(vec []float32, chunk *chunker.Chunk, offset, dims int) {
	content := chunk.Content

	idioms := map[string]float32{
		// Error handling
		"err_check":      float32(strings.Count(content, "err != nil")),
		"err_return":     float32(strings.Count(content, "return err")),
		"errors_new":     float32(strings.Count(content, "errors.New")),
		"fmt_errorf":     float32(strings.Count(content, "fmt.Errorf")),
		"err_wrap":       float32(strings.Count(content, "errors.Wrap") + strings.Count(content, "%w")),

		// Concurrency
		"channel_make":   float32(strings.Count(content, "make(chan")),
		"channel_recv":   float32(strings.Count(content, "<-")),
		"mutex_lock":     float32(strings.Count(content, ".Lock()")),
		"waitgroup":      float32(strings.Count(content, "WaitGroup")),
		"context_use":    float32(strings.Count(content, "context.") + strings.Count(content, "ctx")),

		// Common patterns
		"defer_close":    float32(strings.Count(content, "defer") * boolToInt(strings.Contains(content, "Close"))),
		"nil_check":      float32(strings.Count(content, "== nil") + strings.Count(content, "!= nil")),
		"type_assert":    float32(strings.Count(content, ".(")),
		"interface_impl": float32(boolToInt(strings.Contains(content, "interface{}"))),
		"slice_append":   float32(strings.Count(content, "append(")),
		"map_access":     float32(strings.Count(content, ", ok :=") + strings.Count(content, ", ok =")),

		// Testing patterns
		"test_func":      float32(boolToInt(strings.HasPrefix(chunk.Name, "Test"))),
		"bench_func":     float32(boolToInt(strings.HasPrefix(chunk.Name, "Benchmark"))),
		"t_error":        float32(strings.Count(content, "t.Error") + strings.Count(content, "t.Fatal")),
	}

	i := 0
	for name, count := range idioms {
		if i >= dims {
			break
		}
		idx := offset + (v.hash("idiom:"+name) % dims)
		vec[idx] += sigmoid(count / 2.0)
		i++
	}
}

// addStructuralMetrics adds numeric structural features.
func (v *StructureVectorizer) addStructuralMetrics(vec []float32, chunk *chunker.Chunk, offset, dims int) {
	// Lines of code
	loc := float32(chunk.EndLine - chunk.StartLine + 1)
	vec[offset+0] = sigmoid(loc / 50.0)

	// AST node count
	nodeCount := float32(len(chunk.ASTNodes))
	vec[offset+1] = sigmoid(nodeCount / 100.0)

	// Max depth
	maxDepth := 0
	for _, n := range chunk.ASTNodes {
		if n.Depth > maxDepth {
			maxDepth = n.Depth
		}
	}
	vec[offset+2] = float32(maxDepth) / float32(v.MaxDepth)

	// Average depth
	var totalDepth int
	for _, n := range chunk.ASTNodes {
		totalDepth += n.Depth
	}
	if len(chunk.ASTNodes) > 0 {
		vec[offset+3] = float32(totalDepth) / float32(len(chunk.ASTNodes)) / float32(v.MaxDepth)
	}

	// Number of imports
	vec[offset+4] = sigmoid(float32(len(chunk.Imports)) / 10.0)

	// Number of calls
	vec[offset+5] = sigmoid(float32(len(chunk.Calls)) / 15.0)

	// Unique calls ratio
	uniqueCalls := make(map[string]bool)
	for _, call := range chunk.Calls {
		uniqueCalls[call] = true
	}
	if len(chunk.Calls) > 0 {
		vec[offset+6] = float32(len(uniqueCalls)) / float32(len(chunk.Calls))
	}

	// Cyclomatic complexity estimate
	branchCount := 0
	for _, n := range chunk.ASTNodes {
		switch n.Type {
		case "IfStmt", "ForStmt", "RangeStmt", "SwitchStmt",
			"TypeSwitchStmt", "SelectStmt", "CaseClause", "CommClause":
			branchCount++
		}
	}
	vec[offset+7] = sigmoid(float32(branchCount) / 10.0)

	// Node type diversity (entropy-like)
	typeCounts := make(map[string]int)
	for _, n := range chunk.ASTNodes {
		typeCounts[n.Type]++
	}
	vec[offset+8] = float32(len(typeCounts)) / 30.0 // Normalize by typical max types

	// Expression density
	exprCount := 0
	for _, n := range chunk.ASTNodes {
		if strings.HasSuffix(n.Type, "Expr") {
			exprCount++
		}
	}
	if len(chunk.ASTNodes) > 0 {
		vec[offset+9] = float32(exprCount) / float32(len(chunk.ASTNodes))
	}

	// Statement density
	stmtCount := 0
	for _, n := range chunk.ASTNodes {
		if strings.HasSuffix(n.Type, "Stmt") {
			stmtCount++
		}
	}
	if len(chunk.ASTNodes) > 0 {
		vec[offset+10] = float32(stmtCount) / float32(len(chunk.ASTNodes))
	}

	// Fields count (for structs/methods)
	vec[offset+11] = sigmoid(float32(len(chunk.Fields)) / 5.0)

	// Has receiver (method indicator)
	if chunk.Type == "method" {
		vec[offset+12] = 1.0
	}

	// Signature complexity (param count estimate from signature)
	if chunk.Signature != "" {
		paramCount := strings.Count(chunk.Signature, ",") + 1
		if strings.Contains(chunk.Signature, "()") {
			paramCount = 0
		}
		vec[offset+13] = sigmoid(float32(paramCount) / 5.0)
	}

	// Return complexity
	returnCount := 0
	for _, n := range chunk.ASTNodes {
		if n.Type == "ReturnStmt" {
			returnCount++
		}
	}
	vec[offset+14] = sigmoid(float32(returnCount) / 5.0)
}

// addChunkTypeFeatures encodes the chunk type.
func (v *StructureVectorizer) addChunkTypeFeatures(vec []float32, chunkType string, offset, dims int) {
	typeMap := map[string]int{
		"function":  0,
		"method":    1,
		"struct":    2,
		"interface": 3,
		"type":      4,
		"const":     5,
		"var":       6,
		"snippet":   7,
	}

	if idx, ok := typeMap[chunkType]; ok && idx < dims {
		vec[offset+idx] = 1.0
	}

	// Additional type-based features
	switch chunkType {
	case "function", "method":
		vec[offset+8] = 1.0 // Is callable
	case "struct", "interface", "type":
		vec[offset+9] = 1.0 // Is type definition
	case "const", "var":
		vec[offset+10] = 1.0 // Is value declaration
	}
}

// Helper functions

func (v *StructureVectorizer) hash(s string) int {
	h := fnv.New32a()
	h.Write([]byte{byte(v.Seed), byte(v.Seed >> 8)})
	h.Write([]byte(s))
	return int(h.Sum32())
}

func (v *StructureVectorizer) hashSign(s string) float32 {
	h := fnv.New32()
	h.Write([]byte(s))
	if h.Sum32()%2 == 0 {
		return 1.0
	}
	return -1.0
}

func countPattern(nodes []string, pattern []string) int {
	count := 0
	for i := 0; i <= len(nodes)-len(pattern); i++ {
		match := true
		for j, p := range pattern {
			if nodes[i+j] != p {
				match = false
				break
			}
		}
		if match {
			count++
		}
	}
	return count
}

func countEarlyReturns(nodes []chunker.ASTNode) int {
	count := 0
	inIf := false
	for _, n := range nodes {
		if n.Type == "IfStmt" {
			inIf = true
		} else if n.Type == "ReturnStmt" && inIf {
			count++
			inIf = false
		} else if n.Depth <= 2 {
			inIf = false
		}
	}
	return count
}

// addSQLIdiomFeatures detects SQL-specific patterns.
func (v *StructureVectorizer) addSQLIdiomFeatures(vec []float32, chunk *chunker.Chunk, offset, dims int) {
	content := strings.ToUpper(chunk.Content)

	idioms := map[string]float32{
		// Query patterns
		"select_star":     float32(strings.Count(content, "SELECT *")),
		"select_distinct": float32(strings.Count(content, "DISTINCT")),
		"subquery":        float32(strings.Count(content, "(SELECT")),
		"cte_with":        float32(boolToInt(strings.HasPrefix(strings.TrimSpace(content), "WITH"))),

		// Join patterns
		"inner_join":   float32(strings.Count(content, "INNER JOIN")),
		"left_join":    float32(strings.Count(content, "LEFT JOIN") + strings.Count(content, "LEFT OUTER")),
		"right_join":   float32(strings.Count(content, "RIGHT JOIN")),
		"cross_join":   float32(strings.Count(content, "CROSS JOIN")),
		"self_join":    float32(boolToInt(strings.Count(content, "JOIN") > 1 && strings.Count(content, "AS ") > 1)),
		"multi_join":   float32(boolToInt(strings.Count(content, "JOIN") >= 3)),

		// Aggregation patterns
		"group_by":     float32(strings.Count(content, "GROUP BY")),
		"having":       float32(strings.Count(content, "HAVING")),
		"count_agg":    float32(strings.Count(content, "COUNT(")),
		"sum_agg":      float32(strings.Count(content, "SUM(")),
		"avg_agg":      float32(strings.Count(content, "AVG(")),
		"window_func":  float32(strings.Count(content, "OVER(")),

		// Condition patterns
		"where_in":     float32(strings.Count(content, " IN (")),
		"where_like":   float32(strings.Count(content, " LIKE ")),
		"where_between":float32(strings.Count(content, " BETWEEN ")),
		"null_check":   float32(strings.Count(content, "IS NULL") + strings.Count(content, "IS NOT NULL")),
		"exists_check": float32(strings.Count(content, "EXISTS(")),
		"case_when":    float32(strings.Count(content, "CASE WHEN")),

		// DML patterns
		"insert_values":    float32(strings.Count(content, "INSERT INTO") * boolToInt(strings.Contains(content, "VALUES"))),
		"insert_select":    float32(strings.Count(content, "INSERT INTO") * boolToInt(strings.Contains(content, "SELECT"))),
		"update_set":       float32(strings.Count(content, "UPDATE") * boolToInt(strings.Contains(content, "SET"))),
		"upsert":           float32(strings.Count(content, "ON CONFLICT") + strings.Count(content, "ON DUPLICATE")),
		"returning":        float32(strings.Count(content, "RETURNING")),

		// DDL patterns
		"create_table":     float32(strings.Count(content, "CREATE TABLE")),
		"create_index":     float32(strings.Count(content, "CREATE INDEX") + strings.Count(content, "CREATE UNIQUE INDEX")),
		"foreign_key":      float32(strings.Count(content, "FOREIGN KEY") + strings.Count(content, "REFERENCES")),
		"cascade":          float32(strings.Count(content, "CASCADE")),

		// Safety patterns
		"transaction":      float32(strings.Count(content, "BEGIN") + strings.Count(content, "COMMIT") + strings.Count(content, "ROLLBACK")),
		"delete_where":     float32(strings.Count(content, "DELETE FROM") * boolToInt(strings.Contains(content, "WHERE"))),
		"update_where":     float32(strings.Count(content, "UPDATE") * boolToInt(strings.Contains(content, "WHERE"))),
	}

	i := 0
	for name, count := range idioms {
		if i >= dims {
			break
		}
		idx := offset + (v.hash("sql_idiom:"+name) % dims)
		vec[idx] += sigmoid(count / 2.0)
		i++
	}
}

// addBashIdiomFeatures detects Bash-specific patterns.
func (v *StructureVectorizer) addBashIdiomFeatures(vec []float32, chunk *chunker.Chunk, offset, dims int) {
	content := chunk.Content

	idioms := map[string]float32{
		// Variable patterns
		"var_assign":       float32(strings.Count(content, "=") - strings.Count(content, "==") - strings.Count(content, "!=")),
		"local_var":        float32(strings.Count(content, "local ")),
		"export_var":       float32(strings.Count(content, "export ")),
		"readonly_var":     float32(strings.Count(content, "readonly ")),
		"param_expansion":  float32(strings.Count(content, "${")),
		"default_value":    float32(strings.Count(content, ":-") + strings.Count(content, ":=")),
		"string_replace":   float32(strings.Count(content, "${") * boolToInt(strings.Contains(content, "/"))),

		// Command patterns
		"pipe":             float32(strings.Count(content, "|") - strings.Count(content, "||")),
		"and_list":         float32(strings.Count(content, "&&")),
		"or_list":          float32(strings.Count(content, "||")),
		"cmd_subst_dollar": float32(strings.Count(content, "$(")),
		"cmd_subst_back":   float32(strings.Count(content, "`")),
		"background":       float32(strings.Count(content, "&") - strings.Count(content, "&&") - strings.Count(content, ">&")),

		// Redirection patterns
		"stdout_file":      float32(strings.Count(content, ">") - strings.Count(content, ">>") - strings.Count(content, ">&") - strings.Count(content, "<")),
		"stdout_append":    float32(strings.Count(content, ">>")),
		"stdin_file":       float32(strings.Count(content, "<") - strings.Count(content, "<<") - strings.Count(content, "<(")),
		"heredoc":          float32(strings.Count(content, "<<")),
		"stderr_redirect":  float32(strings.Count(content, "2>") + strings.Count(content, "2>&1") + strings.Count(content, "&>")),
		"process_subst":    float32(strings.Count(content, "<(") + strings.Count(content, ">(")),

		// Control flow
		"if_then":          float32(strings.Count(content, "if ") + strings.Count(content, "if[")),
		"elif_then":        float32(strings.Count(content, "elif ")),
		"for_loop":         float32(strings.Count(content, "for ")),
		"while_loop":       float32(strings.Count(content, "while ")),
		"until_loop":       float32(strings.Count(content, "until ")),
		"case_stmt":        float32(strings.Count(content, "case ")),
		"select_stmt":      float32(strings.Count(content, "select ")),

		// Test patterns
		"test_bracket":     float32(strings.Count(content, "[ ")),
		"test_double":      float32(strings.Count(content, "[[ ")),
		"test_file":        float32(strings.Count(content, "-f ") + strings.Count(content, "-d ") + strings.Count(content, "-e ")),
		"test_string":      float32(strings.Count(content, "-z ") + strings.Count(content, "-n ")),
		"test_numeric":     float32(strings.Count(content, "-eq ") + strings.Count(content, "-ne ") + strings.Count(content, "-lt ") + strings.Count(content, "-gt ")),
		"regex_match":      float32(strings.Count(content, "=~")),

		// Error handling
		"exit_code":        float32(strings.Count(content, "$?")),
		"set_e":            float32(strings.Count(content, "set -e")),
		"set_u":            float32(strings.Count(content, "set -u")),
		"set_o":            float32(strings.Count(content, "set -o")),
		"trap_signal":      float32(strings.Count(content, "trap ")),
		"exit_stmt":        float32(strings.Count(content, "exit ")),
		"return_stmt":      float32(strings.Count(content, "return ")),

		// Array patterns
		"array_decl":       float32(strings.Count(content, "=(")),
		"array_access":     float32(strings.Count(content, "[@]") + strings.Count(content, "[*]")),
		"array_length":     float32(strings.Count(content, "${#")),

		// Common commands
		"echo_cmd":         float32(strings.Count(content, "echo ")),
		"printf_cmd":       float32(strings.Count(content, "printf ")),
		"read_cmd":         float32(strings.Count(content, "read ")),
		"cd_cmd":           float32(strings.Count(content, "cd ")),
		"source_cmd":       float32(strings.Count(content, "source ") + strings.Count(content, ". ")),

		// Functions
		"func_decl":        float32(boolToInt(strings.Contains(content, "() {") || strings.Contains(content, "function "))),
	}

	i := 0
	for name, count := range idioms {
		if i >= dims {
			break
		}
		idx := offset + (v.hash("bash_idiom:"+name) % dims)
		vec[idx] += sigmoid(count / 2.0)
		i++
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func normalize(vec []float32) {
	var norm float32
	for _, v := range vec {
		norm += v * v
	}
	if norm > 0 {
		norm = float32(math.Sqrt(float64(norm)))
		for i := range vec {
			vec[i] /= norm
		}
	}
}

func sigmoid(x float32) float32 {
	return float32(1.0 / (1.0 + math.Exp(-float64(x))))
}

// VectorizeQuery vectorizes a code query.
func (v *StructureVectorizer) VectorizeQuery(queryChunk *chunker.Chunk) []float32 {
	return v.Vectorize(queryChunk)
}

// ErrUnsupportedLanguage is returned for unsupported languages.
var ErrUnsupportedLanguage = &UnsupportedLanguageError{}

type UnsupportedLanguageError struct{}

func (e *UnsupportedLanguageError) Error() string {
	return "unsupported language"
}
