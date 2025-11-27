// Package chunker provides code chunking by language.
// sql_chunker.go - SQL statement parser and chunker.
package chunker

import (
	"regexp"
	"strings"
)

// SQLChunker chunks SQL files into semantic units.
type SQLChunker struct {
	// Statement patterns
	patterns map[string]*regexp.Regexp
}

// NewSQLChunker creates a new SQL chunker.
func NewSQLChunker() *SQLChunker {
	return &SQLChunker{
		patterns: map[string]*regexp.Regexp{
			"create_table":     regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)`),
			"create_index":     regexp.MustCompile(`(?is)CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)`),
			"create_view":      regexp.MustCompile(`(?is)CREATE\s+(?:OR\s+REPLACE\s+)?VIEW\s+(\w+)`),
			"create_trigger":   regexp.MustCompile(`(?is)CREATE\s+TRIGGER\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)`),
			"create_function":  regexp.MustCompile(`(?is)CREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+(\w+)`),
			"create_procedure": regexp.MustCompile(`(?is)CREATE\s+(?:OR\s+REPLACE\s+)?PROCEDURE\s+(\w+)`),
			"select":           regexp.MustCompile(`(?is)^SELECT\s+`),
			"insert":           regexp.MustCompile(`(?is)^INSERT\s+INTO\s+(\w+)`),
			"update":           regexp.MustCompile(`(?is)^UPDATE\s+(\w+)`),
			"delete":           regexp.MustCompile(`(?is)^DELETE\s+FROM\s+(\w+)`),
			"alter":            regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+(\w+)`),
			"drop":             regexp.MustCompile(`(?is)^DROP\s+(\w+)\s+(?:IF\s+EXISTS\s+)?(\w+)`),
		},
	}
}

// ChunkFile parses a SQL file and returns semantic chunks.
func (c *SQLChunker) ChunkFile(path string) ([]*Chunk, error) {
	content, err := readFileContent(path)
	if err != nil {
		return nil, err
	}

	return c.ChunkContent(path, content)
}

// ChunkContent chunks SQL content directly.
func (c *SQLChunker) ChunkContent(path, content string) ([]*Chunk, error) {
	var chunks []*Chunk

	// Split into statements
	statements := c.splitStatements(content)
	srcLines := strings.Split(content, "\n")

	lineOffset := 0
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			lineOffset += strings.Count(stmt, "\n") + 1
			continue
		}

		chunk := c.parseStatement(stmt, path, srcLines, lineOffset)
		if chunk != nil {
			chunks = append(chunks, chunk)
		}

		lineOffset += strings.Count(stmt, "\n") + 1
	}

	return chunks, nil
}

// splitStatements splits SQL content into individual statements.
func (c *SQLChunker) splitStatements(content string) []string {
	var statements []string
	var current strings.Builder
	inString := false
	stringChar := rune(0)
	inComment := false
	inBlockComment := false

	runes := []rune(content)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}

		// Handle comments
		if !inString {
			if ch == '-' && next == '-' && !inBlockComment {
				inComment = true
			}
			if ch == '/' && next == '*' && !inComment {
				inBlockComment = true
			}
			if ch == '*' && next == '/' && inBlockComment {
				inBlockComment = false
				current.WriteRune(ch)
				current.WriteRune(next)
				i++
				continue
			}
			if ch == '\n' && inComment {
				inComment = false
			}
		}

		// Handle strings
		if !inComment && !inBlockComment {
			if (ch == '\'' || ch == '"') && !inString {
				inString = true
				stringChar = ch
			} else if ch == stringChar && inString {
				inString = false
			}
		}

		// Statement terminator
		if ch == ';' && !inString && !inComment && !inBlockComment {
			current.WriteRune(ch)
			statements = append(statements, current.String())
			current.Reset()
			continue
		}

		current.WriteRune(ch)
	}

	// Don't forget trailing statement without semicolon
	if remaining := strings.TrimSpace(current.String()); remaining != "" {
		statements = append(statements, remaining)
	}

	return statements
}

