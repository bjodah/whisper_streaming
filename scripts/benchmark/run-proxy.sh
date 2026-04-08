#!/usr/bin/env bash
# run-proxy.sh — Start the whisper-proxy with benchmark-friendly settings.
#
# Usage:
#   ./run-proxy.sh [options]
#
# Options:
#   -p PORT    TCP listen port (default: 43007)
#   -o DIR     Log output directory (default: tests/benchmark/runs/proxy-latest)
#   -i IMPL    Implementation: "go" (default) or "python"
#   -d         Enable debug logging
#   -h         Show this help
#
# Environment:
#   OPENAI_BASE_URL   Upstream API URL (default: https://api.openai.com/v1)
#   OPENAI_API_KEY    API key (required)
#   PROXY_EXTRA_ARGS  Extra arguments to pass to the proxy
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
RUNS_DIR="$REPO_ROOT/tests/benchmark/runs"

port=43007
log_dir=""
impl="go"
debug=""

usage() {
    sed -n '2,/^$/s/^# \?//p' "$0"
    exit "${1:-0}"
}

while getopts "p:o:i:dh" opt; do
    case "$opt" in
        p) port="$OPTARG" ;;
        o) log_dir="$OPTARG" ;;
        i) impl="$OPTARG" ;;
        d) debug="yes" ;;
        h) usage 0 ;;
        *) usage 1 ;;
    esac
done
shift $((OPTIND - 1))

if [[ -z "$log_dir" ]]; then
    log_dir="$RUNS_DIR/proxy-latest"
fi
mkdir -p "$log_dir"

echo "=== Starting whisper-proxy ($impl) ==="
echo "  Port: $port"
echo "  Logs: $log_dir"

# Record proxy metadata
git_sha=$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")
cat > "$log_dir/proxy-meta.json" << EOF
{
  "implementation": "$impl",
  "port": $port,
  "git_sha": "$git_sha",
  "openai_base_url": "${OPENAI_BASE_URL:-https://api.openai.com/v1}",
  "debug": ${debug:+true}${debug:-false}
}
EOF

case "$impl" in
    go)
        # Build first
        echo "  Building Go proxy..."
        cd "$REPO_ROOT"
        make build 2>&1 | tee "$log_dir/build.log"

        proxy_cmd=("$REPO_ROOT/bin/whisper-proxy"
            -port "$port"
            -min-chunk-size 1.0
            -buffer-trimming-sec 15.0
        )
        if [[ -n "$debug" ]]; then
            proxy_cmd+=(-debug)
        fi
        if [[ -n "${PROXY_EXTRA_ARGS:-}" ]]; then
            # shellcheck disable=SC2086
            proxy_cmd+=($PROXY_EXTRA_ARGS)
        fi

        echo "  Running: ${proxy_cmd[*]}"
        "${proxy_cmd[@]}" > "$log_dir/proxy-stdout.log" 2> "$log_dir/proxy-stderr.log" &
        ;;
    python)
        PYTHON_IMPL_DIR="${PYTHON_IMPL_DIR:-/work-old-python-impl}"
        if [[ ! -d "$PYTHON_IMPL_DIR" ]]; then
            echo "ERROR: Python implementation not found at $PYTHON_IMPL_DIR" >&2
            exit 1
        fi

        proxy_cmd=(python3 "$PYTHON_IMPL_DIR/whisper_online_server.py"
            --backend openai-api
        )
        if [[ -n "${PROXY_EXTRA_ARGS:-}" ]]; then
            # shellcheck disable=SC2086
            proxy_cmd+=($PROXY_EXTRA_ARGS)
        fi

        echo "  Running: ${proxy_cmd[*]}"
        env OPENAI_BASE_URL="${OPENAI_BASE_URL:-}" \
            OPENAI_API_KEY="${OPENAI_API_KEY:-}" \
            "${proxy_cmd[@]}" > "$log_dir/proxy-stdout.log" 2> "$log_dir/proxy-stderr.log" &
        ;;
    *)
        echo "ERROR: Unknown implementation: $impl (use 'go' or 'python')" >&2
        exit 1
        ;;
esac

PROXY_PID=$!
echo "$PROXY_PID" > "$log_dir/proxy.pid"
echo "  PID: $PROXY_PID"

# Wait briefly for startup
sleep 2

if ! kill -0 "$PROXY_PID" 2>/dev/null; then
    echo "ERROR: Proxy failed to start. Check logs:" >&2
    cat "$log_dir/proxy-stderr.log" >&2
    exit 1
fi

echo "=== Proxy running (PID $PROXY_PID) ==="
echo "  Stop with: kill $PROXY_PID"
