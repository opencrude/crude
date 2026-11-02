# Code-Intelligence Setup

> Status: IN PROGRESS - decisions recorded, implementation not started. This
> plan owns how Crude finds, asks for, installs and accounts for the tools that
> give a project code intelligence. It implements the acquisition stage of
> [autonomous code intelligence](autonomous-code-intelligence.md); read that
> plan's top sections first.

## Scope

Workspace trust is a separate, finished flow
([settings](../reference/settings.md#workspace-analysis-trust)). Trust decides
whether project tools may run in a folder. Setup decides which tools Crude gets,
from where, and with which approval. A trusted folder does not approve any
download, and an approved download does not trust any folder.

Setup is autonomous and agentic. Crude, or an agent working for it, finds what
the project needs, resolves sources, asks the user when policy requires it,
installs, checks and maintains the tools. It uses the existing capability system
([capabilities](../reference/capabilities.md)) as a new kind, `tool`. There is
no second installer, approval store or registration table.

## Decisions

| Date | Decision |
|---|---|
| 2026-11-02 | Trust and setup are separate flows and separate plans. |
| 2026-11-02 | The default policy is to ask. Crude asks once per project for all tools from known sources, not once per language or tool. Example: a project with Go, Python, C and Zig gets one question that lists all its tools. The user can deselect items. |
| 2026-11-02 | A source outside the known sources, such as a GitHub repository that the agent finds, always gets its own question for that exact source. The question shows the repository, the exact release or commit, the files, what will run, and why the agent chose it. |
| 2026-11-02 | Approvals and rejections are remembered. Another project that needs an already installed tool version does not ask again. A rejected or broken candidate is not tried again in a loop. |
| 2026-11-02 | There is no disk cap and no automatic deletion. Settings accounts for every file (see [Accountability](#accountability)). |
| 2026-11-02 | The Microsoft Visual Studio Marketplace is not a source. Its terms say that "you may only install and use Marketplace Offerings with Visual Studio Products and Services". |

## Question flow

- The project question appears after the folder is trusted and discovery has
  produced an assembly plan. It lists each tool with its language, source,
  exact version, size, license and what it runs. **Install** approves that
  exact list. **Not now** keeps editing and syntax features, and Problems says
  in plain language that code intelligence is not set up.
- Later needs (a new language in the project, an available update) are grouped
  into one quiet notice in Problems or the footer. They never open a blocking
  dialog and never interrupt the editor, terminals or agents.
- Manual, Auto Review and YOLO keep their existing meaning. Reviewer or YOLO
  authorization is not a human grant. A new, unplanned source is never covered
  by an earlier project approval.
- All text follows [user language](../reference/user-language.md).

## Sources

Confirmed by the user on 2026-11-02:

| Source | Use | Known limits |
|---|---|---|
| [mason registry](https://github.com/mason-org/mason-registry) | What a tool is, its license, languages, and how to install it for each platform | The clangd and gopls entries checked on 2026-11-02 carry no file digests, so Crude records the digest at first download. Some entries need an ecosystem toolchain (gopls uses `go install`). The registry's own license still needs review. |
| [aqua registry](https://github.com/aquaproj/aqua-registry) | Command-line tools with published checksums | Coverage of language servers is partial. |
| Declarative launch configurations, such as Helix `languages.toml` | How to start a server: command, arguments, file types, root markers | Import only declarative fields. Never run editor plugin code, for example nvim-lspconfig Lua. |
| [Open VSX](https://open-vsx.org/) | Language IDs, file extensions and settings schemas from extension manifests | Most extensions start their server from extension code, so they are not a launch source. Some well-known servers have licenses limited to Microsoft products. |
| Sources found by an agent | Gaps that the sources above do not cover | Always a separate question for the exact source. |

Registry data is mirrored at pinned commits. Whether Crude signs its own index
from the start is an open decision.

## Accountability

Settings gets one page for code-intelligence tools. For each tool:

- Name, version, size, folder on disk, source and digest, license.
- When it was installed, by whom (the user, or automatically with the user's
  approval), which workspaces use it, and when it was last used.
- The history of each install, update, removal and failure, with the reason.

The page also shows the total disk use of everything Crude installed. **Remove**
and **Remove unused** show exactly what will be deleted before anything is
deleted. Tools that the user installed outside Crude (for example on `PATH`) are
listed as not managed by Crude and are never removed.

Agents read the same information through the control API. They install, update
or remove tools only through the same approval flow, and remove tools only when
the user tells them to.

## Checkpoints

The first checkpoint is part of the
[thin assembly path](autonomous-code-intelligence.md#current-checkpoint):
the `tool` capability kind, the shared store under `CRUDE_HOME`, one registry
source, the once-per-project question and the Settings accountability page.
Later checkpoints add the separate question for unplanned sources, more
registries, updates and offline behavior. Write each checkpoint here before
code changes start.
