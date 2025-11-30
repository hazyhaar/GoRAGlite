package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"goraglite/internal/db"
)

// Engine executes workflows.
// Each run is isolated in its own database file.
type Engine struct {
	corpusDB    *db.DB
	workflowsDB *db.DB
	runsDir     string
	extractors  map[string]Extractor
	vectorizers map[string]Vectorizer
}

// Extractor is implemented by external extractors (PDF, DOCX, etc.)
type Extractor interface {
	Name() string
	Version() string
	Extract(ctx context.Context, content []byte, config json.RawMessage) ([]ExtractedSegment, error)
}

// ExtractedSegment is the output of an extractor.
type ExtractedSegment struct {
	ID           string  `json:"id"`
	FileID       string  `json:"file_id"`
	SegmentType  string  `json:"segment_type"`
	Content      string  `json:"content"`
	Page         *int    `json:"page,omitempty"`
	Position     int     `json:"position"`
	BBox         string  `json:"bbox,omitempty"`
	Confidence   float64 `json:"confidence,omitempty"`
}

// Vectorizer creates vectors from content or features.
type Vectorizer interface {
	Name() string
	Version() string
	Vectorize(ctx context.Context, input VectorizerInput) ([]float32, error)
}

// VectorizerInput provides data to vectorizers.
type VectorizerInput struct {
	Content  string             `json:"content"`
	Features map[string]float64 `json:"features,omitempty"`
	Config   json.RawMessage    `json:"config"`
}

// NewEngine creates a new workflow engine.
func NewEngine(corpusDB, workflowsDB *db.DB, runsDir string) *Engine {
	return &Engine{
		corpusDB:    corpusDB,
		workflowsDB: workflowsDB,
		runsDir:     runsDir,
		extractors:  make(map[string]Extractor),
		vectorizers: make(map[string]Vectorizer),
	}
}

// RegisterExtractor registers an extractor for use in workflows.
func (e *Engine) RegisterExtractor(ext Extractor) {
	e.extractors[ext.Name()] = ext
}

// RegisterVectorizer registers a vectorizer for use in workflows.
func (e *Engine) RegisterVectorizer(vec Vectorizer) {
	e.vectorizers[vec.Name()] = vec
}

