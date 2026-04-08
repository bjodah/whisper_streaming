#!/usr/bin/env bash
# compare-runs.sh — Side-by-side comparison of two benchmark runs.
#
# Usage:
#   ./compare-runs.sh RUN_DIR_A RUN_DIR_B
#
# Both directories must contain summary.json (output of score-run.sh).
#
# Output:
#   Prints a side-by-side comparison table to stdout.
#   Optionally writes comparison.json to the directory of RUN_DIR_B.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [[ $# -lt 2 ]]; then
    echo "Usage: $0 RUN_DIR_A RUN_DIR_B" >&2
    exit 1
fi

run_a="$(realpath "$1")"
run_b="$(realpath "$2")"

for d in "$run_a" "$run_b"; do
    if [[ ! -f "$d/summary.json" ]]; then
        echo "ERROR: summary.json not found in $d" >&2
        exit 1
    fi
done

python3 "$SCRIPT_DIR/helpers/compare_runs.py" "$run_a/summary.json" "$run_b/summary.json"
