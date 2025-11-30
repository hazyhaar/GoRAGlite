-- GoRAGlite v2 - PDF Chunking Workflow
-- Workflow: pdf_to_vectors_v1
-- Transforme des PDFs en chunks vectorisés

-- ============================================================================
-- Workflow Definition
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'pdf_chunking_v1',
    'PDF to Vectors Pipeline',
    1,
    'Extrait le texte des PDFs, parse en paragraphes, chunk sémantiquement, vectorise multi-layer',
    '{"tables": ["raw_files"], "filters": {"mime_type": "application/pdf", "status": "pending"}}',
    '{"tables": ["_output", "_output_features", "_output_vectors"]}',
    'active'
);

-- ============================================================================
-- Workflow Steps
-- ============================================================================

-- Step 1: Filter - Sélectionner les PDFs non traités
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
    1,
    'select_pending_pdfs',
    'filter',
    '_input',
    'mime_type = ''application/pdf'' AND status = ''pending''',
    'step_1_pdfs',
    '{"description": "Select unprocessed PDF files"}',
    'skip_remaining'
);

-- Step 2: External - Extraire le texte via pdftotext
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
    2,
    'extract_text',
    'external',
    'step_1_pdfs',
    NULL,
    'step_2_extracted',
    '{
        "extractor": "pdftotext",
        "extractor_version": "0.86.1",
        "options": {
            "layout": true,
            "encoding": "UTF-8"
        },
        "output_columns": ["file_id", "segment_type", "content", "page", "position", "bbox"]
    }',
    'continue'
);

-- Step 3: Parse - Décomposer en paragraphes/headings
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
    3,
    'parse_structure',
    'filter',
    'step_2_extracted',
    'length(content) > 10',  -- ignorer les segments trop courts
    'step_3_parsed',
    '{
        "parse_rules": [
            {"pattern": "^#{1,6}\\s+", "type": "heading"},
            {"pattern": "^\\s*[-*]\\s+", "type": "list_item"},
            {"pattern": "^```", "type": "code_block"},
            {"pattern": "\\n\\n+", "type": "paragraph_break"}
        ],
        "min_content_length": 10
    }',
    'continue'
);

-- Step 4: Aggregate - Compter tokens par segment
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
    4,
    'count_tokens',
    'project',
    'step_3_parsed',
    'id, file_id, content, segment_type, page, position, length(content) / 4 as approx_tokens',
    'step_4_with_tokens',
    '{"description": "Approximate token count (chars/4)"}',
    'continue'
);

-- Step 5: Window - Chunking sémantique avec fenêtrage
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
    5,
    'semantic_chunking',
    'window',
    'step_4_with_tokens',
    NULL,
    'step_5_chunks',
    '{
        "strategy": "semantic",
        "max_tokens": 512,
        "min_tokens": 50,
        "overlap_tokens": 50,
        "boundary_markers": ["heading", "paragraph_break"],
        "prefer_complete_sentences": true
    }',
    'continue'
);

-- Step 6: Hash - Calculer hash pour déduplication
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
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

-- Step 7: Filter - Déduplication
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
    7,
    'deduplicate',
    'filter',
    'step_6_hashed',
    'content_hash NOT IN (SELECT hash FROM corpus.chunks)',
    'step_7_unique',
    '{"description": "Remove duplicates already in corpus"}',
    'continue'
);

-- Step 8: Aggregate - Extraire features
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
    8,
    'extract_features',
    'aggregate',
    'step_7_unique',
    NULL,
    'step_8_features',
    '{
        "features": [
            {"name": "token_count", "expr": "length(content) / 4"},
            {"name": "char_count", "expr": "length(content)"},
            {"name": "line_count", "expr": "length(content) - length(replace(content, char(10), '''')) + 1"},
            {"name": "word_count", "expr": "length(content) - length(replace(content, '' '', '''')) + 1"},
            {"name": "avg_word_length", "expr": "CAST(length(replace(content, '' '', '''')) AS REAL) / NULLIF(length(content) - length(replace(content, '' '', '''')) + 1, 0)"},
            {"name": "uppercase_ratio", "expr": "CAST(length(content) - length(lower(content)) AS REAL) / NULLIF(length(content), 0)"},
            {"name": "digit_ratio", "expr": "CAST(length(replace(replace(replace(replace(replace(replace(replace(replace(replace(replace(content, ''0'', ''''), ''1'', ''''), ''2'', ''''), ''3'', ''''), ''4'', ''''), ''5'', ''''), ''6'', ''''), ''7'', ''''), ''8'', ''''), ''9'', '''')) AS REAL) / NULLIF(length(content), 0)"}
        ]
    }',
    'continue'
);

-- Step 9: Vectorize - Structure vector
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
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
        "features": ["token_count", "char_count", "line_count", "word_count", "avg_word_length", "uppercase_ratio", "digit_ratio"],
        "model_version": "structure_v1"
    }',
    'continue'
);

-- Step 10: Vectorize - Lexical vector (TF-IDF)
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
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
        "max_df": 0.8,
        "ngram_range": [1, 2],
        "model_version": "lexical_tfidf_v1"
    }',
    'continue'
);

-- Step 11: Vectorize - Blend
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
    11,
    'vectorize_blend',
    'vectorize',
    'step_7_unique',
    NULL,
    'step_11_blend_vec',
    '{
        "layer": "blend",
        "algorithm": "blend",
        "dimensions": 256,
        "sources": ["step_9_structure_vec", "step_10_lexical_vec"],
        "weights": {"structure": 0.5, "lexical": 0.5},
        "model_version": "blend_v1"
    }',
    'continue'
);

-- Step 12: Project - Finaliser output
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'pdf_chunking_v1',
    12,
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
    ('pdf_chunking_v1', 'pdf'),
    ('pdf_chunking_v1', 'chunking'),
    ('pdf_chunking_v1', 'vectorization'),
    ('pdf_chunking_v1', 'production');
