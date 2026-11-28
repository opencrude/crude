# Stable v1

> Status: ACTIVE PLAN (2026-11-28). The current release is v0.9.0. v1 needs the
> five gates on this page, each closed by evidence on real hardware. Feature
> plans keep their own scope; this page owns the v1 definition and the release
> decision.

## What v1 means

A version number does not make a release stable. v1 is a promise: the product
below works on a weak laptop, the risky work runs in a throwaway sandbox, a new
person can bring their history and their existing agent subscriptions, and
right-to-left languages keep working.

Rules for this plan:

- A gate closes only with recorded evidence in this repository. A passing local
  test on faster hardware is not evidence for a gate.
- No gate is dropped, renamed or postponed silently to make a date. Change this
  page first and say why.
- v0.x keeps shipping while the gates are open. A release is never called v1
  early to look finished.

## What already ships

The v1 work builds on a product that already exists. This list is the baseline,
not a wish:

| Area | Shipped behavior | Reference |
|---|---|---|
| Shell and core | A per-user core keeps sessions, agents and terminals alive after the window closes. A shell attaches and detaches without stopping work. Local, SSH, TLS and QUIC carriers. | [core protocol](../reference/core-client-protocol.md), [core restart](../reference/core-restart.md) |
| Remote work | `--ssh HOST --workspace PATH` keeps the editor local and runs agents, language servers and terminals on the server. Strict host keys, no extra exposed port. | [network](../reference/network.md) |
| Agents | Child agents in isolated Git worktrees, immutable reports with evidence, exact identities, cancellation and recovery. | [agent orchestration](../reference/agent-orchestration.md), [managed executions](../reference/managed-executions.md) |
| Collaboration | Board, directed requests with revisions, claims, unread state and durable delivery. | [workspace collaboration](../reference/workspace-collaboration.md) |
| Context | Native-first compaction with whole-history fallback, continuity that keeps the system prompt and tools, and manual compact that never waits for another loop. | [adaptive context](../reference/adaptive-context.md) |
| Editor | Completion, hover, rename, code actions, inlay hints, semantic highlighting, project-wide problems for files that are not open. | [editor](../reference/editor.md) |
| Terminals | Embedded PTY terminals on Linux, ConPTY on Windows, owned by the core. | [in-app terminals](../reference/in-app-terminals.md) |
| Review and recovery | Hunk-level Git staging, risky-command approval, Local History previews and restore. | [local history](../reference/local-history.md) |
| Memory | Workspace memory with evidence, search over past sessions, user edit and removal. | [documentation index](../README.md) |
| Right-to-left | UAX #9 levels, Arabic joining, editor isolates, logical caret and copy, arranged cells for terminals that cannot arrange text. | [bidi](../reference/bidi.md) |
| Capabilities | Agent Skills and MCP servers with exact install previews, approval of exact bytes and revocable grants. | [capabilities](../reference/capabilities.md) |
| Session control | Plan mode, goals with an independent verifier, forks, slash commands and the palette. | [session commands](../reference/session-commands.md) |
| Errand | A background workspace agent that investigates reported problems and reports back. | [Ask Crude and Errand](ask-crude-and-errand-suggestions.md) |
| Releases | 35 releases from v0.1.1 to v0.9.0, reproducible Linux and Windows installers, checksums and a verification workflow. | [releases](../guides/releases.md) |
| Performance | Allocation ceilings, per-frame budgets and a dashboard, with recorded baselines. | [performance baselines](../reference/performance-baselines.md) |
| Crashes | Fatal capture before cleanup, crash reports and TUI recovery. | [crash reports](../reference/crash-reports.md) |

**Decision (2026-11-28).** v1 is gated on the five items below. Each one is a
release requirement, not a roadmap suggestion.

## Gate 1: smooth on a weak machine

**Requirement.** Crude shows no visible slowdown on a ThinkPad T450-class
machine. This is the machine a person actually owns, not the fastest one in the
office.

