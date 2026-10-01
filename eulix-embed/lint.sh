#!/bin/bash
# lint.sh - Eulix_embed linting script
# usage: ./lint.sh or use env vars
# LINT_TIMEOUT=10m MAX_LINE_LENGTH=100 PYTHON_VERSION=3.11 ./lint.sh <DIR>

set +e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# Configuration
TIMEOUT="${LINT_TIMEOUT:-5m}"
DIRS="${1:-.}"
MAX_LINE_LENGTH="${MAX_LINE_LENGTH:-120}"
PYTHON_VERSION="${PYTHON_VERSION:-3.11}"
EXIT_CODE=0

# Tracking
PASSED=()
FAILED=()
SKIPPED=()

# Helpers
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

print_header() {
    echo -e "${BOLD}${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BOLD}${BLUE}  $1${NC}"
    echo -e "${BOLD}${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

print_section() {
    echo -e "\n${CYAN}▸ $1${NC}"
}

run_linter() {
    local name="$1"
    local cmd="$2"

    print_section "$name"

    if timeout "$TIMEOUT" bash -c "$cmd"; then
        echo -e "${GREEN}  ✓ ${name} passed${NC}"
        PASSED+=("$name")
        return 0
    else
        local exit_code=$?
        if [ $exit_code -eq 124 ]; then
            echo -e "${RED}  ✗ ${name} timed out after ${TIMEOUT}${NC}"
        else
            echo -e "${RED}  ✗ ${name} failed${NC}"
        fi
        FAILED+=("$name")
        EXIT_CODE=1
        return 1
    fi
}

skip_linter() {
    local name="$1"
    local install="$2"
    echo -e "${YELLOW}  ⚠  ${name} not found — install with: ${install}${NC}"
    SKIPPED+=("$name")
}

# Banner
echo ""
print_header "🐍  Python Linter Suite"
echo -e "  Target : ${BOLD}${DIRS}${NC}"
echo -e "  Timeout: ${BOLD}${TIMEOUT}${NC}"
echo -e "  Python : ${BOLD}${PYTHON_VERSION}${NC}"
echo -e "  Max len: ${BOLD}${MAX_LINE_LENGTH}${NC}"
echo ""

# 1. Ruff — Lint (replaces flake8, isort, pyupgrade, bugbear, etc.)
print_header "🎨  Lint (ruff)"

if command_exists ruff; then
    run_linter "ruff-check" "ruff check ${DIRS} \
        --line-length=${MAX_LINE_LENGTH} \
        --target-version=py${PYTHON_VERSION//./} \
        --output-format=concise"
else
    skip_linter "ruff" "pip install ruff"
fi

# 2. Ruff — Format check (replaces black --check)
if command_exists ruff; then
    run_linter "ruff-format" "ruff format ${DIRS} \
        --line-length=${MAX_LINE_LENGTH} \
        --target-version=py${PYTHON_VERSION//./} \
        --check \
        --diff"
else
    skip_linter "ruff-format" "pip install ruff"
fi

# 3. Radon — Cyclomatic Complexity
print_header "🧮  Complexity"

if command_exists radon; then
    run_linter "radon" "radon cc ${DIRS} \
        --total-average \
        --show-complexity \
        --ignore .venv,venv \
        --min=C \
        && radon mi ${DIRS} --ignore .venv,venv --min=B"
else
    skip_linter "radon" "pip install radon"
fi

# 4. Mypy — Type Checking
print_header "📝  Type Checking"

if command_exists mypy; then
    run_linter "mypy" "mypy ${DIRS} \
        --python-version=${PYTHON_VERSION} \
        --exclude '(\.venv|venv)' \
        --warn-return-any \
        --warn-unused-configs \
        --warn-redundant-casts \
        --warn-unused-ignores \
        --warn-unreachable \
        --no-implicit-optional \
        --strict-equality \
        --check-untyped-defs \
        --disallow-incomplete-defs \
        --pretty"
else
    skip_linter "mypy" "pip install mypy"
fi

# 5. Bandit — Security
print_header "🔒  Security"

if command_exists bandit; then
    run_linter "bandit" "bandit -r ${DIRS} \
        --format=screen \
        --severity-level=low \
        --confidence-level=medium \
        --exclude='.venv,venv,**/*test*.py,**/*_test.py,**/test_*.py' \
        --skip=B104,B204"
else
    skip_linter "bandit" "pip install bandit"
fi

# 6. Codespell — Spelling
print_header "📖  Spelling"

if command_exists codespell; then
    CODESPELL_IGNORE_FILE=""
    [ -f ".codespell-ignore" ] && CODESPELL_IGNORE_FILE="--ignore-words=.codespell-ignore"

    run_linter "codespell" "codespell ${DIRS} \
        --skip='*.pyc,*.pyo,*.pyd,__pycache__,*.so,*.o,*.a,*.egg-info,*.dist-info,.git,lint.sh,.venv,venv,bin*' \
        ${CODESPELL_IGNORE_FILE} \
        --quiet-level=2 \
        --check-filenames \
        --check-hidden"
else
    skip_linter "codespell" "pip install codespell"
fi

# Summary
echo ""
print_header "📊  Summary"

if [ ${#PASSED[@]} -gt 0 ]; then
    echo -e "${GREEN}  Passed  (${#PASSED[@]}): ${PASSED[*]}${NC}"
fi

if [ ${#SKIPPED[@]} -gt 0 ]; then
    echo -e "${YELLOW}  Skipped (${#SKIPPED[@]}): ${SKIPPED[*]}${NC}"
fi

if [ ${#FAILED[@]} -gt 0 ]; then
    echo -e "${RED}  Failed  (${#FAILED[@]}): ${FAILED[*]}${NC}"
fi

echo ""
if [ $EXIT_CODE -eq 0 ]; then
    echo -e "${BOLD}${GREEN}  ✅  All checks passed!${NC}"
else
    echo -e "${BOLD}${RED}  ❌  One or more checks failed. See above for details.${NC}"
fi
echo ""

exit $EXIT_CODE
