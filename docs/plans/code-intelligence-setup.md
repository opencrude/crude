# Code-Intelligence Setup

> Status: IN PROGRESS - BLOCKED. The user rejected the setup-page design and
> reported unusable switches and dismissal on 2026-11-03. Common package-manager
> installations are unimplemented. Connected approval/store/evaluation primitives
> do not establish a usable setup workflow. See [blocking failures](#blocking-failures-and-rejected-design).
> This plan owns how Crude finds, asks for, installs and accounts for the tools
> that give a project code intelligence. It implements the acquisition stage of
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
| 2026-11-03 | The initial project decision uses the existing authenticated native-shell consent boundary, like workspace trust and history protection. A bounded, expiring preview belongs to the exact shell owner and workspace; the setup owner consumes it once and rechecks trust, project and candidate identity before each effect. Generic control, settings and agent calls cannot manufacture this human decision. Agents can inspect setup and inventory; automatic/reviewer authorization never becomes a durable human grant. |
| 2026-11-03 | Implementation rule: remembered refusals and failed attempts suppress automatic offers, not a later explicit **Set up tools** action. That action requests a fresh native preview and decision for still-missing candidates. A changed decision history invalidates an older preview; no old approval or failed request is replayed. |
| 2026-11-03 | The user excluded website work and its gate from this task: keep the work on code intelligence and this plan. |
| 2026-11-03 | The user rejected the current setup page: "the design of this page is completely unacceptable", and asked to document its failures. The page is not accepted and needs a replacement design, not cosmetic approval of the existing form. |

## Blocking failures and rejected design

Recorded on 2026-11-03 after the user's real-project report, **Pasted IMG5**
(reference ref-32b150624c394e46db3c2519a2da945e). These failures remain open.
No product correction or replacement-design acceptance was completed during
this documentation pass.

### Functional failures and evidence

| Failure | Evidence and boundary | Required outcome |
|---|---|---|
| Common tools cannot be installed through setup. | `insight/catalog/download.go`, `Package.DownloadFor`, accepts only GitHub release-file sources and rejects every other source type before installation. The imported entries for bash-language-server, typescript-language-server, pyright, css-lsp and yaml-language-server use npm; asm-lsp uses Cargo. This is missing Crude implementation, not evidence that those tools cannot run on Windows, that a download failed, or that the user's computer lacks a prerequisite. Even an installed package manager does not bypass this rejection. | Support the catalog-driven installation methods needed by the actual project, with explicit prerequisites, reviewed effects and the existing shared store/evaluation/accountability owners. Importing package metadata alone is not installation support. Do not add a language/tool-named special case or run an unreviewed global install. |
| The visible switches cannot be changed. | The user reported this directly. `tui/screens/toolsetup/screen.go` creates an On/Off field for every candidate, then sets `Disabled = !item.Installable`. Thus the unavailable tools are deliberately inert but look like ordinary enable/disable controls. The page controls installation selection, not language-service enablement. | Make available choices genuinely selectable by keyboard and pointer, and identify unavailable tools and the exact reason without a false switch. Do not allow an On state that has no working installation path. Changing labels or hiding unavailable tools alone does not complete acquisition. |
| The user cannot dismiss the page normally. | User-reported failure, not yet reproduced through a completed native input journey. A concrete source defect is confirmed in `tui/viewmodel/project_tools.go`, `showProjectTools`: the frame is created with its close glyph enabled, while its floating window uses `wm.WithClosable(false)`. `tui/wm/manager.go` routes title clicks before content, and `tui/wm/window.go` excludes close cells from dragging only when the window is closable. The displayed and interactive close contracts disagree. | X, Escape and the visible defer action must have coherent behavior and return input to the original shell owner. No click may turn the displayed X into a title drag. |
| The reported broader dismissal/input failure is not fully explained. | The user reported being unable to close the page, but did not identify every attempted exit. The source mismatch above does not explain all possible dismissal paths. **Not now**, native pointer dispatch, command delivery, preview lifetime and focus remain unverified for the reported workload. There is no evidence that every control has the same cause. | Reproduce and verify each actual exit path; do not close this issue merely after correcting the X configuration. |

### Design rejection

The screenshot's design is **rejected**, not a mostly accepted form awaiting
small styling changes:

- A large flat form alternates package identifiers, noninteractive **Off**
  steppers and repeated **Details** labels. It makes unsupported installation
  look like an available feature that is switched off.
- Wide label/value separation and repeated detail rows use substantial space
  without making the choice clearer. Long reason text is clipped in the shown
  viewport, including the explanation needed to understand the disabled control.
- The introduction asks the user to select tools Crude can install while the
  visible list consists of tools it cannot install. The page does not clearly
  distinguish what can be installed now, what needs a prerequisite, and what
  Crude does not support.
- The fixed **Install selected / Not now** footer does not give an adequate
  explanation when nothing is installable. A visible X that cannot be clicked
  compounds the misleading controls and leaves a blocking first-use experience.
- This is an installation-consent review. It must not masquerade as the page
  for enabling and disabling language support.

A replacement must make the task and actionable choices clear, show full
source/version/program/permission details before approval, explain unavailable
items without fake controls, retain a usable safe exit and preserve editing.
Use the shared [dialog](../reference/dialog-design-language.md) and
[control](../reference/control-usage.md) contracts; do not create another close,
focus, scrolling or selection implementation. No replacement layout has yet
been approved or accepted. The user's rejection must not be turned into an
invented approval for a particular card, table or wizard.

### Acceptance failure and reopening criteria

The earlier "setup previews pass" claim was too broad. The screen test checked
rendered labels and directly changed form-model values. The App journey checked
a simplified installable fixture and keyboard confirmation. Those checks did
not establish pointer operation of the title X, the actual project's
unavailable-heavy list, every dismissal route or installation of the tools in
this report. The thin direct-download fixture proved one acquisition path,
not useful general setup. Earlier preview captures and passing backend tests do
not override the user's failure report or design rejection.

Before setup-page acceptance can be claimed again:

1. Review a replacement design against the reported workload, with actionable,
   unavailable and prerequisite-dependent tools represented honestly. Do not
   retain the rejected form behind another entry point.
2. Exercise the real mounted App through ordinary pointer and keyboard input:
   select/deselect a supported tool, read complete details, scroll, resize,
   choose **Not now**, close with X and Escape, reopen and resume editing.
   Cover supported-only, mixed and unavailable-only reviews at desktop and
   constrained sizes. Verify the exact selection and that closing installs
   nothing, consumes no editor/composer draft and leaves no input capture.
3. Install representative tools from the actual catalog's package-manager
   recipes through the real core/store path, including prerequisite absence,
   refusal, cancellation, failure and explicit retry. Verify usable activation,
   not merely successful package-manager exit or an enabled switch.
4. Retain exact-source consent, trust separation, once-per-project decisions,
   no automatic retry loop and honest partial/uncertain outcomes. Missing
   support must not be disguised by weakened security checks or empty success.
5. Verify the rendered native Windows journey and the user's reported exits,
   not only direct calls to screen actions or source-removal assertions.

This section supersedes earlier setup-page acceptance wording. The current
thin checkpoint cannot be banked while these product failures remain open.

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
| [mason registry](https://github.com/mason-org/mason-registry) | What a tool is, its license, languages, and how to install it for each platform | The clangd and gopls entries checked on 2026-11-02 carry no file digests, so Crude records the digest at first download. Some entries need an ecosystem toolchain (gopls uses `go install`). The pinned registry attribution is Apache-2.0 in `insight/catalog/NOTICE`. |
| [aqua registry](https://github.com/aquaproj/aqua-registry) | Command-line tools with published checksums | Coverage of language servers is partial. |
| Declarative launch configurations, such as Helix `languages.toml` | How to start a server: command, arguments, file types, root markers | Import only declarative fields. Never run editor plugin code, for example nvim-lspconfig Lua. |
| [Open VSX](https://open-vsx.org/) | Language IDs, file extensions and settings schemas from extension manifests | Most extensions start their server from extension code, so they are not a launch source. Some well-known servers have licenses limited to Microsoft products. |
| Sources found by an agent | Gaps that the sources above do not cover | Always a separate question for the exact source. |

Registry data is mirrored at pinned commits and built into the program after a
checksum check. There is no signed index yet (decided 2026-11-02): a tool file
whose source publishes no checksum gets its checksum recorded at the first
download, and the install preview says so. A signature with one built-in public
key comes when Crude updates its catalog between releases.

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

The first implementation is connected on 2026-11-03. Native previews belong to
an exact shell, project/root, assembly/settings generation and catalog. The
one-shot decision records refusals and selections without making an audit row
reusable authority. Install stages sealed bytes, checks initialize/initialized
in an independent inert root, joins Stop, then rechecks publication and use.
Failed/cancelled candidates do not publish or retry automatically; unverified
teardown retains the exact process and marked staged bytes for inspection.

Normal and native Windows race tests cover the acquired fixture language,
refusal/reopen, partial failures, cancelled/changed owners, two-workspace use,
exact executable leases and native Settings removal at both terminal sizes.
Real installed-server captures verify unopened diagnostics and honest coverage.
Earlier Settings/setup previews were captured at 160x40 and 80x24, but the
setup-page acceptance claim is withdrawn after the
[reported failures and design rejection](#blocking-failures-and-rejected-design).
Fresh explicit retry after refusal/offline failure has focused normal/race and
real connected-preview evidence; another shell's changed history invalidates the old preview inside the
atomic writer as well as before it. Final combined repository gates remain open
before the thin checkpoint is banked. There is no claim yet for a new
agent-discovered source, additional registries, automatic updates, semantic
feature certification or a recovery action for uncertain staged processes.
