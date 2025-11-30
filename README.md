# GoRAGlite v2

A SQLite-powered RAG (Retrieval-Augmented Generation) system built on a workflow architecture where every transformation is a table.

## Philosophy

> "Remplacer l'inférence opaque par des cascades de transformations SQL auditables."

### Core Principles

1. **Every transformation is a table** — No in-memory state. Each operation materializes its result.
2. **Filters are data** — A cascade is an ordered list of stored predicates. Versionable, composable, diffable.
3. **Active residual** — Each step calculates its delta vs the previous step. Divergence is signal, not noise.
4. **Go = I/O, SQL = Logic** — Go handles file reading and external API calls; SQL handles all transformations.

## Architecture

```
┌──────────────────────────────────────┐
│            ORCHESTRATOR              │
│  (decides what to process)           │
└──────────────┬───────────────────────┘
               │
  ┌────────────┼────────────────┐
  ▼            ▼                ▼
┌──────────┐ ┌──────────┐ ┌──────────┐
│ Worker 1 │ │ Worker 2 │ │ Worker N │
│ run_a.db │ │ run_b.db │ │ run_n.db │
└────┬─────┘ └────┬─────┘ └────┬─────┘
     │            │            │
     ▼            ▼            ▼
┌──────────────────────────────────────┐
│             QUEUE                     │
│        runs/pending/*.db              │
└──────────────────┬───────────────────┘
                   │
                   ▼
        ┌──────────────────────┐
        │       MERGER         │
        │    (singleton)       │
        │  sole writer to      │
        │    corpus.db         │
        └──────────┬───────────┘
                   │
                   ▼
        ┌──────────────────────┐
        │      corpus.db       │
        │     (permanent)      │
        └──────────────────────┘
```

### Database Topology

```
PERMANENT
─────────
corpus.db          The consolidated knowledge graph
workflows.db       Workflow definitions

TRANSIENT
───────────
runs/
├── {run_id}.db    Worker workspace (ephemeral)
└── ...

queue/
├── pending/       Outputs awaiting merge
├── processing/    Being merged (one at a time)
├── done/          Archived
└── failed/        Failures for inspection
```

## Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/GoRAGlite.git
cd GoRAGlite

# Build
go build -o raglite ./cmd/raglite

# Initialize
./raglite init
```

## Usage

### Basic Commands

```bash
# Initialize data directory
raglite init

# Ingest files
raglite ingest ./documents/
raglite ingest ./code.go
raglite ingest ./project/

# Process pending files
raglite process

# Search the corpus
raglite search "how to handle HTTP errors"
raglite search "function that parses JSON"

# Show system status
raglite status

# List available workflows
raglite workflows

# Run a specific workflow
raglite run pdf_chunking_v1

# Inspect a run
raglite inspect ./data/runs/run_xxx.db

# Garbage collect old runs
raglite gc
raglite gc 24h  # Keep last 24 hours

# Export data
raglite export json > corpus.json
raglite export csv > corpus.csv
```

## Supported Formats

### Documents
- **PDF** — via `pdftotext` (layout-aware extraction)
- **DOCX** — native XML parsing (preserves styles, headings)
- **XLSX** — cell-by-cell extraction with formulas

### Code (Language-Aware Parsing)
- **Go** — AST parsing (functions, types, interfaces)
- **Python** — Function/class extraction with type hints
- **JavaScript** — Functions, classes, arrow functions, modules
- **TypeScript** — Type-aware with interfaces, generics
- **Bash** — Function extraction, pipe detection
- **SQL** — Statement-type classification
- **HTML/HTMX** — Section extraction, htmx attribute detection
- **Markdown** — Heading-based sections, code block preservation

### Plain Text
- Paragraph-based chunking with semantic boundaries

## Workflows

Each workflow is a cascade of SQL transformations:

```
Input → Filter → Parse → Chunk → Features → Vectors → Output
```

### Available Workflows

| Workflow | Description |
|----------|-------------|
| `pdf_chunking_v1` | PDF → text segments → chunks → vectors |
| `docx_chunking_v1` | DOCX → structured paragraphs → chunks |
| `go_chunking_v1` | Go code → AST blocks → code-aware vectors |
| `python_chunking_v1` | Python → functions/classes → vectors |
| `javascript_chunking_v1` | JS → functions/classes → vectors |
| `typescript_chunking_v1` | TS → type-aware extraction |
| `bash_chunking_v1` | Shell scripts → function blocks |
| `sql_chunking_v1` | SQL → statement classification |
| `html_chunking_v1` | HTML/HTMX → section extraction |
| `markdown_chunking_v1` | Markdown → heading-based sections |
| `text_chunking_v1` | Plain text → paragraph chunking |
| `search_v1` | Multi-layer hybrid search |

### Workflow Steps

Each step is an atomic operation:

| Operation | Description |
|-----------|-------------|
| `filter` | SQL WHERE clause |
| `project` | SELECT columns |
| `join` | Table joins |
| `aggregate` | GROUP BY with functions |
| `window` | Chunking/windowing |
| `hash` | Content hashing |
| `vectorize` | Vector generation |
| `external` | Call external extractor |

## Vectorization

Vectors are built **without external APIs** using:

### Layers

1. **Structure** — Features like line count, code patterns, formatting density
2. **Lexical** — TF-IDF on tokens/n-grams
3. **Contextual** — Graph-based features (relations, co-occurrence)
4. **Blend** — Weighted combination of layers

### Feature Hashing

```go
// Features are hashed into fixed-dimension vectors
features := map[string]float64{
    "token_count": 150,
    "has_function": 1.0,
    "has_error_handling": 1.0,
}
vector := hasher.HashFeatures(features) // → [256]float32
```

## Search

Search executes as a workflow cascade:

```
Query "function that handles HTTP errors"
    ↓
