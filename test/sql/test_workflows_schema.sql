-- Test: Workflows Schema Validation
-- Vérifie que le schéma workflows.sql est valide

.bail on
.headers on
.mode column

-- ============================================================================
-- TEST 1: Schema Creation
-- ============================================================================
.print "=== TEST 1: Workflows Schema Creation ==="

.read ../../sql/schema/workflows.sql

SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;

-- ============================================================================
-- TEST 2: Load Built-in Workflows
-- ============================================================================
.print ""
.print "=== TEST 2: Load Built-in Workflows ==="

.read ../../sql/workflows/chunk_pdf.sql
.read ../../sql/workflows/chunk_docx.sql
.read ../../sql/workflows/search_default.sql
.read ../../sql/workflows/chunk_code.sql

-- List all workflows
SELECT id, name, version, status FROM workflows ORDER BY id;

-- ============================================================================
-- TEST 3: Workflow Steps Count
-- ============================================================================
.print ""
.print "=== TEST 3: Workflow Steps Count ==="

SELECT workflow_id, COUNT(*) as step_count
FROM workflow_steps
GROUP BY workflow_id
ORDER BY workflow_id;

-- ============================================================================
-- TEST 4: Verify Step Order
-- ============================================================================
.print ""
.print "=== TEST 4: Verify PDF Workflow Steps ==="

SELECT step_order, step_name, operation, on_empty
FROM workflow_steps
WHERE workflow_id = 'pdf_chunking_v1'
ORDER BY step_order;

-- ============================================================================
-- TEST 5: Verify Go Workflow Steps
-- ============================================================================
.print ""
.print "=== TEST 5: Verify Go Workflow Steps ==="

SELECT step_order, step_name, operation
FROM workflow_steps
WHERE workflow_id = 'go_chunking_v1'
ORDER BY step_order;

-- ============================================================================
-- TEST 6: Workflow Tags
-- ============================================================================
.print ""
.print "=== TEST 6: Workflow Tags ==="

SELECT workflow_id, GROUP_CONCAT(tag, ', ') as tags
FROM workflow_tags
GROUP BY workflow_id
ORDER BY workflow_id;

-- ============================================================================
-- TEST 7: Search Configs
-- ============================================================================
.print ""
.print "=== TEST 7: Search Configs ==="

SELECT id, name, top_k, min_score FROM search_configs;

-- ============================================================================
-- TEST 8: Find Workflows by Tag
-- ============================================================================
.print ""
.print "=== TEST 8: Find 'code' Workflows ==="

SELECT w.id, w.name
FROM workflows w
JOIN workflow_tags t ON w.id = t.workflow_id
WHERE t.tag = 'code'
ORDER BY w.id;

-- ============================================================================
-- TEST 9: Operation Types Used
-- ============================================================================
.print ""
.print "=== TEST 9: Operation Types Distribution ==="

SELECT operation, COUNT(*) as usage_count
FROM workflow_steps
GROUP BY operation
ORDER BY usage_count DESC;

-- ============================================================================
-- TEST 10: Verify All Operations are Valid
-- ============================================================================
.print ""
.print "=== TEST 10: Invalid Operations Check ==="

SELECT CASE WHEN COUNT(*) = 0
       THEN 'All operations valid'
       ELSE 'ERROR: Found invalid operations' END as result
FROM workflow_steps
WHERE operation NOT IN ('filter', 'project', 'join', 'aggregate', 'diff',
                        'window', 'hash', 'vectorize', 'external', 'fork', 'merge');

-- ============================================================================
-- SUMMARY
-- ============================================================================
.print ""
.print "=== WORKFLOW SUMMARY ==="

SELECT
    (SELECT COUNT(*) FROM workflows) as total_workflows,
    (SELECT COUNT(*) FROM workflow_steps) as total_steps,
    (SELECT COUNT(DISTINCT tag) FROM workflow_tags) as unique_tags;

.print ""
.print "=== ALL WORKFLOWS SCHEMA TESTS PASSED ==="