// parseStatement parses a single SQL statement into a chunk.
func (c *SQLChunker) parseStatement(stmt, path string, srcLines []string, lineOffset int) *Chunk {
	upperStmt := strings.ToUpper(strings.TrimSpace(stmt))

	var chunkType, name string
	var nodes []ASTNode

	// Detect statement type
	switch {
	case strings.HasPrefix(upperStmt, "CREATE TABLE"):
		chunkType = "create_table"
		name = c.extractName(stmt, c.patterns["create_table"])
		nodes = c.parseCreateTable(stmt)

	case strings.HasPrefix(upperStmt, "CREATE INDEX"), strings.HasPrefix(upperStmt, "CREATE UNIQUE INDEX"):
		chunkType = "create_index"
		name = c.extractName(stmt, c.patterns["create_index"])
		nodes = c.parseCreateIndex(stmt)

	case strings.HasPrefix(upperStmt, "CREATE VIEW"), strings.HasPrefix(upperStmt, "CREATE OR REPLACE VIEW"):
		chunkType = "create_view"
		name = c.extractName(stmt, c.patterns["create_view"])
		nodes = c.parseSelect(stmt)

	case strings.HasPrefix(upperStmt, "CREATE TRIGGER"):
		chunkType = "create_trigger"
		name = c.extractName(stmt, c.patterns["create_trigger"])
		nodes = c.parseTrigger(stmt)

	case strings.HasPrefix(upperStmt, "CREATE FUNCTION"), strings.HasPrefix(upperStmt, "CREATE OR REPLACE FUNCTION"):
		chunkType = "create_function"
		name = c.extractName(stmt, c.patterns["create_function"])
		nodes = c.parseFunction(stmt)

	case strings.HasPrefix(upperStmt, "CREATE PROCEDURE"), strings.HasPrefix(upperStmt, "CREATE OR REPLACE PROCEDURE"):
		chunkType = "create_procedure"
		name = c.extractName(stmt, c.patterns["create_procedure"])
		nodes = c.parseFunction(stmt)

	case strings.HasPrefix(upperStmt, "SELECT"):
		chunkType = "select"
		name = c.extractSelectName(stmt)
		nodes = c.parseSelect(stmt)

	case strings.HasPrefix(upperStmt, "INSERT"):
		chunkType = "insert"
		name = c.extractName(stmt, c.patterns["insert"])
		nodes = c.parseInsert(stmt)

	case strings.HasPrefix(upperStmt, "UPDATE"):
		chunkType = "update"
		name = c.extractName(stmt, c.patterns["update"])
		nodes = c.parseUpdate(stmt)

	case strings.HasPrefix(upperStmt, "DELETE"):
		chunkType = "delete"
		name = c.extractName(stmt, c.patterns["delete"])
		nodes = c.parseDelete(stmt)

	case strings.HasPrefix(upperStmt, "ALTER"):
		chunkType = "alter"
		name = c.extractName(stmt, c.patterns["alter"])
		nodes = c.parseAlter(stmt)

	case strings.HasPrefix(upperStmt, "DROP"):
		chunkType = "drop"
		matches := c.patterns["drop"].FindStringSubmatch(stmt)
		if len(matches) >= 3 {
			name = matches[2]
		}
		nodes = []ASTNode{{Type: "DropStmt", Depth: 1}}

	default:
		// Unknown statement type
		return nil
	}

	if name == "" {
		name = chunkType + "_" + hashContent(path, stmt)[:8]
	}

	stmtLines := strings.Count(stmt, "\n") + 1
	startLine := lineOffset + 1
	endLine := lineOffset + stmtLines

	// Extract tables referenced
	tables := c.extractTables(stmt)
	// Extract columns
	columns := c.extractColumns(stmt)

	chunk := &Chunk{
		FilePath:  path,
		Language:  "sql",
		Type:      chunkType,
		Name:      name,
		Content:   stmt,
		StartLine: startLine,
		EndLine:   endLine,
		ASTNodes:  nodes,
		Calls:     tables,  // Tables are like "calls" to other entities
		Fields:    columns, // Columns are like "fields"
	}

	chunk.Hash = hashContent(path, stmt)
	return chunk
}

