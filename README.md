<p align="center">
  <img src="assets/crude-icon.svg" width="88" height="88" alt="Crude icon: a drop of crude oil with a terminal prompt">
</p>

<h1 align="center">Crude</h1>

<p align="center">
  An open-source, terminal-native agentic development environment (ADE) for Linux and Windows.<br>
  AI coding agents, an LSP-powered editor, persistent terminals, Git review, and workspace memory.
</p>

<p align="center">
  <a href="https://opencrude.github.io/">Website</a> ·
  <a href="#installation">Install</a> ·
  <a href="docs/README.md">Documentation</a> ·
  <a href="https://github.com/opencrude/crude/releases">Releases</a> ·
  <a href="https://github.com/opencrude/crude/issues">Report an issue</a>
</p>

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

| System | Command |
|---|---|
| Linux x86-64 (glibc 2.35+) | `curl -fsSL https://opencrude.github.io/linux.sh \| sh` |
| Windows x64 (PowerShell 5.1+) | `irm https://opencrude.github.io/windows.ps1 \| iex` |
| macOS | No release yet. [Build from source](#build-from-source). |

Run the same command again to update. See the [installation guide](https://opencrude.github.io/#install) for the optional GUI window and manual installation, or [download the latest release](https://github.com/opencrude/crude/releases/latest).

## Quick start

1. Open a terminal in your project folder and run `crude`.
2. Open **Tools → Settings → Accounts** and add a provider account.
3. Open **Agents**, select a model, enter a task, and press **Enter**.

Press **Ctrl+Alt+P** to open the command palette.

### Open a remote project

Use the same Crude build on both computers. Set up key-based SSH and verify the server’s host key first. Then run this **on your computer**, using the project path on the server:

```sh
crude --ssh build-host --workspace /srv/projects/example
```

Crude keeps the editor window on your computer. The core runs agents, language servers and terminals on the server. SSH alone is enough; you do not need to expose another port. Use **Connection Details** in the command palette to see the active connection. See [remote sessions and recovery](docs/guides/core-shell-workflows.md#ssh-and-invocation-selection).

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

Crude's original code is licensed under [Apache-2.0](LICENSE-APACHE). Third-party code retains its own license; see [NOTICE](NOTICE) and [Codex attribution](agent/codexio/NOTICE). `LICENSE-MIT` applies only to included third-party material.
