-- GoRAGlite v2 - Code Chunking Workflows
-- Workflows for: Go, Python, JavaScript, TypeScript, Bash, SQL, HTML, Markdown

-- ============================================================================
-- GO WORKFLOW
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'go_chunking_v1',
    'Go Code Chunking Pipeline',
    1,
    'Parse Go source files using AST, extract functions/types/methods, vectorize with code-aware features',
    '{"tables": ["raw_files"], "filters": {"mime_type": "text/x-go"}}',
    '{"tables": ["_output", "_output_features", "_output_vectors"]}',
    'active'
);

INSERT OR REPLACE INTO workflow_steps VALUES
    ('go_chunking_v1', 1, 'select_go_files', 'filter', '_input',
     'mime_type = ''text/x-go'' AND status = ''pending''',
     'step_1_go', '{"description": "Select unprocessed Go files"}', 0, 'skip_remaining'),

    ('go_chunking_v1', 2, 'extract_ast', 'external', 'step_1_go', NULL, 'step_2_parsed',
     '{"extractor": "code", "extractor_version": "1.0.0", "options": {"language": "go", "parse_mode": "ast"}}', 0, 'continue'),

    ('go_chunking_v1', 3, 'filter_meaningful', 'filter', 'step_2_parsed',
     'length(content) > 20 AND segment_type = ''code''',
     'step_3_filtered', '{"description": "Keep meaningful code blocks"}', 0, 'continue'),

    ('go_chunking_v1', 4, 'extract_features', 'aggregate', 'step_3_filtered', NULL, 'step_4_features',
     '{"features": [
         {"name": "line_count", "expr": "length(content) - length(replace(content, char(10), '''')) + 1"},
         {"name": "has_func", "expr": "CAST(instr(content, ''func '') > 0 AS INTEGER)"},
         {"name": "has_struct", "expr": "CAST(instr(content, ''type '') > 0 AND instr(content, '' struct'') > 0 AS INTEGER)"},
         {"name": "has_interface", "expr": "CAST(instr(content, ''interface {'') > 0 AS INTEGER)"},
         {"name": "has_error_handling", "expr": "CAST(instr(content, ''err != nil'') > 0 OR instr(content, ''error'') > 0 AS INTEGER)"},
         {"name": "has_goroutine", "expr": "CAST(instr(content, ''go func'') > 0 OR instr(content, ''go '') > 0 AS INTEGER)"},
         {"name": "has_channel", "expr": "CAST(instr(content, ''chan '') > 0 OR instr(content, ''<-'') > 0 AS INTEGER)"},
         {"name": "complexity", "expr": "(length(content) - length(replace(content, ''if '', ''''))) + (length(content) - length(replace(content, ''for '', ''''))) + (length(content) - length(replace(content, ''switch '', '''')))"}
     ]}', 0, 'continue'),

    ('go_chunking_v1', 5, 'hash_content', 'hash', 'step_4_features', NULL, 'step_5_hashed',
     '{"algorithm": "sha256", "columns": ["content"], "output_column": "content_hash"}', 0, 'continue'),

    ('go_chunking_v1', 6, 'deduplicate', 'filter', 'step_5_hashed',
     'content_hash NOT IN (SELECT hash FROM corpus.chunks)',
     'step_6_unique', '{}', 0, 'continue'),

    ('go_chunking_v1', 7, 'vectorize_structure', 'vectorize', 'step_6_unique', NULL, 'step_7_vec_struct',
     '{"layer": "structure", "algorithm": "feature_hash", "dimensions": 256,
       "features": ["line_count", "has_func", "has_struct", "has_interface", "has_error_handling", "has_goroutine", "has_channel", "complexity"],
       "model_version": "go_structure_v1"}', 0, 'continue'),

    ('go_chunking_v1', 8, 'vectorize_lexical', 'vectorize', 'step_6_unique', NULL, 'step_8_vec_lex',
     '{"layer": "lexical", "algorithm": "tfidf", "dimensions": 256, "ngram_range": [1, 2], "model_version": "go_lexical_v1"}', 0, 'continue'),

    ('go_chunking_v1', 9, 'vectorize_blend', 'vectorize', 'step_6_unique', NULL, 'step_9_vec_blend',
     '{"layer": "blend", "algorithm": "blend", "dimensions": 256,
       "sources": ["step_7_vec_struct", "step_8_vec_lex"], "weights": {"structure": 0.4, "lexical": 0.6},
       "model_version": "go_blend_v1"}', 0, 'continue'),

    ('go_chunking_v1', 10, 'finalize', 'project', 'step_6_unique', '*', '_output', '{}', 0, 'continue');