// extractName extracts the name from a SQL statement using a pattern.
func (c *SQLChunker) extractName(stmt string, pattern *regexp.Regexp) string {
	matches := pattern.FindStringSubmatch(stmt)
	if len(matches) >= 2 {
		return strings.Trim(matches[1], "`\"[]")
	}
	return ""
}

// extractSelectName generates a name for SELECT statements.
func (c *SQLChunker) extractSelectName(stmt string) string {
	// Try to find the main table
	fromPattern := regexp.MustCompile(`(?i)FROM\s+(\w+)`)
	matches := fromPattern.FindStringSubmatch(stmt)
	if len(matches) >= 2 {
		return "select_" + strings.ToLower(matches[1])
	}
	return "select"
}

// parseCreateTable extracts AST nodes from CREATE TABLE.
func (c *SQLChunker) parseCreateTable(stmt string) []ASTNode {
	nodes := []ASTNode{
		{Type: "CreateTableStmt", Depth: 1},
	}

	// Extract column definitions
	colPattern := regexp.MustCompile(`(?i)(\w+)\s+(INTEGER|TEXT|REAL|BLOB|VARCHAR|INT|BIGINT|BOOLEAN|TIMESTAMP|DATE|DECIMAL|NUMERIC|FLOAT|DOUBLE)`)
	for _, match := range colPattern.FindAllStringSubmatch(stmt, -1) {
		nodes = append(nodes, ASTNode{
			Type:  "ColumnDef",
			Path:  match[1],
			Depth: 2,
		})
		nodes = append(nodes, ASTNode{
			Type:  "DataType_" + strings.ToUpper(match[2]),
			Depth: 3,
		})
	}

	// Constraints
	if regexp.MustCompile(`(?i)PRIMARY\s+KEY`).MatchString(stmt) {
		nodes = append(nodes, ASTNode{Type: "PrimaryKey", Depth: 2})
	}
	if regexp.MustCompile(`(?i)FOREIGN\s+KEY`).MatchString(stmt) {
		nodes = append(nodes, ASTNode{Type: "ForeignKey", Depth: 2})
	}
	if regexp.MustCompile(`(?i)UNIQUE`).MatchString(stmt) {
		nodes = append(nodes, ASTNode{Type: "UniqueConstraint", Depth: 2})
	}
	if regexp.MustCompile(`(?i)NOT\s+NULL`).MatchString(stmt) {
		nodes = append(nodes, ASTNode{Type: "NotNull", Depth: 3})
	}
	if regexp.MustCompile(`(?i)DEFAULT`).MatchString(stmt) {
		nodes = append(nodes, ASTNode{Type: "Default", Depth: 3})
	}
	if regexp.MustCompile(`(?i)CHECK`).MatchString(stmt) {
		nodes = append(nodes, ASTNode{Type: "CheckConstraint", Depth: 2})
	}

	return nodes
}

// parseCreateIndex extracts AST nodes from CREATE INDEX.
func (c *SQLChunker) parseCreateIndex(stmt string) []ASTNode {
	nodes := []ASTNode{
		{Type: "CreateIndexStmt", Depth: 1},
	}

	if regexp.MustCompile(`(?i)UNIQUE`).MatchString(stmt) {
		nodes = append(nodes, ASTNode{Type: "UniqueIndex", Depth: 2})
	}

	// Extract indexed columns
	colPattern := regexp.MustCompile(`(?i)ON\s+\w+\s*\(([^)]+)\)`)
	if matches := colPattern.FindStringSubmatch(stmt); len(matches) >= 2 {
		cols := strings.Split(matches[1], ",")
		for _, col := range cols {
			nodes = append(nodes, ASTNode{
				Type:  "IndexColumn",
				Path:  strings.TrimSpace(col),
				Depth: 2,
			})
		}
	}

	return nodes
}