// LoadWorkflow loads a workflow definition from the database.
func (e *Engine) LoadWorkflow(ctx context.Context, workflowID string) (*Workflow, error) {
	var w Workflow
	var inputSchema, outputSchema sql.NullString

	err := e.workflowsDB.QueryRowContext(ctx, `
		SELECT id, name, version, description, input_schema, output_schema, status, created_at, updated_at
		FROM workflows
		WHERE id = ? AND status = 'active'
		ORDER BY version DESC
		LIMIT 1
	`, workflowID).Scan(
		&w.ID, &w.Name, &w.Version, &w.Description,
		&inputSchema, &outputSchema, &w.Status,
		&w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("load workflow %s: %w", workflowID, err)
	}

	if inputSchema.Valid {
		w.InputSchema = json.RawMessage(inputSchema.String)
	}
	if outputSchema.Valid {
		w.OutputSchema = json.RawMessage(outputSchema.String)
	}

	// Load steps
	rows, err := e.workflowsDB.QueryContext(ctx, `
		SELECT workflow_id, step_order, step_name, operation, source, predicate, output, config, expects_delta, on_empty
		FROM workflow_steps
		WHERE workflow_id = ?
		ORDER BY step_order
	`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("load workflow steps: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var s Step
		var predicate, config sql.NullString

		err := rows.Scan(
			&s.WorkflowID, &s.StepOrder, &s.StepName,
			&s.Operation, &s.Source, &predicate,
			&s.Output, &config, &s.ExpectsDelta, &s.OnEmpty,
		)
		if err != nil {
			return nil, fmt.Errorf("scan step: %w", err)
		}

		if predicate.Valid {
			s.Predicate = predicate.String
		}
		if config.Valid {
			s.Config = json.RawMessage(config.String)
		}

		w.Steps = append(w.Steps, s)
	}

	return &w, nil
}

// Run executes a workflow and returns the run ID.
func (e *Engine) Run(ctx context.Context, workflowID string, cfg RunConfig) (*Run, error) {
	// Load workflow
	workflow, err := e.LoadWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}

	// Create run
	run := &Run{
		ID:              uuid.New().String(),
		WorkflowID:      workflow.ID,
		WorkflowVersion: workflow.Version,
		StartedAt:       time.Now(),
		Status:          RunStatusRunning,
		WorkerID:        "worker-1", // TODO: from config
		Config:          cfg,
	}

	// Create run database
	runDB, err := db.CreateRun(e.runsDir, run.ID)
	if err != nil {
		return nil, fmt.Errorf("create run db: %w", err)
	}
	run.DBPath = runDB.Path()
	defer runDB.Close()

	// Initialize run metadata
	if err := e.initRunMeta(ctx, runDB, run, workflow); err != nil {
		return nil, fmt.Errorf("init run meta: %w", err)
	}

	// Attach corpus for reading
	if err := runDB.Attach(ctx, e.corpusDB.Path(), "corpus"); err != nil {
		return nil, fmt.Errorf("attach corpus: %w", err)
	}
	defer runDB.Detach(ctx, "corpus")

	// Execute steps
	var lastStepOutput string
	for i, step := range workflow.Steps {
		execution, err := e.executeStep(ctx, runDB, run, &step, lastStepOutput, i > 0)
		if err != nil {
			run.Status = RunStatusFailed
			e.updateRunStatus(ctx, runDB, run)
			return run, fmt.Errorf("step %d (%s): %w", step.StepOrder, step.StepName, err)
		}

		// Log execution
		if err := e.logStepExecution(ctx, runDB, execution); err != nil {
			return nil, fmt.Errorf("log step execution: %w", err)
		}

		// Handle empty results
		if execution.RowsOut == 0 {
			switch step.OnEmpty {
			case OnEmptyFail:
				run.Status = RunStatusFailed
				e.updateRunStatus(ctx, runDB, run)
				return run, fmt.Errorf("step %d produced no results", step.StepOrder)
			case OnEmptySkipRemaining:
				break
			}
		}

		lastStepOutput = step.Output
	}

	// Finalize
	run.Status = RunStatusCompleted
	run.FinishedAt = time.Now()
	e.updateRunStatus(ctx, runDB, run)

	return run, nil
}

// initRunMeta initializes the run metadata in the run database.
func (e *Engine) initRunMeta(ctx context.Context, runDB *db.DB, run *Run, workflow *Workflow) error {
	configJSON, _ := json.Marshal(run.Config)

	_, err := runDB.ExecContext(ctx, `
		INSERT INTO _run_meta (run_id, workflow_id, workflow_version, input_source, started_at, status, worker_id, config)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, run.ID, run.WorkflowID, run.WorkflowVersion, run.InputSource, run.StartedAt, run.Status, run.WorkerID, string(configJSON))
	if err != nil {
		return err
	}

	// Copy workflow steps
	for _, step := range workflow.Steps {
		_, err := runDB.ExecContext(ctx, `
			INSERT INTO _workflow_steps (step_order, step_name, operation, source, predicate, output, config, expects_delta, on_empty)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, step.StepOrder, step.StepName, step.Operation, step.Source, step.Predicate, step.Output, string(step.Config), step.ExpectsDelta, step.OnEmpty)
		if err != nil {
			return err
		}
	}

	return nil
}

// executeStep executes a single workflow step.
func (e *Engine) executeStep(ctx context.Context, runDB *db.DB, run *Run, step *Step, prevOutput string, hasPrev bool) (*StepExecution, error) {
	exec := &StepExecution{
		StepOrder:   step.StepOrder,
		StepName:    step.StepName,
		StartedAt:   time.Now(),
		OutputTable: step.Output,
	}

	// Determine source table
	source := step.Source
	if source == "_input" && hasPrev {
		source = prevOutput
	}

	// Count input rows
	if source != "" && source != "_input" {
		exists, _ := runDB.TableExists(ctx, source)
		if exists {
			exec.RowsIn, _ = runDB.RowCount(ctx, source)
		}
	}

	// Execute based on operation type
	var err error
	switch step.Operation {
	case OpFilter:
		err = e.executeFilter(ctx, runDB, step, source)
	case OpProject:
		err = e.executeProject(ctx, runDB, step, source)
	case OpJoin:
		err = e.executeJoin(ctx, runDB, step, source)
	case OpAggregate:
		err = e.executeAggregate(ctx, runDB, step, source)
	case OpWindow:
		err = e.executeWindow(ctx, runDB, step, source)
	case OpHash:
		err = e.executeHash(ctx, runDB, step, source)
	case OpVectorize:
		err = e.executeVectorize(ctx, runDB, step, source)
	case OpExternal:
		err = e.executeExternal(ctx, runDB, step, source)
	default:
		err = fmt.Errorf("unknown operation: %s", step.Operation)
	}

	exec.FinishedAt = time.Now()
	exec.DurationMs = exec.FinishedAt.Sub(exec.StartedAt).Milliseconds()

	if err != nil {
		exec.Error = err.Error()
		return exec, err
	}

	// Count output rows
	exec.RowsOut, _ = runDB.RowCount(ctx, step.Output)

	// Calculate delta
	if exec.RowsIn > 0 {
		exec.DeltaScore = 1.0 - float64(exec.RowsOut)/float64(exec.RowsIn)
	}

	return exec, nil
}

