-- GoRAGlite v2 - DOCX Chunking Workflow
-- Workflow: docx_chunking_v1
-- Transforme des DOCX en chunks vectorisés

-- ============================================================================
-- Workflow Definition
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'docx_chunking_v1',
    'DOCX to Vectors Pipeline',
    1,
    'Extrait le texte structuré des DOCX via pandoc, préserve les styles et la hiérarchie',
    '{"tables": ["raw_files"], "filters": {"mime_type": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "status": "pending"}}',
    '{"tables": ["_output", "_output_features", "_output_vectors"]}',
    'active'
);

-- ============================================================================
-- Workflow Steps
-- ============================================================================

-- Step 1: Filter - Sélectionner les DOCX non traités
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    1,
    'select_pending_docx',
    'filter',
    '_input',
    'mime_type IN (''application/vnd.openxmlformats-officedocument.wordprocessingml.document'', ''application/msword'') AND status = ''pending''',
    'step_1_docx',
    '{"description": "Select unprocessed DOCX/DOC files"}',
    'skip_remaining'
);

-- Step 2: External - Extraire via pandoc
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    2,
    'extract_pandoc',
    'external',
    'step_1_docx',
    NULL,
    'step_2_extracted',
    '{
        "extractor": "pandoc",
        "extractor_version": "3.1",
        "options": {
            "from": "docx",
            "to": "markdown",
            "extract_media": false,
            "preserve_tabs": true
        },
        "output_columns": ["file_id", "segment_type", "content", "position", "style", "level"]
    }',
    'continue'
);

-- Step 3: Parse - Identifier structure (headings, listes, paragraphes)
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    3,
    'parse_markdown_structure',
    'filter',
    'step_2_extracted',
    'length(content) > 5',
    'step_3_parsed',
    '{
        "parse_rules": [
            {"pattern": "^#{1}\\s+", "type": "heading", "level": 1},
            {"pattern": "^#{2}\\s+", "type": "heading", "level": 2},
            {"pattern": "^#{3}\\s+", "type": "heading", "level": 3},
            {"pattern": "^#{4,6}\\s+", "type": "heading", "level": 4},
            {"pattern": "^\\s*[-*+]\\s+", "type": "list_item"},
            {"pattern": "^\\s*\\d+\\.\\s+", "type": "list_item"},
            {"pattern": "^>\\s+", "type": "blockquote"},
            {"pattern": "^```", "type": "code_block"}
        ],
        "preserve_hierarchy": true
    }',
    'continue'
);

-- Step 4: Aggregate - Reconstruire hiérarchie de sections
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    4,
    'build_hierarchy',
    'aggregate',
    'step_3_parsed',
    NULL,
    'step_4_hierarchy',
    '{
        "description": "Build document hierarchy from headings",
        "hierarchy_column": "parent_section_id",
        "level_column": "level",
        "path_column": "section_path"
    }',
    'continue'
);

-- Step 5: Window - Chunking par section
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    5,
    'chunk_by_section',
    'window',
    'step_4_hierarchy',
    NULL,
    'step_5_chunks',
    '{
        "strategy": "semantic",
        "max_tokens": 512,
        "min_tokens": 30,
        "overlap_tokens": 30,
        "boundary_markers": ["heading"],
        "group_by": "section_path",
        "keep_section_context": true
    }',
    'continue'
);

-- Step 6: Hash - Déduplication
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    6,
    'compute_hash',
    'hash',
    'step_5_chunks',
    NULL,
    'step_6_hashed',
    '{
        "algorithm": "sha256",
        "columns": ["content"],
        "output_column": "content_hash"
    }',
    'continue'
);

-- Step 7: Filter - Exclure doublons
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    7,
    'deduplicate',
    'filter',
    'step_6_hashed',
    'content_hash NOT IN (SELECT hash FROM corpus.chunks)',
    'step_7_unique',
    '{"description": "Remove duplicates already in corpus"}',
    'continue'
);

-- Step 8: Features - Extraire caractéristiques spécifiques DOCX
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    8,
    'extract_features',
    'aggregate',
    'step_7_unique',
    NULL,
    'step_8_features',
    '{
        "features": [
            {"name": "token_count", "expr": "length(content) / 4"},
            {"name": "heading_depth", "expr": "level"},
            {"name": "section_length", "expr": "length(section_path) - length(replace(section_path, ''/'', ''''))"},
            {"name": "list_density", "expr": "CAST(instr(content, ''- '') > 0 OR instr(content, ''* '') > 0 AS INTEGER)"},
            {"name": "has_code", "expr": "CAST(instr(content, ''```'') > 0 AS INTEGER)"},
            {"name": "formatting_density", "expr": "CAST(length(content) - length(replace(replace(replace(content, ''**'', ''''), ''__'', ''''), ''``'', '''')) AS REAL) / NULLIF(length(content), 0)"}
        ]
    }',
    'continue'
);

-- Step 9: Vectorize - Structure vector
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    9,
    'vectorize_structure',
    'vectorize',
    'step_8_features',
    NULL,
    'step_9_structure_vec',
    '{
        "layer": "structure",
        "algorithm": "feature_hash",
        "dimensions": 256,
        "features": ["token_count", "heading_depth", "section_length", "list_density", "has_code", "formatting_density"],
        "model_version": "structure_docx_v1"
    }',
    'continue'
);

-- Step 10: Vectorize - Lexical
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    10,
    'vectorize_lexical',
    'vectorize',
    'step_7_unique',
    NULL,
    'step_10_lexical_vec',
    '{
        "layer": "lexical",
        "algorithm": "tfidf",
        "dimensions": 256,
        "min_df": 2,
        "max_df": 0.85,
        "ngram_range": [1, 2],
        "model_version": "lexical_tfidf_v1"
    }',
    'continue'
);

-- Step 11: Vectorize - Contextual (basé sur hiérarchie)
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    11,
    'vectorize_contextual',
    'vectorize',
    'step_7_unique',
    NULL,
    'step_11_contextual_vec',
    '{
        "layer": "contextual",
        "algorithm": "graph_embed",
        "dimensions": 256,
        "relations": ["parent_of", "follows"],
        "model_version": "contextual_graph_v1"
    }',
    'continue'
);

-- Step 12: Vectorize - Blend
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    12,
    'vectorize_blend',
    'vectorize',
    'step_7_unique',
    NULL,
    'step_12_blend_vec',
    '{
        "layer": "blend",
        "algorithm": "blend",
        "dimensions": 256,
        "sources": ["step_9_structure_vec", "step_10_lexical_vec", "step_11_contextual_vec"],
        "weights": {"structure": 0.35, "lexical": 0.35, "contextual": 0.30},
        "model_version": "blend_docx_v1"
    }',
    'continue'
);

-- Step 13: Finalize
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'docx_chunking_v1',
    13,
    'finalize_output',
    'project',
    'step_7_unique',
    '*',
    '_output',
    '{"description": "Copy final chunks to output table"}',
    'continue'
);

-- ============================================================================
-- Tags
-- ============================================================================

INSERT OR REPLACE INTO workflow_tags (workflow_id, tag) VALUES
    ('docx_chunking_v1', 'docx'),
    ('docx_chunking_v1', 'word'),
    ('docx_chunking_v1', 'chunking'),
    ('docx_chunking_v1', 'vectorization'),
    ('docx_chunking_v1', 'production');
