#!/usr/bin/env bash
# Documentation acceptance gate.
#
# Enforces the documentation rules from docs/README.md:
#   1. kebab-case filenames under docs/
#   2. a "> Status:" header in every markdown document
#   3. relative .md links resolve to existing files
#   4. doc references in code (Go comments, shell scripts, workflow YAML,
#      vendored go.mod comments) point at existing files under docs/
#   5. no orphan docs — every file under docs/ is indexed in docs/README.md
#   6. no stale references to moved/renamed legacy doc names
#   7. code paths and test names cited in AGENTS.md and docs/ exist
#
# Usage: scripts/docs_check.sh    (CI: make docs-check)

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

fail=0

fail_msg() {
  echo "FAIL: $1"
  fail=1
}

echo "== Documentation acceptance gate =="

# ---------------------------------------------------------------------------
# 1) Naming: kebab-case filenames under docs/
# ---------------------------------------------------------------------------
bad_names="$(find docs -type f \( -name '*.md' -o -name '*.json' \) \
  | grep -vE '^docs/([a-z0-9-]+/)*[a-z0-9-]+\.(md|json)$' \
  | grep -v '^docs/README\.md$' || true)"
if [[ -n "$bad_names" ]]; then
  while IFS= read -r f; do
    fail_msg "non-kebab-case doc filename: $f"
  done <<< "$bad_names"
else
  echo "OK: doc filenames are kebab-case"
fi

# ---------------------------------------------------------------------------
# 2) Status headers: every .md under docs/ carries "> Status:"
# ---------------------------------------------------------------------------
for f in $(find docs -type f -name '*.md' | sort); do
  if ! grep -qE '^> \*\*?Status:|^> Status:' "$f"; then
    fail_msg "missing '> Status:' header in $f"
  fi
done
if [[ $fail -eq 0 ]]; then
  echo "OK: every doc has a status header"
fi

# ---------------------------------------------------------------------------
# 3) Links: relative .md links must resolve
# ---------------------------------------------------------------------------
link_targets="$(grep -rhoE '\]\([^)#]+\.(md|json)\)' docs --include='*.md' \
  | sed -E 's/^\]\(//; s/\)$//' | sort -u)"
broken=0
while IFS= read -r target; do
  [[ -z "$target" ]] && continue
  # resolve each link relative to the directory of the file that contains it
  while IFS=$'\t' read -r doc rel; do
    dir="$(dirname "$doc")"
    resolved="$ROOT/$dir/$rel"
    if [[ ! -f "$resolved" ]]; then
      fail_msg "broken link in $doc: ($rel)"
      broken=1
    fi
  done < <(grep -rF "($target)" docs --include='*.md' -l 2>/dev/null | while IFS= read -r d; do printf '%s\t%s\n' "$d" "$target"; done)
done <<< "$link_targets"
if [[ $broken -eq 0 ]]; then
  echo "OK: all relative links resolve"
fi

# ---------------------------------------------------------------------------
# 4) Cross-references from code: docs/ paths cited in Go/sh/yml must exist
# ---------------------------------------------------------------------------
code_doc_refs="$(grep -rhoE 'docs/[A-Za-z0-9_./-]+\.(md|json)' \
  --include='*.go' --include='*.sh' --include='*.yml' --include='*.yaml' \
  --include='*.mod' . \
  --exclude-dir=.git --exclude-dir=third_party --exclude-dir=docs \
  --exclude-dir=.commandcode 2>/dev/null | sort -u || true)"
if [[ -n "$code_doc_refs" ]]; then
  while IFS= read -r ref; do
    if [[ ! -f "$ROOT/$ref" ]]; then
      fail_msg "code references missing doc: $ref"
    fi
  done <<< "$code_doc_refs"
  if [[ $fail -eq 0 ]]; then
    echo "OK: code-to-doc references resolve"
  fi
else
  echo "OK: no code-to-doc references to verify"
fi

# ---------------------------------------------------------------------------
# 5) Orphans: every file under docs/ must be linked from docs/README.md
# (index links are written relative to docs/, e.g. (plans/lsp-acceptance.md))
# ---------------------------------------------------------------------------
for f in $(find docs -type f \( -name '*.md' -o -name '*.json' \) ! -path 'docs/README.md' | sort); do
  rel="${f#docs/}"
  if ! grep -qF "($rel)" docs/README.md; then
    fail_msg "orphan doc (not indexed in docs/README.md): $f"
  fi
