# Crude

Crude is a terminal-native IDE for working with AI agents: a three-layer Go
application with a custom TUI component system, an embedded terminal, editor
with LSP integration, and pluggable model providers.

## Architecture

Three layers, strictly separated (no callbacks across boundaries — method
calls down, event bus up):

```
app/        IDE shell: config, workspace lifecycle, CLI startup
workspace/  Per-project state machine: sessions, agents, LSP, file ops
tui/        Dumb renderer + input router: surfaces, components, input routing
```

`workspace/` never imports `tui/`; presentation-neutral contracts live in
`types/`. The full rules live in [`AGENTS.md`](AGENTS.md) — read it before
changing architecture or terminal UI code.

## Build

Building the embedded terminal requires the pinned `libghostty-vt` C library
(Zig 0.16.x and `tic` are needed once to bootstrap it):

```bash
make libghostty     # one-time bootstrap of the pinned libghostty-vt
make build          # go build ./... with the libghostty env
```

## Test

```bash
make test-tui       # full TUI suite
make test-race-tui  # race detector, full repo
go test ./...       # plain full-repo run
```

## Gates

CI (`.github/workflows/tui.yml`) and the Makefile enforce:

| Gate | Command | Enforces |
|------|---------|----------|
| Lint | `golangci-lint run ./...` | govet, ineffassign, misspell, staticcheck, unused, errcheck, gofmt — zero issues |
| Design audit | `make design-audit` | three-layer boundary, theme-token discipline, no raw ANSI in feature code |
| Bench gate | `make bench-gate` | allocation ceilings from `docs/reference/performance-baselines.md` |
| Docs gate | `make docs-check` | documentation naming, status headers, working links, no orphans |

All of them must stay green; a red gate is a broken build, not a warning.

## Documentation

The documentation set lives in [`docs/`](docs/README.md) with its own index
and rules:

- `docs/architecture/` — how the system is built and why
- `docs/reference/` — durable contracts: surfaces, interactions, settings,
  width rules, performance baselines
- `docs/guides/` — how to build TUI screens with the component system
- `docs/plans/` — historical plans, handoffs, and checklists (check the
  status header before acting on them)

## Install

```bash
./install.sh        # Linux/macOS
install.bat         # Windows
```
