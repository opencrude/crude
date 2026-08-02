.PHONY: build test-tui test-race-tui design-audit bench-gate

build:
	go build ./...

# Full TUI test suite (all tui packages).
test-tui:
	go test ./tui/...

# Race-sensitive suite: the full repo is race-clean (2026-08-01).
test-race-tui:
	go test -race -count=1 ./...

design-audit:
	./scripts/tui_design_audit.sh

# Allocation regression gate against PERFORMANCE_BASELINES.md ceilings.
bench-gate:
	./scripts/tui_bench_gate.sh
