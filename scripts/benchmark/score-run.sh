#!/usr/bin/env bash
# score-run.sh — Score a benchmark run against reference data.
#
# Usage:
#   ./score-run.sh [options] RUN_DIR SESSION_DIR
#
# RUN_DIR contains events.jsonl and session-meta.json from run-session.sh.
# SESSION_DIR contains reference.txt and optionally reference-timings.txt.
#
# Options:
#   -o DIR     Output report directory (default: RUN_DIR)
#   -h         Show this help
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

output_dir=""

usage() {
    sed -n '2,/^$/s/^# \?//p' "$0"
    exit "${1:-0}"
}

while getopts "o:h" opt; do
    case "$opt" in
        o) output_dir="$OPTARG" ;;
        h) usage 0 ;;
        *) usage 1 ;;
    esac
done
shift $((OPTIND - 1))

if [[ $# -lt 2 ]]; then
    echo "ERROR: RUN_DIR and SESSION_DIR are required." >&2
    usage 1
fi

run_dir="$(realpath "$1")"
session_dir="$(realpath "$2")"

events_jsonl="$run_dir/events.jsonl"
reference_txt="$session_dir/reference.txt"
session_meta="$run_dir/session-meta.json"
timings_txt="$session_dir/reference-timings.txt"

if [[ ! -f "$events_jsonl" ]]; then
    echo "ERROR: events.jsonl not found in $run_dir" >&2
    exit 1
fi

if [[ ! -f "$reference_txt" ]]; then
    echo "ERROR: reference.txt not found in $session_dir" >&2
    exit 1
fi

if [[ -z "$output_dir" ]]; then
    output_dir="$run_dir"
fi

echo "=== Scoring benchmark run ==="
echo "  Run:     $run_dir"
echo "  Session: $session_dir"

score_args=(
    --events-jsonl "$events_jsonl"
    --reference "$reference_txt"
    --output-dir "$output_dir"
)

if [[ -f "$session_meta" ]]; then
    score_args+=(--session-meta "$session_meta")
fi

if [[ -f "$timings_txt" ]]; then
    score_args+=(--timings "$timings_txt")
fi

python3 "$SCRIPT_DIR/helpers/score_run.py" "${score_args[@]}"

echo "=== Scoring complete ==="
