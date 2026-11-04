# Code-Intelligence Setup

> Status: IN PROGRESS. The setup checklist now builds a task for workspace
> Aeron. Its unrestricted normal tools cover research, installation and LSP
> configuration; the earlier rigid installer is not the setup-screen path.
> Native LSP navigation and edit-review interactions are verified at 80x24 and
> 160x40. Real-model setup acceptance remains open; see the
> [open limits](#open-limits-2026-11-04) and
> [the current direction](#direction-a-workspace-aeron-does-the-setup-2026-11-04).
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
installs, checks and maintains the tools. Project decisions are stored in the
workspace capability settings (`ProjectToolDecisions`), and the installed bytes
and their accounting belong to the [tool store](../reference/tool-store.md).
There is no other installer, approval store or registration table.

> Divergence resolved on 2026-11-03: this plan said tools are "a new capability
> kind, `tool`". The code defines `capability.KindTool`, but
> `ApplyRegistration` rejects it, and tools are not registered like skills or
> MCP. The text above now describes the real ownership. The capability
> **Acquisition** page (Never / Ask first / Within approved sources) does not
> govern tools today; the user decided on 2026-11-03 that it will
> ([Setup review](#setup-review)).

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
| 2026-11-03 | The user decided that Settings has no separate Language Servers and Code intelligence tools pages: language servers are code-intelligence tools. One page lists them in one table that shows whether Crude or the user installed each tool, with a much better design. The user asked to resolve the plan divergences before more IDE plan work. |
| 2026-11-03 | The user asked whether the feature should be called "intellisense". Research: "IntelliSense" is a registered Microsoft trademark and names only part of the feature. Keep **code intelligence** ([Name](#name)). |
| 2026-11-03 | Merge only **Language Servers** and **Code intelligence tools**; keep **Code Intelligence** preferences separate. The user delegated the setup review design. Offer to install missing runtimes and use JetBrains products as a reference instead of asking the user to settle such details. The existing **Acquisition** install policy also governs code-intelligence tools. |

## Direction: a workspace Aeron does the setup (2026-11-04)

This section supersedes the [question flow](#question-flow), the one-shot
consent preview and the "install through catalog recipes only" assumption where
they disagree. Keep it small: the effort goes to editor interaction and the
cooperation with the language server, not to a trust framework around setup.

- **One Aeron per workspace.** Aeron is the existing narrow-task agent role, not
  a new kind of agent. The workspace has one instance. The shell opens it
  shortly after the workspace opens, once startup has settled and the user is
  not working, so a restore or first input never competes with it. It starts
  minimized, and shows in the Agents sidebar like any agent with its idle or
  running state. A restart reopens the same saved session. The user can open it
  to see what it does and task it whenever they choose. Crude parts and the main
  agent task it with normal messages. The user can also task the main agent to
  do the same job if Aeron falls short.
- **The setup screen stays.** It shows the languages Crude found. Its switches
  say what Aeron should set up and what it should leave alone. **Set up** builds
  a prompt from them and sends it to Aeron. The screen installs nothing itself.
  Tools that the catalog cannot install directly are no longer fake switches:
  Aeron works out how to get them (a package manager, a release file).
- **Auto approve by default.** Aeron runs with the session permission mode and
  starts in the automatic mode. There is no second approval store, no scope
  compiler and no deduplicating question service. The switches are prompt text.
- **Aeron has full control.** It has no tool allow-list or deny-list and no
  capability limit beyond the session permission mode. It searches the web,
  finds the right server and settings for the project, downloads and installs
  the tool, and writes the server's launch command and arguments in the
  workspace settings (`lsp.*` paths, through `crude_control`). That is the
  point of an agent here: the user does not do these steps. Only Aeron may
  write the `lsp.*` authority paths; every other agent keeps the human-only
  rule (`aeronOwnsSetting` in `core/control_settings.go`). The existing check
  (start, initialize, answer a fixture) runs after the change, and Problems
  shows the result.
- **Few interruptions.** Aeron asks the user only for a decision it cannot make
  (for example, a tool that needs administrator rights). It uses the normal agent
  question. The prompt tells it to ask once, to keep progress in its own view and
  to report a failure once. The prompt text lives in `prompt/`.
- **Cleanup.** Removal of tools stays on the Settings page. Aeron removes a tool
  only when the user asks.

### Implementation status (2026-11-04)

Built: the Agents sidebar fold; `InvocationSelection.MainAgent` and
`NewAgentSessionAs` (a session rooted in a built-in role, over the same core
RPC); the Aeron role with no tool or capability restriction; the setup screen
(the 2026-11-03 list-and-details review, with every tool selectable and
selected at first) that sends `prompt.ToolSetupMessage` to the workspace's
Aeron, minimized and on automatic approval (`tui/viewmodel/workspace_aeron.go`);
the close button of the setup window; and the permanent workspace Aeron
(`EnsureAeronSession`): the core records its session in `workspace_metadata`,
reopens that saved session on the next attach, reports `idle` in the Agents
panel, and never takes the user's tab. The shell creates it after startup
settles and only while no restore, lifecycle operation or user input is
running; a temporary admission conflict retries quietly. Not built or not
verified: a rendered
native Windows run of the whole journey, and an end-to-end Aeron installation
run with a real model.

### Open limits (2026-11-04)

These items are not done. Do not report them as complete.

- **Code-action support is not complete.** Command-based code actions
  (`workspace/executeCommand`) and code actions that change more than one file
  are not supported. Crude lists them as not available.
- **Aeron is not verified with a real model.** No end-to-end run has installed
  a tool through Aeron with a real model. After a cold core restart, Crude does
  not find the saved Aeron session; it opens a new one.
- **The Linux-only headless captures did not run.** `TestNavigationVisualHeadless`
  and `TestCodeActionVisualHeadless` need Linux (`/proc`). They were not run on
  the Windows development machine, so their changed phase tables are not
  verified. The native Windows captures at 80x24 and 160x40 passed.
- **Three tests from the 2026-11-03 store and Settings work fail on Windows.**
  They fail the same way on `e2b4a463` alone, before the merge:
  `TestPreparedInstallRejectsReplacedOutputFolder` and
  `TestPreparedInstallRetainsReservationIfStagingIsReplaced` (Windows does not
  let the test rename an open staging folder), and
  `TestEverySettingsCatalogActionsTouchFooterRule` for Language Tools at 88x24
  and 70x18 (the painted catalog action has no hit target). That work has only
  Linux evidence.

### Language-server interaction fixes (2026-11-04)

- **Navigation no longer fails while the server loads.** Measured with real
  gopls on this repository: the first references search took about 50 s and
  about 10 s after a restart, and every try was cancelled at 10 s, so the user
  only saw "Navigation timed out". Manual requests now share `ManualLSPRequestTimeout`
  (two minutes) in the manager, the connection and the shell. A request made
  while the server starts or restarts waits for it (`awaitSynchronized`)
  instead of failing. Guarded by `TestEditorNavigationWaitsForRealServerAfterStartAndRestart`.
- **Restart remains usable through the UI.** The Code menu keeps navigation and
  code actions while the server starts. Results wait for the exact connected
  producer as well as the context publication, instead of appearing and closing
  against older readiness. `TestConnectedNavigationDuringRestartLanguageServers`
  exercises the actual restart command.
- **Choose a place and go there.** Navigation rows open the code on one Enter
  or click. Available code actions go directly to their shared change review.
  Removed the metadata inspection page and extra Open/Preview step. Real gopls
  import removal after restart is exercised by
  `TestRealServerQuickFixWorksAfterRestart`. Server commands, resource operations
  and multi-file code actions remain unsupported; these UI changes do not
  implement those protocol paths.
- **Compact native pickers.** Opus supplied a UI-only design review: size each
  picker to its rows, place it beside the source caret, use muted native list
  detail for folders, show the result count, and remove Close-only footers.
  Both pickers share one placement helper and the List's cached intrinsic
  measurement. WindowFrame still owns chrome and the editor owns caret geometry.
- **The shared change review shows the code first.** Rename and code actions
  use one relative-path line diff and one Cancel-default review. Opus designed
  its compact header and layout after the native capture exposed a second
  hash dump. Original action provenance remains in `ReviewedEditPreview.Evidence`,
  while file hashes and source/provider checks stay in their existing fields.
  The ordinary review and error messages no longer print those internal values.
- **File tree markers match Problems.** The tree counted only exact-source
  facts, so files found by a project check and facts kept after a restart showed
  in Problems but not in the tree. The tree now counts every problem Problems
  lists; the switch hides markers only. Guarded by
  `TestFileTreeMarkersMatchProblemsPanel`.

Native Windows acceptance uses `TestLSPInteractionNativeCapture` with installed
gopls and sequential `scripts/tuishot.ps1` captures at both sizes. The actual Code
menu opens the two-reference picker, Enter on its second row lands in code, and
the real Organize Imports action opens its exact import-removal review directly.
Cancel leaves the source and private draft unchanged. The observer checks the
actual changed line is visible and no action hash, canonical-source text or
protocol wording is shown. These checks do not certify unimplemented command
actions or a model-driven tool installation.

## Blocking failures and rejected design

Recorded on 2026-11-03 after the user's real-project report, **Pasted IMG5**
(reference ref-32b150624c394e46db3c2519a2da945e). The original evidence below
describes the rejected implementation. The replacement review (list and
details) has focused Linux selection and dismissal evidence and is the layout
of the current setup screen. The 2026-11-04 direction above supersedes its
catalog-only installation requirements: every tool is a real choice that
Aeron sets up. Package-manager support in the store, real-model acceptance and
full setup acceptance remain open.

### Functional failures and evidence

| Failure | Evidence and boundary | Required outcome |
|---|---|---|
| Common tools cannot be installed through setup. | `insight/catalog/download.go`, `Package.DownloadFor`, accepts only GitHub release-file sources and rejects every other source type before installation. The imported entries for bash-language-server, typescript-language-server, pyright, css-lsp and yaml-language-server use npm; asm-lsp uses Cargo. This is missing Crude implementation, not evidence that those tools cannot run on Windows, that a download failed, or that the user's computer lacks a prerequisite. Even an installed package manager does not bypass this rejection. | Support the catalog-driven installation methods needed by the actual project, with explicit prerequisites, reviewed effects and the existing shared store/evaluation/accountability owners. Importing package metadata alone is not installation support. Do not add a language/tool-named special case or run an unreviewed global install. |
| The visible switches cannot be changed. | The user reported this directly. `tui/screens/toolsetup/screen.go` creates an On/Off field for every candidate, then sets `Disabled = !item.Installable`. Thus the unavailable tools are deliberately inert but look like ordinary enable/disable controls. The page controls installation selection, not language-service enablement. | Make available choices genuinely selectable by keyboard and pointer, and identify unavailable tools and the exact reason without a false switch. Do not allow an On state that has no working installation path. Changing labels or hiding unavailable tools alone does not complete acquisition. |
| The user cannot dismiss the page normally. | User-reported failure, not yet reproduced through a completed native input journey. A concrete source defect is confirmed in `tui/viewmodel/project_tools.go`, `showProjectTools`: the frame is created with its close glyph enabled, while its floating window uses `wm.WithClosable(false)`. `tui/wm/manager.go` routes title clicks before content, and `tui/wm/window.go` excludes close cells from dragging only when the window is closable. The displayed and interactive close contracts disagree. | X, Escape and the visible defer action must have coherent behavior and return input to the original shell owner. No click may turn the displayed X into a title drag. |
| Linux reproduction, 2026-11-03. | `scripts/tuishot.sh` at 160x40, a trusted project with Go, Python, TypeScript, Bash, YAML, CSS and JSON files and an isolated `CRUDE_HOME`. The review opened with five candidates (bash-language-server, pyright, css-lsp, yaml-language-server, json-lsp), all npm packages, all inert **Off** switches. **Install selected** looked enabled with nothing to install. The reason text is cut ("is installed with a language package"). gopls and typescript-language-server were found on `PATH` and were correctly not offered. A click on the title X changed only the frame ink; the review stayed open. Esc closed it. **Not now** was not exercised. | Same as the rows above. |
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

## Replacement design

The user chose the page merge, runtime offer and shared install policy, and
delegated the review design. The following layout and name are implementation
proposals, not user approval of a particular rendering. Names, versions, sizes
and states in the sketches are examples. Use JetBrains IDEs as the behavior
reference for details this plan does not settle.

### Name

Keep **code intelligence**. "IntelliSense" is a registered Microsoft trademark
(USPTO registration 2030471, EUIPO 000079269). In Visual Studio it names only
completion, parameter info, quick info and member lists. Crude also gives
diagnostics for files nobody opened, navigation, rename, code actions, inlay
hints and answers to agents. JetBrains calls this "code insight"; Sourcegraph
uses "code intelligence". Write **Code Intelligence** as a page title and
"code intelligence" in sentences. Use one word for one meaning: the
"Language intelligence" switch on the Code Intelligence page becomes
**Code intelligence**.

### Language Tools page

Today two IDE pages describe one thing: **Language Servers** (179 catalog
servers, each **On**, which does not say if a server is installed) and **Code
intelligence tools** (a flat form of tools that Crude installed, with tools on
`PATH` as text blocks). They become one page, **Language Tools**, directly
after **Code Intelligence** in the IDE group. **Code Intelligence** keeps the
preferences (on/off, start on open, features, prediction, resources) and is not
changed by this merge. A language server is one kind of language tool; the page
can list other tools later without another rename.

The page is a standard collection (settings-design "Collections"):

```
 Language Tools  6
 The tools that give code intelligence, and who installed them.

 [ Search tools, languages and commands      ⌕ ]   This project  Installed  All

 Tool                     Languages        Installed by     This project
 gopls                    Go               You              Running
 typescript-language-se…  TypeScript, JS   You              Running
 yaml-language-server     YAML             Crude            Running
 pyright                  Python           Not installed    Can install
 bash-language-server     Bash             Not installed    Can install with Node.js
 marksman                 Markdown         Not installed    No download for this computer

 Crude installed 1 tool. It uses 41.2 MB.
 [ Set up tools ]  [ Add server ]  [ History ]  [ Remove unused ]
```

- One row for each tool (a server program), not for each language. The
  filter is **This project** when a workspace is open (the tools for the
  languages that the project contains), **Installed** (by anyone) or **All**
  (the whole catalog, which replaces the old Language Servers list).
- **Installed by** is **Crude** (the shared tool store), **You** (found on
  `PATH` or set as a custom command; Crude never removes it) or
  **Not installed**. Runtimes that Crude installed for tools are rows too.
- **This project** comes from the assembly plan, which is the one owner of
  that state. Healthy states stay short. A state that needs action names it.
- Enter opens the tool: breadcrumb `Language Tools ▸ pyright`, the subtitle
  says the state, and shared tabs **Overview**, **Launch**, **Files**,
  **History** (Files and History only for tools that Crude installed).
  Overview aligns `Key: value` lines: languages, file types, installed by,
  version, program, folder, source, checksum, license, approval, workspaces,
  last use; for a tool that is not installed it says how Crude installs it,
  what runs and what it needs. **Launch** is the current per-server editor
  (On/Off, command, arguments, environment, settings, initialization options).
- Footer decisions follow the tool: **Install** (opens the same native review
  for this one tool), **Remove** (Crude-installed only, with the existing exact
  preview and Cancel-default confirmation), **Restore default** (after a launch
  edit), then **Back**. A tool installed by the user never gets Remove. A
  runtime that an installed tool still uses cannot be removed alone; the
  preview names the tools that need it.

### Setup review

The review is an installation consent dialog in the shared dialog frame, not a
settings form:

```
 Set up code intelligence ─────────────────────────────────────── ×
 This project uses Python, Bash and YAML. Crude can install tools
 for them. Editing works without them.

  [x] pyright 1.1.389              Python   npm
  [x] bash-language-server 5.4.0   Bash     npm
  [ ] yaml-language-server 1.15.0  YAML     npm
      Node.js 22.11.0 (needed by the tools above)

 pyright 1.1.389
 Source    npm registry, package pyright@1.1.389
 Runs      npm in a private folder. Package scripts do not run.
 Needs     Node.js 18 or later. Crude installs Node.js 22.11.0 for it.
 Program   pyright-langserver
 License   MIT     Size  about 12 MB

 Crude cannot set up Markdown. marksman has no download for this
 computer. Install it yourself, then choose Check again.

                                  [ Install 3 items ]  [ Not now ]
```

- It opens by itself only when at least one tool can install now. When nothing
  can install, no dialog opens; Problems and Language Tools say why.
- Each installable tool is one check row (Space or a click toggles it). A tool
  that cannot install is never a control: one sentence gives the reason and
  the next step.
- A runtime that a selected tool needs and that is not on the computer is a
  dependent row: it is included while any selected tool needs it and is not a
  separate switch. A runtime already on the computer (on `PATH`, at the needed
  version) is used and not installed.
- The focused row shows its complete details below the list, wrapped, never
  cut.
- The primary button names the count. With nothing selected it is disabled and
  the status says why.
- X and Esc close without a decision, so Crude asks again later. **Not now**
  remembers the refusal. Both return input to the editor.
- After **Install**, each row shows its progress and result in place. Closing
  the dialog does not stop the installation; a footer notice reports the end.
- The capability **Acquisition** policy governs it: **Never** offers nothing
  and Language Tools says installation is turned off; **Ask first** is this
  dialog; **Within approved sources** installs tools from the known sources
  without the dialog and reports them in a footer notice. A source outside the
  known sources always asks, whatever the policy.

### Package-manager installation and runtimes

Installation methods are data in the catalog, keyed by the package source type,
never Go code named after a tool or language. Each method names the runtime it
needs (as a catalog package with a minimum version), the command that installs
into a private staging folder and where the program is in it:

| Source | Needs | Installs with | Packages |
|---|---|---|---|
| npm | Node.js | `npm install --prefix <stage> --no-save --ignore-scripts <name>@<version>` | 27 |
| PyPI | Python 3 | `python -m venv <stage>`, then its pip installs `<name>==<version>` | 12 |
| Go | Go | `go install <name>@<version>` with `GOBIN=<stage>/bin` | 5 |
| Cargo | Rust (cargo) | `cargo install --locked --root <stage> <name> --version <version>` | 9 |

Runtimes are catalog packages with official release files and published
checksums (for example the Node.js distribution with its `SHASUMS256.txt`, Go
from go.dev with its SHA-256 list, Python standalone builds, a Rust toolchain).
Crude finds a runtime on `PATH` first, then in the tool store, and installs it
into the store only when neither has the needed version. Runtimes in the store
are private to Crude: Crude never changes `PATH`, shell profiles or the system.

NuGet, gem, LuaRocks, opam and generic sources come later. Never install into a
global location. The existing store seals the staged tree, records a tree
digest and the package manager's own integrity data, runs the same inert
evaluation and publishes through the same gates. The child process uses
Crude's network settings, including its proxy. The review states what runs.
Cargo builds from source, so the review says that build scripts run.

The proposed PyPI command is not sufficient by itself: Python documents that
virtual environments are not movable and that their scripts use absolute
interpreter paths. The store evaluates a staged tree and then renames it, so
the implementation must preserve a valid launch after publication instead of
assuming that a staged virtual environment survives the move. See the
[Python venv documentation](https://docs.python.org/3/library/venv.html).
The solution must also retain exact staged evaluation and the store's existing
publication and removal fences.

The example commands do not establish the complete source or execution
boundary. npm can inherit user and global configuration, and pip can build
source distributions while resolving dependencies. The implementation must
isolate package-manager configuration, account for transitive sources and
disclose any build code before approval. See the
[npm configuration reference](https://docs.npmjs.com/cli/v11/using-npm/config/)
and [pip install reference](https://pip.pypa.io/en/stable/cli/pip_install/).
Do not treat the top-level package name as proof that every dependency came
from an approved registry.

The Go command can connect directly to its checksum database when a module
proxy reports that the database endpoint is unavailable with HTTP 404 or 410.
An installer bridge must prevent that fallback as well as omitting `direct`
from `GOPROXY`; it must fail closed when it cannot proxy checksum requests.
See the [module proxy protocol](https://go.dev/ref/mod#module-proxy-protocol)
and [checksum database](https://go.dev/ref/mod#checksum-database).

The imported package URLs also contain behavior needed by installation:
PyPI extras, Cargo repository qualifiers and Go subpaths. Some npm entries
start a script with Node.js rather than an npm binary. Preserve those fields
in the reviewed recipe or report the entry as unavailable; do not silently
install a different package or program.

### Checkpoints of the replacement

Write each one under [Checkpoints](#checkpoints) before its code starts.

1. **Review and dismissal.** The dialog above, coherent X/Esc/Not now, no
   automatic open when nothing can install. Exit: reopening criteria 1, 2 and 4
   at 160x40 and 80x24.
2. **Language Tools page.** Merge the two pages; remove the Language Servers
   and Code intelligence tools rail entries and their code paths. Exit: mounted
   keyboard and pointer journeys for filters, detail tabs, Install, Remove and
   launch edits; captures at both sizes.
3. **Package-manager installation** for npm, PyPI, Go and Cargo, with
   prerequisite, refusal, cancellation, failure and retry cases on the real
   store. Exit: reopening criteria 3 and 5.
4. **Runtimes** as catalog packages in the store, used by checkpoint 3, with
   removal blocked while a tool needs them.
5. **One install policy.** Acquisition Never / Ask first / Within approved
   sources governs tools.

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

Settings shows this on the **Language Tools** page (decided 2026-11-03; see
[Language Tools page](#language-tools-page)), beside the tools that the user
installed. For each tool that Crude installed:

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
workspace project decisions, the shared store under `CRUDE_HOME`, one registry
source, the once-per-project question and the Settings accountability page.
It is blocked by the [rejected design](#blocking-failures-and-rejected-design).
The next checkpoints are the five
[replacement checkpoints](#checkpoints-of-the-replacement), in order. Later
checkpoints add the separate question for unplanned sources, more registries,
updates and offline behavior. Write each checkpoint here before code changes
start.

**Combined tools inventory, Linux implementation.** Present configuration and managed or
external installations in one collection, with accurate installation ownership,
readable details, server editing and the existing exact removal review. A
missing inventory match is not proof that a tool is absent from the computer.
Scope changes, stale results and navigation must not release or replay removal
authority. Exit requires mounted Settings journeys at both terminal sizes,
including custom configurations, managed and external tools, multiple versions,
history, removal and unsuccessful inventory reads. Installation implementation
and policy changes are separate work.

The combined page now uses the project assembly snapshot and a shared installed
inventory. Linux desktop and narrow captures show project states and external
ownership; mounted tests cover search, filters, detail tabs, configuration,
removal review and stale owners. **Set up tools** opens a project-wide review
from the collection and a single-candidate review from a missing-tool detail.
The latter is bound to the exact project generation and refuses ambiguous
packages. The full Linux test suite, focused race tests, build, lint, design,
documentation and benchmark gates passed. Connected Linux captures at 160x40
and 80x24 show the single-tool review. Native Windows acceptance and the full
race campaign remain open.

**Current: package-manager acquisition.** Resolve npm, PyPI, Go and Cargo
source identities and runtime requirements as catalog data. Offer missing
runtimes in the same review. Install into the shared private store under the
exact reviewed source, policy and process owner; preserve staged evaluation,
transactional publication, dependency leases and cancellation. Do not mark a
candidate installable until its real path is connected. Exit requires real
representative installations, activation, refusal, cancellation, failure and
retry, plus the native Windows journey. Policy integration follows.

The first acquisition slice reads package-manager identity, program form,
qualifiers and runtime name from catalog data. The native review now explains
these prerequisites for recognized packages. This is metadata only: it does not
install tools or runtimes, verify a runtime version or authorize execution.
Package-manager execution must use an exact-argument, owned process tree with
explicit environment and bounded output. A staging marker must survive a crash
or an uncertain process teardown; publication is forbidden in either case.
The store now has a prepared-tree primitive with those retention and
publication fences. No package-manager installer is connected to it yet.

The replacement review is implemented with native list, reader and action bar.
Focused Linux tests cover X, Escape, mixed selection, unavailable-only automatic
review and exact selection. Linux captures show a real mixed project at 160x40
and 80x24, including X dismissal at 80x24. Windows and the remaining reopening
criteria are not yet verified; this does not close setup acceptance.

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
