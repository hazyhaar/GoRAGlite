-- Test: End-to-End Workflow Simulation
-- Simule l'exécution complète d'un workflow avec merge

.bail on
.headers on
.mode column

-- ============================================================================
-- SETUP: Create corpus and run databases
-- ============================================================================
.print "=== E2E TEST: Setup ==="

-- Create corpus schema
.read ../../sql/schema/corpus.sql

-- Insert test files
INSERT INTO raw_files (id, source_path, mime_type, size, external_path, checksum, status)
VALUES
    ('file_001', '/docs/report.pdf', 'application/pdf', 10240, '/storage/raw/fi/file_001', 'chk001', 'pending'),
    ('file_002', '/docs/manual.pdf', 'application/pdf', 20480, '/storage/raw/fi/file_002', 'chk002', 'pending'),
    ('file_003', '/src/main.go', 'text/x-go', 2048, '/storage/raw/fi/file_003', 'chk003', 'pending');

.print "Created 3 test files"

-- ============================================================================
-- SIMULATE: Worker creates run and processes
-- ============================================================================
.print ""
.print "=== E2E TEST: Simulate Run ==="

-- Run metadata (would normally be in separate run.db)
CREATE TABLE run_meta AS SELECT
    'run_e2e_001' as run_id,
    'pdf_chunking_v1' as workflow_id,
    1 as workflow_version,
    datetime('now') as started_at,
    'running' as status;

-- Step 1: Filter PDFs
CREATE TABLE step_1_pdfs AS
SELECT * FROM raw_files WHERE mime_type = 'application/pdf' AND status = 'pending';

.print "Step 1: Filtered PDFs"
SELECT id, source_path FROM step_1_pdfs;

-- Step 2: Simulate extraction (normally external)
CREATE TABLE step_2_extracted AS
SELECT
    id || '_seg_' || seq as segment_id,
    id as file_id,
    'text' as segment_type,
    'Extracted content segment ' || seq || ' from ' || source_path as content,
    seq as position
FROM step_1_pdfs, (SELECT 1 as seq UNION SELECT 2 UNION SELECT 3);

.print ""
.print "Step 2: Extracted segments"
SELECT segment_id, file_id, position FROM step_2_extracted;

-- Step 3: Chunk
CREATE TABLE step_3_chunks AS
SELECT
    segment_id || '_chunk' as chunk_id,
    file_id,
    content,
    length(content) / 4 as token_count,
    'semantic' as chunk_type,
    hex(randomblob(8)) as hash,
    position
FROM step_2_extracted;

.print ""
.print "Step 3: Created chunks"
SELECT chunk_id, file_id, token_count FROM step_3_chunks;

-- Step 4: Extract features
CREATE TABLE step_4_features AS
SELECT
    chunk_id,
    file_id,
    content,
    token_count,
    chunk_type,
    hash,
    position,
    token_count as feature_token_count,
    length(content) as feature_char_count,
    CAST(length(content) - length(replace(content, ' ', '')) + 1 AS INTEGER) as feature_word_count
FROM step_3_chunks;

.print ""
.print "Step 4: Extracted features"
SELECT chunk_id, feature_token_count, feature_char_count, feature_word_count FROM step_4_features LIMIT 3;

-- Step 5: Create vectors (simulated)
CREATE TABLE step_5_vectors AS
SELECT
    chunk_id,
    'structure' as layer,
    zeroblob(256*4) as vector,
    256 as dimensions,
    'test_v1' as model_version
FROM step_4_features;

.print ""
.print "Step 5: Created vectors"
SELECT chunk_id, layer, dimensions FROM step_5_vectors LIMIT 3;

-- ============================================================================
-- SIMULATE: Merge into corpus
-- ============================================================================
.print ""
.print "=== E2E TEST: Merge Results ==="

-- Insert chunks into corpus
INSERT INTO chunks (id, file_id, content, token_count, chunk_type, hash, position, created_by_run)
SELECT chunk_id, file_id, content, token_count, chunk_type, hash, position, 'run_e2e_001'
FROM step_4_features;

.print "Merged chunks into corpus"
SELECT id, file_id, token_count FROM chunks;

-- Insert features
INSERT INTO chunk_features (chunk_id, feature_name, feature_value)
SELECT chunk_id, 'token_count', feature_token_count FROM step_4_features
UNION ALL
SELECT chunk_id, 'char_count', feature_char_count FROM step_4_features
UNION ALL
SELECT chunk_id, 'word_count', feature_word_count FROM step_4_features;

.print ""
.print "Merged features"
SELECT chunk_id, feature_name, feature_value FROM chunk_features ORDER BY chunk_id, feature_name LIMIT 9;

-- Insert vectors
INSERT INTO chunk_vectors (chunk_id, layer, vector, dimensions, model_version)
SELECT chunk_id, layer, vector, dimensions, model_version FROM step_5_vectors;

.print ""
.print "Merged vectors"
SELECT chunk_id, layer, dimensions FROM chunk_vectors;

-- Update file status
UPDATE raw_files SET status = 'vectorized'
WHERE id IN (SELECT DISTINCT file_id FROM step_4_features);

.print ""
.print "Updated file status"
SELECT id, status FROM raw_files;

-- Record run history
INSERT INTO run_history (run_id, workflow_id, workflow_version, started_at, finished_at, status, rows_produced, merge_status)
SELECT run_id, workflow_id, workflow_version, started_at, datetime('now'), 'completed',
       (SELECT COUNT(*) FROM step_4_features), 'merged'
FROM run_meta;

.print ""
.print "Recorded run history"
SELECT * FROM run_history;

-- ============================================================================
-- SIMULATE: Search
-- ============================================================================
.print ""
.print "=== E2E TEST: Search Simulation ==="

-- FTS search
.print "FTS search for 'Extracted content':"
SELECT c.id, c.file_id, substr(c.content, 1, 50) as snippet
FROM chunks c
WHERE c.id IN (SELECT rowid FROM chunks_fts WHERE chunks_fts MATCH 'Extracted content');

-- ============================================================================
-- VERIFY: Final State
-- ============================================================================
.print ""
.print "=== E2E TEST: Final Verification ==="

SELECT 'Files' as entity, COUNT(*) as total, SUM(CASE WHEN status='vectorized' THEN 1 ELSE 0 END) as processed
FROM raw_files
UNION ALL
SELECT 'Chunks', COUNT(*), COUNT(DISTINCT file_id) FROM chunks
UNION ALL
SELECT 'Features', COUNT(*), COUNT(DISTINCT chunk_id) FROM chunk_features
UNION ALL
SELECT 'Vectors', COUNT(*), COUNT(DISTINCT chunk_id) FROM chunk_vectors;

-- ============================================================================
-- CLEANUP
-- ============================================================================
.print ""
.print "=== E2E TEST: Cleanup ==="

DROP TABLE IF EXISTS run_meta;
DROP TABLE IF EXISTS step_1_pdfs;
DROP TABLE IF EXISTS step_2_extracted;
DROP TABLE IF EXISTS step_3_chunks;
DROP TABLE IF EXISTS step_4_features;
DROP TABLE IF EXISTS step_5_vectors;

.print "Cleaned up temporary tables"

.print ""
.print "=== ALL E2E TESTS PASSED ==="
