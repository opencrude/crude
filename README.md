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

## Windows terminal setup

Crude uses patched Nerd Font glyphs for its activity rail, file tree, source
control, Skills, MCP, and Settings icons. On Windows, install JetBrainsMono Nerd
Font system-wide and configure the terminal that launches Crude.

### 1. Install the font

Run PowerShell as Administrator:

```powershell
winget install --id DEVCOM.JetBrainsMonoNerdFont --exact `
  --accept-source-agreements --accept-package-agreements
```

### 2. Configure Windows Terminal

Open **Windows Terminal → Settings → Defaults → Appearance** and set:

```text
Font face: JetBrainsMono NF
Font size: 12
```

Alternatively, add this under `profiles.defaults` in Windows Terminal's
`settings.json`:

```json
{
  "profiles": {
    "defaults": {
      "font": {
        "face": "JetBrainsMono NF",
        "size": 12
      }
    }
  }
}
```

Use **`JetBrainsMono NF`**, not **`JetBrainsMono NFM`**. Both variants have the
same text metrics, but `NFM` compresses patched icons into a single text-cell
ink box, making navigation and sidebar icons look like tiny dots. `NF` preserves
their intended outlines.

If you do not want to change every Windows Terminal profile, apply the same
font settings only to the profile used to launch Crude.

### 3. Enable icons in Crude

Open **Tools → Settings → IDE → Interface** and enable:

- **Show file icons**
- **Nerd font**

Close every Windows Terminal window and reopen Crude after installing or
changing the font. Color emoji are rendered by Windows Terminal's system emoji
fallback and may still look different from Kitty or Alacritty on Linux; Crude's
patched interface icons should remain clear and consistently sized.

For native Windows visual testing, see
[`docs/guides/windows-visual-capture.md`](docs/guides/windows-visual-capture.md).
