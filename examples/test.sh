#!/bin/bash
# Test Bash file for GoRAGlite

set -euo pipefail

# Configuration
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly LOG_FILE="${SCRIPT_DIR}/output.log"

# Logging function
log() {
    local level="$1"
    shift
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [$level] $*" | tee -a "$LOG_FILE"
}

# Error handler
error_handler() {
    local line="$1"
    log "ERROR" "Script failed at line $line"
    exit 1
}
trap 'error_handler $LINENO' ERR

# Check dependencies
check_deps() {
    local deps=("curl" "jq" "git")
    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &>/dev/null; then
            log "ERROR" "Missing dependency: $dep"
            return 1
        fi
    done
    log "INFO" "All dependencies satisfied"
}

# Process files
process_files() {
    local dir="${1:-.}"
    local pattern="${2:-*}"
    local count=0

    while IFS= read -r -d '' file; do
        if [[ -f "$file" ]]; then
            log "INFO" "Processing: $file"
            ((count++))
        fi
    done < <(find "$dir" -name "$pattern" -print0)

    echo "$count"
}

# Main function
main() {
    log "INFO" "Starting script..."

    check_deps || exit 1

    local target_dir="${1:-$PWD}"

    if [[ ! -d "$target_dir" ]]; then
        log "ERROR" "Directory not found: $target_dir"
        exit 1
    fi

    local processed
    processed=$(process_files "$target_dir" "*.go")

    log "INFO" "Processed $processed files"
    log "INFO" "Script completed successfully"
}

# Run main if not sourced
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