INSERT OR REPLACE INTO workflow_tags (workflow_id, tag) VALUES
    ('go_chunking_v1', 'go'), ('go_chunking_v1', 'code'), ('go_chunking_v1', 'production');

-- ============================================================================
-- PYTHON WORKFLOW
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'python_chunking_v1',
    'Python Code Chunking Pipeline',
    1,
    'Parse Python source files, extract classes/functions/imports, vectorize with Python-aware features',
    '{"tables": ["raw_files"], "filters": {"mime_type": "text/x-python"}}',
    '{"tables": ["_output", "_output_features", "_output_vectors"]}',
    'active'
);

INSERT OR REPLACE INTO workflow_steps VALUES
    ('python_chunking_v1', 1, 'select_python_files', 'filter', '_input',
     'mime_type = ''text/x-python'' AND status = ''pending''',
     'step_1_py', '{}', 0, 'skip_remaining'),

    ('python_chunking_v1', 2, 'extract_ast', 'external', 'step_1_py', NULL, 'step_2_parsed',
     '{"extractor": "code", "extractor_version": "1.0.0", "options": {"language": "python"}}', 0, 'continue'),

    ('python_chunking_v1', 3, 'filter_meaningful', 'filter', 'step_2_parsed',
     'length(content) > 15',
     'step_3_filtered', '{}', 0, 'continue'),

    ('python_chunking_v1', 4, 'extract_features', 'aggregate', 'step_3_filtered', NULL, 'step_4_features',
     '{"features": [
         {"name": "line_count", "expr": "length(content) - length(replace(content, char(10), '''')) + 1"},
         {"name": "has_class", "expr": "CAST(instr(content, ''class '') > 0 AS INTEGER)"},
         {"name": "has_def", "expr": "CAST(instr(content, ''def '') > 0 AS INTEGER)"},
         {"name": "has_async", "expr": "CAST(instr(content, ''async '') > 0 OR instr(content, ''await '') > 0 AS INTEGER)"},
         {"name": "has_decorator", "expr": "CAST(instr(content, ''@'') > 0 AS INTEGER)"},
         {"name": "has_type_hints", "expr": "CAST(instr(content, '': '') > 0 AND instr(content, '' ->'') > 0 AS INTEGER)"},
         {"name": "has_exception", "expr": "CAST(instr(content, ''try:'') > 0 OR instr(content, ''except'') > 0 AS INTEGER)"},
         {"name": "indentation_level", "expr": "(length(content) - length(ltrim(content))) / 4"}
     ]}', 0, 'continue'),

    ('python_chunking_v1', 5, 'hash_content', 'hash', 'step_4_features', NULL, 'step_5_hashed',
     '{"algorithm": "sha256", "columns": ["content"], "output_column": "content_hash"}', 0, 'continue'),

    ('python_chunking_v1', 6, 'deduplicate', 'filter', 'step_5_hashed',
     'content_hash NOT IN (SELECT hash FROM corpus.chunks)', 'step_6_unique', '{}', 0, 'continue'),

    ('python_chunking_v1', 7, 'vectorize_structure', 'vectorize', 'step_6_unique', NULL, 'step_7_vec_struct',
     '{"layer": "structure", "algorithm": "feature_hash", "dimensions": 256, "model_version": "py_structure_v1"}', 0, 'continue'),

    ('python_chunking_v1', 8, 'vectorize_lexical', 'vectorize', 'step_6_unique', NULL, 'step_8_vec_lex',
     '{"layer": "lexical", "algorithm": "tfidf", "dimensions": 256, "model_version": "py_lexical_v1"}', 0, 'continue'),

    ('python_chunking_v1', 9, 'vectorize_blend', 'vectorize', 'step_6_unique', NULL, 'step_9_vec_blend',
     '{"layer": "blend", "algorithm": "blend", "dimensions": 256,
       "weights": {"structure": 0.35, "lexical": 0.65}, "model_version": "py_blend_v1"}', 0, 'continue'),

    ('python_chunking_v1', 10, 'finalize', 'project', 'step_6_unique', '*', '_output', '{}', 0, 'continue');

INSERT OR REPLACE INTO workflow_tags (workflow_id, tag) VALUES
    ('python_chunking_v1', 'python'), ('python_chunking_v1', 'code'), ('python_chunking_v1', 'production');

-- ============================================================================
-- JAVASCRIPT WORKFLOW
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'javascript_chunking_v1',
    'JavaScript Code Chunking Pipeline',
    1,
    'Parse JavaScript source files, extract functions/classes/modules',
    '{"tables": ["raw_files"], "filters": {"mime_type": ["text/javascript", "application/javascript"]}}',
    '{"tables": ["_output", "_output_features", "_output_vectors"]}',
    'active'
);