// executeFilter executes a filter operation (WHERE clause).
func (e *Engine) executeFilter(ctx context.Context, runDB *db.DB, step *Step, source string) error {
	predicate := step.Predicate
	if predicate == "" {
		predicate = "1=1" // No filter
	}

	query := fmt.Sprintf(`
		CREATE TABLE %s AS
		SELECT * FROM %s
		WHERE %s
	`, step.Output, source, predicate)

	_, err := runDB.ExecContext(ctx, query)
	return err
}

// executeProject executes a project operation (SELECT columns).
func (e *Engine) executeProject(ctx context.Context, runDB *db.DB, step *Step, source string) error {
	columns := step.Predicate
	if columns == "" || columns == "*" {
		columns = "*"
	}

	query := fmt.Sprintf(`
		CREATE TABLE %s AS
		SELECT %s FROM %s
	`, step.Output, columns, source)

	_, err := runDB.ExecContext(ctx, query)
	return err
}

// executeJoin executes a join operation.
func (e *Engine) executeJoin(ctx context.Context, runDB *db.DB, step *Step, source string) error {
	// Predicate contains the JOIN clause
	query := fmt.Sprintf(`
		CREATE TABLE %s AS
		SELECT * FROM %s
		%s
	`, step.Output, source, step.Predicate)

	_, err := runDB.ExecContext(ctx, query)
	return err
}

// executeAggregate executes an aggregate operation.
func (e *Engine) executeAggregate(ctx context.Context, runDB *db.DB, step *Step, source string) error {
	var cfg struct {
		Features []FeatureSpec `json:"features"`
	}

	if step.Config != nil {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return fmt.Errorf("parse aggregate config: %w", err)
		}
	}

	if len(cfg.Features) > 0 {
		// Feature extraction mode
		var featureCols []string
		for _, f := range cfg.Features {
			featureCols = append(featureCols, fmt.Sprintf("(%s) AS %s", f.Expr, f.Name))
		}

		query := fmt.Sprintf(`
			CREATE TABLE %s AS
			SELECT *, %s FROM %s
		`, step.Output, strings.Join(featureCols, ", "), source)

		_, err := runDB.ExecContext(ctx, query)
		return err
	}

	// Standard aggregate with GROUP BY
	query := fmt.Sprintf(`
		CREATE TABLE %s AS
		SELECT %s FROM %s
	`, step.Output, step.Predicate, source)

	_, err := runDB.ExecContext(ctx, query)
	return err
}

// executeWindow executes a window operation (chunking).
func (e *Engine) executeWindow(ctx context.Context, runDB *db.DB, step *Step, source string) error {
	var cfg WindowConfig
	if step.Config != nil {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return fmt.Errorf("parse window config: %w", err)
		}
	}

	// Set defaults
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 512
	}
	if cfg.MinTokens == 0 {
		cfg.MinTokens = 50
	}

	// For now, simple chunking based on token count
	// Real implementation would use the strategy
	query := fmt.Sprintf(`
		CREATE TABLE %s AS
		SELECT
			*,
			ROW_NUMBER() OVER (ORDER BY position) as chunk_position
		FROM %s
		WHERE approx_tokens BETWEEN %d AND %d
	`, step.Output, source, cfg.MinTokens, cfg.MaxTokens)

	_, err := runDB.ExecContext(ctx, query)
	return err
}

