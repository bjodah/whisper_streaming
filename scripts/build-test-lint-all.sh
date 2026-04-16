#!/usr/bin/env bash
set -euo pipefail
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
"$SCRIPTS_DIR/_20-build_go-code.sh"
"$SCRIPTS_DIR/_25-build-dotnet-windows-client.sh"
"$SCRIPTS_DIR/_40-test_go-code.sh"
"$SCRIPTS_DIR/_60-lint_go-code.sh"
"$SCRIPTS_DIR/_80-test_emacs-elisp.sh"
pytest -q "$SCRIPTS_DIR/../tests/benchmark/test_scorer.py"