INSERT OR REPLACE INTO workflow_steps VALUES
    ('javascript_chunking_v1', 1, 'select_js_files', 'filter', '_input',
     '(mime_type = ''text/javascript'' OR mime_type = ''application/javascript'') AND status = ''pending''',
     'step_1_js', '{}', 0, 'skip_remaining'),

    ('javascript_chunking_v1', 2, 'extract_ast', 'external', 'step_1_js', NULL, 'step_2_parsed',
     '{"extractor": "code", "extractor_version": "1.0.0", "options": {"language": "javascript"}}', 0, 'continue'),

    ('javascript_chunking_v1', 3, 'filter_meaningful', 'filter', 'step_2_parsed',
     'length(content) > 15', 'step_3_filtered', '{}', 0, 'continue'),

    ('javascript_chunking_v1', 4, 'extract_features', 'aggregate', 'step_3_filtered', NULL, 'step_4_features',
     '{"features": [
         {"name": "line_count", "expr": "length(content) - length(replace(content, char(10), '''')) + 1"},
         {"name": "has_function", "expr": "CAST(instr(content, ''function '') > 0 AS INTEGER)"},
         {"name": "has_arrow", "expr": "CAST(instr(content, ''=>'') > 0 AS INTEGER)"},
         {"name": "has_class", "expr": "CAST(instr(content, ''class '') > 0 AS INTEGER)"},
         {"name": "has_async", "expr": "CAST(instr(content, ''async '') > 0 OR instr(content, ''await '') > 0 AS INTEGER)"},
         {"name": "has_import", "expr": "CAST(instr(content, ''import '') > 0 AS INTEGER)"},
         {"name": "has_export", "expr": "CAST(instr(content, ''export '') > 0 AS INTEGER)"},
         {"name": "has_react", "expr": "CAST(instr(content, ''React'') > 0 OR instr(content, ''useState'') > 0 OR instr(content, ''<'') > 0 AS INTEGER)"},
         {"name": "has_promise", "expr": "CAST(instr(content, ''Promise'') > 0 OR instr(content, ''.then('') > 0 AS INTEGER)"}
     ]}', 0, 'continue'),

    ('javascript_chunking_v1', 5, 'hash_content', 'hash', 'step_4_features', NULL, 'step_5_hashed',
     '{"algorithm": "sha256", "columns": ["content"], "output_column": "content_hash"}', 0, 'continue'),

    ('javascript_chunking_v1', 6, 'deduplicate', 'filter', 'step_5_hashed',
     'content_hash NOT IN (SELECT hash FROM corpus.chunks)', 'step_6_unique', '{}', 0, 'continue'),

    ('javascript_chunking_v1', 7, 'vectorize_structure', 'vectorize', 'step_6_unique', NULL, 'step_7_vec_struct',
     '{"layer": "structure", "algorithm": "feature_hash", "dimensions": 256, "model_version": "js_structure_v1"}', 0, 'continue'),

    ('javascript_chunking_v1', 8, 'vectorize_lexical', 'vectorize', 'step_6_unique', NULL, 'step_8_vec_lex',
     '{"layer": "lexical", "algorithm": "tfidf", "dimensions": 256, "model_version": "js_lexical_v1"}', 0, 'continue'),

    ('javascript_chunking_v1', 9, 'vectorize_blend', 'vectorize', 'step_6_unique', NULL, 'step_9_vec_blend',
     '{"layer": "blend", "algorithm": "blend", "dimensions": 256,
       "weights": {"structure": 0.4, "lexical": 0.6}, "model_version": "js_blend_v1"}', 0, 'continue'),

    ('javascript_chunking_v1', 10, 'finalize', 'project', 'step_6_unique', '*', '_output', '{}', 0, 'continue');

INSERT OR REPLACE INTO workflow_tags (workflow_id, tag) VALUES
    ('javascript_chunking_v1', 'javascript'), ('javascript_chunking_v1', 'js'), ('javascript_chunking_v1', 'code'), ('javascript_chunking_v1', 'production');

-- ============================================================================
-- TYPESCRIPT WORKFLOW
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'typescript_chunking_v1',
    'TypeScript Code Chunking Pipeline',
    1,
    'Parse TypeScript source files with type-aware features',
    '{"tables": ["raw_files"], "filters": {"mime_type": "text/typescript"}}',
    '{"tables": ["_output", "_output_features", "_output_vectors"]}',
    'active'
);