// parseSelect extracts AST nodes from SELECT statements.
func (c *SQLChunker) parseSelect(stmt string) []ASTNode {
	nodes := []ASTNode{
		{Type: "SelectStmt", Depth: 1},
	}

	upperStmt := strings.ToUpper(stmt)

	// SELECT clause analysis
	if strings.Contains(upperStmt, "DISTINCT") {
		nodes = append(nodes, ASTNode{Type: "Distinct", Depth: 2})
	}

	// Aggregations
	aggregates := []string{"COUNT", "SUM", "AVG", "MAX", "MIN", "GROUP_CONCAT", "TOTAL"}
	for _, agg := range aggregates {
		pattern := regexp.MustCompile(`(?i)\b` + agg + `\s*\(`)
		if pattern.MatchString(stmt) {
			nodes = append(nodes, ASTNode{Type: "Aggregate_" + agg, Depth: 2})
		}
	}

	// JOINs
	joinTypes := []string{"INNER JOIN", "LEFT JOIN", "RIGHT JOIN", "FULL JOIN", "CROSS JOIN", "LEFT OUTER JOIN", "RIGHT OUTER JOIN"}
	for _, jt := range joinTypes {
		if strings.Contains(upperStmt, jt) {
			nodes = append(nodes, ASTNode{Type: strings.ReplaceAll(jt, " ", ""), Depth: 2})
		}
	}
	// Simple JOIN
	if regexp.MustCompile(`(?i)\bJOIN\b`).MatchString(stmt) && !strings.Contains(upperStmt, "INNER") && !strings.Contains(upperStmt, "LEFT") && !strings.Contains(upperStmt, "RIGHT") {
		nodes = append(nodes, ASTNode{Type: "Join", Depth: 2})
	}

	// Subquery
	if regexp.MustCompile(`(?i)\(\s*SELECT`).MatchString(stmt) {
		nodes = append(nodes, ASTNode{Type: "Subquery", Depth: 2})
	}

	// CTE (WITH clause)
	if regexp.MustCompile(`(?i)^\s*WITH\b`).MatchString(stmt) {
		nodes = append(nodes, ASTNode{Type: "CTE", Depth: 2})
	}

	// Window functions
	if regexp.MustCompile(`(?i)\bOVER\s*\(`).MatchString(stmt) {
		nodes = append(nodes, ASTNode{Type: "WindowFunction", Depth: 2})
	}

	// WHERE clause
	if strings.Contains(upperStmt, "WHERE") {
		nodes = append(nodes, ASTNode{Type: "WhereClause", Depth: 2})
		c.parseConditions(stmt, &nodes)
	}

	// GROUP BY
	if strings.Contains(upperStmt, "GROUP BY") {
		nodes = append(nodes, ASTNode{Type: "GroupBy", Depth: 2})
	}

	// HAVING
	if strings.Contains(upperStmt, "HAVING") {
		nodes = append(nodes, ASTNode{Type: "Having", Depth: 2})
	}

	// ORDER BY
	if strings.Contains(upperStmt, "ORDER BY") {
		nodes = append(nodes, ASTNode{Type: "OrderBy", Depth: 2})
	}

	// LIMIT
	if strings.Contains(upperStmt, "LIMIT") {
		nodes = append(nodes, ASTNode{Type: "Limit", Depth: 2})
	}

	// UNION/INTERSECT/EXCEPT
	if strings.Contains(upperStmt, "UNION") {
		nodes = append(nodes, ASTNode{Type: "Union", Depth: 2})
	}
	if strings.Contains(upperStmt, "INTERSECT") {
		nodes = append(nodes, ASTNode{Type: "Intersect", Depth: 2})
	}
	if strings.Contains(upperStmt, "EXCEPT") {
		nodes = append(nodes, ASTNode{Type: "Except", Depth: 2})
	}

	return nodes
}

