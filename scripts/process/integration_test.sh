#!/bin/bash
set -euo pipefail

# ============================================================
# integration_test.sh — Integration Test Runner
#
# Runs Go integration tests under features/*/integration/.
#
# Usage:
#   ./scripts/process/integration_test.sh [OPTIONS]
#
# Options:
#   --categories <list>  Comma-separated categories (default: all)
#                        Allowed: vram,monitor,bus,stats,lifecycle,palette
#   --specify <Filter>   Further filter test names (regex; intersected
#                        with the category-selected names)
#   --race               Run with go test -race
#   --require-tests      Fail (exit 1) when zero tests would run
#   --help               Show this help message
#
# Exit Codes:
#   0 = All tests passed
#   1 = Test failure, unknown category, unclassified file, or
#       zero tests when --require-tests is set
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# --- Colors ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color
BOLD='\033[1m'

# --- Helpers ---
info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
success() { echo -e "${GREEN}[PASS]${NC} $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()    { echo -e "${RED}[FAIL]${NC} $*"; }
step()    { echo -e "${CYAN}${BOLD}===> $*${NC}"; }

# Canonical category → file mapping (basename only).
declare -A CATEGORY_FILES=(
    [vram]="vram_page_test.go vram_enhancement_test.go vram_multicore_test.go"
    [monitor]="vram_monitor_test.go"
    [bus]="bus_panic_guard_test.go"
    [stats]="stats_http_test.go vram_stats_test.go"
    [lifecycle]="shutdown_test.go"
    [palette]="palette_test.go"
)

ALLOWED_CATEGORIES="vram,monitor,bus,stats,lifecycle,palette"

show_help() {
    cat << EOF
Usage: ./scripts/process/integration_test.sh [OPTIONS]

Runs Go integration tests under features/*/integration/.

Options:
  --categories <list>  Comma-separated categories (default: all)
                       Allowed: ${ALLOWED_CATEGORIES}
  --specify <Filter>   Further filter test names (regex)
  --race               Run with go test -race
  --require-tests      Fail when zero tests would run
  --help               Show this help message

Exit Codes:
  0 = All tests passed
  1 = Failure (see header)

Examples:
  ./scripts/process/integration_test.sh
  ./scripts/process/integration_test.sh --categories "vram"
  ./scripts/process/integration_test.sh --categories "vram,stats"
  ./scripts/process/integration_test.sh --race --categories "vram,monitor"
  ./scripts/process/integration_test.sh --specify "TestPageSize" --categories "vram"
EOF
}

# --- Argument Parsing ---
SPECIFY=""
CATEGORIES=""
RACE=false
REQUIRE_TESTS=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --specify)
            if [[ -z "${2:-}" ]]; then
                fail "--specify requires a value"
                exit 1
            fi
            SPECIFY="$2"
            shift 2
            ;;
        --categories)
            if [[ -z "${2:-}" ]]; then
                fail "--categories requires a value"
                exit 1
            fi
            CATEGORIES="$2"
            shift 2
            ;;
        --race)
            RACE=true
            shift
            ;;
        --require-tests)
            REQUIRE_TESTS=true
            shift
            ;;
        --help|-h)
            show_help
            exit 0
            ;;
        *)
            fail "Unknown option: $1"
            show_help
            exit 1
            ;;
    esac
done

FAILED=false
TOTAL_RUN=0

# Returns 0 if basename $1 is listed in any CATEGORY_FILES value.
is_classified() {
    local base="$1"
    local cat files
    for cat in "${!CATEGORY_FILES[@]}"; do
        files="${CATEGORY_FILES[$cat]}"
        for f in $files; do
            if [[ "$f" == "$base" ]]; then
                return 0
            fi
        done
    done
    return 1
}

# Fail if any *_test.go under features/*/integration is not in CATEGORY_FILES.
check_unclassified() {
    local dir base
    for dir in "$PROJECT_ROOT"/features/*/integration; do
        [[ -d "$dir" ]] || continue
        for path in "$dir"/*_test.go; do
            [[ -f "$path" ]] || continue
            base=$(basename "$path")
            if ! is_classified "$base"; then
                fail "Unclassified integration test file: ${path#"$PROJECT_ROOT"/}"
                fail "Add it to CATEGORY_FILES in integration_test.sh."
                return 1
            fi
        done
    done
    return 0
}

# Resolve selected category names into an array SELECTED_CATS.
resolve_categories() {
    SELECTED_CATS=()
    if [[ -z "$CATEGORIES" ]]; then
        local cat
        for cat in vram monitor bus stats lifecycle palette; do
            SELECTED_CATS+=("$cat")
        done
        return 0
    fi

    local IFS=','
    local -a requested
    read -r -a requested <<< "$CATEGORIES"
    local c
    for c in "${requested[@]}"; do
        c="${c// /}" # trim spaces
        [[ -n "$c" ]] || continue
        if [[ -z "${CATEGORY_FILES[$c]+x}" ]]; then
            fail "Unknown category: $c (allowed: ${ALLOWED_CATEGORIES})"
            return 1
        fi
        SELECTED_CATS+=("$c")
    done
    if [[ ${#SELECTED_CATS[@]} -eq 0 ]]; then
        fail "No categories selected from --categories \"$CATEGORIES\""
        return 1
    fi
    return 0
}

# Collect test function names from selected category files under $1 (integration dir).
# Writes names into global array TEST_NAMES.
collect_test_names() {
    local integ_dir="$1"
    TEST_NAMES=()
    local cat files f path fn
    local -A seen=()

    for cat in "${SELECTED_CATS[@]}"; do
        files="${CATEGORY_FILES[$cat]}"
        for f in $files; do
            path="$integ_dir/$f"
            [[ -f "$path" ]] || continue
            while IFS= read -r fn; do
                [[ -n "$fn" ]] || continue
                if [[ -n "$SPECIFY" ]]; then
                    if [[ ! "$fn" =~ $SPECIFY ]]; then
                        continue
                    fi
                fi
                if [[ -z "${seen[$fn]+x}" ]]; then
                    seen[$fn]=1
                    TEST_NAMES+=("$fn")
                fi
            done < <(grep -oE '^func Test[A-Za-z0-9_]+' "$path" | sed 's/^func //')
        done
    done
}

run_feature_integration() {
    local feature_dir="$1"
    local feature_name
    feature_name=$(basename "$feature_dir")
    local integ_dir="$feature_dir/integration"

    step "Feature: $feature_name (integration)"
    collect_test_names "$integ_dir"

    if [[ ${#TEST_NAMES[@]} -eq 0 ]]; then
        warn "No matching integration tests for $feature_name (categories=${SELECTED_CATS[*]}, specify=${SPECIFY:-<none>})."
        return 0
    fi

    local run_regex
    run_regex=$(IFS='|'; echo "${TEST_NAMES[*]}")
    info "Running ${#TEST_NAMES[@]} test(s): $run_regex"
    [[ "$RACE" == "true" ]] && info "Race detector enabled"

    cd "$feature_dir"
    local go_test_args=("-v" "-count=1")
    if [[ "$RACE" == "true" ]]; then
        go_test_args+=("-race")
    fi
    go_test_args+=("-run" "^($run_regex)$" "./integration/")

    if go test "${go_test_args[@]}"; then
        success "Integration tests passed for $feature_name."
        TOTAL_RUN=$((TOTAL_RUN + ${#TEST_NAMES[@]}))
        cd "$PROJECT_ROOT"
        return 0
    else
        fail "Integration tests FAILED for $feature_name."
        FAILED=true
        cd "$PROJECT_ROOT"
        return 1
    fi
}

run_all() {
    cd "$PROJECT_ROOT"

    if ! check_unclassified; then
        FAILED=true
        return 1
    fi

    if ! resolve_categories; then
        FAILED=true
        return 1
    fi
    info "Categories: ${SELECTED_CATS[*]}"

    local found_any=false
    local feature_dir
    for feature_dir in features/*/; do
        [[ -d "$feature_dir" ]] || continue
        [[ -f "$feature_dir/go.mod" ]] || continue
        [[ -d "$feature_dir/integration" ]] || continue
        found_any=true
        run_feature_integration "$feature_dir" || true
        # Continue other features even on failure so the summary lists everything;
        # overall exit is still non-zero via FAILED.
    done

    if [[ "$found_any" == "false" ]]; then
        if [[ "$REQUIRE_TESTS" == "true" ]]; then
            fail "No features/*/integration/ directories found."
            FAILED=true
            return 1
        fi
        warn "No features/*/integration/ directories found — nothing to run."
        return 0
    fi

    if [[ "$TOTAL_RUN" -eq 0 ]]; then
        if [[ "$REQUIRE_TESTS" == "true" ]]; then
            fail "Zero integration tests matched the selection (use --require-tests carefully)."
            FAILED=true
            return 1
        fi
        warn "Zero integration tests matched the selection."
        return 0
    fi

    if [[ "$FAILED" == "true" ]]; then
        return 1
    fi
    return 0
}

main() {
    echo ""
    echo -e "${BOLD}╔══════════════════════════════════════════╗${NC}"
    echo -e "${BOLD}║     Integration Test Pipeline            ║${NC}"
    echo -e "${BOLD}╚══════════════════════════════════════════╝${NC}"
    echo ""

    local start_time=$SECONDS

    run_all || true

    local elapsed=$(( SECONDS - start_time ))
    echo ""
    echo -e "${BOLD}─────────────────────────────────────────────${NC}"

    if [[ "$FAILED" == "true" ]]; then
        fail "Integration tests FAILED. (${elapsed}s)"
        exit 1
    else
        success "All integration tests passed! (${elapsed}s)"
        exit 0
    fi
}

main
