-- GoRAGlite v2 - Workflows Schema
-- Les workflows sont des données, pas du code.
-- Versionnable, composable, diffable.

PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

-- ============================================================================
-- Workflows (définitions)
-- ============================================================================

CREATE TABLE IF NOT EXISTS workflows (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    description TEXT,
    input_schema TEXT,                      -- JSON (quelles tables/colonnes attendues)
    output_schema TEXT,                     -- JSON (quelles tables/colonnes produites)
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    status TEXT NOT NULL DEFAULT 'draft'    -- draft | active | deprecated
        CHECK (status IN ('draft', 'active', 'deprecated')),
    UNIQUE(id, version)
);

CREATE INDEX IF NOT EXISTS idx_workflows_status ON workflows(status);

-- ============================================================================
-- Workflow Steps (étapes atomiques)
-- ============================================================================

CREATE TABLE IF NOT EXISTS workflow_steps (
    workflow_id TEXT NOT NULL,
    step_order INTEGER NOT NULL,            -- position dans la séquence (1, 2, 3...)
    step_name TEXT NOT NULL,                -- nom lisible
    operation TEXT NOT NULL                 -- opération atomique
        CHECK (operation IN (
            'filter',       -- WHERE clause
            'project',      -- SELECT colonnes
            'join',         -- JOIN tables
            'aggregate',    -- GROUP BY
            'diff',         -- calcul delta
            'window',       -- fenêtrage SQL
            'hash',         -- feature hashing
            'vectorize',    -- génération vecteur
            'external',     -- appel extracteur externe
            'fork',         -- split en N branches
            'merge'         -- union de branches
        )),
    source TEXT NOT NULL,                   -- table source (step précédent ou table nommée)
    predicate TEXT,                         -- expression SQL (WHERE/SELECT/etc)
    output TEXT NOT NULL,                   -- nom de la table résultat
    config TEXT,                            -- JSON params spécifiques à l'opération
    expects_delta INTEGER NOT NULL DEFAULT 0, -- cette étape utilise-t-elle le delta précédent ?
    on_empty TEXT NOT NULL DEFAULT 'continue' -- que faire si résultat vide
        CHECK (on_empty IN ('continue', 'skip_remaining', 'fail')),
    PRIMARY KEY (workflow_id, step_order),
    FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_steps_workflow ON workflow_steps(workflow_id);
CREATE INDEX IF NOT EXISTS idx_steps_operation ON workflow_steps(operation);

-- ============================================================================
-- Workflow Step Dependencies (pour DAG non-linéaires)
-- ============================================================================

CREATE TABLE IF NOT EXISTS workflow_step_dependencies (
    workflow_id TEXT NOT NULL,
    step_order INTEGER NOT NULL,
    depends_on_step INTEGER NOT NULL,       -- autre step requis
    dependency_type TEXT NOT NULL           -- type de dépendance
        CHECK (dependency_type IN ('data', 'delta', 'config')),
    PRIMARY KEY (workflow_id, step_order, depends_on_step),
    FOREIGN KEY (workflow_id, step_order) REFERENCES workflow_steps(workflow_id, step_order) ON DELETE CASCADE,
    FOREIGN KEY (workflow_id, depends_on_step) REFERENCES workflow_steps(workflow_id, step_order) ON DELETE CASCADE
);

-- ============================================================================
-- Parse Rules (règles de parsing stockées)
-- ============================================================================

CREATE TABLE IF NOT EXISTS parse_rules (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    pattern TEXT NOT NULL,                  -- regex ou pattern
    target_type TEXT NOT NULL,              -- type de segment ciblé
    output_type TEXT NOT NULL,              -- type de parsed_unit produit
    priority INTEGER NOT NULL DEFAULT 0,    -- ordre d'application
    config TEXT,                            -- JSON params
    active INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_parse_rules_target ON parse_rules(target_type);

-- ============================================================================
-- Chunking Strategies (stratégies de chunking)
-- ============================================================================

CREATE TABLE IF NOT EXISTS chunking_strategies (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    chunk_type TEXT NOT NULL                -- type de chunk produit
        CHECK (chunk_type IN ('semantic', 'fixed_window', 'sentence', 'paragraph')),
    max_tokens INTEGER NOT NULL DEFAULT 512,
    min_tokens INTEGER NOT NULL DEFAULT 50,
    overlap_tokens INTEGER NOT NULL DEFAULT 50,
    boundary_pattern TEXT,                  -- regex pour délimitation
    config TEXT,                            -- JSON params supplémentaires
    active INTEGER NOT NULL DEFAULT 1
);

-- ============================================================================
-- Vectorization Configs (configurations de vectorisation)
-- ============================================================================

CREATE TABLE IF NOT EXISTS vectorization_configs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    layer TEXT NOT NULL                     -- layer cible
        CHECK (layer IN ('structure', 'lexical', 'contextual', 'blend')),
    dimensions INTEGER NOT NULL DEFAULT 256,
    algorithm TEXT NOT NULL                 -- algorithme de vectorisation
        CHECK (algorithm IN ('feature_hash', 'tfidf', 'graph_embed', 'blend')),
    features TEXT,                          -- JSON array des features à utiliser
    weights TEXT,                           -- JSON weights pour blend
    config TEXT,                            -- JSON params supplémentaires
    model_version TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1
);

-- ============================================================================
-- Search Configs (configurations de recherche)
-- ============================================================================

CREATE TABLE IF NOT EXISTS search_configs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    layers TEXT NOT NULL,                   -- JSON array des layers à utiliser
    layer_weights TEXT NOT NULL,            -- JSON weights par layer
    top_k INTEGER NOT NULL DEFAULT 10,
    min_score REAL NOT NULL DEFAULT 0.0,
    rerank_enabled INTEGER NOT NULL DEFAULT 0,
    config TEXT,                            -- JSON params supplémentaires
    active INTEGER NOT NULL DEFAULT 1
);

