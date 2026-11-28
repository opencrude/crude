<p align="center">
  <img src="assets/crude-icon.svg" width="96" height="96" alt="Crude icon: a drop of crude oil with a terminal prompt">
</p>

<h1 align="center">Crude</h1>

<p align="center">
  <strong>An open-source, cross-platform agentic development environment (ADE) for the terminal.</strong><br>
  AI agents, a language-server editor, persistent terminals and Git review in one workspace.<br>
  Any model, hosted or local. Right-to-left text in every pane.
</p>

<p align="center">
  <a href="https://github.com/opencrude/crude/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/opencrude/crude?include_prereleases&amp;style=flat-square"></a>
  <a href="https://github.com/opencrude/crude/actions/workflows/conventions.yml"><img alt="Checks" src="https://img.shields.io/github/actions/workflow/status/opencrude/crude/conventions.yml?branch=master&amp;label=checks&amp;style=flat-square"></a>
  <a href="LICENSE-APACHE"><img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache--2.0-blue?style=flat-square"></a>
  <a href="#install"><img alt="Cross-platform: Linux, Windows and macOS" src="https://img.shields.io/badge/cross--platform-Linux%20%7C%20Windows%20%7C%20macOS-lightgrey?style=flat-square"></a>
  <a href="#right-to-left-text"><img alt="Right-to-left: Arabic, Persian and Hebrew" src="https://img.shields.io/badge/RTL-Arabic%20%7C%20Persian%20%7C%20Hebrew-2ea44f?style=flat-square"></a>
</p>

<p align="center">
  <a href="#install"><strong>Install</strong></a> &nbsp;|&nbsp;
  <a href="#get-started">Get started</a> &nbsp;|&nbsp;
  <a href="#features">Features</a> &nbsp;|&nbsp;
  <a href="#the-road-to-v1">Road to v1</a> &nbsp;|&nbsp;
  <a href="docs/README.md">Documentation</a> &nbsp;|&nbsp;
  <a href="https://opencrude.github.io/">Website</a>
</p>

> [!WARNING]
> **Crude is beta software.** Crude and its agents change files and run commands with your user permissions. Crude is not a security sandbox. Commit your work, keep backups and review what agents do.

## Why Crude

Crude puts the whole development loop in one terminal window. You and the AI agents share one workspace: the same editor, terminals, Git and history.

- **Edit by hand or hand off a task.** You do not have to choose. Agents work beside you with real tools, in the code you have open.
- **Every change stays reviewable.** Agents report back with evidence. You review each diff, stage or discard hunks, and restore earlier versions from Local History.
- **Your work keeps running.** A per-user core keeps sessions, agents and terminals alive when you close the window. Open Crude again, on the same computer or over SSH, and continue.
- **The harness is yours.** Project guidance, prompts, tools, Skills and MCP servers stay editable. Choose a hosted or local model for each agent role.
- **Context survives long work.** Compaction keeps the whole history, and workspace memory carries decisions and lessons into the next session.

## Install