INSERT OR REPLACE INTO workflow_steps VALUES
    ('typescript_chunking_v1', 1, 'select_ts_files', 'filter', '_input',
     'mime_type = ''text/typescript'' AND status = ''pending''',
     'step_1_ts', '{}', 0, 'skip_remaining'),

    ('typescript_chunking_v1', 2, 'extract_ast', 'external', 'step_1_ts', NULL, 'step_2_parsed',
     '{"extractor": "code", "extractor_version": "1.0.0", "options": {"language": "typescript"}}', 0, 'continue'),

    ('typescript_chunking_v1', 3, 'filter_meaningful', 'filter', 'step_2_parsed',
     'length(content) > 15', 'step_3_filtered', '{}', 0, 'continue'),

    ('typescript_chunking_v1', 4, 'extract_features', 'aggregate', 'step_3_filtered', NULL, 'step_4_features',
     '{"features": [
         {"name": "line_count", "expr": "length(content) - length(replace(content, char(10), '''')) + 1"},
         {"name": "has_interface", "expr": "CAST(instr(content, ''interface '') > 0 AS INTEGER)"},
         {"name": "has_type", "expr": "CAST(instr(content, ''type '') > 0 AS INTEGER)"},
         {"name": "has_generic", "expr": "CAST(instr(content, ''<T>'') > 0 OR instr(content, ''<T,'') > 0 AS INTEGER)"},
         {"name": "has_decorator", "expr": "CAST(instr(content, ''@'') > 0 AS INTEGER)"},
         {"name": "has_enum", "expr": "CAST(instr(content, ''enum '') > 0 AS INTEGER)"},
         {"name": "has_namespace", "expr": "CAST(instr(content, ''namespace '') > 0 AS INTEGER)"},
         {"name": "type_annotation_density", "expr": "CAST((length(content) - length(replace(content, '': '', ''''))) AS REAL) / NULLIF(length(content), 0) * 100"}
     ]}', 0, 'continue'),

    ('typescript_chunking_v1', 5, 'hash_content', 'hash', 'step_4_features', NULL, 'step_5_hashed',
     '{"algorithm": "sha256", "columns": ["content"], "output_column": "content_hash"}', 0, 'continue'),

    ('typescript_chunking_v1', 6, 'deduplicate', 'filter', 'step_5_hashed',
     'content_hash NOT IN (SELECT hash FROM corpus.chunks)', 'step_6_unique', '{}', 0, 'continue'),

    ('typescript_chunking_v1', 7, 'vectorize_structure', 'vectorize', 'step_6_unique', NULL, 'step_7_vec_struct',
     '{"layer": "structure", "algorithm": "feature_hash", "dimensions": 256, "model_version": "ts_structure_v1"}', 0, 'continue'),

    ('typescript_chunking_v1', 8, 'vectorize_lexical', 'vectorize', 'step_6_unique', NULL, 'step_8_vec_lex',
     '{"layer": "lexical", "algorithm": "tfidf", "dimensions": 256, "model_version": "ts_lexical_v1"}', 0, 'continue'),

    ('typescript_chunking_v1', 9, 'vectorize_blend', 'vectorize', 'step_6_unique', NULL, 'step_9_vec_blend',
     '{"layer": "blend", "algorithm": "blend", "dimensions": 256,
       "weights": {"structure": 0.45, "lexical": 0.55}, "model_version": "ts_blend_v1"}', 0, 'continue'),

    ('typescript_chunking_v1', 10, 'finalize', 'project', 'step_6_unique', '*', '_output', '{}', 0, 'continue');

INSERT OR REPLACE INTO workflow_tags (workflow_id, tag) VALUES
    ('typescript_chunking_v1', 'typescript'), ('typescript_chunking_v1', 'ts'), ('typescript_chunking_v1', 'code'), ('typescript_chunking_v1', 'production');

-- ============================================================================
-- BASH WORKFLOW
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'bash_chunking_v1',
    'Bash Script Chunking Pipeline',
    1,
    'Parse Bash/Shell scripts, extract functions and command sequences',
    '{"tables": ["raw_files"], "filters": {"mime_type": ["text/x-sh", "application/x-sh"]}}',
    '{"tables": ["_output", "_output_features", "_output_vectors"]}',
    'active'
);