// parseConditions adds condition-related nodes.
func (c *SQLChunker) parseConditions(stmt string, nodes *[]ASTNode) {
	upperStmt := strings.ToUpper(stmt)

	if strings.Contains(upperStmt, " AND ") {
		*nodes = append(*nodes, ASTNode{Type: "AndCondition", Depth: 3})
	}
	if strings.Contains(upperStmt, " OR ") {
		*nodes = append(*nodes, ASTNode{Type: "OrCondition", Depth: 3})
	}
	if strings.Contains(upperStmt, " IN ") || strings.Contains(upperStmt, " IN(") {
		*nodes = append(*nodes, ASTNode{Type: "InCondition", Depth: 3})
	}
	if strings.Contains(upperStmt, " LIKE ") {
		*nodes = append(*nodes, ASTNode{Type: "LikeCondition", Depth: 3})
	}
	if strings.Contains(upperStmt, " BETWEEN ") {
		*nodes = append(*nodes, ASTNode{Type: "BetweenCondition", Depth: 3})
	}
	if strings.Contains(upperStmt, " IS NULL") || strings.Contains(upperStmt, " IS NOT NULL") {
		*nodes = append(*nodes, ASTNode{Type: "NullCheck", Depth: 3})
	}
	if strings.Contains(upperStmt, " EXISTS") {
		*nodes = append(*nodes, ASTNode{Type: "ExistsCondition", Depth: 3})
	}
	if regexp.MustCompile(`(?i)\s+[<>=!]+\s*\(\s*SELECT`).MatchString(stmt) {
		*nodes = append(*nodes, ASTNode{Type: "ScalarSubquery", Depth: 3})
	}
}

// parseInsert extracts AST nodes from INSERT statements.
func (c *SQLChunker) parseInsert(stmt string) []ASTNode {
	nodes := []ASTNode{
		{Type: "InsertStmt", Depth: 1},
	}

	upperStmt := strings.ToUpper(stmt)

	if strings.Contains(upperStmt, "INSERT OR REPLACE") || strings.Contains(upperStmt, "INSERT OR IGNORE") {
		nodes = append(nodes, ASTNode{Type: "ConflictClause", Depth: 2})
	}

	if strings.Contains(upperStmt, "ON CONFLICT") {
		nodes = append(nodes, ASTNode{Type: "OnConflict", Depth: 2})
	}

	if strings.Contains(upperStmt, "VALUES") {
		nodes = append(nodes, ASTNode{Type: "ValuesClause", Depth: 2})
		// Count value groups
		valueGroups := strings.Count(stmt, "(") - 1 // Subtract column list
		if valueGroups > 1 {
			nodes = append(nodes, ASTNode{Type: "MultiRowInsert", Depth: 2})
		}
	}

	if regexp.MustCompile(`(?i)SELECT\b`).MatchString(stmt) {
		nodes = append(nodes, ASTNode{Type: "InsertSelect", Depth: 2})
	}

	if strings.Contains(upperStmt, "RETURNING") {
		nodes = append(nodes, ASTNode{Type: "Returning", Depth: 2})
	}

	return nodes
}

// parseUpdate extracts AST nodes from UPDATE statements.
func (c *SQLChunker) parseUpdate(stmt string) []ASTNode {
	nodes := []ASTNode{
		{Type: "UpdateStmt", Depth: 1},
	}

	upperStmt := strings.ToUpper(stmt)

	// Count SET assignments
	setPattern := regexp.MustCompile(`(?i)\bSET\b(.+?)(?:WHERE|$)`)
	if matches := setPattern.FindStringSubmatch(stmt); len(matches) >= 2 {
		assignments := strings.Count(matches[1], "=")
		if assignments > 1 {
			nodes = append(nodes, ASTNode{Type: "MultiColumnUpdate", Depth: 2})
		}
	}

	if strings.Contains(upperStmt, "WHERE") {
		nodes = append(nodes, ASTNode{Type: "WhereClause", Depth: 2})
		c.parseConditions(stmt, &nodes)
	}

	if strings.Contains(upperStmt, "RETURNING") {
		nodes = append(nodes, ASTNode{Type: "Returning", Depth: 2})
	}

	return nodes
}

