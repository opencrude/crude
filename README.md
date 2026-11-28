<p align="center">
  <img src="assets/crude-icon.svg" width="88" height="88" alt="Crude icon: a drop of crude oil with a terminal prompt">
</p>

<h1 align="center">Crude</h1>

<p align="center">
  <strong>An open-source, cross-platform agentic development environment (ADE) for the terminal.</strong><br>
  Agentic coding with subagents, a language-server editor, persistent terminals, Git review and workspace memory.<br>
  Linux, Windows, and macOS from source. Right-to-left text works in every pane.
</p>

<p align="center">
  <a href="https://github.com/opencrude/crude/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/opencrude/crude?include_prereleases&amp;style=flat-square"></a>
  <a href="LICENSE-APACHE"><img alt="License: Apache-2.0 OR MIT" src="https://img.shields.io/badge/license-Apache--2.0%20OR%20MIT-blue?style=flat-square"></a>
  <a href="https://opencrude.github.io/"><img alt="Website" src="https://img.shields.io/badge/website-opencrude.github.io-informational?style=flat-square"></a>
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

## What Crude is

Crude is an agentic development environment (ADE) that runs in your terminal. It is an editor, a terminal and a Git view that agents share with you. You read and edit the code. Agents work in the same workspace with real tools. Every change they make stays reviewable: a diff, a report with evidence, and Local History to restore it.

Crude is the harness around the agent. Project guidance, prompts, tools, Skills and MCP servers stay editable. Context is compacted with the whole history kept, and workspace memory carries decisions forward. Subagents work in isolated Git worktrees and report back.

Use Crude when you want the whole development loop in one window, on a laptop or over SSH. It is cross-platform: Linux x86-64, Windows x64, and macOS when built from source. It works with any model, hosted or local, open or closed.

## Features

- **AI coding agents:** Delegate tasks to parallel agents, with optional isolated Git worktrees. Use plan mode, goals, and session forks, then review reports with evidence.
- **Editor and language servers:** Use completion, hover, rename, code actions, inlay hints, semantic highlighting, and project-wide diagnostics.
- **Persistent workspace:** A per-user core keeps sessions, agents, and terminals running after the window closes. Reattach locally or over SSH.
- **Workspace memory:** Retain evidence-backed decisions, conventions, and lessons across sessions. Search past sessions and edit or remove memories.
- **Review and recovery:** Stage or discard files and hunks, approve risky commands, and restore changes from Local History.
- **Models and tools:** Choose hosted or local models for each agent role. Extend agents with Agent Skills and MCP servers.
- **Right-to-left text:** Arabic, Persian and Hebrew work in every pane, even in terminals that draw them wrongly or not at all.

## Ways to use Crude

One binary covers several workflows. Pick the one that matches the task.

**A project workspace.** Run `crude` in a folder, or name the folder: `crude ~/code/example`. Crude opens the editor, the terminal, Git and the agents for that project. The session stays alive when you close the window; open Crude again to attach.

**One file, the full IDE.** Run `crude src/main.go`. Crude opens the file, uses its folder as the workspace, and keeps the folder tree closed. Language servers stay off until you ask for them. This is the fast path from "I need to read this file" to a real editor with agents beside it.

**Several workspaces in one window.** Open another workspace or another session without leaving the terminal. Switch between them with the tabs and the command palette; the core keeps each one running.

**A remote machine over SSH.** Run `crude --ssh build-host --workspace /srv/project` on your computer. The editor stays local. Agents, language servers and terminals run on the server, and SSH alone is enough; you do not expose another port.

**From another program.** Run `crude --mcp` to offer Crude's own control as an MCP server. Another agent or a script can then drive a running Crude without an interactive window.

## Right-to-left and bidirectional text

Crude arranges bidirectional text itself, so Arabic, Persian and Hebrew read correctly in the editor, the chat, the terminal and every dialog. Windows Terminal reverses and unjoins Persian; Crude sends arranged cells and does not depend on the terminal for it.

Editing stays logical. The caret, selections, search matches, diagnostics and copied text follow the source order of the code, not the order on screen. Code tokens keep their own direction inside mixed sentences. See [right-to-left and bidirectional text](docs/reference/bidi.md).

## The road to v1

Crude is at v0.x. A version number alone does not make a release stable, so v1 has five gates. Each gate needs evidence in this repository, on real hardware. The [stable v1 plan](docs/plans/stable-v1.md) holds the current criteria and the work that remains.

1. **Smooth on a weak laptop.** No visible slowdown on a ThinkPad T450-class machine: a dual-core mobile CPU, 8 GiB of RAM and an SSD. The existing smoothness targets (p95 input-to-paint under 16 ms, no stall over 32 ms, idle at 2 FPS or more) must hold there, measured on that hardware.
2. **Trampoline sandbox.** A sandboxed Crude core runs the untrusted work in a microVM, and the master core on the host hides host details and audits every message that enters or leaves the sandbox. Built on [microsandbox](https://github.com/superradcompany/microsandbox).
3. **History import.** Bring sessions from other coding agents (Codex, Claude Code, Cursor and others) into Crude, and browse, search and read them in the product, in the shape of [agent-dump](https://github.com/xingkaixin/agent-dump).
4. **Native ACP.** Speak the [Agent Client Protocol](https://agentclientprotocol.com/) so Crude can run the vendor agents a person already pays for, under that person's own login, with no credential sharing.
5. **Bidirectional text cannot regress.** Arabic, Persian and Hebrew ship today. The v1 gate keeps them correct.

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

### macOS

Install the Xcode Command Line Tools (`xcode-select --install`), then run:

```bash
make libghostty
make build
./crude
```

This builds Crude and its embedded terminal library natively. It does not need
the Ghostty app, full Xcode, or a Metal device.

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