INSERT OR REPLACE INTO workflow_steps VALUES
    ('bash_chunking_v1', 1, 'select_bash_files', 'filter', '_input',
     '(mime_type = ''text/x-sh'' OR mime_type = ''application/x-sh'') AND status = ''pending''',
     'step_1_bash', '{}', 0, 'skip_remaining'),

    ('bash_chunking_v1', 2, 'extract_structure', 'external', 'step_1_bash', NULL, 'step_2_parsed',
     '{"extractor": "code", "extractor_version": "1.0.0", "options": {"language": "bash"}}', 0, 'continue'),

    ('bash_chunking_v1', 3, 'filter_meaningful', 'filter', 'step_2_parsed',
     'length(content) > 10', 'step_3_filtered', '{}', 0, 'continue'),

    ('bash_chunking_v1', 4, 'extract_features', 'aggregate', 'step_3_filtered', NULL, 'step_4_features',
     '{"features": [
         {"name": "line_count", "expr": "length(content) - length(replace(content, char(10), '''')) + 1"},
         {"name": "has_function", "expr": "CAST(instr(content, ''() {'') > 0 OR instr(content, ''function '') > 0 AS INTEGER)"},
         {"name": "has_pipe", "expr": "CAST(instr(content, '' | '') > 0 AS INTEGER)"},
         {"name": "has_redirect", "expr": "CAST(instr(content, '' > '') > 0 OR instr(content, '' >> '') > 0 OR instr(content, '' < '') > 0 AS INTEGER)"},
         {"name": "has_variable", "expr": "CAST(instr(content, ''$'') > 0 AS INTEGER)"},
         {"name": "has_loop", "expr": "CAST(instr(content, ''for '') > 0 OR instr(content, ''while '') > 0 AS INTEGER)"},
         {"name": "has_conditional", "expr": "CAST(instr(content, ''if '') > 0 OR instr(content, ''[[ '') > 0 AS INTEGER)"},
         {"name": "has_subshell", "expr": "CAST(instr(content, ''$('') > 0 OR instr(content, ''`'') > 0 AS INTEGER)"}
     ]}', 0, 'continue'),

    ('bash_chunking_v1', 5, 'hash_content', 'hash', 'step_4_features', NULL, 'step_5_hashed',
     '{"algorithm": "sha256", "columns": ["content"], "output_column": "content_hash"}', 0, 'continue'),

    ('bash_chunking_v1', 6, 'deduplicate', 'filter', 'step_5_hashed',
     'content_hash NOT IN (SELECT hash FROM corpus.chunks)', 'step_6_unique', '{}', 0, 'continue'),

    ('bash_chunking_v1', 7, 'vectorize_structure', 'vectorize', 'step_6_unique', NULL, 'step_7_vec_struct',
     '{"layer": "structure", "algorithm": "feature_hash", "dimensions": 256, "model_version": "bash_structure_v1"}', 0, 'continue'),

    ('bash_chunking_v1', 8, 'vectorize_lexical', 'vectorize', 'step_6_unique', NULL, 'step_8_vec_lex',
     '{"layer": "lexical", "algorithm": "tfidf", "dimensions": 256, "model_version": "bash_lexical_v1"}', 0, 'continue'),

    ('bash_chunking_v1', 9, 'finalize', 'project', 'step_6_unique', '*', '_output', '{}', 0, 'continue');

INSERT OR REPLACE INTO workflow_tags (workflow_id, tag) VALUES
    ('bash_chunking_v1', 'bash'), ('bash_chunking_v1', 'shell'), ('bash_chunking_v1', 'code'), ('bash_chunking_v1', 'production');

-- ============================================================================
-- SQL WORKFLOW
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'sql_chunking_v1',
    'SQL Chunking Pipeline',
    1,
    'Parse SQL files, extract statements by type (SELECT, CREATE, etc.)',
    '{"tables": ["raw_files"], "filters": {"mime_type": "text/x-sql"}}',
    '{"tables": ["_output", "_output_features", "_output_vectors"]}',
    'active'
);

