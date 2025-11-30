#!/bin/bash
# GoRAGlite SQL Test Runner
# Exécute tous les tests SQL et affiche les résultats

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "========================================"
echo "GoRAGlite SQL Test Suite"
echo "========================================"
echo ""

TESTS_PASSED=0
TESTS_FAILED=0

run_test() {
    local test_file="$1"
    local test_name="$(basename "$test_file" .sql)"
    local db_file="/tmp/test_${test_name}_$$.db"
    local output_file="/tmp/test_${test_name}_output_$$.txt"

    echo -e "${YELLOW}Running: ${test_name}${NC}"

    # Run test and capture output (include stderr for expected error messages)
    sqlite3 "$db_file" < "$test_file" > "$output_file" 2>&1 || true

    # Display output
    cat "$output_file"

    # Check if test passed by looking for the success marker
    if grep -q "ALL.*TESTS PASSED" "$output_file"; then
        echo -e "${GREEN}✓ PASSED: ${test_name}${NC}"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}✗ FAILED: ${test_name}${NC}"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi

    # Cleanup
    rm -f "$db_file" "${db_file}-wal" "${db_file}-shm" "$output_file"
    echo ""
}

# Run all tests
echo "----------------------------------------"
echo "Test 1: Corpus Schema"
echo "----------------------------------------"
run_test "test_corpus_schema.sql"

echo "----------------------------------------"
echo "Test 2: Workflows Schema"
echo "----------------------------------------"
run_test "test_workflows_schema.sql"

echo "----------------------------------------"
echo "Test 3: Run Schema"
echo "----------------------------------------"
run_test "test_run_schema.sql"

echo "----------------------------------------"
echo "Test 4: End-to-End Workflow"
echo "----------------------------------------"
run_test "test_e2e_workflow.sql"

# Summary
echo "========================================"
echo "TEST SUMMARY"
echo "========================================"
echo -e "Passed: ${GREEN}${TESTS_PASSED}${NC}"
echo -e "Failed: ${RED}${TESTS_FAILED}${NC}"
echo ""

if [ $TESTS_FAILED -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed!${NC}"
    exit 1
fi