done
if [[ $fail -eq 0 ]]; then
  echo "OK: no orphan docs"
fi

# ---------------------------------------------------------------------------
# 6) Stale references: moved/renamed legacy doc names must stay gone
# (docs/README.md is excluded: it quotes forbidden names as rules/examples)
# ---------------------------------------------------------------------------
stale_patterns=(
  'docs/tui/[A-Z]'
  'docs/TERMINAL_WIDTH\.md'
  'docs/PLAN\.md'
  'docs/PROPOSAL_MEMORY_ARCHITECTURE\.md'
  'docs/PROVIDER-CONFIG-PLAN\.md'
  'docs/IMPLEMENTATION_HANDOFF\.md'
  '](\./TUI_COMPONENT_SYSTEM_PLAN\.md)'
  '](\./COMPOSITING_ENGINE_ARCHITECTURE\.md)'
  '](\./TERMINAL_WIDTH\.md)'
  '`TERMINAL_WIDTH\.md`'
  '`PERFORMANCE_BASELINES\.md`'
  '`SURFACE_CATALOG\.md`'
  '`INTERACTION_MATRIX\.md`'
  '`CONTROL_USAGE\.md`'
  '`DESIGN_TOKEN_BYPASSES\.md`'
  '`PHASE15_CHECKLIST\.md`'
  '`SETTINGS\.md`'
  '`TERMINALS\.md`'
  '`PLAN\.md`'
)
stale_hits="$(grep -rnE "$(IFS='|'; echo "${stale_patterns[*]}")" \
  --include='*.md' --include='*.go' --include='*.sh' --include='*.yml' --include='*.mod' \
  docs/ scripts/ tui/ workspace/ app/ third_party/bubbletea/go.mod third_party/ultraviolet/go.mod \
  .github/ 2>/dev/null | grep -v '^docs/README.md:' || true)"
if [[ -n "$stale_hits" ]]; then
  while IFS= read -r line; do
    fail_msg "stale doc reference: $line"
  done <<< "$stale_hits"
else
  echo "OK: no stale doc references"
fi

# ---------------------------------------------------------------------------
# 7) Code paths: a code span naming a repository file or directory must exist.
# Applies to AGENTS.md and docs/. A span counts when it starts with a tracked
# top-level directory and ends in "/" or a source/doc extension; globs must
# match at least one file. Name deleted files in plain text, not code spans.
# ---------------------------------------------------------------------------
tops="$(git ls-files | cut -d/ -f1 | sort -u | grep -v '\.' | paste -sd'|')"
missing_paths=0
while IFS= read -r doc; do
  while IFS=: read -r line span; do
    path="${span//\`/}"
    [[ "$path" =~ ^($tops)/ ]] || continue
    [[ "$path" == */ || "$path" =~ \.(go|sh|md|json|ya?ml|ps1|txt|mod)$ ]] || continue
    if [[ "$path" == *'*'* ]]; then
      compgen -G "$path" >/dev/null && continue
    elif [[ -e "$path" ]]; then
      continue
    fi
    fail_msg "$doc:$line cites missing path: $path"
    missing_paths=1
  done < <(grep -noE '`[A-Za-z0-9_./*-]+`' "$doc" || true)
done < <(echo AGENTS.md; find docs -type f -name '*.md' | sort)
# Cited test and benchmark names (`TestFoo`, `pkg/TestFoo`) must exist.
known_tests="$(grep -rhoE 'func (Test|Benchmark)[A-Za-z0-9_]+' --include='*_test.go' \
  --exclude-dir=.git --exclude-dir=.claude --exclude-dir=.commandcode . | sed 's/^func //' | sort -u)"
while IFS=: read -r doc line span; do
  name="${span//\`/}"
  name="${name##*/}"
  name="${name##*.}"
  if ! grep -qxF "$name" <<< "$known_tests"; then
    fail_msg "$doc:$line cites missing test: $name"
    missing_paths=1
  fi
done < <(grep -rnoE '`[A-Za-z0-9_./]*(Test|Benchmark)[A-Z][A-Za-z0-9_]*`' AGENTS.md docs --include='*.md' || true)
if [[ $missing_paths -eq 0 ]]; then
  echo "OK: cited code paths and tests exist"
fi

if [[ $fail -ne 0 ]]; then
  echo "== DOCS GATE FAILED =="
  exit 1
fi
echo "== DOCS GATE PASSED =="
