# GoRAGlite v2

SQLite-powered RAG (Retrieval-Augmented Generation) system with workflow-based transformations.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         GoRAGlite v2                            │
├─────────────────────────────────────────────────────────────────┤
│  CLI (cmd/raglite)                                              │
│    init | ingest | process | search | status | workflows        │
├─────────────────────────────────────────────────────────────────┤
│  Orchestrator          │  Workflow Engine    │  Merger          │
│  - file ingestion      │  - step execution   │  - sole writer   │
│  - workflow selection  │  - delta tracking   │  - queue FIFO    │
│  - search coordination │  - run isolation    │  - GC management │
├─────────────────────────────────────────────────────────────────┤
│  SQLite Databases                                               │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │
│  │  corpus.db   │  │ workflows.db │  │   run_*.db   │          │
│  │  (permanent) │  │  (read-only) │  │  (ephemeral) │          │
│  └──────────────┘  └──────────────┘  └──────────────┘          │
└─────────────────────────────────────────────────────────────────┘
```

### Design Principles

- **One run = one database**: Each workflow execution creates an isolated `.db` file for easy cleanup
- **Merger as sole writer**: Serialization guaranteed - only the merger writes to `corpus.db`
- **Workflows are data**: SQL predicates stored in database, versionable and diffable
- **Every step is a table**: No in-memory state, full auditability via `CREATE TABLE AS SELECT`

## Implementation Status

| Component | Status | Notes |
|-----------|--------|-------|
| SQL Schemas | Complete | corpus, workflows, run templates |
| Workflow Definitions | Complete | 12 workflows for PDF, DOCX, code (9 langs), search |
| Workflow Engine | Complete | All operations: filter, project, join, aggregate, window, hash, vectorize, external |
| Merger | Complete | Queue-based with retry, GC |
| Extractors | Partial | PDF (pdftotext), DOCX (xml), XLSX (xml), Code (regex) |
| Vectorization | Basic | Feature hashing + TF-IDF (no ML embeddings) |
| CLI | Complete | All commands implemented |
| Go Tests | Not implemented | SQL tests only |
| Search | Basic | FTS5 only, no vector search |

### Known Limitations

- Vectorization uses feature hashing, not neural embeddings
- Hash operation creates placeholder blobs (needs SQLite extension)
- Window chunking is simplified (no semantic boundaries)
- No concurrent worker support yet

## Installation

```bash
# Requirements
go 1.21+
sqlite3 (with FTS5)

# For PDF extraction
apt-get install poppler-utils  # pdftotext

# Build
go build -o raglite ./cmd/raglite
```

## Usage

```bash
# Initialize
raglite init

# Ingest files
raglite ingest ./documents/
raglite ingest ./src/main.go

# Process pending files
raglite process

# Search
raglite search "error handling"

# Status
raglite status

# List workflows
raglite workflows

# Run specific workflow
raglite run pdf_chunking_v1

# Inspect a run
raglite inspect ~/.raglite/runs/run_xxx.db

# Garbage collect
raglite gc 72h
```

## Workflows

Pre-defined workflows in `sql/workflows/`:

| Workflow | File Types | Steps |
|----------|-----------|-------|
| `pdf_chunking_v1` | PDF | 12 (filter → extract → chunk → vectorize) |
| `docx_chunking_v1` | DOCX | 13 |
| `go_chunking_v1` | Go | 10 |
| `python_chunking_v1` | Python | 10 |
| `js_chunking_v1` | JavaScript | 10 |
| `ts_chunking_v1` | TypeScript | 10 |
| `bash_chunking_v1` | Bash | 10 |
| `sql_chunking_v1` | SQL | 10 |
| `html_chunking_v1` | HTML/HTMX | 10 |
| `markdown_chunking_v1` | Markdown | 9 |
| `text_chunking_v1` | Plain text | 8 |
| `search_default_v1` | - | Multi-layer search |

## SQL Tests

Standalone tests runnable with `sqlite3`:

```bash
cd test/sql
bash run_tests.sh
```

Tests validate:
- Schema creation and constraints
- Workflow loading and step counts
- Run database template
- End-to-end pipeline simulation

## Project Structure

```
GoRAGlite/
├── cmd/raglite/          # CLI
├── internal/
│   ├── db/               # SQLite wrapper
│   ├── workflow/         # Engine, loader, types
│   ├── merger/           # Corpus integration
│   ├── orchestrator/     # Coordination
│   ├── extract/          # PDF, DOCX, XLSX, Code
│   └── vector/           # Feature hashing, TF-IDF
├── sql/
│   ├── schema/           # Database schemas
│   └── workflows/        # Workflow definitions
└── test/sql/             # SQL tests
```

## Database Schemas

### corpus.db
- `raw_files`: Ingested files with status tracking
- `chunks`: Text chunks with FTS5 index
- `chunk_vectors`: Multi-layer vectors (BLOB)
- `chunk_features`: Extracted features
- `chunk_relations`: Graph relations
- `run_history`: Audit trail

### workflows.db
- `workflows`: Workflow definitions
- `workflow_steps`: Step definitions with predicates
- `workflow_tags`: Categorization
- `search_configs`: Search parameters

### run_*.db (ephemeral)
- `_run_meta`: Run metadata
- `_workflow_steps`: Copied steps
- `_step_executions`: Execution log with deltas
- `_output`: Final chunks
- `_output_vectors`: Generated vectors

## License

MIT
