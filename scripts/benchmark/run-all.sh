#!/usr/bin/env bash
# run-all.sh — Top-level benchmark orchestration.
#
# Builds sessions, runs them against the proxy, scores results, and prints a summary.
#
# Usage:
#   ./run-all.sh [options]
#
# Options:
#   -H HOST    Proxy host (default: localhost)
#   -p PORT    Proxy port (default: 43007)
#   -c COUNT   Max clips per session (default: 5)
#   -s SPEED   Playback speed (default: 1.0)
#   -i IMPL    Proxy implementation: "go" or "python" (default: go)
#   -P         Start proxy automatically (default: assume already running)
#   -n NAME    Session name (default: auto-generated)
#   -h         Show this help
#
# Examples:
#   # Run against an already-running proxy
#   ./run-all.sh -c 3
#
#   # Start proxy, run 5 clips, then score
#   ./run-all.sh -P -c 5
#
#   # Run against old Python implementation
#   ./run-all.sh -P -i python -c 3
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

host="localhost"
port=43007
max_clips=5
speed=1.0
impl="go"
start_proxy=""
session_name=""

usage() {
    sed -n '2,/^$/s/^# \?//p' "$0"
    exit "${1:-0}"
}

while getopts "H:p:c:s:i:Pn:h" opt; do
    case "$opt" in
        H) host="$OPTARG" ;;
        p) port="$OPTARG" ;;
        c) max_clips="$OPTARG" ;;
        s) speed="$OPTARG" ;;
        i) impl="$OPTARG" ;;
        P) start_proxy="yes" ;;
        n) session_name="$OPTARG" ;;
        h) usage 0 ;;
        *) usage 1 ;;
    esac
done
shift $((OPTIND - 1))

proxy_pid=""
cleanup() {
    if [[ -n "$proxy_pid" ]]; then
        echo ""
        echo "Stopping proxy (PID $proxy_pid)..."
        kill "$proxy_pid" 2>/dev/null || true
        wait "$proxy_pid" 2>/dev/null || true
    fi
}
trap cleanup EXIT

echo "============================================================"
echo "  WHISPER-PROXY BENCHMARK"
echo "============================================================"
echo ""

# Step 1: Optionally start proxy
if [[ -n "$start_proxy" ]]; then
    echo "--- Step 1: Starting proxy ($impl) ---"
    # Source the run-proxy.sh but capture PID
    proxy_log_dir="$REPO_ROOT/tests/benchmark/runs/proxy-latest"
    bash "$SCRIPT_DIR/run-proxy.sh" -p "$port" -i "$impl" -o "$proxy_log_dir" -d
    proxy_pid=$(cat "$proxy_log_dir/proxy.pid" 2>/dev/null || true)
    echo ""
    # Give proxy a moment to be fully ready
    sleep 1
else
    echo "--- Step 1: Proxy (assumed running at $host:$port) ---"
    echo ""
fi

# Step 2: Build session
echo "--- Step 2: Building benchmark session ---"
concat_args=(-c "$max_clips")
if [[ -n "$session_name" ]]; then
    concat_args+=(-n "$session_name")
fi
# Capture the output to find session dir
concat_output=$(bash "$SCRIPT_DIR/concat-session.sh" "${concat_args[@]}" 2>&1)
echo "$concat_output"

# Extract session dir from output
session_dir=$(echo "$concat_output" | grep "Output:" | head -1 | sed 's/.*Output: *//')
if [[ -z "$session_dir" || ! -d "$session_dir" ]]; then
    echo "ERROR: Could not determine session directory from concat output" >&2
    exit 1
fi
echo ""

# Step 3: Run session
echo "--- Step 3: Running session against proxy ---"
run_output=$(bash "$SCRIPT_DIR/run-session.sh" -H "$host" -p "$port" -s "$speed" "$session_dir" 2>&1)
echo "$run_output"

# Extract run dir
run_dir=$(echo "$run_output" | grep "Session run complete:" | sed 's/.*complete: *//')
if [[ -z "$run_dir" || ! -d "$run_dir" ]]; then
    echo "ERROR: Could not determine run directory from session output" >&2
    exit 1
fi
echo ""

# Step 4: Score
echo "--- Step 4: Scoring ---"
bash "$SCRIPT_DIR/score-run.sh" "$run_dir" "$session_dir"
echo ""

# Step 5: Print summary
echo "============================================================"
echo "  RESULTS"
echo "============================================================"
if [[ -f "$run_dir/summary.txt" ]]; then
    cat "$run_dir/summary.txt"
else
    echo "  (No summary.txt found)"
fi
echo ""
echo "Artifacts:"
echo "  Session: $session_dir"
echo "  Run:     $run_dir"
if [[ -f "$run_dir/summary.json" ]]; then
    echo "  Report:  $run_dir/summary.json"
fi
echo "============================================================"