INSERT OR REPLACE INTO workflow_steps VALUES
    ('sql_chunking_v1', 1, 'select_sql_files', 'filter', '_input',
     'mime_type = ''text/x-sql'' AND status = ''pending''',
     'step_1_sql', '{}', 0, 'skip_remaining'),

    ('sql_chunking_v1', 2, 'extract_statements', 'external', 'step_1_sql', NULL, 'step_2_parsed',
     '{"extractor": "code", "extractor_version": "1.0.0", "options": {"language": "sql"}}', 0, 'continue'),

    ('sql_chunking_v1', 3, 'filter_meaningful', 'filter', 'step_2_parsed',
     'length(content) > 10', 'step_3_filtered', '{}', 0, 'continue'),

    ('sql_chunking_v1', 4, 'extract_features', 'aggregate', 'step_3_filtered', NULL, 'step_4_features',
     '{"features": [
         {"name": "line_count", "expr": "length(content) - length(replace(content, char(10), '''')) + 1"},
         {"name": "is_select", "expr": "CAST(upper(content) LIKE ''SELECT%'' AS INTEGER)"},
         {"name": "is_insert", "expr": "CAST(upper(content) LIKE ''INSERT%'' AS INTEGER)"},
         {"name": "is_update", "expr": "CAST(upper(content) LIKE ''UPDATE%'' AS INTEGER)"},
         {"name": "is_create", "expr": "CAST(upper(content) LIKE ''CREATE%'' AS INTEGER)"},
         {"name": "has_join", "expr": "CAST(instr(upper(content), '' JOIN '') > 0 AS INTEGER)"},
         {"name": "has_subquery", "expr": "CAST(instr(content, ''(SELECT '') > 0 AS INTEGER)"},
         {"name": "has_cte", "expr": "CAST(instr(upper(content), ''WITH '') > 0 AS INTEGER)"},
         {"name": "table_count", "expr": "(length(upper(content)) - length(replace(upper(content), '' FROM '', ''''))) + (length(upper(content)) - length(replace(upper(content), '' JOIN '', '''')))"}
     ]}', 0, 'continue'),

    ('sql_chunking_v1', 5, 'hash_content', 'hash', 'step_4_features', NULL, 'step_5_hashed',
     '{"algorithm": "sha256", "columns": ["content"], "output_column": "content_hash"}', 0, 'continue'),

    ('sql_chunking_v1', 6, 'deduplicate', 'filter', 'step_5_hashed',
     'content_hash NOT IN (SELECT hash FROM corpus.chunks)', 'step_6_unique', '{}', 0, 'continue'),

    ('sql_chunking_v1', 7, 'vectorize_structure', 'vectorize', 'step_6_unique', NULL, 'step_7_vec_struct',
     '{"layer": "structure", "algorithm": "feature_hash", "dimensions": 256, "model_version": "sql_structure_v1"}', 0, 'continue'),

    ('sql_chunking_v1', 8, 'vectorize_lexical', 'vectorize', 'step_6_unique', NULL, 'step_8_vec_lex',
     '{"layer": "lexical", "algorithm": "tfidf", "dimensions": 256, "model_version": "sql_lexical_v1"}', 0, 'continue'),

    ('sql_chunking_v1', 9, 'finalize', 'project', 'step_6_unique', '*', '_output', '{}', 0, 'continue');

INSERT OR REPLACE INTO workflow_tags (workflow_id, tag) VALUES
    ('sql_chunking_v1', 'sql'), ('sql_chunking_v1', 'database'), ('sql_chunking_v1', 'code'), ('sql_chunking_v1', 'production');

-- ============================================================================
-- HTML WORKFLOW (includes HTMX)
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'html_chunking_v1',
    'HTML/HTMX Chunking Pipeline',
    1,
    'Parse HTML documents, extract sections/scripts/styles, detect HTMX attributes',
    '{"tables": ["raw_files"], "filters": {"mime_type": "text/html"}}',
    '{"tables": ["_output", "_output_features", "_output_vectors"]}',
    'active'
);

INSERT OR REPLACE INTO workflow_steps VALUES
    ('html_chunking_v1', 1, 'select_html_files', 'filter', '_input',
     'mime_type = ''text/html'' AND status = ''pending''',
     'step_1_html', '{}', 0, 'skip_remaining'),

    ('html_chunking_v1', 2, 'extract_structure', 'external', 'step_1_html', NULL, 'step_2_parsed',
     '{"extractor": "code", "extractor_version": "1.0.0", "options": {"language": "html"}}', 0, 'continue'),

    ('html_chunking_v1', 3, 'filter_meaningful', 'filter', 'step_2_parsed',
     'length(content) > 20', 'step_3_filtered', '{}', 0, 'continue'),

    ('html_chunking_v1', 4, 'extract_features', 'aggregate', 'step_3_filtered', NULL, 'step_4_features',
     '{"features": [
         {"name": "line_count", "expr": "length(content) - length(replace(content, char(10), '''')) + 1"},
         {"name": "has_script", "expr": "CAST(instr(lower(content), ''<script'') > 0 AS INTEGER)"},
         {"name": "has_style", "expr": "CAST(instr(lower(content), ''<style'') > 0 AS INTEGER)"},
         {"name": "has_form", "expr": "CAST(instr(lower(content), ''<form'') > 0 AS INTEGER)"},
         {"name": "has_htmx", "expr": "CAST(instr(content, ''hx-'') > 0 AS INTEGER)"},
         {"name": "has_alpine", "expr": "CAST(instr(content, ''x-'') > 0 OR instr(content, ''@click'') > 0 AS INTEGER)"},
         {"name": "has_template", "expr": "CAST(instr(lower(content), ''<template'') > 0 AS INTEGER)"},
         {"name": "tag_density", "expr": "CAST((length(content) - length(replace(content, ''<'', ''''))) AS REAL) / NULLIF(length(content), 0) * 100"}
     ]}', 0, 'continue'),

    ('html_chunking_v1', 5, 'hash_content', 'hash', 'step_4_features', NULL, 'step_5_hashed',
     '{"algorithm": "sha256", "columns": ["content"], "output_column": "content_hash"}', 0, 'continue'),

    ('html_chunking_v1', 6, 'deduplicate', 'filter', 'step_5_hashed',
     'content_hash NOT IN (SELECT hash FROM corpus.chunks)', 'step_6_unique', '{}', 0, 'continue'),

    ('html_chunking_v1', 7, 'vectorize_structure', 'vectorize', 'step_6_unique', NULL, 'step_7_vec_struct',
     '{"layer": "structure", "algorithm": "feature_hash", "dimensions": 256, "model_version": "html_structure_v1"}', 0, 'continue'),

    ('html_chunking_v1', 8, 'vectorize_lexical', 'vectorize', 'step_6_unique', NULL, 'step_8_vec_lex',
     '{"layer": "lexical", "algorithm": "tfidf", "dimensions": 256, "model_version": "html_lexical_v1"}', 0, 'continue'),

    ('html_chunking_v1', 9, 'finalize', 'project', 'step_6_unique', '*', '_output', '{}', 0, 'continue');