-- ============================================================================
-- Operation Templates (templates réutilisables)
-- ============================================================================

CREATE TABLE IF NOT EXISTS operation_templates (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    operation TEXT NOT NULL,
    predicate_template TEXT,                -- template avec placeholders {{param}}
    config_schema TEXT,                     -- JSON schema des paramètres attendus
    default_config TEXT                     -- JSON valeurs par défaut
);

-- ============================================================================
-- Workflow Tags (classification)
-- ============================================================================

CREATE TABLE IF NOT EXISTS workflow_tags (
    workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    tag TEXT NOT NULL,
    PRIMARY KEY (workflow_id, tag)
);

CREATE INDEX IF NOT EXISTS idx_workflow_tags ON workflow_tags(tag);

-- ============================================================================
-- Workflow Metrics (historique de performance)
-- ============================================================================

CREATE TABLE IF NOT EXISTS workflow_metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workflow_id TEXT NOT NULL REFERENCES workflows(id),
    workflow_version INTEGER NOT NULL,
    metric_name TEXT NOT NULL,              -- 'avg_duration', 'success_rate', 'avg_rows_out', etc.
    metric_value REAL NOT NULL,
    sample_size INTEGER NOT NULL,           -- nombre de runs dans le calcul
    calculated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_metrics_workflow ON workflow_metrics(workflow_id);

-- ============================================================================
-- Hooks Configuration (événements)
-- ============================================================================

CREATE TABLE IF NOT EXISTS hooks_config (
    id TEXT PRIMARY KEY,
    event TEXT NOT NULL                     -- événement déclencheur
        CHECK (event IN ('on_ingest', 'on_run_start', 'on_run_complete', 'on_merge', 'on_search', 'on_error')),
    handler_type TEXT NOT NULL              -- type de handler
        CHECK (handler_type IN ('webhook', 'script', 'internal')),
    handler TEXT NOT NULL,                  -- URL, path, ou nom de fonction
    config TEXT,                            -- JSON params
    priority INTEGER NOT NULL DEFAULT 0,
    active INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_hooks_event ON hooks_config(event);

-- ============================================================================
-- Default Workflows (inserted at initialization)
-- ============================================================================

-- Ces workflows seront insérés par le code Go lors de l'initialisation
-- Voir sql/workflows/*.sql pour les définitions complètes
