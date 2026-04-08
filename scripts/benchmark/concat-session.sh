#!/usr/bin/env bash
# concat-session.sh — Build a benchmark session by concatenating WAV clips.
#
# Usage:
#   ./concat-session.sh [options] [wav_file ...]
#
# If no WAV files are given, uses the bundled LibriSpeech clips.
#
# Options:
#   -o DIR     Output directory (default: tests/benchmark/sessions/<session_id>)
#   -n NAME    Session name / id (default: auto-generated timestamp)
#   -c COUNT   Max number of clips to use from the default set (default: all)
#   -h         Show this help
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEFAULT_DATA_DIR="$REPO_ROOT/tests/test-data/LibriSpeech"
SESSIONS_DIR="$REPO_ROOT/tests/benchmark/sessions"

session_name=""
output_dir=""
max_clips=0

usage() {
    sed -n '2,/^$/s/^# \?//p' "$0"
    exit "${1:-0}"
}

while getopts "o:n:c:h" opt; do
    case "$opt" in
        o) output_dir="$OPTARG" ;;
        n) session_name="$OPTARG" ;;
        c) max_clips="$OPTARG" ;;
        h) usage 0 ;;
        *) usage 1 ;;
    esac
done
shift $((OPTIND - 1))

# Collect WAV files
wav_files=()
if [[ $# -gt 0 ]]; then
    wav_files=("$@")
else
    while IFS= read -r f; do
        wav_files+=("$f")
    done < <(find "$DEFAULT_DATA_DIR" -name '*.wav' | sort)
fi

if [[ ${#wav_files[@]} -eq 0 ]]; then
    echo "ERROR: No WAV files found." >&2
    exit 1
fi

if [[ $max_clips -gt 0 && ${#wav_files[@]} -gt $max_clips ]]; then
    wav_files=("${wav_files[@]:0:$max_clips}")
fi

# Session ID
if [[ -z "$session_name" ]]; then
    session_name="session-$(date +%Y%m%d-%H%M%S)"
fi

if [[ -z "$output_dir" ]]; then
    output_dir="$SESSIONS_DIR/$session_name"
fi
mkdir -p "$output_dir"

echo "=== Building benchmark session: $session_name ==="
echo "  Clips: ${#wav_files[@]}"
echo "  Output: $output_dir"

# Build ffmpeg concat list
concat_list="$output_dir/.concat-list.txt"
: > "$concat_list"

for wav in "${wav_files[@]}"; do
    if [[ ! -f "$wav" ]]; then
        echo "ERROR: File not found: $wav" >&2
        exit 1
    fi
    echo "file '$(realpath "$wav")'" >> "$concat_list"
done

# Concatenate audio via ffmpeg
session_wav="$output_dir/session.wav"
echo "  Concatenating ${#wav_files[@]} clips..."
ffmpeg -y -f concat -safe 0 -i "$concat_list" \
    -ar 16000 -ac 1 -sample_fmt s16 \
    "$session_wav" 2>/dev/null

rm -f "$concat_list"

# Generate manifest, reference transcript, and merged timings via Python helper
wav_list_file="$output_dir/.wav-list.txt"
printf '%s\n' "${wav_files[@]}" > "$wav_list_file"

python3 "$SCRIPT_DIR/helpers/build_manifest.py" \
    --session-id "$session_name" \
    --session-wav "$session_wav" \
    --wav-list "$wav_list_file" \
    --repo-root "$REPO_ROOT" \
    --output-dir "$output_dir"

rm -f "$wav_list_file"

echo "  Session WAV: $session_wav"
echo "  Manifest: $output_dir/manifest.json"
echo "  Reference: $output_dir/reference.txt"
if [[ -f "$output_dir/reference-timings.txt" ]]; then
    echo "  Timings: $output_dir/reference-timings.txt"
fi
echo "=== Session $session_name ready ==="