| Platform | Command |
|---|---|
| Linux x86-64 (glibc 2.35+) | `curl -fsSL https://opencrude.github.io/linux.sh \| sh` |
| Windows x64 (PowerShell 5.1+) | `irm https://opencrude.github.io/windows.ps1 \| iex` |
| macOS | No release yet. [Build from source](#build-from-source). |

Run the same command again to update. The [installation guide](https://opencrude.github.io/#install) covers the optional GUI window and manual installation. Release files are on the [releases page](https://github.com/opencrude/crude/releases/latest).

## Get started

Run `crude`. You do not need to learn any options first: Crude guides you from there.

1. **Choose a project.** Run `crude` in your project folder. On first use, Crude shows a short introduction and then a folder picker that starts at that folder. Confirm it or choose another folder.
2. **Set up AI.** Crude asks for a provider account and two models: one for the main agent, and a fast, low-cost one for Errand, the agent that does background work such as checkpoints, memory and language-server checks.
3. **Give a task.** Type what you want and press **Enter**.

Press **Ctrl+Alt+P** at any time to see every command and its shortcut.

## Ways to use Crude

Everything starts from `crude`. The table shows where each way of working lives.

| Way of working | How |
|---|---|
| **A project workspace** | Run `crude` in the folder, or pick the folder in the project picker. The editor, terminals, Git and agents open for that project. |
| **One file, fast** | Run `crude path/to/file`. The file opens in the full editor, with the folder tree closed and no project scan. Choose **Open folder as workspace** to get the whole project; your unsaved edits stay. |
| **Several projects** | Open **Recent Workspaces** in the side rail, or **Switch Workspace** in the command palette. Work in the other projects keeps running. |
| **Earlier sessions** | Open **Workspace** in the footer and choose **Sessions**. Previous sessions are listed by day. |
| **A remote machine** | Run `crude --ssh build-host --workspace /srv/project` on your computer. The window stays local. Agents, language servers and terminals run on the server, and SSH alone is enough. |
| **Automation** | Run `crude --mcp` so that another agent or a script can control a running Crude as an MCP server. It is read-only unless you allow more with `--mcp-effects`. |

For a remote machine, use the same Crude build on both computers, set up key-based SSH and verify the server's host key first. See [remote sessions](docs/guides/core-shell-workflows.md#ssh-and-invocation-selection).

## Features

| Area | What you get |
|---|---|
| **Agents** | Parallel agents and subagents, each with an optional isolated Git worktree. Plan mode, goals, session forks, and reports with evidence. The **Board** shows what agents send each other. |
| **Editor** | Completion, hover, rename, code actions, inlay hints, semantic highlighting and project-wide problems from language servers. |
| **Terminals** | Embedded terminals built on the Ghostty terminal engine. They keep running when you close the window. |
| **Git and review** | Stage or discard files and hunks, commit, and approve risky commands before they run. Restore earlier versions of files from Local History. |
| **Context and memory** | Compaction keeps the whole history. Workspace memory keeps decisions, conventions and lessons, and you can search past sessions. |
| **Models** | Hosted or local, open or closed. Choose a model for each agent role. |
| **Skills and MCP** | Search for Agent Skills and MCP servers and install them from Settings. |
| **Right-to-left text** | Arabic, Persian and Hebrew in every pane. [See below](#right-to-left-text). |

## Right-to-left text

Crude has full support for Arabic, Persian and Hebrew in every pane: the editor, the chat, the terminals and every dialog.

Most terminal programs leave right-to-left text to the terminal, and many terminals show it reversed or with unjoined letters. Crude arranges the text itself and sends finished cells, so the text reads correctly in Windows Terminal and on Linux.

- **Correct order and shape.** Crude applies the Unicode Bidirectional Algorithm (UAX #9) and Arabic letter joining.
- **Logical editing.** The caret, selections, search matches, diagnostics and copied text follow the source order, not the screen order.
- **Readable mixed text.** Code tokens keep their own direction inside right-to-left sentences.
- **A font with the glyphs.** The Windows installer and the Linux GUI install add Crude RTL, a monospace font for Arabic and Hebrew script.

Right-to-left support is a v1 gate: it must not regress. See the [bidi reference](docs/reference/bidi.md).

## The road to v1

Crude is beta and ships often; see the [releases](https://github.com/opencrude/crude/releases). A version number alone does not make a release stable, so v1 has five gates. Each gate needs evidence on real hardware. The [stable v1 plan](docs/plans/stable-v1.md) holds the criteria, the order of work and what already ships.

| Gate | What v1 requires | Status |
|---|---|---|
| **1. Weak-machine smoothness** | No visible slowdown on a ThinkPad T450-class laptop: dual-core CPU, 8 GiB RAM, SSD. Input-to-paint stays at 16 ms or less (p95), with no stall over 32 ms. | Open |
| **2. Trampoline sandbox** | Two Crude cores. The workspace runs in a throwaway [microsandbox](https://github.com/superradcompany/microsandbox) microVM. The master core on the host hides host details and audits every message in and out. | Open |
| **3. History import** | Browse, search and import sessions from widely used coding agents (Claude Code, Codex, Cursor, Gemini CLI, OpenCode and more) in a visual browser like [agent-dump](https://github.com/xingkaixin/agent-dump). | Open |
| **4. Native ACP** | Run vendor agents over the [Agent Client Protocol](https://agentclientprotocol.com/) under your own login and subscription, within the vendor's terms. Crude does not copy, store or proxy your credentials. | Open |
| **5. Bidirectional text** | Arabic, Persian and Hebrew ship today. They must not regress. | Shipped |

## Build from source

You need the Go version in [`go.mod`](go.mod), Zig 0.16.0 and Git. Linux also needs a C compiler, libc headers and `tic` from ncurses.

<details>
<summary><strong>Linux</strong></summary>

```bash
make libghostty
. scripts/libghostty_env.sh
go run .
```

Run `make build` for a standalone binary.

</details>

<details>
<summary><strong>macOS</strong></summary>

Install the Xcode Command Line Tools (`xcode-select --install`), then run:

```bash
make libghostty
make build
./crude
```

This builds Crude and its embedded terminal library natively. You do not need the Ghostty app, full Xcode or a Metal device.

</details>

<details>
<summary><strong>Windows</strong></summary>

```powershell
scripts/bootstrap_libghostty.ps1
. scripts/libghostty_env.ps1
go run .
```

</details>

Source builds use the `~/.crude-dev` profile, separate from the installed release in `~/.crude`. Set `CRUDE_HOME` to use another profile.

## Documentation

- [Documentation index](docs/README.md): architecture, feature contracts and guides.
- [Code map](docs/architecture/code-map.md): which package owns what.
- [Remote sessions](docs/guides/core-shell-workflows.md): the core, shells and SSH.
- [Right-to-left text](docs/reference/bidi.md): how Crude arranges bidirectional text.
- [Stable v1 plan](docs/plans/stable-v1.md): the five gates and the shipped baseline.

## Contributing

Issues and pull requests are welcome. Read [AGENTS.md](AGENTS.md) first: it holds the rules for every change and a map to the detailed contracts. Run `make hooks` once per clone so that `git push` runs the same convention checks as CI. The [validation guide](docs/guides/validation.md) lists the other checks.

If Crude is useful to you, star the repository. It helps other developers find it.

## License

Crude's original code is licensed under [Apache-2.0](LICENSE-APACHE). This is not a dual license. Third-party code keeps its own license; see [NOTICE](NOTICE) and [Codex attribution](agent/codexio/NOTICE). [`LICENSE-MIT`](LICENSE-MIT) applies only to included third-party material.