INSERT OR REPLACE INTO workflow_tags (workflow_id, tag) VALUES
    ('html_chunking_v1', 'html'), ('html_chunking_v1', 'htmx'), ('html_chunking_v1', 'web'), ('html_chunking_v1', 'production');

-- ============================================================================
-- MARKDOWN WORKFLOW
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'markdown_chunking_v1',
    'Markdown Chunking Pipeline',
    1,
    'Parse Markdown documents, extract sections by headings, preserve code blocks',
    '{"tables": ["raw_files"], "filters": {"mime_type": "text/markdown"}}',
    '{"tables": ["_output", "_output_features", "_output_vectors"]}',
    'active'
);

INSERT OR REPLACE INTO workflow_steps VALUES
    ('markdown_chunking_v1', 1, 'select_md_files', 'filter', '_input',
     'mime_type = ''text/markdown'' AND status = ''pending''',
     'step_1_md', '{}', 0, 'skip_remaining'),

    ('markdown_chunking_v1', 2, 'extract_structure', 'external', 'step_1_md', NULL, 'step_2_parsed',
     '{"extractor": "code", "extractor_version": "1.0.0", "options": {"language": "markdown"}}', 0, 'continue'),

    ('markdown_chunking_v1', 3, 'filter_meaningful', 'filter', 'step_2_parsed',
     'length(content) > 20', 'step_3_filtered', '{}', 0, 'continue'),

    ('markdown_chunking_v1', 4, 'extract_features', 'aggregate', 'step_3_filtered', NULL, 'step_4_features',
     '{"features": [
         {"name": "line_count", "expr": "length(content) - length(replace(content, char(10), '''')) + 1"},
         {"name": "heading_level", "expr": "CASE WHEN content LIKE ''###### %'' THEN 6 WHEN content LIKE ''##### %'' THEN 5 WHEN content LIKE ''#### %'' THEN 4 WHEN content LIKE ''### %'' THEN 3 WHEN content LIKE ''## %'' THEN 2 WHEN content LIKE ''# %'' THEN 1 ELSE 0 END"},
         {"name": "has_code_block", "expr": "CAST(instr(content, ''```'') > 0 AS INTEGER)"},
         {"name": "has_link", "expr": "CAST(instr(content, '']('') > 0 AS INTEGER)"},
         {"name": "has_image", "expr": "CAST(instr(content, ''!['') > 0 AS INTEGER)"},
         {"name": "has_list", "expr": "CAST(instr(content, char(10) || ''- '') > 0 OR instr(content, char(10) || ''* '') > 0 AS INTEGER)"},
         {"name": "has_table", "expr": "CAST(instr(content, ''|'') > 0 AND instr(content, ''---'') > 0 AS INTEGER)"},
         {"name": "formatting_density", "expr": "CAST((length(content) - length(replace(replace(replace(content, ''**'', ''''), ''__'', ''''), ''``'', ''''))) AS REAL) / NULLIF(length(content), 0) * 100"}
     ]}', 0, 'continue'),

    ('markdown_chunking_v1', 5, 'hash_content', 'hash', 'step_4_features', NULL, 'step_5_hashed',
     '{"algorithm": "sha256", "columns": ["content"], "output_column": "content_hash"}', 0, 'continue'),

    ('markdown_chunking_v1', 6, 'deduplicate', 'filter', 'step_5_hashed',
     'content_hash NOT IN (SELECT hash FROM corpus.chunks)', 'step_6_unique', '{}', 0, 'continue'),

    ('markdown_chunking_v1', 7, 'vectorize_structure', 'vectorize', 'step_6_unique', NULL, 'step_7_vec_struct',
     '{"layer": "structure", "algorithm": "feature_hash", "dimensions": 256, "model_version": "md_structure_v1"}', 0, 'continue'),

    ('markdown_chunking_v1', 8, 'vectorize_lexical', 'vectorize', 'step_6_unique', NULL, 'step_8_vec_lex',
     '{"layer": "lexical", "algorithm": "tfidf", "dimensions": 256, "model_version": "md_lexical_v1"}', 0, 'continue'),

    ('markdown_chunking_v1', 9, 'vectorize_blend', 'vectorize', 'step_6_unique', NULL, 'step_9_vec_blend',
     '{"layer": "blend", "algorithm": "blend", "dimensions": 256,
       "weights": {"structure": 0.3, "lexical": 0.7}, "model_version": "md_blend_v1"}', 0, 'continue'),

    ('markdown_chunking_v1', 10, 'finalize', 'project', 'step_6_unique', '*', '_output', '{}', 0, 'continue');

