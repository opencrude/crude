# Crude

Crude is an open-source, terminal-native agentic development environment (ADE) for Linux and Windows. It combines AI coding agents, an LSP-powered editor, persistent terminals, Git review, and workspace memory in one workspace.

[Website](https://opencrude.github.io/) · [Install](https://opencrude.github.io/#install) · [Documentation](docs/README.md) · [Releases](https://github.com/opencrude/crude/releases) · [Report an issue](https://github.com/opencrude/crude/issues)

> **Beta software**
>
> Crude and its agents can change files and run commands with your user permissions. Crude is not a security sandbox. Commit your work, keep backups, and review agent actions.

## Features

- **AI coding agents:** Delegate tasks to parallel agents, with optional isolated Git worktrees. Use plan mode, goals, and session forks, then review reports with evidence.
- **Editor and language servers:** Use completion, hover, rename, code actions, inlay hints, semantic highlighting, and project-wide diagnostics.
- **Persistent workspace:** A per-user core keeps sessions, agents, and terminals running after the window closes. Reattach locally or over SSH.
- **Workspace memory:** Retain evidence-backed decisions, conventions, and lessons across sessions. Search past sessions and edit or remove memories.
- **Review and recovery:** Stage or discard files and hunks, approve risky commands, and restore changes from Local History.
- **Models and tools:** Choose hosted or local models for each agent role. Extend agents with Agent Skills and MCP servers.

## Installation

[Download the latest release](https://github.com/opencrude/crude/releases/latest) for **Linux x86-64** (glibc 2.35 or later) or **Windows x64** (PowerShell 5.1 or later).

The [installation guide](https://opencrude.github.io/#install) provides the installer commands, an optional GUI window, and update instructions. There is no macOS release yet.

## Quick start

1. Open a terminal in your project folder and run `crude`.
2. Open **Tools → Settings → Accounts** and add a provider account.
3. Open **Agents**, select a model, enter a task, and press **Enter**.

Press **Ctrl+Alt+P** to open the command palette.

## Build from source

Requirements: the Go version in [`go.mod`](go.mod), Zig 0.16.0, and Git. Linux also requires a C compiler, libc headers, and `tic` from ncurses.

### Linux

```bash
make libghostty
. scripts/libghostty_env.sh
go run .
```

Run `make build` to produce a standalone binary.

### Windows

```powershell
scripts/bootstrap_libghostty.ps1
. scripts/libghostty_env.ps1
go run .
```

Source builds use `~/.crude-dev`, separate from the installed release profile in `~/.crude`. Set `CRUDE_HOME` to use another profile.

## Contributing

Read [AGENTS.md](AGENTS.md) before making changes. The [documentation index](docs/README.md) covers the architecture and feature contracts, and the [validation guide](docs/guides/validation.md) lists the required checks.

## License

Crude's original code is licensed under [Apache-2.0](LICENSE-APACHE). Third-party code retains its own license; see [NOTICE](NOTICE) and [Codex attribution](codexio/NOTICE). `LICENSE-MIT` applies only to included third-party material.
