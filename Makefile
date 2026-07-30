.PHONY: build test-tui test-race-tui design-audit

build:
	go build ./...

test-tui:
	go test ./tui/cell/ ./tui/surface/ ./tui/compositor/ ./tui/ui/

# Race-sensitive TUI, app, and workspace packages. Run under CI and before Phase 15 exit.
test-race-tui:
	go test -race -count=1 \
		./app/ \
		./tui/cell/ \
		./tui/surface/ \
		./tui/compositor/ \
		./tui/ui/ \
		./tui/filetree/ \
		./tui/screens/shellchat/ \
		./tui/chatprojection/ \
		./tui/viewmodel/ \
		./workspace/

design-audit:
	./scripts/tui_design_audit.sh
