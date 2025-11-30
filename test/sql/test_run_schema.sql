-- Test: Run Schema Validation
-- Vérifie que le schéma run.sql (éphémère) est valide

.bail on
.headers on
.mode column

-- ============================================================================
-- TEST 1: Schema Creation
-- ============================================================================
.print "=== TEST 1: Run Schema Creation ==="

.read ../../sql/schema/run.sql

SELECT name FROM sqlite_master WHERE type='table' AND name LIKE '_%' ORDER BY name;

-- ============================================================================
-- TEST 2: Initialize Run Metadata
-- ============================================================================
.print ""
.print "=== TEST 2: Initialize Run Metadata ==="

INSERT INTO _run_meta (run_id, workflow_id, workflow_version, input_source, status, worker_id)
VALUES ('run_test_001', 'pdf_chunking_v1', 1, '/test/input', 'running', 'worker-1');

SELECT * FROM _run_meta;

-- ============================================================================
-- TEST 3: Copy Workflow Steps
-- ============================================================================
.print ""
.print "=== TEST 3: Copy Workflow Steps ==="

INSERT INTO _workflow_steps (step_order, step_name, operation, source, predicate, output, on_empty)
VALUES
    (1, 'filter_pdfs', 'filter', '_input', 'mime_type = "application/pdf"', 'step_1', 'skip_remaining'),
    (2, 'extract_text', 'external', 'step_1', NULL, 'step_2', 'continue'),
    (3, 'chunk_content', 'window', 'step_2', NULL, 'step_3', 'continue'),
    (4, 'vectorize', 'vectorize', 'step_3', NULL, '_output', 'continue');

SELECT step_order, step_name, operation, output FROM _workflow_steps;

-- ============================================================================
-- TEST 4: Simulate Step Execution
-- ============================================================================
.print ""
.print "=== TEST 4: Simulate Step Execution ==="

-- Step 1 execution
INSERT INTO _step_executions (step_order, step_name, rows_in, rows_out, duration_ms, delta_score, output_table)
VALUES (1, 'filter_pdfs', 100, 45, 12, 0.55, 'step_1');

-- Step 2 execution
INSERT INTO _step_executions (step_order, step_name, rows_in, rows_out, duration_ms, delta_score, output_table)
VALUES (2, 'extract_text', 45, 180, 523, -3.0, 'step_2');

-- Step 3 execution
INSERT INTO _step_executions (step_order, step_name, rows_in, rows_out, duration_ms, delta_score, output_table)
VALUES (3, 'chunk_content', 180, 420, 89, -1.33, 'step_3');

-- Step 4 execution
INSERT INTO _step_executions (step_order, step_name, rows_in, rows_out, duration_ms, delta_score, output_table, finished_at)
VALUES (4, 'vectorize', 420, 420, 1250, 0, '_output', datetime('now'));

SELECT step_order, step_name, rows_in, rows_out, duration_ms FROM _step_executions;

-- ============================================================================
-- TEST 5: Record Deltas
-- ============================================================================
.print ""
.print "=== TEST 5: Record Deltas ==="

INSERT INTO _deltas (step_from, step_to, rows_before, rows_after, rows_lost, rows_gained, delta_type, delta_score, jaccard_index)
VALUES
    (0, 1, 100, 45, 55, 0, 'reduction', 0.55, 0.45),
    (1, 2, 45, 180, 0, 135, 'expansion', -3.0, 0.25),
    (2, 3, 180, 420, 0, 240, 'expansion', -1.33, 0.30),
    (3, 4, 420, 420, 0, 0, 'transformation', 0, 1.0);

SELECT step_from, step_to, rows_before, rows_after, delta_type, delta_score FROM _deltas;

-- ============================================================================
-- TEST 6: Create Output Tables
-- ============================================================================
.print ""
.print "=== TEST 6: Create Output Tables ==="

-- Simulate final output
INSERT INTO _output (id, file_id, content, token_count, chunk_type, hash, position)
VALUES
    ('out1', 'file1', 'First chunk content here', 4, 'semantic', 'h1', 1),
    ('out2', 'file1', 'Second chunk of content', 4, 'semantic', 'h2', 2),
    ('out3', 'file2', 'Another file chunk', 3, 'semantic', 'h3', 1);

SELECT id, file_id, token_count, position FROM _output;

-- Output vectors
INSERT INTO _output_vectors (chunk_id, layer, vector, dimensions, model_version)
VALUES
    ('out1', 'structure', zeroblob(256*4), 256, 'v1'),
    ('out1', 'lexical', zeroblob(256*4), 256, 'v1'),
    ('out2', 'structure', zeroblob(256*4), 256, 'v1'),
    ('out3', 'structure', zeroblob(256*4), 256, 'v1');

SELECT chunk_id, layer, dimensions FROM _output_vectors;

-- ============================================================================
-- TEST 7: Test Views
-- ============================================================================
.print ""
.print "=== TEST 7: Test Run Summary View ==="

-- Update run status
UPDATE _run_meta SET status = 'completed', finished_at = datetime('now');

SELECT * FROM _run_summary;

.print ""
.print "=== TEST 8: Test Step Progression View ==="

SELECT * FROM _step_progression;

-- ============================================================================
-- TEST 9: Record Errors
-- ============================================================================
.print ""
.print "=== TEST 9: Error Logging ==="

INSERT INTO _errors (step_order, error_type, error_message, row_id)
VALUES (2, 'validation_error', 'Empty content detected', 'seg_45');

SELECT * FROM _errors;

-- ============================================================================
-- TEST 10: Step Statistics
-- ============================================================================
.print ""
.print "=== TEST 10: Step Statistics ==="

INSERT INTO _step_stats (step_order, row_count, null_counts, distinct_counts)
VALUES
    (1, 45, '{"content": 0}', '{"file_id": 3}'),
    (2, 180, '{"page": 5}', '{"segment_type": 4}'),
    (3, 420, '{"content": 0}', '{"chunk_type": 1}');

SELECT * FROM _step_stats;

-- ============================================================================
-- SUMMARY
-- ============================================================================
.print ""
.print "=== RUN SCHEMA SUMMARY ==="

SELECT
    (SELECT COUNT(*) FROM _output) as output_rows,
    (SELECT COUNT(*) FROM _output_vectors) as output_vectors,
    (SELECT SUM(duration_ms) FROM _step_executions) as total_duration_ms,
    (SELECT MAX(rows_out) FROM _step_executions) as max_rows;

.print ""
.print "=== ALL RUN SCHEMA TESTS PASSED ==="
