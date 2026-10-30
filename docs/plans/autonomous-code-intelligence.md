# Autonomous Code Intelligence

> Status: IN PROGRESS - the initial native syntax corpus, canonical scheduling
> and connected-editor publication are implemented. Shared diagnostic ownership
> has passed integrated Linux validation; complete semantic ownership,
> autonomous assembly and complete workflow/device acceptance remain open.
> Existing editor and LSP behavior remains governed
> by [editor](../reference/editor.md) and [settings](../reference/settings.md).
> This plan replaces "install and configure a language server first" with
> intelligence Crude assembles, evaluates and governs under the user's policy.
> [LSP acceptance](lsp-acceptance.md) remains open until all four of its items
> have evidence; the phases below include them rather than presuming closure.
> [Human-workflow acceptance](ide-workflow-acceptance.md) defines the local-first
> project corpus, actual TUI development tasks, shared-core pair programming
> and later remote scale campaigns. Its
> [multi-device lab](ide-workflow-acceptance.md#reproducible-multi-device-lab)
> specifies independent VM clients, locality/identity checks and separate
> physical-device calibration. Those product gates have not yet run.

Implementation boundary: [native syntax](../reference/native-syntax.md)
documents the selected parser/query foundation, shared scheduling and initial
editor integration. Focused syntax tests and native captures do not establish
semantic product parity, provider assembly or multi-device acceptance.

The local [checkpointed core restart](../reference/core-restart.md) prerequisite
now owns exact-image staging, natural settlement and one-shot goal continuation.
Its isolated Linux TUI evidence is not a corpus, semantic-feature or device
campaign pass, and the pre-feature running core still needs its bootstrap.

The [shared diagnostic fact slice](../reference/editor.md#shared-diagnostic-facts)
now replaces the raw manager/replica/editor/agent diagnostic paths. Source,
wire-version, context, lifecycle, empty/unversioned/limited and Unicode regressions,
the repository suite, full race campaign with supplemental lifecycle-wake checks,
and real-gopls rendered journeys at 160x40 and 80x24 have passed on Linux.
Neutral Windows/Darwin cross-builds are not native-platform acceptance.
Complete build context, request/response semantic fact contracts, full governor,
assembly and the campaigns below remain open.

The [workspace Trust and admission foundation](../reference/settings.md#workspace-analysis-trust)
now implements a human-owned non-inheriting execution ceiling, native directory
binding, one core-wide LSP process governor and launch/revocation fences.
Its preference, lifecycle, shared-core and source-frozen full ordinary/race
regressions have passed on Linux, along with lint, docs, benchmark and transport
gates. Native rendered Trust grant/reset and real-gopls semantic journeys passed
at 160x40 and 80x24. Neutral Windows/Darwin cross-builds are not native-platform
acceptance. Complete project context, native CPU/RSS containment, pressure
management, additional work classes and autonomous assembly remain open.

## Problem

Crude has shared documents, asynchronous LSP lifecycle ownership, editor
completion/navigation/diagnostics, and session-gated agent probes. The semantic
experience is still limited by the installed server and the context supplied to
it. Current capabilities and safeguards are described in
[editor](../reference/editor.md); this is a proposal, not implementation truth.

| Symptom | Cause in the code today |
|---|---|
| No semantic completion, outline or navigation until a server is installed and running | `requestCompletion` in `tui/viewmodel/keyboard.go` needs a connected server. **Detect servers** does not install executables ([settings](../reference/settings.md)). Chroma already highlights many files without a server. |
| No coverage-qualified whole-project diagnostic counts | Current editor diagnostics are tied to the LSP manager's tracked documents; `ProjectInsights` in `types/lsp.go` is a server snapshot, not proof that every project unit was checked. |
| Background analysis can overwhelm the machine | Server memory and thread settings are hints, not limits (`tui/settings/language_servers.go`). Process-tree containment and pressure management remain open. |
| Project configuration needs manual work | Flags, targets, include paths, interpreters, dependencies and severity policies are not consistently derived from the actual build. |
| Semantic coverage is limited | `types/language_servers.go` contains twelve built-in server entries plus custom configuration; a syntax grammar alone does not provide semantics. |
| Severity tuning is per tool | Each tool has its own suppression syntax. Crude has one minimum-severity filter. |
| Popups do not feel like an IDE | Hover and diagnostic details open centered on the screen (`tui/viewmodel/lsp_intelligence.go`, `tui/viewmodel/lsp_handlers.go`). Completion has no documentation pane, no inline prediction and no signature help. |
| Aeron never helps manual editing | Aeron drafts commit messages and compaction, nothing in the editor. |
| Editor and agent coverage can differ | Agents get a session-gated `lsp` tool (`tools/code_intelligence.go`) with document probes. The editor and agent should share the same revision- and context-qualified answers. |

On one 20,000-line Python fixture Chroma document preparation cost 60–62 ms
and 35.5 MB of cumulative allocations, not retained memory or keystroke-to-screen
latency ([performance baselines](../reference/performance-baselines.md)).
The existing editor deliberately preserves document-wide lexical context and
provisional visible ink. A replacement must preserve those user-visible
contracts, not just win a parser microbenchmark.

## Thesis

JetBrains-level quality is a goal for **measured workflows and languages**, not
an automatic consequence of choosing a parser or adding more language servers.
It requires a correct project model, language-specific semantic engines,
cross-language relationships, reliable editing operations, and a responsive,
honest experience while analysis is incomplete.

- **Syntax:** prefer upstream C Tree-sitter, compiled and pinned with Crude's
  existing Zig/C toolchain, for incremental structure, highlighting and
  injections. It is not a type checker.
- **Semantics:** use the strongest practical language-specific engines and
  compilers for resolution, completion, inspections and transformations.
  LSP is one transport, not the semantic model itself. A provider's features,
  project coverage and limitations must be tested, not inferred from its name.
- **Project context:** derive and retain the actual modules, toolchains, targets,
  flags, dependencies and environment needed by those engines. Allow several
  configurations of the same file; never mistake a guessed command for build
  truth.
- **One experience:** a workspace insight service coordinates revision- and
  context-qualified facts for the editor, Problems, agents and control API.
  Tree-sitter, servers and compilers may each own their own internal trees and
  indexes; Insight owns the published answers and their provenance.
- **Autonomy, not false certainty:** within the user's acquisition and execution
  policy, discover, acquire and configure tools and evaluate them against
  independent fixtures. Passing a fixture is evidence for that fixture, not
  proof that a tool finds all errors.
- **Aeron is additive:** it may propose packs, recipes, policy and editor
  predictions. Deterministic functionality and trust decisions never depend on
  a model, and generated output does not gain execution authority.

### Assembly first, not per-language product modules

Autonomous integration is the foundation, not a late “unknown languages”
extension. Crude implements generic protocols, metadata importers, managed
installers, revision-safe views and evaluation primitives. Mature external
engines supply the expensive language semantics. The ecosystem table below is
a set of provider candidates and acceptance cases, not a mandate to write a
new intelligence implementation for every row.

```
 project + requested features
       → discover → resolve → stage → evaluate → activate
                                      ↑           │
                                      └─ bounded repair / invalidate
```

- Reuse pinned declarative metadata from registries and existing editor
  integrations, preserving source/license provenance. Extract only supported
  fields: do not execute an editor's Lua/plugin hooks as trusted configuration.
  Prefer the engine's own project importer, then a standard interface such as
  BSP where supported, then structured build artifacts and a tested connector.
  Avoid duplicating the engine's project model or semantic graph by default.
- Compile those inputs into an inspectable integration artifact: exact tools
  and compatibility constraints, authorized artifact sources, launch/settings,
  root/configuration resolution, required effects, capabilities, evidence and
  invalidation inputs. Settings and recipes cannot silently authorize a new
  executable or cross a human-owned authority boundary.
- If metadata cannot resolve a gap, optional Aeron research may propose a
  constrained recipe. Deterministic validation and the existing permission
  owner control staging and execution. Bound research/spend/repair attempts;
  remember failed candidates to avoid infinite installation loops. A working
  assembled integration does not require model inference during normal editing.
- Evaluate operational health, project/document integration and semantic
  correctness separately. Incremental-versus-fresh document results,
  cancellation/restart, current revision handling and process cleanup can be
  tested generically. Semantic fixtures still need independent expected
  outcomes. Never let a model certify its own recipe by generating both setup
  and authoritative expected answers.
- Activate feature by feature with exact version/context evidence, not a single
  “verified language” badge. Stage replacements without disturbing a working
  integration; invalidate on relevant changes and retain a previous candidate
  only while compatible. Report unsupported semantics rather than trying to
  invent a missing engine.
- Prove generality on held-out languages and projects through
  [workflow acceptance](ide-workflow-acceptance.md#held-out-autonomy-and-language-coverage):
  standard LSP-backed support must not need language-named product code.
  Nonstandard engines may need explicit connectors; record the engineering
  cost rather than hiding it behind a generic label.

## What the user sees

For a new project, including an offline machine without toolchains:

1. Bundled and quality-gated languages get local syntax highlighting,
   structure and labelled approximate search as soon as the first parse is
   ready. Unsupported files retain usable text and existing lexical coverage.
2. The footer and Problems show progress and per-language, per-configuration
   coverage: for example, **C++: syntax only**, **Go: indexing**, or **TypeScript:
   semantic; 2 of 3 projects checked**. Missing tools and failed, skipped,
   untrusted or stale units are visible; zero is never presented as a complete
   clean project when coverage is incomplete.
3. Crude discovers existing tools and project models. It may acquire pinned
   tools under the user's autonomy policy. If obtaining accurate configuration
   would execute project code, it asks for the applicable trust decision; the
   static-only experience remains usable.
4. Once the relevant engine and project model are ready, completion,
   navigation, diagnostics and supported refactorings use semantic results.
   The same qualified results are available to agents. Cached project facts
   speed reopening only when their complete input identity still matches.

Cold-start, index and interaction latency are measured targets, not promises
for arbitrary file sizes, projects, machines or networks. Phase 0 records
fixtures and baselines; no existing performance gate is loosened.

## Guarantees

1. **Useful without setup:** bundled languages get gated syntax features offline;
   unsupported or failed analysis never prevents editing. Coverage varies by
   language and must be shown.
2. **Autonomous depth within policy:** discover and configure reliable tools
   without needless setup; explain missing trust, authentication, toolchains,
   packages or unsupported configurations and offer a user override.
3. **Coverage before counts:** identify the source, checked units, selected
   build configuration, revision and freshness. A parser error is not a compiler
   error; a successful check is not necessarily a successful full build.
4. **Evidence, not proof:** untested, failed or partial producers cannot claim
   complete coverage. A passed fixture supports only its tested behavior.
5. **Interactive work stays responsive:** analysis never runs on Update/Paint.
   Bound work and apply platform-supported containment; report unenforceable
   limits or lost workers explicitly rather than promising the OS cannot OOM.
6. **Safe operations:** semantic edits require an exact reviewed preimage,
   precondition checks, transactional rollback, a safety snapshot and undo.
7. **Shared answers:** editor, Problems, agents and control APIs use the same
   authoritative facts for the same workspace, context and document revision.
8. **AI is additive and bounded:** mechanical features never depend on Aeron;
   prediction and authoring obey budget, privacy and approval rules.

## Architecture

```
 shared documents ─┐  file watcher ─┐  project model ─┐  toolchains ─┐
                   ▼                ▼               ▼              ▼
   ┌────────────────────────────────────────────────────────────────────┐
   │ Insight service (per workspace, in the core): the only writer      │
   │                                                                    │
   │ producers: syntax ▸ language engines ▸ build/check ▸ analyzers     │
   │      │ facts + provenance + revision + project context             │
   │      ▼                                                             │
   │ fact/index store (incremental, persisted where useful)             │
   │      │                                                             │
   │ policy at view time: preset ▸ rules ▸ baseline ▸ dedupe            │
   └──────┬─────────────────────────────────────────────────────────────┘
          │ versioned snapshots and deltas (core protocol)
   ┌──────┼──────────────┬───────────────┬──────────────┬──────────────┐
 editor  completion   Problems, footer   agent tools   control API
          popup        and file tree

 Governor (one per core): admission and platform-supported containment;
 Aeron is an optional prediction/authoring consumer, never semantic authority
```

Ownership:

- A new presentation-neutral insight package owns fact schemas, indexes,
  storage, recipe descriptions and policy algebra. It imports neither workspace
  nor TUI. A workspace service owns project lifetimes, producer admission,
  document revisions, watchers, cancellation and shutdown joins
  ([async lifecycle](../reference/async-lifecycle.md)).
- The core's shared document service remains the canonical text, undo and save
  owner. The insight service consumes its ordered document revisions and disk
  changes; it does not create a second mutable text authority.
- For a matching canonical revision, parse once per document/grammar and share
  the syntax tree across syntax consumers. Language servers and compilers keep
  their own semantic representations. If a remote shell has unacknowledged
  optimistic edits, it may use an explicitly provisional local syntax
  projection for responsive paint. It is revision/owner-fenced, never
  published as canonical facts, and cannot authorize semantic navigation or
  edits. Reconcile or discard it when the canonical revision arrives.
- Extend the existing LSP manager as an Insight producer and lifecycle owner
  instead of starting a second server fleet. Cut each migrated consumer over
  to the same neutral Insight views through the shell port and core protocol;
  delete its replaced direct diagnostic, insight and completion path. A
  migration is not complete while two authorities can disagree.
- The governor lives in the core, one per user, across workspaces. A server's
  own project index is admitted and accounted for rather than prohibited or
  duplicated by default.

### Fact graph

- Fact kinds include document, build unit, toolchain, declaration, scope,
  import, reference, diagnostic, action, metric and coverage. A result records
  producer/version, recipe and grammar/query identities, workspace and
  document revision, relevant source/dependency digests, build configuration,
  target, toolchain, a privacy-safe environment identity and
  provenance/precision. Never persist credentials or raw sensitive environment
  values. If a relevant input cannot be identified safely, do not reuse the
  dependent fact as current.
- Content digest alone is not a semantic cache key. Reuse across revisions or
  branches only when every relevant input is known and matches. An unknown
  dependency or configuration invalidates the dependent fact; a changed
  toolchain, build flag, target, generated input or producer also fences it.
- Keep a compact syntax definition/identifier index for search and candidate
  discovery. A matching name is not a resolved reference. Prefer semantic
  symbol IDs and the provider's index for exact references; import precise
  indexes when their provenance and snapshot are compatible. Measure index and
  tree memory on large projects instead of assuming it is definition-sized.
- Invalidation follows the best available build graph, including C/C++
  includes, Go packages, TypeScript project references and Rust crate/feature
  graphs. Incomplete graphs are labelled incomplete. Open and changed units
  receive interactive priority without suppressing legitimate server indexing.
- Each fact has a freshness state: current; stale (an input changed and a
  recompute is queued); blocked (tool, trust or budget missing, with the
  reason); or failed (with the tool's output). Stale facts stay visible and
  dimmed. Nothing is cleared to make the view look clean.
- Disk-based facts identify the disk snapshot they describe. Mapping through
  unsaved edits may preserve provisional presentation only; it does not make
  an old semantic fact current. Hover, navigation, agent answers and edits
  require a valid current revision and context, or clearly report stale/partial
  results without acting on them.
- Deduplicate diagnostics only when source identity, configuration, normalized
  code and location establish equivalence. Preserve distinct findings and
  producer provenance; do not hide a real diagnostic merely because ranges
  overlap. Never merge different target configurations into one unqualified
  count.
- Storage is per workspace under `CRUDE_HOME` (the existing pure-Go SQLite),
  with in-memory query structures in the core. Shells receive per-client
  versioned snapshots and deltas through the
  [core client protocol](../reference/core-client-protocol.md). A read-only,
  bounded mirror may supply already-known structural completion candidates
  without a round trip; new semantic requests still have provider and
  transport latency and must be revision-fenced.

### Producers

**Syntax and structural features: built in for supported languages.**

- Language identity comes from Linguist data through go-enry (extension,
  filename, shebang, modeline and content heuristics).
- Prefer the [upstream C Tree-sitter runtime](https://tree-sitter.github.io/)
  and official Go binding, with individually pinned grammars, scanners and
  highlight/locals/tags/fold/injection queries. Crude already ships a native
  Zig/C dependency; avoiding cgo is not a goal. Pin compatible runtime/grammar
  ABI versions and validate licensing, redistribution notices, native builds,
  explicit C resource lifetimes and memory.
- Extract syntax structure, error/missing nodes, highlighting, folding,
  selection, outline and breadcrumbs. Queries and scope resolution need
  language-specific implementation and tests; a parse tree is not a semantic
  model and syntax errors are not authoritative compiler diagnostics.
- Merge semantic tokens over syntax colours only for the exact matching
  revision. Preserve one theme mapping, document-wide context, provisional
  ink and grapheme/tab projection across editor and comparison surfaces.
  Inspect injections separately; HTML/JS/CSS, TSX and fenced Markdown require
  queries, child languages and correct source mapping, not a generic flag.
- Incremental tree reuse does not by itself make highlight queries or range
  projection incremental. Any restricted re-query must handle ancestor
  context, overlapping captures and injections. Regression tests must compare
  its result against a fresh whole-document query after each test edit;
  production must not perform that full verification on every keystroke.
  Measure the whole path.
- Structural/local completion and by-name discovery are labelled approximate.
  A plausible member inferred from syntax is a candidate, never proof of
  receiver type or a safe import/rename. The semantic provider replaces it
  when an exact result is available.
- Keep Chroma as an intentional lexical provider for files not yet covered by
  a quality-gated grammar/query, and where comparison, chat diff and Glamour
  code-block rendering still depend on it. Migrate each consumer separately
  only when coverage and real rendered behavior are equivalent or better;
  switching the editor alone does not remove the dependency.

**Semantic engines.** Use mature language servers or supported native
language-specific interfaces, configured from the project model. They provide
type-aware completion, navigation, semantic tokens, diagnostics and available
refactorings. Some, including clangd, need their own whole-project background
index. Admit and govern that work, reuse it through the provider when possible,
and show its coverage; do not force an open-files-only policy or build a
duplicate semantic index without evidence.

**Project checks and builds.** Distinguish syntax, compiler/type-check,
analyzer and actual build results. A compiler's syntax-only mode, `go vet`,
`cargo check` or `tsc --noEmit` may miss diagnostics from a full build. Label
each by actual command, target, configuration, revision and checked scope;
reserve “build result” for the relevant completed build. Project execution
requires the appropriate trust.

**Extended analyzers.** The strictness preset enables them: clang-tidy,
staticcheck, clippy, eslint, ruff rule sets, and dependency advisories through
osv-scanner.

**Aeron.** Prediction and authoring only. Optional AI review hints are
labelled, opt-in, and never counted as errors or warnings.

Suggestions in the counts are info and hint diagnostics plus findings that
have an available fix.

### Project and build model

The relevant build context is part of the answer. Prefer existing project
artifacts and non-executing reads, then obtain additional context only as
permitted by trust. A command named “metadata”, “dry run” or “check” can still
run project code or plugins; classify its actual effects before admission.

- Prefer engine-owned import and standard interfaces. Metadata recipes may
  consume `go list -json`, Cargo metadata, TypeScript project references,
  Python environment/configuration discovery, compilation databases and the
  CMake File API where needed. Add a product connector only when the generic
  artifact/protocol path cannot express the integration, including for Meson,
  xmake, Nim, Zig, Bazel and MSBuild; record and test that exception explicitly.
- Keep project, module, target, platform, dependency and toolchain identities
  distinct. Retain provenance for inferred versus observed flags, paths,
  generated sources and environment. Let the user inspect, select and override
  configurations without rewriting project files implicitly.
- A build observer is a later, trusted-workspace producer for make, scripts
  and unsupported build systems. Evaluate Linux child tracing and platform
  shims against independent build outputs; either can miss processes or
  arguments. `make -n` can execute `$(shell ...)`, and compiler driver probing
  executes the driver: both need explicit trust. Report incomplete observation
  rather than claiming an exhaustive build.
- A new file or header may borrow a nearby command, but mark it **inferred**.
  Different targets or incompatible commands remain selectable; do not silently
  turn a guessed command into a verified compile unit.
- Store generated compilation databases under `CRUDE_HOME`, not in the project
  unless the user exports them. Give clangd only explicitly trusted
  `--query-driver` paths. The model also feeds project checks, test discovery,
  run configurations and the agent project map.
- A model generation incorporates observed build inputs, toolchain versions,
  environment, target and dependency identities. Changes invalidate affected
  facts; previous results may remain visible as stale, never current.

### Recipes and language packs

Pack metadata and recipes are inspectable and shareable. They can describe or
reference executable artifacts, which require separate trust and authorization.

- **Recipe:** applicability, exact producer/tool version, command and unit
  (file, translation unit, package or project), supported configurations,
  output format, severity mapping, cost, trust/effects, environment needs,
  timeout and test evidence. It states what the command does *not* check.
- **Language pack:** detection, pinned grammar and scanner, compatible queries,
  injections and source maps, semantic providers and capabilities, project
  adapters, recipes, snippets and independently expected fixture results.
  Capabilities are declared per feature and tested; an available grammar or
  an LSP initialize response does not certify semantic quality.
- Sources include built-in and pinned registry packs, explicit workspace
  overrides and, when enabled, Aeron-authored proposals. An authored or
  workspace-supplied pack is not automatically trusted, executable or allowed
  to override a more trusted pack. Apply existing policy, exact source/digest,
  approval, scope and explicit denials before activation.
- Output parsers: SARIF; native JSON (cargo, `go vet -json`, ruff,
  pyright and basedpyright, eslint, mypy); reviewdog/errorformat (pure Go, 49
  predefined tool formats); and the generic `file:line:col: severity: message`
  form.

**Evaluation.** Establish evidence for each pack, tool version, platform and
configuration. Show results together with coverage and the evaluation scope;
passing these checks does not prove completeness for unseen projects.

1. **Exit status:** an unexpected non-zero exit without parsed diagnostics is
   a failure or incomplete analysis, not a clean project. Preserve legitimate
   non-diagnostic tool errors.
2. **Unparsed output:** an output line that looks like a diagnostic but did not
   parse fails the recipe.
3. **Location:** validate any supplied file and range against the appropriate
   revision and authorized scope. Preserve legitimate project-level or
   no-location diagnostics as such; malformed or inaccessible locations never
   become fabricated source locations.
4. **Fixtures:** independent, versioned examples exercise expected and absent
   diagnostics, symbols, completion and edits on the supported versions.
   Record evidence for the exact tool and configuration; do not assume one
   fixture validates every project.
5. **Error injection**, where safe and supported: compare an unchanged input
   with a temporary copy containing a known error. Do not mutate the project
   or execute an untrusted build just to validate a recipe. A failed or
   impossible probe is visible as unverified/partial coverage.

### Native grammars and supply chain

- A parser, generated grammar and external scanner are separately pinned and
  audited. Select bundled grammars individually, include their applicable
  license and NOTICE material in release artifacts, and check every supported
  native build. A query's existence or compilation does not establish useful
  colour coverage. Keep unsupported or substandard languages on Chroma where
  it supports them, otherwise show plain text until an accepted provider exists.
- Native Tree-sitter grammars and external scanners contain executable code.
  Downloading a shared library is **not** acquiring inert data and does not
  automatically resolve license obligations. Optional native grammar loading
  requires a separately designed authenticated acquisition, explicit policy,
  ABI/platform compatibility and crash/security isolation boundary; otherwise
  ship only reviewed built-ins and report the language unavailable. Pure query
  files still need bounded parsing/execution and provenance.
- Manage C parsers, trees, cursors and queries with explicit owners, release,
  cancellation and bounded work. Never keep invalid C or Go pointers across
  calls or use a mutable parser concurrently. Account for C heap/RSS as well
  as Go allocations; Go's race detector alone cannot validate C code. Failure
  or a pathological parse must not wedge Update or hide unavailable coverage.

### Autonomous acquisition

Tools become a new capability kind in the existing capability system
([capabilities](../reference/capabilities.md)). They use the same Manual, Auto
Review and YOLO policy, the same immutable source and digest preimage for
approval, and the same registration publication. Crude gets no second
installer.

- The resolver turns a need ("C++ semantic diagnostics", "Nim language
  server") into candidates, in this order:
  1. A tool that is already installed and matches: on PATH, or pinned by the
     project (rust-toolchain.toml, .nvmrc, .node-version, .python-version, the
     go.mod toolchain line, .tool-versions, mise.toml).
  2. The Crude registry: curated and pinned, with SHA-256 digests and
     signatures or attestations.
  3. Supported mirrored registries at pinned commits, for example aqua and
     mason. Verify the actual artifact and available checksum/signature or
     attestation for each entry; registry presence alone is not proof.
  4. Ecosystem installers inside managed prefixes: uv for PyPI tools (uv also
     provides Python), npm with an acquired Node, `go install` with a matching
     Go, rustup components. A prefix by itself is not a sandbox.
  5. Sources that Aeron found by research. Evaluate their exact source, bytes,
     effects and trust under the same policy; model output itself grants no
     authority.
- Each tool installs once under `CRUDE_HOME`, in a directory named by tool,
  version and digest. The directory is read-only after install and shared by
  all workspaces. Packs hold references to it. An LRU collector keeps the
  store under a disk quota and keeps pinned tools.
- Compiler-sensitive tools follow the project where supported: a compatible
  gopls/Go pair, rust-analyzer and the selected Rust toolchain, zls matched to
  Zig, and a clangd/toolchain pairing tested against the project. Version
  compatibility must be evaluated rather than assumed from ordering.
- After install, a smoke check confirms the tool's version output.
- Under Manual, Auto Review and YOLO, use the existing policy, exact
  acquisition/launch review, explicit denials, source trust and effect/host
  ceilings. Reviewer or YOLO authorization is not a human grant. A digest
  checks artifact identity, not trust or permission. Show the selected tool's
  version, size, source, digest, license, effects and status; authentication
  and consent remain user-owned.
- Offline, already installed tools and cached project data remain usable when
  their required inputs are available and current. Otherwise show syntax-only,
  stale or unavailable coverage as appropriate; resume authorized acquisition
  when the network returns.

### Resource contract

- One governor per core admits language servers, their indexing, checkers,
  indexers, observer, acquisition and Aeron work across all workspaces. Add
  process-tree teardown and admission before enabling heavier project-wide
  work, not as a later optional hardening pass.
- Classes, highest first:
  - **Keystroke:** asynchronous incremental parse and local completion with
    bounded, cancellable work. Mutation and painting never wait for parsing.
  - **Interactive:** server requests for the focused document.
  - **Background:** indexing and checks of changed units.
  - **Bulk:** first full index, full-project checks and observation builds.
    Bulk runs only while the user is idle or when the user asks for it.
- Containment candidates, to validate natively:
  - **Linux:** cgroup/systemd user scopes for accounting and applicable CPU,
    memory and I/O controls; owned process groups and OS limits where supported.
  - **Windows:** Job Objects for owned process-tree lifetime and supported
    memory/CPU controls; investigate safe pause/resume and descendants.
  - **macOS:** process groups, priority and supported resource controls; do not
    claim cgroup-equivalent enforcement without evidence.
- Evaluate platform pressure signals, pause/resume with hysteresis and process
  tree cleanup with actual workloads. Nice/priority is not a hard memory limit;
  an in-process parser shares the core's address space, so cgroups cannot
  isolate it from the core. If a capability cannot be enforced, show the actual
  limit and reason, reduce or refuse background work, or isolate it in an owned
  process. A stopped, failed or incomplete job leaves coverage visibly stale.
- Typing bursts demote Bulk work; idle time promotes it.
- Defaults come from the machine (for example, Background work uses at most a
  quarter of the cores, and all analysis together uses at most a fixed share of
  RAM). There is one place to change them. The Problems header shows progress,
  pauses and their reasons.
- Define and test a project-analysis environment policy that preserves required
  toolchain variables while withholding Crude/provider secrets; disclose any
  unavoidable limitations. This is a new analyzer-process contract, not a
  change to the existing MCP environment contract.

### Policy and control

The user has three choices and one verb.

| Choice | Values | Effect |
|---|---|---|
| Trust (per workspace) | Static only, project execution allowed | Project execution, network, acquisitions and external tools still obey their own effect, source and approval policy |
| Strictness | Project checks, Strict, Errors only | Which producers count, and at what severity; a check is labelled by what it actually ran |
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
  and "since last session" only for comparable producer, toolchain,
  configuration and checked scope. Differences involving dirty buffers or
  incomplete coverage are qualified. Agent post-edit notices use the same
  delta and never turn missing analysis into a resolved issue.
- Optional export to tool configuration (.clangd, .clang-tidy, ruff settings)
  helps teammates who do not use Crude. Crude itself never needs these files.

### Editor experience

- The completion popup is anchored at the cursor. It has kind glyphs, match
  highlighting, a detail column, a documentation pane that loads on demand,
  import edits from semantic providers and snippets with tab stops.
- Local results arrive without waiting for the server; semantic items merge
  without moving the selected row or applying an old-revision result. Measure
  mutation-to-painted-frame and provider-response latency separately. Fuzzy
  filtering runs in the client with the fzf matcher Crude already vendors.
- Signature help opens on `(` and `,`. Hover and diagnostic details anchor at
  the cursor or pointer, never centered. Semantic highlighting, inlay hints,
  type/implementation navigation and call/type hierarchy appear when the
  provider advertises and passes tests for them. End-of-line messages are
  optional. A quick-fix marker offers supported provider, recipe and Aeron
  fixes with their provenance.
- Expand capability negotiation, dynamic registration and resolve-on-demand:
  preserve server completion identity, documentation, snippets, text edits and
  additional edits. Respect the negotiated LSP position encoding, convert to
  source bytes exactly once, then project bytes to Crude's grapheme/tab cell
  geometry. Invalid, overlapping, out-of-range or stale edits fail closed.
- Rename, code actions, extract/inline, organize imports and multi-file edits
  use a shared workspace edit transaction. Preview the exact changes and file
  operations; check all canonical document revisions, disk preimages, paths,
  file types and permissions, capture a Local History safety snapshot, then
  commit atomically or roll back. Dirty/pending buffers and peer edits must
  either be safely incorporated by the canonical document owner or reject the
  entire operation with a reason; never silently overwrite them. Cancel writes
  nothing. Commands requested by an action require separate authorization.
  See [Local History](../reference/local-history.md).
- Ghost text is dimmed inline text (a theme token). Tab accepts it, Ctrl+Right
  accepts one word and Esc dismisses it. If the user types the suggested text,
  the rest stays visible without a new request.
- Next-edit prediction shows a gutter marker. Tab jumps to it and a second Tab
  applies it.
- Definition, references and workspace symbols work by name from the index
  without a server when a structural index supports them (labelled
  approximate); provider-backed results retain their actual precision and
  coverage. Approximate search never authorizes rename.
- Tab still indents when nothing is offered. The keys go into the
  [interaction matrix](../reference/interaction-matrix.md) in the same change.

### Cross-language intelligence

Supporting many languages independently is not the same as understanding the
boundaries between them. Each bridge is a tested, versioned producer with source
maps, project/configuration identity, provenance and a capability declaration.
Start with:

- HTML/JS/CSS, TSX and Markdown fenced code: syntax highlighting and
  structural navigation through injected regions, including malformed and
  edited hosts. Semantic navigation across a boundary requires an engine or
  bridge that actually resolves it.
- SQL embedded in a recognized API, optionally using a user-selected schema.
  Schema access requires its own network/credential consent; analysing text
  must not execute SQL. Unavailable or stale schemas are shown as such.
- Go/C through cgo and framework/template/configuration links only where
  toolchain and framework-specific evidence establish the relationship.

Never equate same-named strings with symbol identity. Cross-language reference
results identify whether they are resolved or candidates. Cross-language
rename requires every relevant bridge to establish its coverage and produce
safe, revision-checked edits; incomplete or unknown coverage refuses the
operation with a reason rather than silently applying a partial rename.

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

Known and unknown languages follow the same assembly procedure above, not two
engines. Keep lexical/plain-text support usable while discovery runs; absent
analysis never counts as zero errors. A pack or native component still obeys
its acquisition, execution and licensing boundaries. A successful initialize
call or low parse-error count cannot certify its features.

Record exact evaluation outcomes as evidence through the existing
[adaptive-context and memory contracts](../reference/adaptive-context.md), not
a second learning authority. Users may inspect/edit local packs and explicitly
export sanitized recipes; sharing source, machine-specific paths or secrets
is never automatic.

A grammar requiring an unavailable external scanner is unsupported until that
scanner is supplied and validated. Do not assume a partial DFA mode is
available or safe. Optional native loading follows the supply-chain contract
above.

### Agents

- An insight tool family replaces the `lsp` tool. It offers symbol search,
  definition, references, diagnostics by scope with the baseline delta, the
  build unit and flags of a file, the project map, and read-by-symbol (one
  function body instead of the whole file).
- Results identify workspace, document revision, configuration, provider,
  precision and coverage, including unsaved buffers and unavailable or stale
  data. An agent can ask for a generation and receive a bounded push-based
  completion; cancellation, missing trust and unreachable freshness settle
  explicitly rather than polling or waiting forever.
- Post-edit notices report only what the edit introduced or fixed.
- Agents change policy, recipes and acquisitions through the control API, with
  the same previews and approvals as the user. Exact refactoring edits follow
  the same transaction and authority checks as editor actions.

### Trust and safety

- Builds, observation, project configuration, compiler driver queries,
  plugins, Makefiles, build.rs, procedural macros and npm scripts can run
  project code. Each operation needs the appropriate explicit trust and
  policy decision. Static-only mode runs only reviewed built-in parsing and
  providers with a verified non-executing configuration; a server whose
  behavior cannot be restricted stays off with a visible reason.
- Fetching dependencies (network) and checking (offline) are separate steps.
  Fetching follows the autonomy policy.
- Investigate Linux namespaces and other platform isolation with explicit
  capability and failure reporting; a process group or environment allowlist
  alone is not a security sandbox.
- Every acquisition and execution goes into a journal with its source, digest,
  command and policy decision.

## Ecosystem coverage targets

These are candidate providers and commands, not a claim of current support or
complete parity. Select and pin actual versions after per-language acceptance;
each row must report checked scope, limitations and configuration.

| Ecosystem | Project model | Primary semantic candidate | Checks and explicit limits |
|---|---|---|---|
| Go | go.work, modules, tags, target and Go toolchain | gopls | gopls diagnostics and `go vet` analysis; an actual project build/test is a separate, trusted result |
| C/C++ | compilation database, compiler, target, includes and macros; CMake/Meson/xmake or trusted observation | clangd, including its governed background index | compiler syntax-only checks are not a full compile/link; surface inferred commands and unsupported flags |
| TypeScript/JS | project references, package workspaces, tsconfig and project TypeScript version | TypeScript language service through an LSP adapter | `tsc --noEmit` is a type check, not a full project build; eslint is separate |
| Rust | Cargo workspace, crate graph, features, targets and toolchain | rust-analyzer | `cargo check` is not a full build; build scripts and procedural macros need trust |
| Python | interpreter, virtual environment, project config and dependencies | Pyright or basedpyright, selected against fixtures | type checks and ruff rules are separate from running tests or building the project |
| Zig | build.zig, target and matching toolchain | zls matched to Zig | `zig ast-check` is syntax-only; a separately authorized `zig build` has broader scope |
| Nim | nimble, configuration and toolchain | nimlangserver, subject to quality tests | `nim check` is labelled as a check; grammar/scanner licensing is audited separately |
| Java/Kotlin | JDK, Maven/Gradle/Bazel, modules and generated sources | JDT LS for Java; evaluate Kotlin LSP and optional JetBrains Java/Kotlin provider | Provider licensing, platform, supported editor/CLI and agent-use terms need verification; never bundle or invoke a proprietary provider without authorization |
| Unknown | discovered or user-supplied model | trusted provider if available | explicit syntax/semantic coverage and approvals; no automatic claim of full support |

A separate Cargo target directory may avoid contention with the user's build
lock, at the cost of disk space and possible configuration differences. Test
both and show the cost and effective configuration.

## Phases

Each phase ships a coherent user-visible slice and its agent journey, updates
the corresponding current references, and removes the path it actually
replaces. A phase is not accepted because its dependencies compile or a mock
passes. Follow the full [validation](../guides/validation.md) gates as changes
land, including native-platform and real-product visual acceptance. Implement
and debug the complete required local workflow before remote deployment;
apply G0–G4 from [human-workflow acceptance](ide-workflow-acceptance.md#promotion-gates-and-local-regression-cadence).

0. **Evidence and contracts.**
   - Pin candidate C runtime, grammar, scanner and query versions. Compare
     native Tree-sitter with current Chroma and, where informative,
     gotreesitter on correctness, rendered colours, cold preparation,
     incremental edit-to-highlight, C/Go allocations, retained memory/RSS
     and release binary size. Cover C, C++, Go, Rust, TS/TSX, JS, Python,
     Bash, Lua, Nim, Zig, JSON, YAML, TOML, Make and CMake as each pack is
     proposed; do not claim untested languages.
   - Include independent expected outcomes for the generic TypeScript async
     arrow, Crystal keyword/comment/string colours, long Python docstrings,
     offscreen delimiter changes, malformed input, injection boundaries,
     CRLF/Unicode and edits/undo. Check platform and grammar license
     manifests. Record exact pins and a decision; the exploratory evidence
     below is not the acceptance corpus.
   - Establish empty, dense and large-project baselines: mutation to painted
     frame, provider response, index coverage, memory and CPU, before/after
     setup and under background load. The p99 16 ms interactive presentation
     target and 200 ms warm-index-open target need defined fixtures and
     repeated measurements; they are not blanket promises or permission to
     raise an existing ceiling.
   - Spike process-tree containment, cancellation and pressure response
     independently on supported platforms. Evaluate build observation
     against real builds and Bear before selecting an implementation.
   - Admit pinned small/medium task fixtures and independent expected outcomes;
     establish the real-TUI driver, observer and audit boundaries. No server
     workload is needed to make these local failures reproducible.
   - Preflight the QEMU/KVM device lab and pinned guest-image sources; record
     missing acceleration, image licenses and isolation failures. Credential
     bootstrap/read-only inventory may happen under explicit authorization,
     but does not authorize early server deployment or count as acceptance.
1. **Generic assembly, shared syntax and safe runtime foundation.**
   - Implement the neutral Insight contracts, workspace lifecycle and
     replicated views; reuse the existing shared-document authority and LSP
     process ownership. Introduce native syntax for a small accepted grammar
     set and preserve Chroma for remaining consumers/languages.
   - Build the shared discover/resolve/stage/evaluate/activate path and bounded
     recipe authoring/repair, with policy-aware acquisition, metadata import,
     capability evidence and invalidation. Use current engines and project
     importers before adding a connector. Do not postpone assembly until the
     end of the language rollout.
   - Before enabling heavier indexing, add bounded admission, exact owned
     process-tree stop/reap and platform-supported resource reporting.
     Unsupported containment must be visible and prevent work that requires
     it. Exercise two workspaces, cancellation, shutdown and pressure; do not
     claim that any platform guarantees the core cannot be OOM-killed.
   - Gate live editor paint and agent/shell revision agreement through edits,
     optimistic pending text, undo, peer updates, theme/width changes,
     offscreen delimiters, tab close, workspace switch, recovery and restart.
     No parsing or native I/O runs on Update/Paint.
2. **Local semantic and developer-workflow acceptance.**
   - Start with Go, C/C++ and TypeScript/JS as test cases for the same generic
     assembly path, then held-out cases. Ship each accepted configuration:
     project/configuration discovery, policy-aware acquisition or installed-tool
     use, real semantic provider and its
     governed index, syntax and semantic highlighting, completion, outline,
     hover, diagnostics, definition/references, and safe rename. Show
     capability and coverage when a provider cannot offer a feature.
   - Agents perform feature, bug-fix, refactoring and profiling tasks through
     actual rendered TUI entry points on local small/medium projects. Record
     fallback/tool bypasses separately from IDE feature passes. Include
     two- and three-client same-core editing, not isolated agent worktrees.
   - Pass the local device rows D0-D6 and D8 using three independent guest OS
     clients and a separate core guest, with distinct state/credentials,
     decoy-path locality tests and one-client-only faults. Process-only
     multi-client tests remain useful but cannot substitute for these rows.
   - Exercise editor **and** agent entry points on real multi-file projects:
     imports/dependencies, unsaved and peer edits, two configurations of the
     same file, build-model changes, server failure/restart, offline or denied
     acquisition, and stale async results. Compare locations and expected
     diagnostics with independent source/compiler/server oracles; test
     negative results as well as positive ones.
   - Migrate the relevant direct LSP presentation paths to Insight as they
     land. Validate all four [LSP acceptance](lsp-acceptance.md) items:
     process-tree containment, real installed-server journeys, exactly-once
     diagnostic position conversion across connected shells, and desktop/
     constrained captures of Code Intelligence settings and language-server
     list/detail pages. Retain that plan until all are met.
3. **Rich editor operations and broader assembly cases.**
   - Expand capability-negotiated semantic-token full/range/delta support,
     signature help, inlay hints, code actions, snippets/additional completion
     edits, type/implementation navigation and hierarchy where the provider
     supports them. The shared transaction handles preview, current unsaved
     documents, file operations, Local History snapshot, rollback and undo;
     unsupported operations fail closed.
   - Validate assembled Rust, Python, Zig and optional Nim configurations
     through real projects and independent fixtures; these are support/evidence
     artifacts, not new semantic implementations in Crude. Evaluate Java/Kotlin
     providers and licensing before accepting those packs.
   - Deliver supported run/test/debug entry points and profiling workflows
     needed by the required task matrix. A generic DAP client may integrate
     existing adapters. Terminal debugging alone cannot pass an integrated
     debugger checkpoint. Keep required capability gaps visible and local.
4. **Cross-language bridges and project coverage.**
   - Deliver tested TSX/HTML/JS/CSS and Markdown injection mappings; add SQL
     and Go/C only where providers and project context substantiate their
     relationships. Test both positive and misleading same-name cases, edits
     across boundaries and incomplete coverage.
   - Add scoped project checks and Problems coverage, per-target baselines,
     source-aware deduplication and **Why?** provenance. Compare each
     check's displayed output with that exact command on the corpus; separately
     label syntax, analysis, compiler checks and actual completed builds.
   - Deliver observation for supported trusted builds only after its
     platform-specific fidelity and execution policy have been tested; never
     interpret an unobserved command as a successful or complete build.
5. **Policy and control.** Add presets, reversible rule previews, `/insight`
   and explicit configuration export. A policy change re-filters without
   running a tool; incomplete and non-comparable baselines stay labelled.
6. **Aeron in the editor.** Add FIM adapters, ghost text, next edit, explain,
   fix, fix all and a budget meter. No keystroke waits on Aeron; cancellation,
   privacy/consent, exact edit preconditions and spending are tested.
7. **Remote data gathering, scale and extended insight.** Only after the
   complete required local workflow passes, stage the exact verified candidate
   on an authorized private remote core, replay medium projects, then progress
   through VS Code, LLVM and Chromium as capacity permits. Keep local regression
   sentinels on every improvement and rerun the original remote reproduction.
   Apply D7 with the same independent client guests over the WAN; validate the
   actual server-side home/profile rather than assuming local CRUDE_HOME is
   forwarded. Mixed-OS VM and physically separate-client acceptance retain
   their own evidence. Key login is established, not deployment acceptance;
   the expanded server storage is verified at about 255 GiB free but still
   needs scoped source/index/build/evidence admission for Chromium.
   Add strict analyzers, advisories, trend history and optional SCIP imports
   with verified identity and coverage. Remote collection is not a substitute
   for missing local IDE features or an excuse to lower a gate.

### Acceptance corpus and gates

- The authoritative project/task selection and progression are in
  [human-workflow acceptance](ide-workflow-acceptance.md#staged-project-corpus).
  Keep small targeted fixtures for multi-module/project-reference workspaces,
  Python environments, Rust features, targets/generated headers, injections,
  denied trust, invalidation and transactional safety. Real development tasks
  supplement those fixtures, not replace them.
- The authoritative simulated-device topology and D0-D8 procedures are in
  [the device lab](ide-workflow-acceptance.md#reproducible-multi-device-lab).
  Keep VM, physical, native-platform and transport verdicts distinct.
  Passwords/private keys, real endpoints and credential-bearing VM artifacts
  stay outside Git; only sanitized test definitions and evidence indexes may
  be shared. No successful key bootstrap closes an IDE checkpoint.
- Each language has independent, hand-checked expected definitions,
  references, completion, diagnostics, quick fixes and rename preimages, plus
  negative cases for similarly named symbols, overloads, generics, macros,
  unsaved edits and malformed source. Compare to real providers, relevant
  compilers and, where licensed and available, the corresponding JetBrains
  workflows. Record differences; server output or a syntactic candidate is
  not itself the oracle for all semantics.
- Test source revisions, negotiated position encodings, graphemes/CRLF,
  generated/deleted files, project/target/toolchain changes, cache invalidation,
  offline and denied trust, server crashes, shutdown and restart, and
  transactional rollback after partial failure. Test agents and editor against
  the same revision and configuration.
- Benchmark the real connected user journey and bounded native-worker costs:
  cold/warm open, mutation, parse, query/projection, presented frame,
  completion response, project indexing, native RSS and teardown, with empty
  and dense content under concurrent analysis. Preserve existing allocation
  and latency gates; never convert a parser-only number into a typing claim.
  Race tests do not establish C memory safety; add native sanitizers where
  available and explicit lifecycle tests. Cross-compiles do not replace native
  Linux and Windows execution and captures.

Documentation: as phases land, a new code-intelligence reference replaces the
affected language-server sections of [editor](../reference/editor.md) and
[settings](../reference/settings.md). Update capabilities, interaction matrix,
surface catalog and performance baselines with the implementation. Update the
AGENTS.md topic map. Delete [LSP acceptance](lsp-acceptance.md) only when all
four items have passed and the current references describe the result.

## Risks and open decisions

| Risk | Handling |
|---|---|
| Native parser/grammar errors, ABI drift or C memory safety | Pin runtime, grammar, scanner and queries; independent corpus, platform, sanitizer and lifecycle gates; preserve lexical coverage when unavailable |
| Grammar/scanner licensing and executable code | Audit each shipped component and notice; a downloadable native library is executable, not automatically safe or license-free data |
| Semantic engine and project-model limitations | Per-feature coverage, exact configuration, fixture evidence and explicit partial/stale states; never equate syntax with semantics |
| LSP coverage and JetBrains-level behavior differ by language | Ship and evaluate vertical slices; optional proprietary provider requires license and agent-use verification |
| Server and syntax indexes duplicate resource use | Reuse provider indexes where possible, measure RSS and latency, govern actual background indexing |
| Dirty or concurrent documents during refactoring | Exact revision/preimage preview, shared transaction, safety snapshot and fail-closed rollback |
| Registry maintenance | Automated mirroring at pinned commits with digest checks; Crude signs its own index |
| Running project builds | Exact trust/effect checks; preserve required toolchain environment without exposing secrets; evaluate isolation per platform |
| Observer fidelity on Windows | Shims plus MSBuild binary logs; document the gaps |
| Very large C++ translation units | Changed and open units first, then by include fan-in; per-unit cache keyed by content and dependency digests |
| FIM availability and cost | Chat fallback, local option, budget |
| 100,000 or more diagnostics | Aggregated, virtualized views; indexed view-time policy |

## Evidence, prior art and sources

**Exploratory evidence, not acceptance:** in a temporary Linux/amd64 probe,
gotreesitter v0.56.0 produced the expected long-Python-docstring captures, but
its Crystal query did not colour a sample keyword, comment or string that
Chroma classified. For `const id = async <T>(v: T): Promise<T> => v;`, it
parsed the variable value as a binary expression. The official C Tree-sitter
Go binding v0.25.0, built with `zig cc` and using upstream TypeScript grammar
commit `75b3874edb2dc714fb1fd77a32013d0f8699989f` (also gotreesitter's
v0.56.0 lock), returned an arrow-function value; TypeScript's compiler
accepted the input. Upstream lists a fix in its
[unreleased changelog](https://github.com/odvcencio/gotreesitter/blob/main/CHANGELOG.md#unreleased).
These focused probes do not establish general parser parity, performance or
native Windows behavior.

- [Tree-sitter](https://tree-sitter.github.io/),
  [official Go binding](https://github.com/tree-sitter/go-tree-sitter) and
  [highlight queries](https://tree-sitter.github.io/tree-sitter/3-syntax-highlighting.html):
  upstream incremental C parser and query-driven syntax.
- [gotreesitter](https://github.com/odvcencio/gotreesitter),
  [v0.56.0 licensing](https://github.com/odvcencio/gotreesitter/blob/v0.56.0/docs/licensing.md#what-this-means-for-a-consumer)
  and [highlighting](https://gotreesitter.m31labs.dev/docs/syntax-highlighting):
  comparison runtime; its aggregate no-copyleft tag does not strip all grammar
  blobs, and its incremental highlighter returns whole-document ranges.
- [JetBrains PSI](https://plugins.jetbrains.com/docs/intellij/psi.html),
  [references](https://plugins.jetbrains.com/docs/intellij/psi-references.html),
  [indexes](https://plugins.jetbrains.com/docs/intellij/indexing-and-psi-stubs.html)
  and [injections](https://plugins.jetbrains.com/docs/intellij/language-injection.html):
  syntactic/semantic model, reference resolution and cross-language examples.
- [clangd compile commands](https://clangd.llvm.org/design/compile-commands)
  and [index design](https://clangd.llvm.org/design/indexing.html):
  project context, dynamic and background semantic indexes.
- [Helix language configuration](https://docs.helix-editor.com/languages.html),
  [nvim-lspconfig](https://github.com/neovim/nvim-lspconfig) and
  [BSP](https://build-server-protocol.github.io/):
  existing integration knowledge and a standard build-context interface where
  supported; not universal configuration or semantic correctness guarantees.
- [Kotlin LSP](https://github.com/Kotlin/kotlin-lsp) and
  [Java and Kotlin by IntelliJ IDEA](https://blog.jetbrains.com/idea/2026/10/intellij-idea-s-java-and-kotlin-for-cursor-and-vs-code-hits-1-0/):
  optional provider candidates with distinct maturity and licensing terms.
- [SCIP](https://github.com/sourcegraph/scip) and
  [Sourcegraph precise navigation](https://sourcegraph.com/docs/code-navigation/precise-code-navigation):
  optional interchange and language-specific indexer examples.
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