Step 1: Tokenize & expand query
    ↓
Step 2: FTS filter (chunks_fts MATCH tokens)
    → 847 → 43 candidates
    ↓
Step 3: Structure score (cosine similarity)
    → score per layer
    ↓
Step 4: Blend scores (weighted average)
    ↓
Step 5: Top-K filter
    → 10 results
```

Each step is logged in `step_executions`. You can:
- See why a chunk was eliminated
- Compare two queries (diff cascades)
- Optimize (which step eliminates most?)

## Inspecting Runs

Every run is its own SQLite database:

```bash
# Open a run for inspection
sqlite3 ./data/runs/run_xxx.db

# See run summary
SELECT * FROM _run_summary;

# See step progression
SELECT * FROM _step_progression;

# See specific step output
SELECT * FROM step_3_filtered LIMIT 10;

# See what was eliminated
SELECT * FROM delta_3;
```

## Configuration

### Data Directory Structure

```
~/.raglite/
├── corpus.db           # Consolidated knowledge
├── workflows.db        # Workflow definitions
├── runs/               # Active run workspaces
└── queue/
    ├── pending/        # Completed runs awaiting merge
    ├── done/           # Merged runs (for GC)
    └── failed/         # Failed merges
```

### Environment Variables

```bash
RAGLITE_DATA_DIR=~/.raglite  # Data directory
```

## Development

### Project Structure

```
goraglite/
├── cmd/raglite/           # CLI entry point
├── internal/
│   ├── db/                # SQLite wrapper
│   ├── workflow/          # Workflow engine
│   ├── merger/            # Merger component
│   ├── orchestrator/      # Task orchestration
│   ├── extract/           # File extractors
│   └── vector/            # Vector operations
├── sql/
│   ├── schema/            # DDL for databases
│   └── workflows/         # Built-in workflow definitions
└── test/
    ├── fixtures/          # Test files
    └── workflows/         # Workflow tests
```

### Adding a New Extractor

```go
type MyExtractor struct{}

func (e *MyExtractor) Name() string { return "myformat" }
func (e *MyExtractor) Version() string { return "1.0.0" }
func (e *MyExtractor) SupportedTypes() []string {
    return []string{"application/x-myformat"}
}
func (e *MyExtractor) Extract(ctx context.Context, content []byte, config json.RawMessage) ([]Segment, error) {
    // Parse content, return segments
}

// Register
registry.Register(&MyExtractor{})
```

### Adding a New Workflow

```sql
INSERT INTO workflows (id, name, version, description, status)
VALUES ('my_workflow_v1', 'My Workflow', 1, 'Description', 'active');

INSERT INTO workflow_steps VALUES
    ('my_workflow_v1', 1, 'step_name', 'filter', '_input',
     'predicate', 'output_table', '{"config": "json"}', 0, 'continue');
```

## Guarantees

| Property | Mechanism |
|----------|-----------|
| Determinism | Same input + same workflow = same output |
| Traceability | Every decision = an intermediate table |
| Isolation | One run = one file, zero side effects |
| Atomicity | Transactional merge, automatic rollback |
| Auditability | No black box, everything is inspectable SQL |
| Reproducibility | Extractor + workflow versioning |
| Resilience | Run crash doesn't affect corpus |
| Scalability | Parallel workers, serialized merger |

## License

MIT License - See [LICENSE](LICENSE) for details.
