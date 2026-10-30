# Autonomous Code Intelligence

> Status: IN PROGRESS — proposed design for review; nothing here is implemented.
> It replaces "install a language server first" with intelligence that Crude
> assembles, verifies and governs by itself. [LSP acceptance](lsp-acceptance.md)
> folds into phases 2 and 4.

## Problem

Crude's intelligence today is a thin client for whatever language server the
user has already installed.

| Symptom | Cause in the code today |
|---|---|
| No completion, outline, navigation or errors until a server is installed and running | `requestCompletion` in `tui/viewmodel/keyboard.go` returns nothing unless the server is connected. **Detect servers** "does not install their executables" ([settings](../reference/settings.md)). |
| No whole-project error, warning or suggestion counts | Diagnostics exist only for documents a server has open. Problems says **Waiting for source file** when no source is open. `ProjectInsights` in `types/lsp.go` is a language-server snapshot. |
| Background analysis can take the machine down | Server memory and thread settings are hints, not limits (`tui/settings/language_servers.go`). Nothing contains CPU, I/O or memory or reacts to pressure. Process-tree containment is still open. |
| Weeks of setup per project | The user translates what the build already knows (flags, include paths, interpreters, severities) into each tool's configuration by hand. |
| Unknown languages get nothing | `types/language_servers.go` is a catalog of twelve hand-written entries. |
| Severity tuning is per tool | Each tool has its own suppression syntax. Crude has one minimum-severity filter. |
| Popups do not feel like an IDE | Hover and diagnostic details open centered on the screen (`tui/viewmodel/lsp_intelligence.go`, `tui/viewmodel/lsp_handlers.go`). Completion has no documentation pane, no inline prediction and no signature help. |
| Aeron never helps manual editing | Aeron drafts commit messages and compaction, nothing in the editor. |
| Agents and the user see different truths | Agents get a session-gated `lsp` tool (`tools/code_intelligence.go`) with separate document probes. |

Chroma lexing costs 60 ms and 35.5 MB of allocation per 20,000 lines
([performance baselines](../reference/performance-baselines.md)). That is
acceptable for highlighting the open file. It is the wrong engine for a
project-wide index.

## Thesis

Understanding a project is a build that Crude assembles for itself.

- **Inputs:** files (including live editor text), the project's build model and
  toolchains.
- **Rules:** analyzers (parsers, compilers in check mode, linters, language
  servers). Each analyzer is described as data.
- **Outputs:** facts (symbols, references, diagnostics, fixes, metrics), each
  with provenance.
- Crude assembles the rule set by itself. It detects what the project is,
  acquires and verifies the tools it needs and configures them from the
  observed build. It proves that each analyzer can see errors before it
  trusts that analyzer's "0 errors".
- Crude runs the rules incrementally under a hard resource contract. Analysis
  can never make the editor or the machine unresponsive.
- One policy layer decides what the user sees. It applies at view time, so a
  severity change is instant and never runs a tool again.
- Aeron is a configuration compiler, not a runtime dependency. It writes
  recipes, language packs and policy rules once. Crude verifies them
  mechanically, then runs them at no token cost. In the editor, Aeron adds
  prediction (ghost text, next edit) on top of mechanical completion, which
  never waits for it.

