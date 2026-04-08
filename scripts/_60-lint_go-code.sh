#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

lint_bin="$(go env GOPATH)/bin/golangci-lint"
if [[ ! -x "$lint_bin" ]]; then
  echo "golangci-lint not found at $lint_bin" >&2
  exit 1
fi

printf '==> Checking formatting with gofmt\n'
mapfile -t unformatted < <(find . -type f -name '*.go' -not -path './vendor/*' -print | sort | xargs gofmt -l)
if (( ${#unformatted[@]} > 0 )); then
  printf 'Unformatted Go files:\n' >&2
  printf '  %s\n' "${unformatted[@]}" >&2
  exit 1
fi

printf '\n==> Running golangci-lint\n'
"$lint_bin" run ./...