// parseDelete extracts AST nodes from DELETE statements.
func (c *SQLChunker) parseDelete(stmt string) []ASTNode {
	nodes := []ASTNode{
		{Type: "DeleteStmt", Depth: 1},
	}

	upperStmt := strings.ToUpper(stmt)

	if strings.Contains(upperStmt, "WHERE") {
		nodes = append(nodes, ASTNode{Type: "WhereClause", Depth: 2})
		c.parseConditions(stmt, &nodes)
	} else {
		nodes = append(nodes, ASTNode{Type: "TruncatePattern", Depth: 2}) // DELETE without WHERE
	}

	if strings.Contains(upperStmt, "RETURNING") {
		nodes = append(nodes, ASTNode{Type: "Returning", Depth: 2})
	}

	return nodes
}

// parseAlter extracts AST nodes from ALTER statements.
func (c *SQLChunker) parseAlter(stmt string) []ASTNode {
	nodes := []ASTNode{
		{Type: "AlterStmt", Depth: 1},
	}

	upperStmt := strings.ToUpper(stmt)

	if strings.Contains(upperStmt, "ADD COLUMN") {
		nodes = append(nodes, ASTNode{Type: "AddColumn", Depth: 2})
	}
	if strings.Contains(upperStmt, "DROP COLUMN") {
		nodes = append(nodes, ASTNode{Type: "DropColumn", Depth: 2})
	}
	if strings.Contains(upperStmt, "RENAME") {
		nodes = append(nodes, ASTNode{Type: "Rename", Depth: 2})
	}
	if strings.Contains(upperStmt, "ADD CONSTRAINT") {
		nodes = append(nodes, ASTNode{Type: "AddConstraint", Depth: 2})
	}

	return nodes
}

// parseTrigger extracts AST nodes from CREATE TRIGGER.
func (c *SQLChunker) parseTrigger(stmt string) []ASTNode {
	nodes := []ASTNode{
		{Type: "CreateTriggerStmt", Depth: 1},
	}

	upperStmt := strings.ToUpper(stmt)

	if strings.Contains(upperStmt, "BEFORE") {
		nodes = append(nodes, ASTNode{Type: "BeforeTrigger", Depth: 2})
	}
	if strings.Contains(upperStmt, "AFTER") {
		nodes = append(nodes, ASTNode{Type: "AfterTrigger", Depth: 2})
	}
	if strings.Contains(upperStmt, "INSTEAD OF") {
		nodes = append(nodes, ASTNode{Type: "InsteadOfTrigger", Depth: 2})
	}

	if strings.Contains(upperStmt, "INSERT") {
		nodes = append(nodes, ASTNode{Type: "OnInsert", Depth: 2})
	}
	if strings.Contains(upperStmt, "UPDATE") {
		nodes = append(nodes, ASTNode{Type: "OnUpdate", Depth: 2})
	}
	if strings.Contains(upperStmt, "DELETE") {
		nodes = append(nodes, ASTNode{Type: "OnDelete", Depth: 2})
	}

	if strings.Contains(upperStmt, "FOR EACH ROW") {
		nodes = append(nodes, ASTNode{Type: "ForEachRow", Depth: 2})
	}

	return nodes
}