| | JetBrains | VS Code | Zed | Crude (this plan) |
|---|---|---|---|---|
| Who integrates tools | The vendor writes a per-language engine | The user installs extensions and writes configuration | The editor downloads servers; the user configures | Crude resolves, acquires, configures and verifies |
| Whole-project problems | Background inspections | Only what each extension reports | Open buffers | Every analyzable unit, with baselines |
| Build accuracy | The IDE's own project model | Whatever the user configured | Server defaults | Observed build flags; **Match build** diagnostics |
| Resource safety | One heap limit per IDE | Per extension, nothing global | Nothing global | One governor; contained and freezable |
| Supply chain | Vendor | Marketplace | Latest release, unverified ([report](https://github.com/zed-industries/zed/issues/12589)) | Pinned digests and signatures under the autonomy policy |
| Unknown language | Plugin required | Extension required | Extension required | Aeron authors a pack; oracles verify it |

## What the user sees

Targets for a project Crude has never seen, on a machine with no toolchains:

1. **Under 1 s:** syntax highlighting, outline, folding, local completion and
   syntax errors for the visible files.
2. **Seconds, in the background:** the whole project is parsed. Problems shows
   project-wide syntax counts, by-name navigation works everywhere, and the
   footer shows coverage ("C++: syntax only").
3. **When build truth would add value:** Problems shows one inline question:
   "This project's build runs code. Allow Crude to run it for build-accurate
   diagnostics? [Allow] [Static only]".
4. **Within the autonomy policy:** Crude acquires and verifies the tools,
   observes or reads the build, then shows one notice: "Added clangd 19.1.7
   and compile commands for 412 units from `make`".
5. **Steady state:** semantic diagnostics that match the real compiler, counts
   for the whole project with "new in my changes", server completion merged
   into the popup, and Aeron ghost text when Aeron is configured. Re-opening
   the project is instant from the persisted facts.

## Guarantees

1. **Zero install.** Any folder gets completion, outline, navigation, folding
   and whole-project syntax errors from built-in parsers before any tool
   exists.
2. **Autonomous depth.** Within the user's autonomy policy, Crude acquires,
   configures and verifies the compilers, checkers and servers it needs. The
   user writes no tool configuration.
3. **Whole-project truth.** Counts cover every analyzable unit. They show
   freshness and what the working tree introduced against a baseline.
4. **Proven, not assumed.** An analyzer that has not passed its oracles reports
   "unverified", never "0 errors".
5. **Never hang.** Interactive work never waits on analysis. Analysis runs
   contained, below the user's work, and freezes under pressure.
6. **One policy.** All sources pass through one severity and visibility layer.
   Every automatic decision has a visible reason and an override.
7. **One truth.** The editor, Problems, the footer, the file tree, agents and
   the control API read the same facts.
8. **AI is additive and bounded.** Mechanical features never depend on Aeron.
   Aeron spending has a budget, a cache and a meter.

## Architecture

```
 live editor text ─┐  file watcher ─┐  build model ─┐  toolchains ─┐
                   ▼                ▼               ▼              ▼
   ┌────────────────────────────────────────────────────────────────────┐
   │ Insight service (per workspace, in the core): the only writer      │
   │                                                                    │
   │ producers: static ▸ build truth ▸ language servers ▸ extended      │
   │            analyzers ▸ Aeron (prediction and authoring only)       │
   │      │ facts + provenance + input digests                          │
   │      ▼                                                             │
   │ fact graph (content-addressed, incremental, persisted)             │
   │      │                                                             │
   │ policy at view time: preset ▸ rules ▸ baseline ▸ dedupe            │
   └──────┬─────────────────────────────────────────────────────────────┘
          │ versioned snapshots and deltas (core protocol)
   ┌──────┼──────────────┬───────────────┬──────────────┬──────────────┐
 editor  completion   Problems, footer   agent tools   control API
          popup        and file tree

 Governor (one per core, across all workspaces): admission, containment
 and pressure freeze for every process and job above
```

Ownership:

- A new neutral `insight/` package, like `memory/`, owns the fact schema,
  storage, recipe model, output parsers and policy algebra. It never imports
  workspace or TUI code.
- A workspace insight service owns the lifecycle: generations, document
  revisions, watcher input, cancellation and shutdown joins
  ([async lifecycle](../reference/async-lifecycle.md)).
- `workspace/lsp_manager.go` becomes one producer. It stops owning diagnostics
  that the TUI reads directly.
- The TUI reads Insight views only. The cutover deletes the direct `LSPMgr()`
  diagnostic, insight and completion paths in `tui/viewmodel/`. No adapter
  remains.
- The governor lives in the core, one per user. Two workspaces that each stay
  inside their own "reasonable" limits can still overload a machine together.

### Fact graph

- Fact kinds: file (language, unit membership), symbol definition, scope,
  import, reference candidate, diagnostic, fix, metric, build unit, toolchain
  and coverage.
- Each fact records its producer (analyzer, version, recipe digest), input
  digests, generation and trust level.
- Facts are keyed by content digest. Switching between branches reuses facts
  instead of analyzing again.
- Definitions are eager and references are lazy. The index keeps per-file
  identifier postings; a reference query parses only the candidate files.
  Memory stays close to the size of the definition set.
- Invalidation follows the build graph: compiler dependency files give a
  reverse include map for C and C++, `go list` gives the package graph, and
  project references and crate graphs do the same for TypeScript and Rust.
  Open and changed units are checked first.
- Each fact has a freshness state: current; stale (an input changed and a
  recompute is queued); blocked (tool, trust or budget missing, with the
  reason); or failed (with the tool's output). Stale facts stay visible and
  dimmed. Nothing is cleared to make the view look clean.
- Facts from disk-based tools are mapped through unsaved edits. A fact whose
  mapped range was edited becomes stale.
- Diagnostics are deduplicated across sources by path, overlapping range and
  normalized code. Precedence is build truth, then language server, then
  static. Counts never include a duplicate.
- Storage is per workspace under `CRUDE_HOME` (the existing pure-Go SQLite),
  with in-memory query structures in the core. Shells receive per-client
  snapshots and deltas, as LSP diagnostics do today
  ([core client protocol](../reference/core-client-protocol.md)). The
  candidate sets of open documents are mirrored to the shell, so a remote shell
  paints completion without a round trip. The mirror is read-only.

### Producers

**Static: built in, always on, nothing to install.**

- Language identity comes from Linguist data through go-enry (extension,
  filename, shebang, modeline and content heuristics).
- Parsing uses gotreesitter, a pure-Go tree-sitter runtime: MIT licensed, 206
  grammars, hand-written scanners for 119 of them, microsecond incremental
  edits, and tags and highlight queries. It needs no cgo and cross-compiles to
  every target Crude ships. Phase 0 decides between it and C tree-sitter built
  with `zig cc` (the `terminalvt` precedent). Copyleft grammars stay out of the
  binary; Crude acquires them at runtime as data.
- From the tree Crude gets: project-wide syntax errors (error and missing
  nodes), symbols (tags queries), scopes (locals queries), folding, selection
  ranges, outline and breadcrumbs.
- Completion without a server offers locals in scope, project symbols and
  their module paths, and member completion after `.` or `->`. For members,
  Crude resolves the receiver's declared or assigned type name through the
  index and lists that type's members: struct literals in Go and C, attributes
  assigned on `self` in Python, interface members in TypeScript. These items
  are marked approximate, and server items replace them when they arrive.
  Import edits come only from servers.
- A language with no grammar still gets an identifier index from chroma tokens:
  word completion, search and by-name navigation.

**Build truth: needs trust.** The project's own compilers and checkers run in
check mode on the smallest unit, with structured output. **Match build**
diagnostics come from here.

**Language servers.** Acquired and configured from the build model. They serve
interactive semantics for open and recently edited files. They never scan the
whole project.

**Extended analyzers.** The strictness preset enables them: clang-tidy,
staticcheck, clippy, eslint, ruff rule sets, and dependency advisories through
osv-scanner.

**Aeron.** Prediction and authoring only. Optional AI review hints are
labelled, opt-in, and never counted as errors or warnings.

Suggestions in the counts are info and hint diagnostics plus findings that
have an available fix.

### Build model

The build already knows the truth. Crude reads it or watches it instead of
asking the user.

- Structured adapters: `go list -json` and go.work; `cargo metadata`; tsconfig
  references and package workspaces; pyproject, uv and poetry lock files, and
  interpreter and virtual-environment discovery; the CMake File API;
  `meson introspect`; `xmake project -k compile_commands`; nimble and nim.cfg;
  build.zig; Bazel `aquery`; MSBuild binary logs.
- For everything else (plain make, scons, shell scripts, in-house tools), the
  **build observer** runs the project's build once, in trusted workspaces only.
  It records each process execution and deduces compile units from compiler
  driver arguments. Bear uses the same method; Crude implements it itself:
  - On Linux, Crude uses ptrace on its own child (exec, fork and clone
    events). This also catches statically linked tools, and nothing needs to
    be installed.
  - On every platform, Crude puts PATH shims first: its own binary in shim
    mode records the call and then runs the real tool.
  - The static fallback is a `make -n -k` dry run. `$(shell ...)` still runs
    while make parses the Makefile, so the fallback also needs trust.
- The result is a build-model generation keyed by the digests of the build
  inputs, the compilers and the relevant environment. When these change, Crude
  observes again in the background while the previous generation keeps
  serving.
- A new file outside the model gets a command interpolated from the nearest
  unit in the same target, as clangd does.
- Uses: compile_commands.json under `CRUDE_HOME`, passed to clangd with
  `--query-driver` for the observed compilers (never written into the
  repository unless the user asks); build-truth check units; test discovery;
  run configurations; the agent project map.

### Recipes and language packs

Intelligence is data, so users and agents can read, change and share it.

- **Recipe:** an applies-to predicate; required tools with version ranges; a
  command template per unit (file, translation unit, package or project); an
  incremental strategy; an output parser; a severity mapping; a cost class; a
  trust requirement; an environment allowlist; a timeout; oracles.
- **Language pack:** detection hints; a grammar; queries (tags, locals, folds,
  injections); recipes; a server recipe whose command and settings derive from
  the build model; snippets; a fixture project with expected diagnostics.
- Sources in priority order: workspace overrides (settings overlay, shared as a
  repository file only when the user exports them); authored by Aeron; the
  Crude registry (signed index, pinned); built in.
- Output parsers: SARIF; native JSON (cargo, `go vet -json`, ruff,
  pyright and basedpyright, eslint, mypy); reviewdog/errorformat (pure Go, 49
  predefined tool formats); and the generic `file:line:col: severity: message`
  form.

**Oracles.** Before Crude shows a recipe's counts, the recipe must pass these
checks for that project and toolchain:

1. **Exit status:** a non-zero exit with zero parsed diagnostics means a broken
   recipe, not a clean project.
2. **Unparsed output:** an output line that looks like a diagnostic but did not
   parse fails the recipe.
3. **Location:** each diagnostic resolves to an existing file and a valid range.
4. **Fixture:** the pack's fixture produces exactly its expected diagnostics
   with this tool version. This runs once per tool version, shared by all
   projects.
5. **Injection** (tools that check one file): a copy of a real file with an
   injected error produces a diagnostic at that location, and the unchanged
   copy does not.

### Autonomous acquisition

Tools become a new capability kind in the existing capability system
([capabilities](../reference/capabilities.md)). They use the same Manual, Auto
Review and YOLO policy, the same immutable source and digest preimage for
approval, and the same registration publication. Crude gets no second
installer.

- The resolver turns a need ("C++ build-truth diagnostics", "Nim language
  server") into candidates, in this order:
  1. A tool that is already installed and matches: on PATH, or pinned by the
     project (rust-toolchain.toml, .nvmrc, .node-version, .python-version, the
     go.mod toolchain line, .tool-versions, mise.toml).
  2. The Crude registry: curated and pinned, with SHA-256 digests and
     signatures or attestations.
  3. Mirrored registries at pinned commits: the aqua registry (checksums,
     cosign, SLSA) for command-line tools, and the mason registry (Apache-2.0)
     for servers, linters and formatters.
  4. Ecosystem installers inside hermetic prefixes: uv for PyPI tools (uv also
     provides Python), npm with an acquired Node, `go install` with a matching
     Go, rustup components.
  5. Sources that Aeron found by research. These always need approval and are
     never automatic.
- Each tool installs once under `CRUDE_HOME`, in a directory named by tool,
  version and digest. The directory is read-only after install and shared by
  all workspaces. Packs hold references to it. An LRU collector keeps the
  store under a disk quota and keeps pinned tools.
- Compiler-sensitive tools follow the project: gopls built with the project's
  Go, rust-analyzer from its toolchain, zls for its Zig version, clangd no
  older than its Clang.
- After install, a smoke check confirms the tool's version output.
- Under Auto Review and YOLO, registry tools with verified digests install
  without a prompt, and Crude shows one non-modal notice. Under Manual, Crude
  shows one consolidated card per project with each tool's version, size,
  source, digest and license. Sources without a verified digest always ask the
  user.
- Offline, everything degrades to static intelligence. The acquisition queue
  resumes when the network returns.

### Resource contract

- One governor per core admits every language server, checker, indexer,
  observer, acquisition and Aeron request, across all workspaces.
- Classes, highest first:
  - **Keystroke:** in-process incremental parse and local completion, in
    bounded slices, never queued behind other work.
  - **Interactive:** server requests for the focused document.
  - **Background:** indexing and build-truth checks of changed units.
  - **Bulk:** first full index, full-project checks and observation builds.
    Bulk runs only while the user is idle or when the user asks for it.
- Containment:
  - **Linux:** a systemd user scope per job class where available
    (`CPUWeight=idle`, `MemoryHigh`, `MemoryMax`, low `IOWeight`), frozen
    through `cgroup.freeze`. Without systemd: nice 19, `SCHED_IDLE`, idle I/O
    priority, resource limits, and SIGSTOP/SIGCONT of the owned process group.
  - **Windows:** the Job Objects Crude already uses
    (`fileio/async_owner_windows.go`), plus a job memory limit, CPU rate
    control, idle priority and background processing mode. Crude suspends the
    job's processes under pressure.
  - **macOS:** background QoS priority, plus limits that Crude monitors and
    enforces.
- Pressure signals: Linux PSI triggers (`/proc/pressure/memory`, `cpu` and
  `io`), Windows available-commit and memory notifications, and macOS
  memory-pressure events. Bulk work freezes first, then Background work, and
  both resume with hysteresis. A job over its hard limit stops, and its facts
  become stale with the reason. Nothing is dropped without a visible reason.
- Typing bursts demote Bulk work; idle time promotes it.
- Defaults come from the machine (for example, Background work uses at most a
  quarter of the cores, and all analysis together uses at most a fixed share of
  RAM). There is one place to change them. The Problems header shows progress,
  pauses and their reasons.
- The analyzer environment is an allowlist. Provider keys and Crude secrets
  never reach a project build.

### Policy and control

The user has three choices and one verb.

| Choice | Values | Effect |
|---|---|---|
| Trust (per workspace, asked once, when first needed) | Static only, Full | Full allows Crude to run the project's build, build scripts and procedural macros |
| Strictness | Match build, Strict, Errors only | Which producers count, and at what severity |
| Aeron | Off, Light, Full | Ghost text and next-edit prediction within a token budget |

The verb: point at a diagnostic, group, file or folder and choose an action,
or say what you want.

- Actions: **Not a problem here** (line, file, folder or project), **Treat as
  error / warning / hint**, **Ignore folder**, **Only show new**.
- Each action previews its effect ("hides 312 warnings in third_party/") and
  can be undone. Rules are explicit settings choices in the workspace overlay
  ([settings](../reference/settings.md)). Sharing them as a repository file is
  an explicit export.
- `/insight <request>` turns plain language into rules or recipe overrides
  through Aeron, with the same preview. Examples: "ignore generated/", "use
  the debug configuration", "check with -Wall -Wextra", "use basedpyright".
- Every fact and every automatic decision has a **Why?** card: producer, tool
  version, flags and where they came from, and the rule that set the severity.
- Rule algebra: a match (source, code or category, path glob, symbol kind,
  introduced or existing) gives an outcome (severity, hidden, group). Order:
  user rules, shared rules, preset, tool default. Rules apply at view time and
  are indexed by code and path, so a project with 100,000 diagnostics
  re-filters within one frame.
- Baselines: a snapshot at the merge base or HEAD gives "new in my changes"
  and "since last session". Agent post-edit notices use the same delta.
- Optional export to tool configuration (.clangd, .clang-tidy, ruff settings)
  helps teammates who do not use Crude. Crude itself never needs these files.

### Editor experience

- The completion popup is anchored at the cursor. It has kind glyphs, match
  highlighting, a detail column, a documentation pane that loads on demand,
  import edits from servers and snippets with tab stops.
- Local results paint in the first frame after the keystroke. Server items
  merge in without moving the selected row. Fuzzy filtering runs in the client
  on every keystroke with the fzf matcher Crude already vendors.
- Signature help opens on `(` and `,`. Hover and diagnostic details anchor at
  the cursor or pointer, never centered. End-of-line messages are optional. A
  quick-fix marker in the gutter offers server, recipe and Aeron fixes.
- Ghost text is dimmed inline text (a theme token). Tab accepts it, Ctrl+Right
  accepts one word and Esc dismisses it. If the user types the suggested text,
  the rest stays visible without a new request.
- Next-edit prediction shows a gutter marker. Tab jumps to it and a second Tab
  applies it.
- Definition, references and workspace symbols work by name from the index
  without a server (labelled approximate) and precisely when a server is
  available.
- Tab still indents when nothing is offered. The keys go into the
  [interaction matrix](../reference/interaction-matrix.md) in the same change.

### Aeron in the editor

- Fill-in-the-middle requests go through the narrowest provider adapter:
  DeepSeek's FIM endpoint, Codestral FIM, or an OpenAI-compatible
  `/v1/completions` call with a suffix for local servers. Where no FIM endpoint
  exists, Crude falls back to a chat prompt. Prompt text lives in `prompt/`.
- Context comes from facts, not whole files: the enclosing scope, signatures of
  referenced symbols, imports, nearby diagnostics and recent edits, capped at
  about 2,000 tokens to start.
- A request starts after a pause tuned to the connection's measured latency.
  Each editor has at most one request in flight. The next keystroke cancels
  it. Results are cached by context digest. No request starts when the local
  popup already has an exact match.
- Cost estimate: 2,000 input tokens × about 400 requests in an active hour ≈
  0.8M input tokens per hour. The budget caps this, caching and typed-through
  suggestions reduce it, and the meter shows it.
- Next-edit prediction uses the same path, with edit history from editor
  transactions as context.
- **Explain** is a one-shot request. **Fix** produces a patch, shows a preview
  and applies it as an editor transaction with a Local History snapshot. **Fix
  all** for one rule in one scope goes to an agent that verifies its work
  through Insight deltas.
- Aeron can be a local server. JetBrains runs 100M-parameter per-language
  models on the CPU for full-line completion, so a small local model is a
  valid tool for Crude to acquire like any other.

### Unknown languages

1. Detection finds files in a language that has no pack.
2. Static fallback starts at once.
3. Crude looks the language up in its registry. If it finds a pack, it
   acquires, verifies and activates it.
4. Otherwise, if Aeron is configured and the policy allows it, an authoring job
   (researcher category) finds the compiler or checker command line and its
   output format, any language server and any tree-sitter grammar. It drafts a
   pack with a fixture.
5. Crude verifies the pack mechanically. The grammar parses the project's files
   below an error-node threshold, the recipe passes every oracle, and the
   server answers initialize and documentSymbol on a sample file.
6. The outcomes become memory evidence
   ([adaptive context](../reference/adaptive-context.md)). Validation results
   become episodes and lessons, so the existing consolidation loop improves
   later authoring and recipe choice. Insight adds no second learning system.
7. Packs are files. The user can edit them, keep them locally or contribute
   them to the Crude registry.

A grammar that needs an external scanner it does not have runs in partial mode
(tables plus a DFA lexer). The pack records this, and coverage shows it.

### Agents

- An insight tool family replaces the `lsp` tool. It offers symbol search,
  definition, references, diagnostics by scope with the baseline delta, the
  build unit and flags of a file, the project map, and read-by-symbol (one
  function body instead of the whole file).
- Freshness is push-based. An agent asks for results at a generation and wakes
  when they are current. It never polls.
- Post-edit notices report only what the edit introduced or fixed.
- Agents change policy, recipes and acquisitions through the control API, with
  the same previews and approvals as the user.

### Trust and safety

- Build truth and build observation run project code: Makefiles, build.rs,
  procedural macros, npm scripts. They need Full trust. In a Static-only
  workspace, parsers and servers run in non-executing modes (for example,
  rust-analyzer without build scripts and procedural macros).
- Fetching dependencies (network) and checking (offline) are separate steps.
  Fetching follows the autonomy policy.
- A Linux hardening phase runs analyzers in user, mount and network namespaces
  with a read-only view of the source and a writable cache.
- Every acquisition and execution goes into a journal with its source, digest,
  command and policy decision.

## Ecosystem coverage targets

| Ecosystem | Build model | Build-truth unit and output | Server | Acquired from |
|---|---|---|---|---|
| C/C++ (CMake, Meson, xmake, make, custom) | File API, introspect, xmake or observer | The project's compiler with `-fsyntax-only` per translation unit; SARIF where supported, otherwise gcc-style text | clangd with generated compile commands and `--query-driver` | LLVM releases (pinned digest) |
| Go | `go list -json`, go.work | `go vet -json` per package | gopls | Built with the project's Go |
| Rust | `cargo metadata` | `cargo check --message-format=json` in a Crude target directory | rust-analyzer | rustup component or release |
| TypeScript/JS | tsconfig graph, workspaces | Incremental `tsc --noEmit` with build info in the cache; eslint JSON under Strict | typescript-language-server | npm with an acquired Node |
| Python | pyproject, lock files, interpreter and venv discovery | ruff JSON; basedpyright or pyright JSON | basedpyright or pyright | uv |
| Nim | nimble, nim.cfg | `nim check` through a recipe parser | nimlangserver | Official releases |
| Zig | build.zig | `zig ast-check` per file; `zig build` for semantics | zls matched to the Zig version | ziglang.org (minisign) |
| Unknown | Observer | Authored recipe | Authored, if a server exists | Requires approval |

A separate Crude target directory for cargo avoids contention with the user's
build lock, at the cost of disk space. The toolchain page shows that cost.

## Phases

Each phase ships user-visible value, deletes what it replaces, updates the
references, and passes the [validation](../guides/validation.md) gates with
real-product captures. Gates below are targets; Phase 0 and the first landing
set the actual ceilings from repeated measurements.

0. **Evidence.**
   - Compare gotreesitter with C tree-sitter: corpus correctness for C, C++,
     Go, Rust, TS/TSX, JS, Python, Bash, Lua, Nim, Zig, JSON, YAML, TOML, Make
     and CMake; throughput, retained memory and incremental latency on this
     repository and a large C/C++ tree. Write a decision record.
   - Governor spike: run a heavy background index with and without
     containment. Measure Crude's frame p99 and PSI. Prove freeze and resume.
   - Observer spike: compare ptrace and shim observation with Bear output on
     make, xmake and script builds.
1. **Insight owner and zero-install intelligence.** Fact graph, workspace
   service, shell replication, TUI cutover away from `LSPMgr()`; static
   producers; project-wide syntax counts; local completion in the new popup;
   anchored hover and diagnostic details; by-name navigation. Gates:
   keystroke to painted popup p99 ≤ 16 ms on a dense file with a large index;
   no regression in the typing benchmarks; a warm open of a persisted index
   ≤ 200 ms.
2. **Resource contract.** The governor and OS containment for servers and
   jobs, and pressure freeze. Closes LSP acceptance item 1. Gate: under induced
   memory pressure and Bulk indexing, frame p99 stays within the existing
   typing gate, and Crude is never the process the OOM killer picks.
3. **Build truth.** Adapters, recipes, parsers, oracles, deduplication,
   baselines, the Problems project view and the agent insight tools. Gate per
   ecosystem: counts equal the tool's own command-line output on the corpus,
   and an injected error appears and then clears.
4. **Acquisition and server configuration.** The tool capability kind, the
   resolver, registries, verification, the hermetic store, and server settings
   from the build model. Closes LSP acceptance item 2. Gate: a clean machine
   opens the corpus and reaches semantic diagnostics and completion with zero
   user configuration; offline use degrades cleanly; collection frees disk.
5. **Observer.** Arbitrary build systems. Gate: compile units match Bear on
   make, xmake and script builds, and build-truth counts match `make` output.
6. **Policy and control.** Presets, rules, previews, **Why?** cards,
   `/insight` and export. Gates: hiding third-party warnings takes two
   actions; a preset change re-filters without running a tool; "new in my
   changes" stays correct across branch switches.
7. **Aeron in the editor.** FIM adapters, ghost text, next edit, explain, fix,
   fix all and the budget meter. Gates: no keystroke waits on Aeron;
   cancellation releases the request; spending matches the meter.
8. **Self-assembling languages.** Authoring jobs, fixtures, oracles, memory
   evidence and pack export. Gate: a language that is not in the registry is
   brought up end to end with no user configuration; a failed verification
   leaves a visible reason.
9. **Extended insight.** Strict-preset analyzers, dependency advisories, dead
   code, trend history, and optional SCIP import for precise cross-repository
   navigation.

Acceptance corpus: this repository (Go), `site/` (TypeScript), a Python project
with a virtual environment, a make-based C project, an xmake C++ project, a
Cargo workspace, a Nim project, a Zig project, and a large C/C++ tree for
pressure tests.

Documentation: as phases land, a new code-intelligence reference replaces the
language-server sections of [editor](../reference/editor.md) and
[settings](../reference/settings.md). The capabilities, interaction matrix,
surface catalog and performance baselines references change with them. The
AGENTS.md topic map gains one line. [LSP acceptance](lsp-acceptance.md) is
deleted when phases 2 and 4 close its items.

## Risks and open decisions

| Risk | Handling |
|---|---|
| gotreesitter is young and changes fast (v0.56 at the time of writing) | Phase 0 corpus gate, pinned versions, and a cgo fallback built with `zig cc` |
| Grammar licenses (GPL, MPL) vs Crude's MIT/Apache license | Ship permissive grammars; acquire the others at runtime as data |
| Registry maintenance | Automated mirroring at pinned commits with digest checks; Crude signs its own index |
| Running project builds | Trust gate, environment allowlist, namespaces on Linux |
| Observer fidelity on Windows | Shims plus MSBuild binary logs; document the gaps |
| Very large C++ translation units | Changed and open units first, then by include fan-in; per-unit cache keyed by content and dependency digests |
| FIM availability and cost | Chat fallback, local option, budget |
| 100,000 or more diagnostics | Aggregated, virtualized views; indexed view-time policy |

## Prior art and sources

- [gotreesitter](https://github.com/odvcencio/gotreesitter) and its
  [package documentation](https://pkg.go.dev/github.com/odvcencio/gotreesitter@v0.9.1):
  pure-Go tree-sitter runtime, grammar coverage, scanners, query engine and
  benchmarks.
- [go-enry](https://github.com/go-enry/go-enry): Linguist-based language
  detection in Go.
- [reviewdog/errorformat](https://github.com/reviewdog/errorformat): Vim
  errorformat in Go with predefined tool formats.
- [Bear](https://www.mankier.com/1/bear): compile database from intercepted
  execution, through preload or wrappers.
- [aqua checksum verification](https://dev.to/suzukishunsuke/checksum-verification-by-aqua-5038)
  and the [mason registry](https://github.com/mason-org/mason-registry):
  machine-readable tool registries.
- [Zed language configuration](https://zed.dev/docs/configuring-languages):
  automatic server downloads, and a
  [user report](https://github.com/zed-industries/zed/issues/12589) that they
  are unverified.
- [Zed edit prediction](https://zed.dev/docs/ai/edit-prediction): next-edit
  prediction with selectable providers.
- [JetBrains Full Line Code Completion](https://blog.jetbrains.com/blog/2024/04/04/full-line-code-completion-in-jetbrains-ides-all-you-need-to-know/)
  and [Mellum](https://blog.jetbrains.com/ai/2025/04/mellum-goes-open-source-a-purpose-built-llm-for-developers-now-on-hugging-face/):
  local and focused completion models.
- [DeepSeek FIM completion](https://api-docs.deepseek.com/guides/fim_completion):
  prefix and suffix completion endpoint.
- [stack-graphs](https://github.com/github/stack-graphs): precise name
  resolution framework, archived in September 2025. This plan does not depend
  on it.
- [ThinkRail](https://github.com/JetBrains/thinkrail): project knowledge kept as
  reviewable files beside the code. It provides no code intelligence of its
  own.