**Reference hardware.** Dual-core, four-thread mobile CPU (T450 class), 8 GiB
RAM, SSD, 1600 by 900 panel, local terminal. The current smoothness targets name
a T470-class reference; v1 must hold on the weaker T450 class.

**Workloads.**

- A dense session with 200 retained messages, an active tool row and streaming
  output.
- An embedded terminal that streams while the session repaints.
- A Go project of about 100k lines with an installed language server, problems
  open and a rename in flight.
- Local History on, with one agent editing files.
- Sizes 80 by 24 and 160 by 40.

**Budgets.** These are the existing acceptance targets, applied on that
hardware:

- p95 input-to-paint at most 16 ms, with no UI-thread stall over 32 ms.
- Idle heartbeat at 2 Hz or more.
- No dropped model event, tool result or source update for presentation speed.
- Memory, CPU and startup recorded and bounded; the ceiling is set from three
  runs with limited headroom.

**Evidence.** Three runs before and three after on the T450-class machine, at
the same sizes and workloads. GOMAXPROCS=2 on a faster processor is a contention
test, not acceptance.

**Status: open.** Baselines exist for faster hardware. The weak-machine
acceptance run does not.

## Gate 2: trampoline sandbox

**Requirement.** Untrusted agent work runs in a throwaway sandbox, and the host
keeps control of what enters and leaves it.

**Design.**

- Two cores. The **master core** runs on the host and owns the shell, policy,
  approvals, auditing and host identity. The **trampoline core** runs inside a
  microVM and owns the workspace, agents, terminals, language servers and Git.
