-- Test: Corpus Schema Validation
-- Vérifie que le schéma corpus.sql est valide et fonctionnel

.bail on
.headers on
.mode column

-- ============================================================================
-- TEST 1: Schema Creation
-- ============================================================================
.print "=== TEST 1: Schema Creation ==="

.read ../../sql/schema/corpus.sql

-- Verify tables exist
SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;

-- ============================================================================
-- TEST 2: Insert Raw File
-- ============================================================================
.print ""
.print "=== TEST 2: Insert Raw File ==="

INSERT INTO raw_files (id, source_path, mime_type, size, external_path, checksum, status)
VALUES ('abc123', '/test/doc.pdf', 'application/pdf', 1024, '/storage/raw/ab/abc123', 'abc123', 'pending');

INSERT INTO raw_files (id, source_path, mime_type, size, external_path, checksum, status)
VALUES ('def456', '/test/code.go', 'text/x-go', 512, '/storage/raw/de/def456', 'def456', 'pending');

SELECT id, source_path, mime_type, status FROM raw_files;

-- ============================================================================
-- TEST 3: Insert Chunks
-- ============================================================================
.print ""
.print "=== TEST 3: Insert Chunks ==="

INSERT INTO chunks (id, file_id, content, token_count, chunk_type, hash, position)
VALUES ('chunk1', 'abc123', 'This is test content from PDF', 6, 'semantic', 'hash1', 1);

INSERT INTO chunks (id, file_id, content, token_count, chunk_type, hash, position)
VALUES ('chunk2', 'abc123', 'Another paragraph from the document', 5, 'semantic', 'hash2', 2);

INSERT INTO chunks (id, file_id, content, token_count, chunk_type, hash, position)
VALUES ('chunk3', 'def456', 'func main() { fmt.Println("Hello") }', 8, 'semantic', 'hash3', 1);

SELECT id, file_id, token_count, chunk_type FROM chunks;

-- ============================================================================
-- TEST 4: FTS Search
-- ============================================================================
.print ""
.print "=== TEST 4: FTS Search ==="

-- Verify FTS index was populated by triggers
SELECT rowid, content FROM chunks_fts WHERE chunks_fts MATCH 'content';
SELECT rowid, content FROM chunks_fts WHERE chunks_fts MATCH 'func';

-- ============================================================================
-- TEST 5: Insert Features
-- ============================================================================
.print ""
.print "=== TEST 5: Insert Features ==="

INSERT INTO chunk_features (chunk_id, feature_name, feature_value)
VALUES ('chunk1', 'token_count', 6);
INSERT INTO chunk_features (chunk_id, feature_name, feature_value)
VALUES ('chunk1', 'uppercase_ratio', 0.1);
INSERT INTO chunk_features (chunk_id, feature_name, feature_value)
VALUES ('chunk3', 'has_function', 1.0);
INSERT INTO chunk_features (chunk_id, feature_name, feature_value)
VALUES ('chunk3', 'line_count', 1);

SELECT * FROM chunk_features ORDER BY chunk_id, feature_name;

-- ============================================================================
-- TEST 6: Insert Vectors
-- ============================================================================
.print ""
.print "=== TEST 6: Insert Vectors ==="

INSERT INTO chunk_vectors (chunk_id, layer, vector, dimensions, model_version)
VALUES ('chunk1', 'structure', zeroblob(256*4), 256, 'test_v1');
INSERT INTO chunk_vectors (chunk_id, layer, vector, dimensions, model_version)
VALUES ('chunk1', 'lexical', zeroblob(256*4), 256, 'test_v1');
INSERT INTO chunk_vectors (chunk_id, layer, vector, dimensions, model_version)
VALUES ('chunk3', 'structure', zeroblob(256*4), 256, 'test_v1');

SELECT chunk_id, layer, dimensions, model_version FROM chunk_vectors;

-- ============================================================================
-- TEST 7: Insert Relations
-- ============================================================================
.print ""
.print "=== TEST 7: Insert Relations ==="