INSERT OR REPLACE INTO workflow_tags (workflow_id, tag) VALUES
    ('markdown_chunking_v1', 'markdown'), ('markdown_chunking_v1', 'md'), ('markdown_chunking_v1', 'documentation'), ('markdown_chunking_v1', 'production');

-- ============================================================================
-- TEXT WORKFLOW (generic text)
-- ============================================================================

INSERT OR REPLACE INTO workflows (id, name, version, description, input_schema, output_schema, status)
VALUES (
    'text_chunking_v1',
    'Plain Text Chunking Pipeline',
    1,
    'Parse generic text files with paragraph-based chunking',
    '{"tables": ["raw_files"], "filters": {"mime_type": "text/plain"}}',
    '{"tables": ["_output", "_output_features", "_output_vectors"]}',
    'active'
);

INSERT OR REPLACE INTO workflow_steps VALUES
    ('text_chunking_v1', 1, 'select_text_files', 'filter', '_input',
     'mime_type = ''text/plain'' AND status = ''pending''',
     'step_1_text', '{}', 0, 'skip_remaining'),

    ('text_chunking_v1', 2, 'parse_paragraphs', 'filter', 'step_1_text',
     'length(content) > 10', 'step_2_parsed', '{}', 0, 'continue'),

    ('text_chunking_v1', 3, 'chunk_by_tokens', 'window', 'step_2_parsed', NULL, 'step_3_chunks',
     '{"strategy": "semantic", "max_tokens": 512, "min_tokens": 50, "overlap_tokens": 50}', 0, 'continue'),

    ('text_chunking_v1', 4, 'extract_features', 'aggregate', 'step_3_chunks', NULL, 'step_4_features',
     '{"features": [
         {"name": "token_count", "expr": "length(content) / 4"},
         {"name": "line_count", "expr": "length(content) - length(replace(content, char(10), '''')) + 1"},
         {"name": "word_count", "expr": "length(content) - length(replace(content, '' '', '''')) + 1"},
         {"name": "avg_word_length", "expr": "CAST(length(replace(content, '' '', '''')) AS REAL) / NULLIF(length(content) - length(replace(content, '' '', '''')) + 1, 0)"}
     ]}', 0, 'continue'),

    ('text_chunking_v1', 5, 'hash_content', 'hash', 'step_4_features', NULL, 'step_5_hashed',
     '{"algorithm": "sha256", "columns": ["content"], "output_column": "content_hash"}', 0, 'continue'),

    ('text_chunking_v1', 6, 'deduplicate', 'filter', 'step_5_hashed',
     'content_hash NOT IN (SELECT hash FROM corpus.chunks)', 'step_6_unique', '{}', 0, 'continue'),

    ('text_chunking_v1', 7, 'vectorize_lexical', 'vectorize', 'step_6_unique', NULL, 'step_7_vec_lex',
     '{"layer": "lexical", "algorithm": "tfidf", "dimensions": 256, "model_version": "text_lexical_v1"}', 0, 'continue'),

    ('text_chunking_v1', 8, 'finalize', 'project', 'step_6_unique', '*', '_output', '{}', 0, 'continue');

INSERT OR REPLACE INTO workflow_tags (workflow_id, tag) VALUES
    ('text_chunking_v1', 'text'), ('text_chunking_v1', 'plain'), ('text_chunking_v1', 'production');
