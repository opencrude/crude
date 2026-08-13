.PHONY: libghostty ensure-libghostty build test-tui test-race-tui design-audit bench-gate

libghostty:
	./scripts/bootstrap_libghostty.sh

ensure-libghostty:
	@if [ ! -f deps/libghostty-vt/lib/libghostty-vt.a ]; then ./scripts/bootstrap_libghostty.sh; fi

build: ensure-libghostty
	. scripts/libghostty_env.sh && go build ./...

# Full TUI test suite (all tui packages).
test-tui: ensure-libghostty
	. scripts/libghostty_env.sh && go test ./tui/...

# Race-sensitive suite: the full repo is race-clean (2026-08-01).
test-race-tui: ensure-libghostty
	. scripts/libghostty_env.sh && go test -race -count=1 ./...

design-audit:
	./scripts/tui_design_audit.sh

# Allocation regression gate against PERFORMANCE_BASELINES.md ceilings.
bench-gate:
	./scripts/tui_bench_gate.sh
