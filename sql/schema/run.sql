-- GoRAGlite v2 - Run Schema Template
-- Un run = un fichier .db isolé
-- Chaque exécution de workflow matérialise ses étapes ici.

PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA synchronous = NORMAL;

-- ============================================================================
-- Run Metadata
-- ============================================================================

CREATE TABLE IF NOT EXISTS _run_meta (
    run_id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL,
    workflow_version INTEGER NOT NULL,
    input_source TEXT,                      -- d'où vient l'input (snapshot path, query, etc.)
    input_hash TEXT,                        -- hash du snapshot
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    finished_at TEXT,
    status TEXT NOT NULL DEFAULT 'pending'  -- pending | running | completed | failed
        CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    worker_id TEXT,
    config TEXT                             -- JSON (paramètres de ce run spécifique)
);

-- ============================================================================
-- Workflow Steps Copy (pour self-contained run)
-- ============================================================================

CREATE TABLE IF NOT EXISTS _workflow_steps (
    step_order INTEGER PRIMARY KEY,
    step_name TEXT NOT NULL,
    operation TEXT NOT NULL,
    source TEXT NOT NULL,
    predicate TEXT,
    output TEXT NOT NULL,
    config TEXT,
    expects_delta INTEGER NOT NULL DEFAULT 0,
    on_empty TEXT NOT NULL DEFAULT 'continue'
);

-- ============================================================================
-- Input (snapshot from corpus)
-- ============================================================================

-- La table _input sera créée dynamiquement selon le workflow
-- Structure typique pour chunking workflow :

-- CREATE TABLE _input AS SELECT * FROM corpus.raw_files WHERE status = 'pending';

-- ============================================================================
-- Step Executions (métriques d'exécution)
-- ============================================================================

CREATE TABLE IF NOT EXISTS _step_executions (
    step_order INTEGER PRIMARY KEY,
    step_name TEXT NOT NULL,
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    finished_at TEXT,
    duration_ms INTEGER,
    rows_in INTEGER,
    rows_out INTEGER,
    delta_score REAL,                       -- divergence vs step-1
    output_table TEXT,                      -- table matérialisée
    memory_peak INTEGER,                    -- bytes si trackable
    notes TEXT                              -- JSON (warnings, stats)
);

-- ============================================================================
-- Delta Tracking (résiduel actif)
-- ============================================================================

CREATE TABLE IF NOT EXISTS _deltas (
    step_from INTEGER NOT NULL,
    step_to INTEGER NOT NULL,
    rows_before INTEGER NOT NULL,
    rows_after INTEGER NOT NULL,
    rows_lost INTEGER NOT NULL,
    rows_gained INTEGER NOT NULL,
    delta_type TEXT NOT NULL                -- reduction | expansion | transformation
        CHECK (delta_type IN ('reduction', 'expansion', 'transformation')),
    delta_score REAL,                       -- métrique de divergence (0-1)
    jaccard_index REAL,                     -- intersection / union des IDs
    sample_lost TEXT,                       -- JSON (échantillon de lignes éliminées)
    sample_gained TEXT,                     -- JSON (échantillon de lignes gagnées)
    PRIMARY KEY (step_from, step_to)
);

-- ============================================================================
-- Output Tables (résultats finaux à merger)
-- ============================================================================

-- _output : chunks finaux
CREATE TABLE IF NOT EXISTS _output (
    id TEXT PRIMARY KEY,
    file_id TEXT NOT NULL,
    unit_ids TEXT,                          -- JSON array des units source
    content TEXT NOT NULL,
    token_count INTEGER NOT NULL,
    chunk_type TEXT NOT NULL,
    overlap_prev INTEGER DEFAULT 0,
    overlap_next INTEGER DEFAULT 0,
    hash TEXT NOT NULL,
    position INTEGER NOT NULL,
    parent_id TEXT,
    _source_chain TEXT,                     -- JSON [input_id, step1_row, step2_row, ...] (lignée complète)
    _confidence REAL                        -- score de confiance (optionnel)
);

-- _output_features : features des chunks
CREATE TABLE IF NOT EXISTS _output_features (
    chunk_id TEXT NOT NULL,
    feature_name TEXT NOT NULL,
    feature_value REAL NOT NULL,
    feature_meta TEXT,
    PRIMARY KEY (chunk_id, feature_name)
);

-- _output_vectors : vecteurs des chunks
CREATE TABLE IF NOT EXISTS _output_vectors (
    chunk_id TEXT NOT NULL,
    layer TEXT NOT NULL,
    vector BLOB NOT NULL,
    dimensions INTEGER NOT NULL,
    model_version TEXT NOT NULL,
    PRIMARY KEY (chunk_id, layer)
);

-- _output_relations : relations entre chunks
CREATE TABLE IF NOT EXISTS _output_relations (
    from_chunk_id TEXT NOT NULL,
    to_chunk_id TEXT NOT NULL,
    relation_type TEXT NOT NULL,
    weight REAL NOT NULL DEFAULT 1.0,
    PRIMARY KEY (from_chunk_id, to_chunk_id, relation_type)
);

-- ============================================================================
-- Error Log
-- ============================================================================

CREATE TABLE IF NOT EXISTS _errors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    step_order INTEGER,
    error_type TEXT NOT NULL,               -- 'sql_error', 'validation_error', 'external_error'
    error_message TEXT NOT NULL,
    error_details TEXT,                     -- JSON (stack trace, context)
    row_id TEXT,                            -- ID de la ligne problématique si applicable
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- ============================================================================
-- Debug / Inspection Tables
-- ============================================================================

-- Samples de chaque étape pour debug rapide
CREATE TABLE IF NOT EXISTS _step_samples (
    step_order INTEGER NOT NULL,
    sample_type TEXT NOT NULL               -- 'first', 'last', 'random', 'outlier'
        CHECK (sample_type IN ('first', 'last', 'random', 'outlier')),
    row_data TEXT NOT NULL,                 -- JSON de la row
    PRIMARY KEY (step_order, sample_type)
);

-- Statistiques par étape
CREATE TABLE IF NOT EXISTS _step_stats (
    step_order INTEGER PRIMARY KEY,
    row_count INTEGER NOT NULL,
    null_counts TEXT,                       -- JSON {column: count}
    distinct_counts TEXT,                   -- JSON {column: count}
    numeric_stats TEXT,                     -- JSON {column: {min, max, avg, std}}
    text_stats TEXT                         -- JSON {column: {min_len, max_len, avg_len}}
);

-- ============================================================================
-- Views pour inspection facile
-- ============================================================================

CREATE VIEW IF NOT EXISTS _run_summary AS
SELECT
    m.run_id,
    m.workflow_id,
    m.status,
    m.started_at,
    m.finished_at,
    (SELECT COUNT(*) FROM _workflow_steps) as total_steps,
    (SELECT COUNT(*) FROM _step_executions WHERE finished_at IS NOT NULL) as completed_steps,
    (SELECT SUM(duration_ms) FROM _step_executions) as total_duration_ms,
    (SELECT rows_out FROM _step_executions ORDER BY step_order DESC LIMIT 1) as final_rows
FROM _run_meta m;

CREATE VIEW IF NOT EXISTS _step_progression AS
SELECT
    e.step_order,
    e.step_name,
    e.rows_in,
    e.rows_out,
    ROUND(100.0 * e.rows_out / NULLIF(e.rows_in, 0), 2) as retention_pct,
    e.delta_score,
    e.duration_ms,
    d.delta_type
FROM _step_executions e
LEFT JOIN _deltas d ON d.step_to = e.step_order
ORDER BY e.step_order;