// parseFunction extracts AST nodes from CREATE FUNCTION/PROCEDURE.
func (c *SQLChunker) parseFunction(stmt string) []ASTNode {
	nodes := []ASTNode{
		{Type: "CreateFunctionStmt", Depth: 1},
	}

	upperStmt := strings.ToUpper(stmt)

	// Parameters
	paramPattern := regexp.MustCompile(`(?i)\(([^)]+)\)`)
	if matches := paramPattern.FindStringSubmatch(stmt); len(matches) >= 2 {
		params := strings.Split(matches[1], ",")
		for range params {
			nodes = append(nodes, ASTNode{Type: "Parameter", Depth: 2})
		}
	}

	if strings.Contains(upperStmt, "RETURNS") {
		nodes = append(nodes, ASTNode{Type: "ReturnType", Depth: 2})
	}

	if strings.Contains(upperStmt, "BEGIN") {
		nodes = append(nodes, ASTNode{Type: "BeginBlock", Depth: 2})
	}

	if strings.Contains(upperStmt, "DECLARE") {
		nodes = append(nodes, ASTNode{Type: "DeclareBlock", Depth: 2})
	}

	return nodes
}

// extractTables extracts table names from a SQL statement.
func (c *SQLChunker) extractTables(stmt string) []string {
	var tables []string
	seen := make(map[string]bool)

	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)FROM\s+(\w+)`),
		regexp.MustCompile(`(?i)JOIN\s+(\w+)`),
		regexp.MustCompile(`(?i)INTO\s+(\w+)`),
		regexp.MustCompile(`(?i)UPDATE\s+(\w+)`),
		regexp.MustCompile(`(?i)TABLE\s+(\w+)`),
	}

	for _, p := range patterns {
		for _, match := range p.FindAllStringSubmatch(stmt, -1) {
			if len(match) >= 2 {
				table := strings.ToLower(match[1])
				if !seen[table] && !isSQLKeyword(table) {
					seen[table] = true
					tables = append(tables, table)
				}
			}
		}
	}

	return tables
}

// extractColumns extracts column names from a SQL statement.
func (c *SQLChunker) extractColumns(stmt string) []string {
	var columns []string
	seen := make(map[string]bool)

	// SELECT columns (before FROM)
	selectPattern := regexp.MustCompile(`(?is)SELECT\s+(.+?)\s+FROM`)
	if matches := selectPattern.FindStringSubmatch(stmt); len(matches) >= 2 {
		cols := strings.Split(matches[1], ",")
		for _, col := range cols {
			col = strings.TrimSpace(col)
			// Remove aliases
			if idx := strings.LastIndex(strings.ToUpper(col), " AS "); idx != -1 {
				col = col[:idx]
			}
			col = strings.TrimSpace(col)
			// Skip aggregates and *
			if col != "*" && !regexp.MustCompile(`(?i)^\w+\s*\(`).MatchString(col) {
				// Extract column name (may have table prefix)
				parts := strings.Split(col, ".")
				colName := strings.ToLower(parts[len(parts)-1])
				if !seen[colName] && colName != "" {
					seen[colName] = true
					columns = append(columns, colName)
				}
			}
		}
	}

	return columns
}

// isSQLKeyword checks if a word is a SQL keyword.
func isSQLKeyword(word string) bool {
	keywords := map[string]bool{
		"select": true, "from": true, "where": true, "and": true, "or": true,
		"insert": true, "into": true, "values": true, "update": true, "set": true,
		"delete": true, "create": true, "table": true, "index": true, "view": true,
		"drop": true, "alter": true, "add": true, "column": true, "constraint": true,
		"primary": true, "key": true, "foreign": true, "references": true,
		"not": true, "null": true, "unique": true, "default": true, "check": true,
		"join": true, "inner": true, "left": true, "right": true, "outer": true,
		"on": true, "as": true, "order": true, "by": true, "group": true, "having": true,
		"limit": true, "offset": true, "union": true, "all": true, "distinct": true,
		"case": true, "when": true, "then": true, "else": true, "end": true,
		"exists": true, "in": true, "like": true, "between": true, "is": true,
	}
	return keywords[strings.ToLower(word)]
}

// ChunkDir chunks all SQL files in a directory.
func (c *SQLChunker) ChunkDir(root string) ([]*Chunk, error) {
	return chunkDirByExtension(root, []string{".sql"}, c.ChunkFile)
}