// executeHash executes a hash operation.
func (e *Engine) executeHash(ctx context.Context, runDB *db.DB, step *Step, source string) error {
	var cfg HashConfig
	if step.Config != nil {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return fmt.Errorf("parse hash config: %w", err)
		}
	}

	if cfg.OutputColumn == "" {
		cfg.OutputColumn = "hash"
	}

	// SQLite doesn't have native hashing, so we use a simple approach
	// Real implementation would use a custom function
	cols := strings.Join(cfg.Columns, " || ")

	query := fmt.Sprintf(`
		CREATE TABLE %s AS
		SELECT
			*,
			hex(zeroblob(32)) AS %s
		FROM %s
	`, step.Output, cfg.OutputColumn, source)
	// Note: Real implementation needs proper hashing via SQLite extension

	_, err := runDB.ExecContext(ctx, query)
	return err
}

// executeVectorize executes a vectorize operation.
func (e *Engine) executeVectorize(ctx context.Context, runDB *db.DB, step *Step, source string) error {
	var cfg VectorizeConfig
	if step.Config != nil {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return fmt.Errorf("parse vectorize config: %w", err)
		}
	}

	// Create output table for vectors
	// Real implementation would call the registered vectorizer
	query := fmt.Sprintf(`
		CREATE TABLE %s AS
		SELECT
			id as chunk_id,
			'%s' as layer,
			zeroblob(%d * 4) as vector,
			%d as dimensions,
			'%s' as model_version
		FROM %s
	`, step.Output, cfg.Layer, cfg.Dimensions, cfg.Dimensions, cfg.ModelVersion, source)

	_, err := runDB.ExecContext(ctx, query)
	return err
}

// executeExternal executes an external extraction.
func (e *Engine) executeExternal(ctx context.Context, runDB *db.DB, step *Step, source string) error {
	var cfg ExternalConfig
	if step.Config != nil {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return fmt.Errorf("parse external config: %w", err)
		}
	}

	extractor, ok := e.extractors[cfg.Extractor]
	if !ok {
		// No extractor registered, create empty output
		query := fmt.Sprintf(`
			CREATE TABLE %s AS
			SELECT * FROM %s WHERE 0
		`, step.Output, source)
		_, err := runDB.ExecContext(ctx, query)
		return err
	}

	// Query source for content
	rows, err := runDB.QueryContext(ctx, fmt.Sprintf("SELECT id, content FROM %s", source))
	if err != nil {
		return err
	}
	defer rows.Close()

	// Create output table
	colDefs := "id TEXT, file_id TEXT, segment_type TEXT, content TEXT, page INTEGER, position INTEGER"
	_, err = runDB.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s (%s)", step.Output, colDefs))
	if err != nil {
		return err
	}

	// Process each row
	for rows.Next() {
		var id string
		var content []byte
		if err := rows.Scan(&id, &content); err != nil {
			continue
		}

		segments, err := extractor.Extract(ctx, content, step.Config)
		if err != nil {
			continue
		}

		for i, seg := range segments {
			seg.FileID = id
			seg.Position = i
			_, err := runDB.ExecContext(ctx, fmt.Sprintf(`
				INSERT INTO %s (id, file_id, segment_type, content, page, position)
				VALUES (?, ?, ?, ?, ?, ?)
			`, step.Output), seg.ID, seg.FileID, seg.SegmentType, seg.Content, seg.Page, seg.Position)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// logStepExecution logs a step execution to the run database.
func (e *Engine) logStepExecution(ctx context.Context, runDB *db.DB, exec *StepExecution) error {
	_, err := runDB.ExecContext(ctx, `
		INSERT INTO _step_executions (step_order, step_name, started_at, finished_at, duration_ms, rows_in, rows_out, delta_score, output_table, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, exec.StepOrder, exec.StepName, exec.StartedAt, exec.FinishedAt, exec.DurationMs, exec.RowsIn, exec.RowsOut, exec.DeltaScore, exec.OutputTable, exec.Notes)
	return err
}

// updateRunStatus updates the run status in the run database.
func (e *Engine) updateRunStatus(ctx context.Context, runDB *db.DB, run *Run) error {
	_, err := runDB.ExecContext(ctx, `
		UPDATE _run_meta SET status = ?, finished_at = ? WHERE run_id = ?
	`, run.Status, run.FinishedAt, run.ID)
	return err
}
