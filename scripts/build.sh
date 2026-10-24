#!/usr/bin/env bash
# Checked native developer build. Regenerate with make protocol-generate after
# production-source edits; plain go build does not establish cohort freshness.
# Extra arguments are ordinary go build flags (for example -o /tmp/crude).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=libghostty_env.sh
. scripts/libghostty_env.sh
go run ./internal/wiregen -check
exec go build "$@" .
