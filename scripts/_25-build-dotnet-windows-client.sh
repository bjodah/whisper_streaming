#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

printf '\n==> Building dotnet Windows client\n'
dotnet build dotnet-windows-client/StrisperClient.sln
