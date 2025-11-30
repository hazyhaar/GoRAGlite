// Package workflow implements the workflow execution engine.
// Workflows are cascades of SQL transformations, each step matérialised as a table.
package workflow

import (
	"encoding/json"
	"time"
)

// Workflow represents a workflow definition.
type Workflow struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Version      int             `json:"version"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	Status       string          `json:"status"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	Steps        []Step          `json:"steps"`
}

// Step represents a single step in a workflow.
type Step struct {
	WorkflowID   string          `json:"workflow_id"`
	StepOrder    int             `json:"step_order"`
	StepName     string          `json:"step_name"`
	Operation    Operation       `json:"operation"`
	Source       string          `json:"source"`
	Predicate    string          `json:"predicate"`
	Output       string          `json:"output"`
	Config       json.RawMessage `json:"config"`
	ExpectsDelta bool            `json:"expects_delta"`
	OnEmpty      OnEmptyAction   `json:"on_empty"`
}

// Operation represents the type of operation a step performs.
type Operation string

const (
	OpFilter    Operation = "filter"
	OpProject   Operation = "project"
	OpJoin      Operation = "join"
	OpAggregate Operation = "aggregate"
	OpDiff      Operation = "diff"
	OpWindow    Operation = "window"
	OpHash      Operation = "hash"
	OpVectorize Operation = "vectorize"
	OpExternal  Operation = "external"
	OpFork      Operation = "fork"
	OpMerge     Operation = "merge"
)

// OnEmptyAction specifies what to do when a step produces no results.
type OnEmptyAction string

const (
	OnEmptyContinue      OnEmptyAction = "continue"
	OnEmptySkipRemaining OnEmptyAction = "skip_remaining"
	OnEmptyFail          OnEmptyAction = "fail"
)

// Run represents an execution of a workflow.
type Run struct {
	ID              string    `json:"id"`
	WorkflowID      string    `json:"workflow_id"`
	WorkflowVersion int       `json:"workflow_version"`
	InputSource     string    `json:"input_source"`
	InputHash       string    `json:"input_hash"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at,omitempty"`
	Status          RunStatus `json:"status"`
	WorkerID        string    `json:"worker_id"`
	Config          RunConfig `json:"config"`
	DBPath          string    `json:"db_path"`
}

// RunStatus represents the status of a run.
type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
	RunStatusMerged    RunStatus = "merged"
)

// RunConfig holds run-specific configuration.
type RunConfig struct {
	BatchSize   int               `json:"batch_size,omitempty"`
	Timeout     time.Duration     `json:"timeout,omitempty"`
	Parameters  map[string]string `json:"parameters,omitempty"`
	Debug       bool              `json:"debug,omitempty"`
	KeepTables  bool              `json:"keep_tables,omitempty"`
	SampleSize  int               `json:"sample_size,omitempty"`
}

// StepExecution records the execution of a single step.
type StepExecution struct {
	StepOrder   int       `json:"step_order"`
	StepName    string    `json:"step_name"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
	DurationMs  int64     `json:"duration_ms"`
	RowsIn      int64     `json:"rows_in"`
	RowsOut     int64     `json:"rows_out"`
	DeltaScore  float64   `json:"delta_score"`
	OutputTable string    `json:"output_table"`
	MemoryPeak  int64     `json:"memory_peak,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// Delta represents the change between two steps.
type Delta struct {
	StepFrom    int     `json:"step_from"`
	StepTo      int     `json:"step_to"`
	RowsBefore  int64   `json:"rows_before"`
	RowsAfter   int64   `json:"rows_after"`
	RowsLost    int64   `json:"rows_lost"`
	RowsGained  int64   `json:"rows_gained"`
	DeltaType   string  `json:"delta_type"` // reduction, expansion, transformation
	DeltaScore  float64 `json:"delta_score"`
	JaccardIdx  float64 `json:"jaccard_index"`
	SampleLost  string  `json:"sample_lost,omitempty"`  // JSON sample
	SampleGain  string  `json:"sample_gained,omitempty"`
}

// FilterConfig holds configuration for filter operations.
type FilterConfig struct {
	Description string `json:"description,omitempty"`
}

// ProjectConfig holds configuration for project operations.
type ProjectConfig struct {
	Description string   `json:"description,omitempty"`
	Columns     []string `json:"columns,omitempty"`
}

// JoinConfig holds configuration for join operations.
type JoinConfig struct {
	Description string `json:"description,omitempty"`
	JoinType    string `json:"join_type,omitempty"` // inner, left, right, full
	OnCondition string `json:"on_condition,omitempty"`
}

// AggregateConfig holds configuration for aggregate operations.
type AggregateConfig struct {
	Description string   `json:"description,omitempty"`
	GroupBy     []string `json:"group_by,omitempty"`
	Functions   []string `json:"functions,omitempty"`
}

// WindowConfig holds configuration for window operations.
type WindowConfig struct {
	Description           string   `json:"description,omitempty"`
	Strategy              string   `json:"strategy"` // semantic, fixed_window, sentence
	MaxTokens             int      `json:"max_tokens"`
	MinTokens             int      `json:"min_tokens"`
	OverlapTokens         int      `json:"overlap_tokens"`
	BoundaryMarkers       []string `json:"boundary_markers,omitempty"`
	GroupBy               string   `json:"group_by,omitempty"`
	PreferCompleteSentences bool   `json:"prefer_complete_sentences,omitempty"`
}

// HashConfig holds configuration for hash operations.
type HashConfig struct {
	Description  string   `json:"description,omitempty"`
	Algorithm    string   `json:"algorithm"` // sha256, fnv, xxhash
	Columns      []string `json:"columns"`
	OutputColumn string   `json:"output_column"`
}

// VectorizeConfig holds configuration for vectorize operations.
type VectorizeConfig struct {
	Description  string            `json:"description,omitempty"`
	Layer        string            `json:"layer"` // structure, lexical, contextual, blend
	Algorithm    string            `json:"algorithm"` // feature_hash, tfidf, graph_embed, blend
	Dimensions   int               `json:"dimensions"`
	Features     []string          `json:"features,omitempty"`
	Sources      []string          `json:"sources,omitempty"` // for blend
	Weights      map[string]float64 `json:"weights,omitempty"`
	ModelVersion string            `json:"model_version"`
}

// ExternalConfig holds configuration for external operations.
type ExternalConfig struct {
	Description      string            `json:"description,omitempty"`
	Extractor        string            `json:"extractor"`
	ExtractorVersion string            `json:"extractor_version"`
	Options          map[string]any    `json:"options,omitempty"`
	OutputColumns    []string          `json:"output_columns,omitempty"`
}

// FeatureSpec defines a feature to extract.
type FeatureSpec struct {
	Name string `json:"name"`
	Expr string `json:"expr"` // SQL expression
}

// ParseRule defines a parsing rule.
type ParseRule struct {
	Pattern string `json:"pattern"`
	Type    string `json:"type"`
	Level   int    `json:"level,omitempty"`
}
