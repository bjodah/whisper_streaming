#!/usr/bin/env bash
# run-session.sh — Run one benchmark session against the proxy.
#
# Usage:
#   ./run-session.sh [options] SESSION_DIR
#
# SESSION_DIR should contain session.wav and manifest.json (output of concat-session.sh).
#
# Options:
#   -H HOST    Proxy host (default: localhost)
#   -p PORT    Proxy port (default: 43007)
#   -o DIR     Output run directory (default: tests/benchmark/runs/<run_id>)
#   -s SPEED   Playback speed multiplier (default: 1.0)
#   -f MS      Frame duration in ms (default: 40)
#   -t SEC     Receive timeout after audio ends (default: 15)
#   -h         Show this help
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RUNS_DIR="$REPO_ROOT/tests/benchmark/runs"

host="localhost"
port=43007
output_dir=""
speed=1.0
frame_ms=40
recv_timeout=15

usage() {
    sed -n '2,/^$/s/^# \?//p' "$0"
    exit "${1:-0}"
}

while getopts "H:p:o:s:f:t:h" opt; do
    case "$opt" in
        H) host="$OPTARG" ;;
        p) port="$OPTARG" ;;
        o) output_dir="$OPTARG" ;;
        s) speed="$OPTARG" ;;
        f) frame_ms="$OPTARG" ;;
        t) recv_timeout="$OPTARG" ;;
        h) usage 0 ;;
        *) usage 1 ;;
    esac
done
shift $((OPTIND - 1))

if [[ $# -lt 1 ]]; then
    echo "ERROR: SESSION_DIR is required." >&2
    usage 1
fi

session_dir="$(realpath "$1")"
session_wav="$session_dir/session.wav"
manifest="$session_dir/manifest.json"

if [[ ! -f "$session_wav" ]]; then
    echo "ERROR: session.wav not found in $session_dir" >&2
    exit 1
fi

if [[ ! -f "$manifest" ]]; then
    echo "ERROR: manifest.json not found in $session_dir" >&2
    exit 1
fi

# Generate run ID
run_id="run-$(date +%Y%m%d-%H%M%S)"
if [[ -z "$output_dir" ]]; then
    output_dir="$RUNS_DIR/$run_id"
fi
mkdir -p "$output_dir"

echo "=== Running benchmark session ==="
echo "  Session: $session_dir"
echo "  Target:  $host:$port"
echo "  Run:     $output_dir"

# Copy manifest to run dir for reproducibility
cp "$manifest" "$output_dir/manifest.json"

# Record run metadata
git_sha=$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")
python3 -c "
import json, os
meta = {
    'run_id': '$run_id',
    'session_dir': '$session_dir',
    'git_sha': '$git_sha',
    'proxy_host': '$host',
    'proxy_port': $port,
    'speed': $speed,
    'frame_ms': $frame_ms,
    'recv_timeout': $recv_timeout,
}
# Redact secrets
env_vars = {}
for k in ['OPENAI_BASE_URL', 'OPENAI_API_KEY']:
    v = os.environ.get(k, '')
    if k == 'OPENAI_API_KEY' and v:
        env_vars[k] = v[:4] + '...' + v[-4:] if len(v) > 8 else '***'
    else:
        env_vars[k] = v
meta['env'] = env_vars
with open('$output_dir/run-meta.json', 'w') as f:
    json.dump(meta, f, indent=2)
    f.write('\n')
"

# Run the transport client
python3 "$SCRIPT_DIR/helpers/session_client.py" \
    --wav "$session_wav" \
    --host "$host" \
    --port "$port" \
    --output-dir "$output_dir" \
    --frame-ms "$frame_ms" \
    --speed "$speed" \
    --recv-timeout "$recv_timeout"

echo "=== Session run complete: $output_dir ==="