- The trampoline runs on [microsandbox](https://github.com/superradcompany/microsandbox):
  local microVMs with hardware isolation, OCI images, snapshot and fork, and an
  embeddable SDK. A sandbox starts in under 100 ms on that project's numbers.
- **The master hides host facts.** The trampoline gets no host home directory,
  hostname, environment, SSH agent, keyring, clipboard or network identity. The
  project arrives through one explicit mount. Secrets injected by microsandbox
  never enter the guest.
- **The master audits every message.** Each inbound and outbound message passes
  the master. The record holds the direction, the peer identity, the message
  type and size, the decision and the time. A refused message is a visible error
  in Crude, never silence.
- **Network is denied by default.** A session gets an explicit allow list. DNS
  goes through the master.
- **Lifecycle is throwaway.** Create, start, stop, fail, restart and delete are
  explicit and visible. A failed sandbox never leaves a host process behind. A
  fork copies the state; deleting the sandbox removes it.

**Non-goals.** A general container manager. Replacing the host core. Running
untrusted extensions outside the sandbox.

**Risks to measure.** Nested virtualization cost on a T450-class machine, file
sync latency, terminal latency through the audit hop, Windows host support
(WhPX or Hyper-V), and the audit log write cost.

**Acceptance.**

- An escape suite fails: path traversal, host environment read, clipboard read,
  keyring read, direct network attempt, forged peer identity.
- The audit log is complete: a scripted session produces exactly one record per
  message, in both directions.
- Kill and restart: a killed trampoline loses no host authority and reports the
  failure; a restart reattaches or starts clean, as chosen.
- Measured overhead on the reference hardware stays inside Gate 1 budgets.

**Status: open.** Nothing is implemented.

## Gate 3: history import

**Requirement.** A person can bring sessions from the coding agents they already
use, read them inside Crude, and keep working from them.

**Shape.** A visual browser in the product, in the spirit of
[agent-dump](https://github.com/xingkaixin/agent-dump): list, search, preview,
then import the chosen sessions. Imports are read-only against the source.

**Sources for the first cohort.** Codex, Claude Code, Cursor, Gemini CLI,
OpenCode, Kimi, Droid, Pi. The set is data, not code: a provider entry names the
path, the format parser and the fields it can map. New agents are added by data
where the format allows it.

**What an import carries.** Session identity, messages in order, tool calls and
their results, timestamps, model and provider, workspace root and attachments.
Unknown fields are kept as raw evidence instead of being dropped.

**Rules.**

- Never write to another tool's store. Checksums before and after prove it.
- Show the person what will be stored before storing it. Imported history can
  contain secrets.
- Mark imported sessions as imported. They do not silently join the live agent
  history.
- Repeat import does not duplicate.

**Acceptance.**

- One real fixture per provider, taken from that tool's own export.
- Round trip counts: messages, tool calls and results match the source.
- Search finds text across imported and native sessions with the same behavior.
- Large session, corrupt file, missing workspace and unknown format each fail
  clearly without touching the source.
- Source tree checksum is unchanged after every one of these cases.

**Status: open.** Crude has its own session store and resume; it has no import
from other tools.

## Gate 4: native ACP

**Requirement.** Crude runs the agents a person already pays for, under that
person's own login, without breaking the vendor's terms.

**What ACP is.** The [Agent Client Protocol](https://agentclientprotocol.com/)
standardizes the conversation between an editor and a coding agent over stdio
JSON-RPC. The current stable protocol version is 1. It defines session setup,
prompt turns, streamed updates, tool calls, permission requests and terminal and
file access negotiated through capabilities.

**Crude's role.** Crude is an ACP **client**. It starts the vendor's own agent
program, which authenticates with the person's own account the same way it does
in its own terminal. Crude does not extract, store, replay or share a
subscription credential, and it does not proxy one person's account to another.
The vendor's limits, login flow and terms stay with the vendor.

**Product integration.**

- ACP agents appear next to native agents, with their own model and session
  list.
- Permission requests become Crude approvals. No silent auto-approve path.
- File edits become ordinary workspace changes, so Git review and Local History
  still apply.
- Plan updates and streamed output paint through the existing chat rows.
- Cancellation, stop and resume follow the same run recovery rules.

**Non-goals.** Speaking a vendor model API directly. Sharing one subscription
between people. Bypassing a vendor limit or a login.

**Acceptance.**

- The ACP schema version 1 conformance tests pass for the client role.
- At least three real agents run end to end: a prompt, streamed output, a tool
  call, a permission prompt, a cancel and a resume.
- One live journey on Linux and one on Windows with a real subscription, driven
  only through the vendor's own login.
- A short written terms review that names what Crude does and does not do with
  the credential.

**Status: open.** Nothing is implemented.

## Gate 5: bidirectional text stays a release gate

**Requirement.** The bidi behavior that ships today cannot regress before v1.

**Criteria.** UAX #9 level suites, Arabic joining, editor isolates, logical
caret, selection and copy, arranged output in Windows Terminal and Linux
terminals, and correct RTL layout in chat, diffs, dialogs and the embedded
terminal.

**Evidence.** The existing conformance and layout tests, plus visual capture at
80 by 24 and 160 by 40 on Windows Terminal and one Linux terminal.

**Status: implemented.** Kept as a release gate.

## Order

1. Gate 1 measurement, because it sets the budget every other gate must fit.
2. Gate 3 import, which is independent and useful early.
3. Gate 4 ACP, independent of the sandbox.
4. Gate 2 trampoline, the largest change, using the Gate 1 budgets.
5. Gate 5 acceptance, then the release decision.

## What v1 does not promise

- A macOS release.
- A multi-user server or a hosted sandbox service.
- Sandboxing for arbitrary third-party extensions.
- Any vendor credential handling beyond the vendor's own login.

## Acceptance record

| Gate | Area | Status |
|---|---|---|
| 1 | Smooth on a T450-class machine | Open |
| 2 | Trampoline sandbox | Open |
| 3 | History import | Open |
| 4 | Native ACP | Open |
| 5 | Bidirectional text | Implemented, kept as a gate |
