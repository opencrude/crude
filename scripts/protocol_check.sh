#!/usr/bin/env bash
# Non-mutating source-cohort gate; usable without the native terminal library.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
exec go run ./internal/wiregen -check
