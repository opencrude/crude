# Recipes source scripts/libghostty_env.sh, which uses BASH_SOURCE; make's
# default /bin/sh (dash) rejects it with "Bad substitution".
SHELL := /bin/bash

.PHONY: libghostty ensure-libghostty build test-tui test-race-tui design-audit bench-gate docs-check

libghostty:
	./scripts/bootstrap_libghostty.sh

ensure-libghostty:
	@if [ ! -f deps/libghostty-vt/lib/libghostty-vt.a ]; then ./scripts/bootstrap_libghostty.sh; fi

build: ensure-libghostty
	. scripts/libghostty_env.sh && go build ./... && go build -o crude .

# Full TUI test suite (all tui packages).
test-tui: ensure-libghostty
	. scripts/libghostty_env.sh && go test ./tui/...

# Race-sensitive suite: the full repo is race-clean (2026-08-01).
test-race-tui: ensure-libghostty
	. scripts/libghostty_env.sh && go test -race -count=1 ./...

design-audit:
	./scripts/tui_design_audit.sh

# Allocation regression gate against docs/reference/performance-baselines.md ceilings.
bench-gate:
	./scripts/tui_bench_gate.sh

# Documentation acceptance gate (docs/README.md rules).
docs-check:
	bash scripts/docs_check.sh

# --- Windows targets (use PowerShell + zig cc) ---
libghostty-windows:
	pwsh scripts/bootstrap_libghostty.ps1

ensure-libghostty-windows:
	@if (-not (Test-Path deps/libghostty-vt/lib/ghostty-vt-static.lib)) { pwsh scripts/bootstrap_libghostty.ps1 }

build-windows: ensure-libghostty-windows
	. scripts/libghostty_env.ps1; go build ./...

test-tui-windows: ensure-libghostty-windows
	. scripts/libghostty_env.ps1; go test ./tui/...
