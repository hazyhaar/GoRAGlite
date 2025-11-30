-- GoRAGlite v2 - Default Search Workflow
-- Workflow: search_v1
-- Recherche multi-layer avec cascade de filtres

-- ============================================================================
-- Workflow Definition
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'search_v1',
    'Multi-Layer Search Pipeline',
    1,
    'Recherche hybride: FTS + vecteurs multi-layer avec reranking par blend',
    '{"params": {"query": "string", "top_k": "integer", "layers": "array"}}',
    '{"tables": ["_output"], "columns": ["chunk_id", "score", "layer_scores", "snippet", "file_id"]}',
    'active'
);

-- ============================================================================
-- Workflow Steps
-- ============================================================================

-- Step 1: Tokenize - Extraire les tokens de la query
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'search_v1',
    1,
    'tokenize_query',
    'project',
    '_query_input',
    'query_text, tokenize(query_text) as tokens',
    'step_1_tokens',
    '{
        "description": "Tokenize query into words",
        "tokenizer": "simple",
        "lowercase": true,
        "remove_stopwords": true
    }',
    'fail'
);

-- Step 2: Expand - Synonymes et stemming
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'search_v1',
    2,
    'expand_query',
    'project',
    'step_1_tokens',
    'query_text, tokens, expand_tokens(tokens) as expanded_tokens',
    'step_2_expanded',
    '{
        "description": "Expand with synonyms and stems",
        "enable_synonyms": true,
        "enable_stemming": true,
        "max_expansion": 3
    }',
    'continue'
);

-- Step 3: FTS Filter - Premier filtre large via FTS
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'search_v1',
    3,
    'fts_filter',
    'filter',
    'corpus.chunks',
    'id IN (SELECT rowid FROM corpus.chunks_fts WHERE corpus.chunks_fts MATCH :expanded_tokens)',
    'step_3_fts_candidates',
    '{
        "description": "Full-text search filter",
        "max_candidates": 1000,
        "bm25_weights": {"content": 1.0}
    }',
    'continue'
);

-- Step 4: Structure Score - Score basé sur features structurelles
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'search_v1',
    4,
    'score_structure',
    'join',
    'step_3_fts_candidates',
    'LEFT JOIN corpus.chunk_vectors cv ON step_3_fts_candidates.id = cv.chunk_id AND cv.layer = ''structure''',
    'step_4_with_structure',
    '{
        "description": "Add structure layer scores",
        "score_function": "cosine_similarity",
        "query_vector_source": "query_structure_vec"
    }',
    'continue'
);

-- Step 5: Lexical Score - Score TF-IDF
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'search_v1',
    5,
    'score_lexical',
    'join',
    'step_4_with_structure',
    'LEFT JOIN corpus.chunk_vectors cv2 ON step_4_with_structure.id = cv2.chunk_id AND cv2.layer = ''lexical''',
    'step_5_with_lexical',
    '{
        "description": "Add lexical layer scores",
        "score_function": "cosine_similarity",
        "query_vector_source": "query_lexical_vec"
    }',
    'continue'
);

-- Step 6: Contextual Score - Score basé sur graphe
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'search_v1',
    6,
    'score_contextual',
    'join',
    'step_5_with_lexical',
    'LEFT JOIN corpus.chunk_vectors cv3 ON step_5_with_lexical.id = cv3.chunk_id AND cv3.layer = ''contextual''',
    'step_6_with_contextual',
    '{
        "description": "Add contextual layer scores",
        "score_function": "cosine_similarity",
        "query_vector_source": "query_contextual_vec"
    }',
    'continue'
);

-- Step 7: Blend Scores - Fusion pondérée des scores
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'search_v1',
    7,
    'blend_scores',
    'project',
    'step_6_with_contextual',
    '*, (COALESCE(structure_score, 0) * :w_structure + COALESCE(lexical_score, 0) * :w_lexical + COALESCE(contextual_score, 0) * :w_contextual) as blend_score',
    'step_7_blended',
    '{
        "description": "Weighted average of layer scores",
        "default_weights": {
            "structure": 0.45,
            "lexical": 0.30,
            "contextual": 0.25
        },
        "dynamic_weights": true
    }',
    'continue'
);

-- Step 8: Top K - Garder les meilleurs
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'search_v1',
    8,
    'top_k_filter',
    'filter',
    'step_7_blended',
    'blend_score >= :min_score ORDER BY blend_score DESC LIMIT :top_k',
    'step_8_top_k',
    '{
        "description": "Keep top K results",
        "default_top_k": 10,
        "default_min_score": 0.1
    }',
    'continue'
);

-- Step 9: Enrich - Ajouter contexte (fichier source, snippet)
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'search_v1',
    9,
    'enrich_results',
    'join',
    'step_8_top_k',
    'JOIN corpus.chunks c ON step_8_top_k.id = c.id JOIN corpus.raw_files rf ON c.file_id = rf.id',
    'step_9_enriched',
    '{
        "description": "Add file metadata and snippets",
        "snippet_length": 200,
        "highlight_matches": true
    }',
    'continue'
);

-- Step 10: Finalize - Format output
INSERT OR REPLACE INTO workflow_steps
(workflow_id, step_order, step_name, operation, source, predicate, output, config, on_empty)
VALUES (
    'search_v1',
    10,
    'finalize_output',
    'project',
    'step_9_enriched',
    'id as chunk_id, blend_score as score, json_object(''structure'', structure_score, ''lexical'', lexical_score, ''contextual'', contextual_score) as layer_scores, substr(content, 1, 200) as snippet, file_id, source_path',
    '_output',
    '{"description": "Format final output"}',
    'continue'
);

-- ============================================================================
-- Search Configs
-- ============================================================================

INSERT OR REPLACE INTO search_configs (id, name, description, layers, layer_weights, top_k, min_score, rerank_enabled)
VALUES
    ('default', 'Default Search', 'Balanced multi-layer search',
     '["structure", "lexical", "contextual"]',
     '{"structure": 0.45, "lexical": 0.30, "contextual": 0.25}',
     10, 0.1, 0),

    ('code', 'Code Search', 'Optimized for code snippets',
     '["structure", "lexical"]',
     '{"structure": 0.6, "lexical": 0.4}',
     20, 0.15, 0),

    ('semantic', 'Semantic Search', 'Favor contextual understanding',
     '["lexical", "contextual"]',
     '{"lexical": 0.3, "contextual": 0.7}',
     10, 0.2, 0);

-- ============================================================================
-- Tags
-- ============================================================================

INSERT OR REPLACE INTO workflow_tags (workflow_id, tag) VALUES
    ('search_v1', 'search'),
    ('search_v1', 'multilayer'),
    ('search_v1', 'production');
