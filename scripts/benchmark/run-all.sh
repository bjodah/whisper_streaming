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

# Generate a unique tag for this benchmark run
bench_tag="$(date +%Y%m%d-%H%M%S)"
if [[ -z "$session_name" ]]; then
    session_name="session-$bench_tag"
fi
run_id="run-$bench_tag"
session_dir="$REPO_ROOT/tests/benchmark/sessions/$session_name"
run_dir="$REPO_ROOT/tests/benchmark/runs/$run_id"

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
    proxy_log_dir="$REPO_ROOT/tests/benchmark/runs/proxy-latest"
    bash "$SCRIPT_DIR/run-proxy.sh" -p "$port" -i "$impl" -o "$proxy_log_dir" -d
    proxy_pid=$(cat "$proxy_log_dir/proxy.pid" 2>/dev/null || true)
    echo ""
    sleep 1
else
    echo "--- Step 1: Proxy (assumed running at $host:$port) ---"
    echo ""
fi

# Step 2: Build session (explicit output dir — no stdout parsing needed)
echo "--- Step 2: Building benchmark session ---"
bash "$SCRIPT_DIR/concat-session.sh" -c "$max_clips" -n "$session_name" -o "$session_dir"
echo ""

# Step 3: Run session (explicit output dir — no stdout parsing needed)
echo "--- Step 3: Running session against proxy ---"
bash "$SCRIPT_DIR/run-session.sh" -H "$host" -p "$port" -s "$speed" -o "$run_dir" "$session_dir"
echo ""

# Step 3b: Copy proxy artifacts into run dir for reproducibility
if [[ -n "$start_proxy" ]]; then
    echo "--- Step 3b: Preserving proxy artifacts in run dir ---"
    for artifact in proxy-stdout.log proxy-stderr.log proxy-meta.json; do
        src="$proxy_log_dir/$artifact"
        if [[ -f "$src" ]]; then
            cp "$src" "$run_dir/$artifact"
            echo "  Copied $artifact"
        fi
    done
    echo ""
fi

# Enrich run-meta.json with implementation info
python3 -c "
import json, os
meta_path = '$run_dir/run-meta.json'
if os.path.isfile(meta_path):
    with open(meta_path) as f:
        meta = json.load(f)
else:
    meta = {}
meta['implementation'] = '$impl'
meta['proxy_port'] = $port
meta['openai_base_url'] = os.environ.get('OPENAI_BASE_URL', '')
if '$start_proxy':
    meta['proxy_log_dir'] = '$proxy_log_dir'
    meta['proxy_started_by_harness'] = True
with open(meta_path, 'w') as f:
    json.dump(meta, f, indent=2)
    f.write('\n')
"

# Step 4: Score
echo "--- Step 4: Scoring ---"
score_args=("$run_dir" "$session_dir")
bash "$SCRIPT_DIR/score-run.sh" "${score_args[@]}"
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