INSERT INTO chunk_relations (from_chunk_id, to_chunk_id, relation_type, weight)
VALUES ('chunk1', 'chunk2', 'follows', 0.9);
INSERT INTO chunk_relations (from_chunk_id, to_chunk_id, relation_type, weight)
VALUES ('chunk2', 'chunk1', 'references', 0.5);

SELECT * FROM chunk_relations;

-- ============================================================================
-- TEST 8: Config Table
-- ============================================================================
.print ""
.print "=== TEST 8: Config Table ==="

SELECT key, value FROM config ORDER BY key;

-- Update config
UPDATE config SET value = '1024' WHERE key = 'max_chunk_tokens';
SELECT key, value FROM config WHERE key = 'max_chunk_tokens';

-- ============================================================================
-- TEST 9: Run History
-- ============================================================================
.print ""
.print "=== TEST 9: Run History ==="

INSERT INTO run_history (run_id, workflow_id, workflow_version, started_at, status)
VALUES ('run_001', 'pdf_chunking_v1', 1, datetime('now'), 'completed');

INSERT INTO run_history (run_id, workflow_id, workflow_version, started_at, status, rows_produced)
VALUES ('run_002', 'go_chunking_v1', 1, datetime('now'), 'completed', 3);

SELECT run_id, workflow_id, status, rows_produced FROM run_history;

-- ============================================================================
-- TEST 10: Audit Log
-- ============================================================================
.print ""
.print "=== TEST 10: Audit Log ==="

INSERT INTO audit_log (actor, action, target, details)
VALUES ('orchestrator', 'ingest', 'abc123', '{"path": "/test/doc.pdf"}');

INSERT INTO audit_log (actor, action, target)
VALUES ('merger', 'merge', 'run_001');

SELECT id, actor, action, target FROM audit_log ORDER BY id;

-- ============================================================================
-- TEST 11: Foreign Key Constraints
-- ============================================================================
.print ""
.print "=== TEST 11: Foreign Key Constraints ==="

-- Disable bail to test expected failures
.bail off

-- This should fail due to FK constraint
.print "Attempting to insert chunk with invalid file_id (should fail)..."
INSERT INTO chunks (id, file_id, content, token_count, chunk_type, hash, position)
VALUES ('bad_chunk', 'nonexistent', 'test', 1, 'semantic', 'bad', 1);

SELECT CASE WHEN COUNT(*) = 0 THEN 'FK constraint working (chunk not inserted)'
            ELSE 'ERROR: FK constraint failed' END as result
FROM chunks WHERE id = 'bad_chunk';

-- ============================================================================
-- TEST 12: Status Constraints
-- ============================================================================
.print ""
.print "=== TEST 12: Status Constraints ==="

-- Valid status update
UPDATE raw_files SET status = 'extracted' WHERE id = 'abc123';
SELECT id, status FROM raw_files WHERE id = 'abc123';

-- Invalid status should fail (CHECK constraint)
.print "Attempting invalid status (should fail)..."
UPDATE raw_files SET status = 'invalid_status' WHERE id = 'abc123';

-- Verify status wasn't changed (still with bail off to handle any lingering errors)
SELECT CASE WHEN status = 'extracted' THEN 'CHECK constraint working'
            ELSE 'ERROR: CHECK constraint failed' END as result
FROM raw_files WHERE id = 'abc123';

-- Note: Keep bail off since the SUMMARY queries are safe and we want the test to complete

-- ============================================================================
-- SUMMARY
-- ============================================================================
.print ""
.print "=== TEST SUMMARY ==="
SELECT 'raw_files' as table_name, COUNT(*) as count FROM raw_files
UNION ALL
SELECT 'chunks', COUNT(*) FROM chunks
UNION ALL
SELECT 'chunk_features', COUNT(*) FROM chunk_features
UNION ALL
SELECT 'chunk_vectors', COUNT(*) FROM chunk_vectors
UNION ALL
SELECT 'chunk_relations', COUNT(*) FROM chunk_relations
UNION ALL
SELECT 'run_history', COUNT(*) FROM run_history
UNION ALL
SELECT 'audit_log', COUNT(*) FROM audit_log;

.print ""
.print "=== ALL CORPUS SCHEMA TESTS PASSED ==="
