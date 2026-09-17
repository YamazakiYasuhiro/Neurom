#!/bin/bash
set -euo pipefail

# ============================================================
# build.sh — Full Build & Unit Test Runner
#
# Builds each Go feature and runs unit tests + go vet.
# Integration packages (features/*/integration/) are excluded;
# use integration_test.sh for those.
#
# Usage:
#   ./scripts/process/build.sh [OPTIONS]
#
# Options:
#   --keep-going     Continue after a feature failure; report all
#                    failures at the end (exit 1 if any failed)
#   --help           Show this help message
#
# Exit Codes:
#   0 = All builds and tests passed
#   1 = Build, unit test, or go vet failure
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

show_help() {
    cat << 'EOF'
Usage: ./scripts/process/build.sh [OPTIONS]

Builds each Go feature and runs unit tests + go vet.
Integration packages under features/*/integration/ are excluded.

Options:
  --keep-going     Continue after a feature failure; list all failures
  --help           Show this help message

Exit Codes:
  0 = All builds and tests passed
  1 = Build, unit test, or go vet failure

Examples:
  ./scripts/process/build.sh
  ./scripts/process/build.sh --keep-going
EOF
}

# --- Argument Parsing ---
KEEP_GOING=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --keep-going)
            KEEP_GOING=true
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

# --- Track overall result ---
FAILED=false
FAILED_FEATURES=()

# ============================================================
# Go Build & Unit Test
# ============================================================
build_go() {
    step "Go: Build & Unit Test"

    cd "$PROJECT_ROOT"

    mkdir -p "$PROJECT_ROOT/bin"

    local found_any=false
    for feature_dir in features/*/; do
        [[ -d "$feature_dir" ]] || continue

        if [[ ! -f "$feature_dir/go.mod" ]]; then
            info "Skipping $feature_dir — no go.mod found."
            continue
        fi

        found_any=true
        local feature_name
        feature_name=$(basename "$feature_dir")

        step "Feature: $feature_name"
        cd "$PROJECT_ROOT/$feature_dir"

        # --- Unit Tests (exclude integration packages) ---
        info "Running Go unit tests for $feature_name (excluding integration/)..."

        local UNIT_PKGS
        UNIT_PKGS=$(go list ./... | grep -v '/integration$' | grep -v '/integration/' || true)

        if [[ -z "$UNIT_PKGS" ]]; then
            warn "No Go unit test packages found for $feature_name."
        elif echo "$UNIT_PKGS" | xargs go test -v -count=1; then
            success "Unit tests passed for $feature_name."
        else
            fail "Unit tests failed for $feature_name."
            FAILED=true
            FAILED_FEATURES+=("$feature_name (unit)")
            if [[ "$KEEP_GOING" != "true" ]]; then
                return 1
            fi
            cd "$PROJECT_ROOT"
            continue
        fi

        # --- go vet ---
        if [[ -z "$UNIT_PKGS" ]]; then
            warn "Skipping go vet for $feature_name — no packages."
        else
            info "Running go vet for $feature_name..."
            if echo "$UNIT_PKGS" | xargs go vet; then
                success "go vet passed for $feature_name."
            else
                fail "go vet failed for $feature_name."
                FAILED=true
                FAILED_FEATURES+=("$feature_name (vet)")
                if [[ "$KEEP_GOING" != "true" ]]; then
                    return 1
                fi
                cd "$PROJECT_ROOT"
                continue
            fi
        fi

        # --- Build ---
        info "Building $feature_name..."
        local ext
        ext=$(go env GOEXE)
        if go build -ldflags "-s -w" -o "$PROJECT_ROOT/bin/${feature_name}${ext}" ./cmd/...; then
            success "Build succeeded for $feature_name → bin/${feature_name}${ext}"
        else
            fail "Build failed for $feature_name."
            FAILED=true
            FAILED_FEATURES+=("$feature_name (build)")
            if [[ "$KEEP_GOING" != "true" ]]; then
                return 1
            fi
            cd "$PROJECT_ROOT"
            continue
        fi

        cd "$PROJECT_ROOT"
    done

    if [[ "$found_any" == "false" ]]; then
        warn "No Go projects found under features/*/."
        warn "Expected structure: features/{name}/go.mod"
        return 0
    fi

    if [[ "$FAILED" == "true" ]]; then
        return 1
    fi
    return 0
}

# ============================================================
# Main
# ============================================================
main() {
    echo ""
    echo -e "${BOLD}╔══════════════════════════════════════════╗${NC}"
    echo -e "${BOLD}║     Build & Unit Test Pipeline           ║${NC}"
    echo -e "${BOLD}╚══════════════════════════════════════════╝${NC}"
    echo ""

    local start_time=$SECONDS

    build_go || true

    local elapsed=$(( SECONDS - start_time ))
    echo ""
    echo -e "${BOLD}─────────────────────────────────────────────${NC}"

    if [[ "$FAILED" == "true" ]]; then
        fail "Build pipeline FAILED (${elapsed}s)"
        if [[ ${#FAILED_FEATURES[@]} -gt 0 ]]; then
            echo -e "${RED}Failed features:${NC}"
            for f in "${FAILED_FEATURES[@]}"; do
                echo -e "  - $f"
            done
        fi
        echo -e "${RED}Fix the errors above before running integration tests.${NC}"
        exit 1
    else
        success "Build pipeline PASSED (${elapsed}s)"
        echo -e "${GREEN}Ready for integration tests: ./scripts/process/integration_test.sh${NC}"
        exit 0
    fi
}

main
