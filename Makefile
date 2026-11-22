# Recipes source scripts/libghostty_env.sh, which uses BASH_SOURCE; make's
# default /bin/sh (dash) rejects it with "Bad substitution".
SHELL := /bin/bash
.DEFAULT_GOAL := libghostty

.PHONY: libghostty ensure-libghostty protocol-generate protocol-check build test test-tui test-race-tui test-syntax test-lsp design-audit layout-check lint lint-new conventions hooks bench-gate docs-check site-serve

# Regenerate explicitly after production-source edits. Checks never mutate files.
protocol-generate: ensure-libghostty
	. scripts/libghostty_env.sh && go run ./tools/wiregen

protocol-check:
	go run ./tools/wiregen -check

libghostty:
	./scripts/bootstrap_libghostty.sh

# Windows builds a .lib with the PowerShell bootstrap; other systems a .a.
ifeq ($(OS),Windows_NT)
ensure-libghostty:
	@if [ ! -f third_party/libghostty-vt/lib/ghostty-vt-static.lib ]; then powershell -NoProfile -ExecutionPolicy Bypass -File scripts/bootstrap_libghostty.ps1; fi
else
ensure-libghostty:
	@if [ ! -f third_party/libghostty-vt/lib/libghostty-vt.a ]; then ./scripts/bootstrap_libghostty.sh; fi
endif

build: ensure-libghostty protocol-check
	. scripts/libghostty_env.sh && go build ./... && go build -o crude .

# Full repository coverage, with exhaustive groups for the largest packages.
test: ensure-libghostty protocol-check
	. scripts/libghostty_env.sh && python3 scripts/test_go.py

# Full TUI test suite (all tui packages).
test-tui: ensure-libghostty protocol-check
	. scripts/libghostty_env.sh && go test ./tui/...

# Race-sensitive suite: the full repo is race-clean (2026-08-01).
test-race-tui: ensure-libghostty protocol-check
	. scripts/libghostty_env.sh && python3 scripts/test_go.py --race

# The native binding is a replacement module, excluded from go test ./....
test-syntax:
	go test ./insight/syntax -count=1 -timeout=120s
	cd third_party/go-tree-sitter && go test . -count=1 -timeout=180s

# Neutral data/transport must not acquire a native-parser dependency. The nested
# process/transport owner is a replacement module, excluded by the root glob.
test-lsp:
	CGO_ENABLED=0 go test ./types/... ./lsp/lspclient -count=1 -timeout=90s
	cd third_party/jsonrpc2 && go test ./... -count=1 -timeout=60s
	cd third_party/powernap && go test ./pkg/lsp ./pkg/transport -count=1 -timeout=90s

# Includes the names gate: product Go code names no language, tool or server
# that the code-intelligence catalog owns (docs/reference/code-intelligence-catalog.md).
design-audit: layout-check
	./scripts/tui_design_audit.sh
	go run ./tools/namesgate

# Folder, package, file and exported-name rules (docs/reference/code-layout.md).
layout-check:
	go run ./tools/layoutcheck

lint: ensure-libghostty
	. scripts/libghostty_env.sh && golangci-lint run ./...

# Exported names added since DOC_BASE need doc comments. Locally the base is
# the branch's upstream (origin/master when there is none); CI passes the
# commit before the push or the pull request base.
DOC_BASE ?= $(shell git rev-parse --abbrev-ref --symbolic-full-name @{upstream} 2>/dev/null || echo origin/master)
lint-new: ensure-libghostty
	. scripts/libghostty_env.sh && golangci-lint run -c scripts/golangci-new-code.yml --new-from-rev=$(DOC_BASE) ./...

# Every naming and layout gate. The pre-push hook runs this target.
conventions: layout-check lint lint-new

# Run the conventions gate before every git push in this clone.
hooks:
	git config core.hooksPath .githooks
	@echo "Git now runs .githooks/pre-push (make conventions) before every push."

# Allocation regression gate against docs/reference/performance-baselines.md ceilings.
bench-gate: protocol-check
	./scripts/tui_bench_gate.sh

# Documentation acceptance gate (docs/README.md rules).
docs-check:
	bash scripts/docs_check.sh

# Local preview of the website sources in site/ (docs/guides/website.md).
site-serve:
	python3 -m http.server 8080 --directory site

# --- Windows targets (use PowerShell + zig cc) ---
libghostty-windows:
	pwsh scripts/bootstrap_libghostty.ps1

ensure-libghostty-windows:
	@if (-not (Test-Path third_party/libghostty-vt/lib/ghostty-vt-static.lib)) { pwsh scripts/bootstrap_libghostty.ps1 }

build-windows: ensure-libghostty-windows protocol-check
	. scripts/libghostty_env.ps1; go build ./...

test-tui-windows: ensure-libghostty-windows protocol-check
	. scripts/libghostty_env.ps1; go test ./tui/...
