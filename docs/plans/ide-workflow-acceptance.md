# Human-Workflow IDE Acceptance

> Status: IN PROGRESS - proposed harness and campaign design. No corpus checkout,
> task fixture, language integration, remote deployment or acceptance run is
> established by this document.

## Outcome and scope

Prove that people can do real development in Crude, not just that a server
answers requests. Agents must use the rendered IDE to understand unfamiliar
code, add features and tests, diagnose failures, refactor, debug, profile,
review and work together. A correct patch made through external file tools
does not prove any of those IDE features work.

This is the workflow acceptance track for
[autonomous code intelligence](autonomous-code-intelligence.md). Language
support is assembled through generic protocols, imported metadata and evaluated
recipes, not a growing set of handwritten language engines. Projects below are
test cases for that mechanism, not new product language modules.

**Sequence:** build the required functionality and harness, iterate on small and
medium local projects until the complete required workflow passes, then deploy
the exact candidate for distinct-host and progressively larger server campaigns.
Local regression work continues during every remote round. Opening Chromium is
not the first test and is not itself an acceptance result.

## Existing assets and missing work

- Reuse `scripts/core_shell_acceptance.py`, `scripts/core_editor_acceptance.py`
  and `scripts/core_ssh_acceptance.py` for isolated real CLI processes, shared
  documents and retained-client lifecycle. The editor harness currently uses
  a small text fixture with LSP and Local History disabled; it is not semantic
  or refactoring acceptance.
- Follow [core/shell workflows](../guides/core-shell-workflows.md),
  [editor authority](../reference/editor.md),
  [control](../reference/control-contract.md) and
  [validation](../guides/validation.md). Keep their owners, authorization,
  receipts and limits; a test harness does not gain special production powers.
- Use `scripts/tuishot.sh` or `scripts/tuishot.ps1` for native pixel evidence.
  Extend that workflow for long, observe/act journeys and multi-client sessions
  rather than substituting a new screenshot/computer-use package. The current
  step driver is not a ready-made autonomous developer or multi-client recorder.
- Implement a bounded runner, exact-client input/observation adapter, task
  manifests, independent oracle runner and evidence index. Reuse existing
  lifecycle and metrics APIs. Do not write a second document or LSP owner.
- Runtime debugging needs an explicit supported path: a generic DAP client and
  validated adapters if claiming integrated debugger support. Until implemented,
  a debugger in the embedded terminal is useful but is reported as
  **terminal debugging**, not an integrated-debugger pass. Profiling similarly
  requires a real tool, raw profile and usable result inspection, not a claim
  inferred from timing a build.

## Staged project corpus

Size labels are planning categories, not measured LOC, disk or RAM. Before
admitting a project, pin its commit and dependency/toolchain identities, measure
source files/bytes and build/index footprint, read its applicable guidance and
licenses, and verify a healthy baseline on the selected target. A current
default branch or an issue number alone is not a reproducible fixture.

| Tier | Project | Main coverage and developer task candidates |
|---|---|---|
| Local small | Crude-owned workbench projects, to be created | Small Go/C, TypeScript/HTML/CSS/embedded SQL, Python, Rust and Zig cases; deterministic Unicode/CRLF, stale revisions, injection mappings, trust and transaction failures. These complement real projects, not substitute for them. |
| Local medium | [Crude](https://github.com/opencrude/crude), including `site/` | Go, TSX, CSS, C/Zig dependency boundary; dogfood a feature plus regression test, follow a shell-to-core call path, profile an observed IDE defect. Work on a separate candidate checkout; keep the running benchmark image immutable. |
| Local medium | [Neovim](https://github.com/neovim/neovim) | C, Lua, Vimscript and CMake; add or repair a small editor behavior with a functional test, trace a Lua-to-C boundary, inspect native performance. Its own terminal UI is a target program, not a replacement for Crude's editing UI. |
| Local medium | [Ruff](https://github.com/astral-sh/ruff) | Rust workspace, Python fixtures and tooling; implement a narrow rule or edge-case fix with diagnostic and negative fixtures, review snapshots, profile a selected operation. Python test data alone is not Python semantic support. |
| Local medium | [pytest](https://github.com/pytest-dev/pytest) | Actual Python source, virtual environment, plugins and dynamic fixtures; change a small collection/reporting behavior and verify both positive and negative tests. Dynamic resolution limits remain visible. |
| Local medium, targeted lane | [Ghostty](https://github.com/ghostty-org/ghostty), headless terminal-library/unit-test scope | Zig/C and toolchain compatibility; fix a bounded terminal behavior with a unit test or profile the selected library operation. Do not require a GPU/macOS app build for this lane or claim Swift/GUI coverage. |
| Remote large | [VS Code](https://github.com/microsoft/vscode) | Large TypeScript codebase with workers, extensions and native dependencies; navigate a cross-module path, add a small behavior/test, refactor with unrelated same-name cases, inspect a hotspot. |
| Remote very large | [LLVM/Clang](https://github.com/llvm/llvm-project) | C/C++, templates, macros, generated headers, CMake, Python/lit; add a narrowly scoped diagnostic/check or fix a bug with a focused test target. |
| Remote browser scale | [Chromium](https://chromium.googlesource.com/chromium/src/) | Full monorepo discovery and indexing pressure, C/C++, TypeScript, Python, GN and selected Rust/Java targets where configured; complete a small component feature/bug task and its focused tests. Use open-source Chromium, not proprietary Google Chrome. |

Do not run every project after every edit. Keep tiny cases always on, rotate
relevant medium tasks for changes, and run the full admitted local matrix before
promotion. Run one giant build/index at a time unless a checkpoint explicitly
tests admission under concurrent load.

### Held-out autonomy and language coverage

Freeze the generic assembly implementation before revealing at least two
additional integration cases. One must use a language not in the seeded product
catalog; [JUnit Framework](https://github.com/junit-team/junit-framework) is the
Java candidate. [Humanizer](https://github.com/Humanizr/Humanizer) is a C#
candidate for a held-out project/configuration; C# already in the catalog does
not qualify as a new-language test. If Java enters the catalog before the
experiment, select a different unseeded language at fixture admission. Kotlin
build scripts alone do not establish Kotlin semantic coverage.

Separate three cohorts: (1) imported metadata suffices; (2) a metadata gap
requires a bounded authored recipe; (3) no usable provider or permission exists
and the correct result is explicit partial support. Record new recipes,
connectors, operator interventions, setup time and research cost. Adding standard
LSP-backed support must not require a language-named branch in Crude's Go code;
if it does, it is an integration gap, not a successful generic-assembly result.

### Upstream and fixture boundaries

- All development happens in owned disposable checkouts. Do not open upstream
  issues/PRs, claim issues, post agent comments or push benchmark changes without
  separate human authorization and compliance with upstream policy.
- Respect repository guidance at the pinned revision. Ruff and pytest have
  human-accountability requirements for contributions; Ghostty has an explicit
  AI policy. ZLS is not selected as an edit corpus because its contribution
  guidance adopts a strict no-AI policy. This does not prohibit using an
  authorized installed ZLS to analyse an allowed Zig project.
- Never relax third-party guidance or licenses to make a fixture runnable.
  Replace an unsuitable fixture with an equivalent permitted one and preserve
  the reason. Task completion and compliance are separate acceptance conditions.
- Store large source/build trees outside Crude's checkout and data folder.
  Use new checkouts or exact reviewed reverse patches to repeat tasks, never
  destructive Git rollback against shared/user work. Keep baseline caches,
  source snapshots, patch histories and evidence retention explicit.

## Three lanes, never one blended score

1. **Deterministic regression:** fixed public tasks and paced action schedules.
   Exercises real CLI entry points and exact negative cases reproducibly.
   Headless PTY captures can prove text/protocol outcomes, not native pixels.
2. **Exploratory developer:** an agent receives an issue-level goal and relevant
   setup guidance, observes the actual TUI, chooses the next action, reads the
   result and adapts. It is not given a solution patch, symbol offsets, hidden
   oracle results or an exact click script. Hold back task variants and expected
   answers from the driver. Audit assistance, failed attempts and fallbacks.
3. **Human calibration:** people perform a representative subset, including a
   real two-person session. Compare dead ends, navigation, discoverability and
   confusing states with agent journeys. Agent self-reports and artificial
   typing delays alone cannot establish human usability. Record this lane as
   unverified until humans actually perform it.

Agent-operated Crude chat/tools are a separate **agent-assisted** cohort.
They are valuable pair-programming workflows but cannot substitute for the
manual-editor cohort. Never enable multi-agent settings or launch participants
against a user's disabled policy; the harness needs its own authorized
participants or an explicitly labelled deterministic multi-client schedule.

## Driver and observer contract

The developer driver gets only task instructions, allowed guidance, its own
public action history and the rendered terminal/window observations. Drive
normal keyboard, pointer, scrolling, selection and bracketed paste into one
exact CLI/attachment. Paste is a real human action, but record it separately
from individual keystrokes; never disguise a whole-file patch as typing.

- Use an observe → choose intent → input → observe loop. Re-read the painted
  view after navigation, dialogs, edits, errors and reconnect. Stop or reconcile
  on uncertain input; never keep blindly typing into a possibly changed pane.
  Record focus and exact process/client/attachment identity.
- Feature checkpoints must use their actual UI: completion, hover, references,
  diagnostic navigation, rename preview, test/debug/profile controls and
  version-control review. No direct LSP requests, document RPC mutations,
  hidden semantic reads, filesystem patches or external editors may satisfy
  these steps.
- The embedded terminal may run builds, tests, debuggers, profilers and other
  legitimate commands under policy. Shell-based search or manual edits can be
  an honest fallback in an exploratory task, but do not earn the missing IDE
  feature a pass. Log the command, intent, outcome and fallback reason. Do not
  run arbitrary scripts merely to avoid an editor interaction.
- Setup automation may fetch fixtures, provision authorized dependencies, seed
  failures and deploy binaries **before** the scored task. Mark all setup and
  later interventions. Maintain clean-install, cached and pre-provisioned
  cohorts separately; silently preinstalling tools defeats acquisition testing.
- An independent observer can read exact document revisions, receipts,
  diagnostics, process inventory, disk bytes and test results for assertions.
  It has a separate output channel and must not feed hidden solutions to the
  driver or mutate scored product state. A typed-control/domain result alone
  does not prove the UI was reachable, intelligible or painted.
- Add enforcement and a violation log at the runner/tool boundary; do not rely
  only on an instruction asking a coding agent not to use its file tools.
  Separate identities/process permissions where practical. Same-user core
  access is full authority, not a secure adversarial-agent sandbox.
- No agent input can impersonate a human permission, authentication, secret,
  pairing or privilege decision. Use authorized preconditions or pause for the
  real person; a blocked gate is an outcome, not permission to work around it.

Human-like workloads include short typing bursts and pauses, incomplete code,
selection corrections, pointer and keyboard navigation, undo/redo, occasional
paste, reading documentation, failed builds and recovery. Use recorded or
explicitly synthetic seeded pacing. Do not insert delays to improve latency
statistics, reward minimum action count, or claim a trace reproduces human
behavior merely because it is slow.

## Auditable checkpoints

Each task declares required capabilities, preconditions, expected observable
outcomes, independent positive/negative oracles, time/resource budgets and
checkpoint IDs before execution. A phase's required IDs cannot be silently
made optional. Outcomes are **pass, fail, blocked, unsupported, unverified**;
only pass satisfies a required checkpoint.
An expected-denial test can pass for correct denial handling, never as a pass
for the unavailable semantic feature.
The independent oracle runner/verifier assigns the verdict from the recorded
evidence; the driver may submit a completion claim but cannot self-certify it.
Missing evidence is unverified, not a pass inferred from the final patch.

| ID | Required developer journey | Evidence and negative assertion |
|---|---|---|
| C0 | Open the pinned project in an isolated normal CLI; discover/assemble support | Exact binary/cohort and fixture manifest, root/target/toolchain, recipe sources, capability coverage and approvals. Baseline tests first. Denied/offline/unsupported setup stays honest; no hidden manual configuration or “0 errors” from absent analysis. |
| C1 | Explore unfamiliar code using file/symbol search, outline, hover, definition and references | Before/action/after frames plus exact expected locations and provider/revision provenance from the observer. Same-name or comment matches are not resolved references. |
| C2 | Add a small feature and its test through the editor | Completion/snippet/import interactions, partial-code diagnostics, final diff and independent acceptance tests. No missing dependency, unintended edit or stale completion silently applied. |
| C3 | Reproduce a bug, follow failing output to source, fix and re-test | Baseline failure, navigation from the visible failure/diagnostic, relevant patch, passing targeted and negative tests. No hidden oracle access or weakening tests to pass. |
| C4 | Inspect a rename/code action, cancel it, then apply a valid one | Painted preview and unchanged bytes after Cancel; changed preimage/peer revision refuses stale Apply. Successful exact edit set, snapshot, undo/redo and unrelated symbols preserved. Unsupported operations cannot pass as manual replacements. |
| C5 | Run and debug a failing program/test | Selected configuration, breakpoint/stop, source location, stack/locals and step/continue results where integrated debugging is required. Terminal-only debugging is labelled separately; typing a debugger command is not proof of a working session. |
| C6 | Profile a real slow operation and evaluate a change | Reproducible workload, usable raw profile and interpreted hotspot, before/after runs, correctness tests and unchanged acceptance budget. An unsuccessful optimization remains a failed hypothesis, not an improvement. |
| C7 | Develop together with two, then three independently attached clients | The schedules below, all clients' rendered state, canonical revisions, author history, saved bytes and resource/process ownership. No lost peer work, duplicate edit or shared cursor/focus. |
| C8 | Recover through shell/network/server loss, core restart and project changes | Accepted text and private unresolved drafts distinguished; original receipts reconciled, stale facts labelled/dropped, correct source after reconnect/branch or configuration change. Never replay an unknown mutation as new. |
| C9 | Review the patch, save, reopen and finish | In-product diff/test review, exact durable bytes, reopened document/diagnostic state, accepted task oracles, archive manifest and cleanup outcome. Task success alone does not imply feature success. |

Choose bounded tasks with genuine behavior and tests, not placeholder edits.
Each admitted medium project needs a feature task, a bug task, a safe
refactoring task and a measured profiling task, possibly sharing a campaign.
Select debug tasks supported by its real runtime. Historical regressions may
provide fixtures, but withhold the fix and disclose possible model memorization;
include new specifications and misleading same-name cases. Write independent
expected results rather than deriving both inputs and expectations from
Crude's own index or the setup-authoring model.

## Pair and three-person acceptance

Use two/three distinct CLI processes and exclusive client-profile names,
attached to **the same core, workspace and canonical documents**. Separate
clones or separate cores do not test this. People are trusted collaborators
under the current same-user authority model, not separate tenants or accounts
with per-person ACLs; do not claim those security features.
The later remote lane includes at least two physically distinct client devices,
including native Linux/Windows combinations when those platforms are claimed.
Before that, the simulated-device lab below must pass on independent guest OS
instances. Neither three host processes nor three VMs on one machine are
evidence of physically distinct client hardware.

1. A drives a feature; B independently navigates/reviews. Cursor, selection,
   scroll, tabs, focus and private drafts remain independent. Swap driver roles.
2. A and B make disjoint unsaved edits in one file. Both see canonical text;
   disk stays unchanged until Save. A's undo preserves B's accepted work;
   B saves the exact combined state and both dirty indicators settle correctly.
3. B edits an affected document while A has a completion or multi-file rename
   preview open. Stale results cannot overwrite B; preview application refuses
   or requires a new review. Exercise true overlapping edits and preserve
   recoverable drafts when safe reconciliation is impossible.
4. Add C running tests/profiling and then editing another file. Verify the
   actual PTY input-controller contract, shared output and no cross-client
   misrouting; do not assume every client can control every terminal at once.
   All clients' intelligence for the same revision/configuration agrees.
5. Disconnect the active author both after canonical acceptance and at an
   unresolved-acknowledgement boundary. Reconnect, inspect original receipts,
   retain accepted work and preserve unknown private edits without replay.
   Repeat through core restart and an external file change.
6. Close a view/session, detach a shell and close the last shell while owned
   work is running. Retained work and peer views must obey current lifecycle;
   teardown of the test core is a separate, explicitly owned operation.

Deterministic schedules control ordering and delay in owned test endpoints;
exploratory participants choose their own work within a recorded schedule.
Multiple live PTYs are allowed, but desktop input/capture is serialized: use
one capture owner, fence the exact intended window, sample each client's view
and never run two capture scripts concurrently. Extend the repository capture
workflow if it cannot yet record this session. A headless multi-PTY pass without
native views remains protocol/text evidence only.

## Reproducible multi-device lab

### Topology and fidelity

Use full QEMU/KVM guests as the default reproducible device simulation. A guest
is a device endpoint with its own kernel, filesystem, login, network stack,
terminal/display, client profile and credentials, not another host terminal.
VMs still share physical CPU, storage and network infrastructure; record that
limit and never promote a virtual result to a physical-device result.

```
 Controller and verifier, outside the scored clients
   | management only: provision, observe, deliver input, archive, teardown
   |
   +-- client A guest: Linux + terminal + shell A --+
   +-- client B guest: Linux + terminal + shell B --+--> one core R
   +-- client C guest: Linux or Windows + shell C -+    one project/document owner

 Local phase:  R is a fourth Linux guest, source/tools only on R.
 Remote phase: R is the authorized private server endpoint, same client guests.
 Physical lane: replace at least two client guests with real separate devices.
```

| Lane | What it establishes | What it cannot establish |
|---|---|---|
| Process smoke | Existing two/three-CLI ordering and regression checks | Independent devices, kernels, filesystems or network paths |
| Linux VM lab | Three independent guest endpoints and a separate core guest; reproducible locality, credential, failure and collaboration checks | Physical machines or host-native performance |
| Mixed-OS VM lab | Replace one client with a licensed, supported Windows image; run the actual native Windows binary, Terminal and capture script | Windows via Wine/WSL or cross-compilation is not a substitute; VM timing is not bare-metal timing |
| Remote-core lab | Keep independent client guests locally and use the physically remote core, with measured WAN and per-client faults | It does not prove physically independent client hardware or independent internet providers |
| Physical calibration | At least two actual client devices, including native platforms claimed by the campaign, sharing the real remote core | No blanket hardware/platform coverage beyond the recorded configuration |

Containers and network namespaces may accelerate separate regressions, but
share the host kernel and are not accepted in place of the VM lane. Emulated
TCG execution, missing acceleration or an unavailable licensed Windows image
is a labelled limitation or blocked lane, never an implicit KVM/native pass.

### Provisioning and preflight contract

The runner has explicit phases: preflight, provision, prove-isolation, connect,
exercise, verify, archive and cleanup. Record an immutable run ID and every
guest's exact process/monitor identity before effects. A failure stops promotion,
preserves evidence and tears down only that run's owned resources.

1. Pin QEMU machine/accelerator, base OS image, image digest and authenticated
   checksum/signature source, native libraries, terminal, fonts and candidate
   binary/cohort. Build generalized base images without project data, user
   credentials, SSH host keys or pre-enrolled Crude device identities.
   Provision Windows from a user-authorized licensed image; use its supported
   generalization and boot requirements, not bypasses.
2. Create one private copy-on-write disk overlay per guest, with unique
   cloud-init instance identity where used, machine ID, hostname, MAC, host
   keys and device identity. The base remains read-only. Reconnect/reboot uses
   that same overlay; resetting a VM into a new identity is a different test.
   Never clone an already enrolled device or restore a live credential-bearing
   snapshot to impersonate a second participant.
3. Give each guest a separate home, clipboard, temp area, SSH known-hosts and
   Crude state. No host project, home, SSH agent/socket or private key mounts,
   shared clipboard, drag/drop filesystem sharing, 9p or virtiofs source
   mounts. Explicitly stage the binary, public pins and test inputs. The
   controller's administrator key never enters a guest or golden image.
4. Bootstrap a distinct guest-management key and distinct product SSH key or
   direct device credential per client, under the owner's approval. Private
   keys stay in their owning private store, not in a recipe, command argument,
   cloud-init log, screenshot or evidence bundle. Obtain host fingerprints
   through the authenticated provisioning/control channel; ssh-keyscan alone
   is not verification. Test rejection of changed authorities before sending
   any application credential.
5. Use QEMU user-mode networking and private loopback-bound management
   endpoints so the basic lab does not require host TAP/bridge or sudo
   configuration. For local R, publish only the selected guest SSH/TLS/QUIC
   data ports on orchestrator loopback and route clients explicitly through
   their guest data NICs. Separate management and data NICs/routes: packet
   faults must not kill the observation or cleanup channel. Verify TCP and
   UDP routes independently. NAT can give different clients the same external
   source IP; distinguish devices by authenticated identity, never by IP.
6. Apply delay/loss/rate/disconnect only to owned guest data interfaces and
   test endpoints, using guest-local shaping or an owned virtual router.
   Permit narrowly scoped guest administration for setup, but run Crude and
   scored drivers unprivileged. Do not weaken the host's user-namespace,
   AppArmor or firewall policies to make a convenience backend work.
7. Keep source and semantic engines only on R. Use a short, actual private
   core profile and assert all clients attach to that exact core generation,
   workspace and document IDs. Client-local profiles stay distinct. The
   local client's CRUDE_HOME does not select the remote server's profile:
   SSH helpers read the server-side environment.
8. Run on-demand resource admission before boot: sum guest RAM, QEMU overhead,
   core/engine/build budgets, overlay growth and host reserve. A starting
   local envelope is three 2 GiB clients plus a 4-6 GiB core guest, booted
   sequentially; this is a tuning candidate, not an accepted capacity bound.
   Measure KVM and scheduling behavior, apply supported host process limits
   and abort before pressure or disk exhaustion. Do not count thin-disk
   virtual capacity as free physical space.

For the real server, prefer a separately provisioned unprivileged benchmark
account with its own home and keys, or an explicitly owned isolated SSH
endpoint whose profile locality is independently tested. Account/endpoint
provisioning needs separate authorized administration at G2; none is performed
by this plan update. Never rewrite the administrator's global login environment
or assume a client environment variable isolates its production core. Verify
the default profile has not been contacted before any candidate deployment.

### Device-isolation checkpoints

These are prerequisites to interpreting C7/C8 as multi-device evidence, not
alternative ways to bypass the human-workflow checkpoints.

| ID | Procedure | Required evidence and refusal |
|---|---|---|
| D0 | Boot clients A/B/C and R from pinned images and unique overlays | Hypervisor process/monitor identities, guest boot/machine identities, disk/MAC inventory and native binary/cohort digests. Missing/duplicated identity or failed boot blocks the run. A KVM capability probe alone is not a boot pass. |
| D1 | Put a random nonce on R and conflicting decoy bytes at the same path on each client; keep a second path absent on clients | Every TUI opens R's exact content through the product, not a local mount or decoy. Disallow shared mounts. Engine/build process inventory and test output identify R; no hidden local-provider answer satisfies the check. |
| D2 | Enrol independent clients and then revoke only B's direct credential or exact SSH authorized-key entry | A/C continue and B loses access as specified. A copied display label or shared key cannot pass. Existing SSH connections need explicit owned disconnect for the SSH-key test; key removal by itself does not terminate established sessions. Direct live-revocation behavior is checked against its actual contract. |
| D3 | Execute C7 with two, then three guests, independent viewports and roles | Exact shared revisions, preserved author undo, disk bytes, all guests' painted state, and no cross-device focus/input/clipboard or private-draft leakage. Exercise 160x40 and 80x24; optional extra geometry is recorded. |
| D4 | Delay/drop B only while A/C keep editing and a build emits output | Capture configured and measured faults, per-client RTT/response and continued healthy-peer work. Locally repeat healthy, 50 ms RTT/1% loss, and asymmetric/jittered cases; remotely measure the actual WAN baseline and add declared faults rather than claiming a lower RTT than the link provides. Mark unachieved profiles unverified. |
| D5 | Disconnect or crash B at accepted and unresolved-ack boundaries, then reboot B with its existing disk and reconnect | A/C and R survive; accepted edits persist, unknown private edits/receipts reconcile without duplicate mutations. Also test an expired resume and a new attachment epoch; no stale callback may overwrite current text. |
| D6 | Stage incompatible native/cohort image, changed host key/pin, denied trust and misrouted localhost endpoint as negative cases | Exact refusals before unsafe effects, preserved drafts and no credential disclosure or accidental local-core fallback. A compatible native image from the same cohort is a separate positive case. |
| D7 | After G1, keep the guest clients and switch R to the real server; exercise SSH, authorized direct TLS/QUIC and mixed-carrier attachments | Identical pinned source/task inputs, all clients sharing the new remote core's workspace/document identities, and C0-C9 outcomes over a real host boundary. Local-core IDs are not reused or confused with the remote domain. Source paths never resolve to client disks; configured authenticated carrier fallback cannot widen authority. Do not equate SSH-TUI-on-server with a local TUI attached to remote core. |
| D8 | Archive, stop and re-run the complete lab from a clean fixture | Sealed per-device evidence, retained recovery state as required, no owned guest/window/port/process left live and no changed host network configuration. Verify cleanup even on failure; replay keeps the former failed attempt. |

D0-D6 and D8 run locally before G2. D7 gates remote equivalence. The mixed-OS
variant repeats D0-D8 for claimed Windows behavior. Physical calibration repeats
the relevant C/D rows on real devices; a green Linux VM matrix never silently
completes that row.

### Input, observation and clocks

The controller delivers scored input to an exact guest/client only; provisioning
SSH/QMP is not a back door for modifying source or querying hidden semantics.
Guest management services expose bounded input/capture and read-only evidence,
not an unrestricted scored shell. Log any setup or oracle command separately.
Separate driver and verifier channels as in the main harness contract.

Run the existing Linux/Windows capture workflow **inside** each corresponding
guest. Reuse its terminal and input contracts, and serialize capture launches
through one coordinator even when displays are isolated. For concurrent edits,
keep the participating CLIs alive and use a single session recorder/driver
that fences each device; do not launch multiple capture scripts. Extending the
current single-window workflow is remaining harness work, not existing support.
Input dispatch is fenced per device, not followed by a global wait for every
acknowledgement: explicit barriers must allow A/B/C mutations and replies to
overlap, including edits issued while another client's result is pending.
Serializing all product work would erase the races the device lane must expose.
Sample every participant's viewport at each barrier. Virtual-display pixel
evidence, native host desktop evidence and headless PTY text are labelled
separately. VNC/RDP video is not a substitute for exact terminal-window input.

Use monotonic clocks for local durations; record guest/server clock offsets and
uncertainty for cross-machine correlation. Causality comes from operation,
document revision, frame and receipt identities, not unsynchronized wall-clock
subtraction. Keep driver-thinking time and management/capture latency out of
the product latency metric while preserving them in task elapsed time. Same-host
VM contention and remote network baseline remain part of the reported setup.

### Infrastructure evidence and remaining work

Planning-time checks found local QEMU 8.2.2, accessible KVM API 12, about 23 GiB
RAM and about 604 GiB free storage. An unprivileged namespace probe failed with
Operation not permitted despite the sysctl being enabled. No OS image was
downloaded and no guest was booted. Do not call this D0 or VM acceptance.

Remaining lab deliverables are pinned generalized images, the overlay/network
provisioner, exact-guest recorder/input integration, private credential
provisioning, isolation tests D0-D8 and clean failure/cleanup tests. Build on
[QEMU disk overlays](https://www.qemu.org/docs/master/system/images.html),
[security guidance](https://www.qemu.org/docs/master/system/security.html)
and [cloud-init NoCloud](https://docs.cloud-init.io/en/latest/reference/datasources/nocloud.html).
Do not introduce a bundled general-purpose desktop-control feature into Crude.

## Evidence and measurement

Every run produces a bounded, indexed evidence bundle, retained before temporary
state is cleaned:

- Manifest: run/task IDs, project commit/dependency lock identities, task patch
  digests (with hidden solutions retained separately), task/oracle revisions,
  candidate binary digest and protocol cohort, driver
  model/version and permitted tools, config/policy, OS/native dependencies,
  hardware/limits, transport, terminal/font/grid, cache state and network facts.
  For device lanes include guest image/overlay/process identities, independently
  generated device identities, data/management routes, measured fault schedules
  and clock uncertainty; archive no private credentials.
- Ordered actions: actor/client/attachment, observed frame ID, public action
  intent, exact input class, timestamps and visible result/error; no private
  model reasoning. Record setup interventions, fallbacks and retries, including
  failed attempts. Never silently replace a failed run with its passing retry.
- Checkpoint evidence: referenced screenshots/lossless recording segments,
  terminal transcript, sanitized command/receipt IDs, canonical revisions,
  preview/preimage/patch digests, test outputs and raw profile references.
- Outcomes: per-checkpoint and per-feature results, task completion separately,
  human interventions, unsupported/blocked reasons, compared baseline,
  measured regressions, unresolved defects and cleanup/retention status.

Hash the manifest and artifact index; archive append-only attempts to an owned
durable evidence location. Hashes detect accidental changes, not semantic
correctness or tampering by a privileged same-host actor. Restrict hidden
oracles and credentials to their owners. No credentials, unrelated user
documents or private project content may leak into screenshots/logs or shared
recipes. Durable evidence needs a retention budget; `CRUDE_TEMP_DIR` alone is
scratch, not a completed audit deliverable.

Measure separately:

- Task completion/time, dead ends, manual fallbacks, unassisted setup and
  per-feature use/correctness. Do not turn faster model inference into an IDE
  performance claim.
- Input admission → mutation → view installation → terminal write →
  observed pixels, and semantic request → current usable response. Correlate
  events; PTY writes and render counters are not proof of physical display.
  Separate model thinking/reading time and network latency.
- Core, shell and owned servers/checkers/builds: CPU, Go heap, native RSS,
  I/O, disk/cache/index growth and teardown. Separate attributable hosts;
  missing figures are unknown, never zero. Keep profiling/capture overhead
  and a minimally instrumented baseline visible.
- Cold tool acquisition, cold project/index, warm reopen and steady typing,
  with cache conditions recorded. A warm cache must not pass a cold-start
  checkpoint. Editing during legitimate indexing/build output must still work.
- Both fresh profiles and owned long-lived/resumed dense sessions, with many
  open documents, retained task/chat history and ongoing work. Include
  multi-hour developer sessions and role handoffs; repeatedly restarting on
  an empty profile can conceal lifecycle leaks and degraded interactivity.

Follow [performance baselines](../reference/performance-baselines.md).
Pre-register fixture/hardware-specific latency and resource budgets before a
candidate run; keep existing gates unchanged. Use enough events to support a
tail percentile and repeated matched baseline/candidate runs; a handful of
actions does not justify p99. Native pixel samples have finite resolution: a
low-rate screenshot cannot validate a 16 ms presentation bound. Use replayed
interaction micro-runs to locate regressions without replacing developer tasks.

For quality comparisons, run the same pinned tasks/configuration in the
appropriate licensed JetBrains IDE (and optionally another editor), recording
the exact product/version, coverage, hardware, cache state and failures.
Compare outcomes and workflows rather than shortcut counts. Claim superiority
only for measured dimensions, not a blanket “JetBrains killer” score.

## Promotion gates and local regression cadence

| Gate | Work and prerequisite | Acceptance boundary |
|---|---|---|
| G0 - Build locally | Implement the generic assembly path, required IDE surfaces, safe edits/history, runner/observer and VM provision/capture integration | Unit/race/native gates and tiny fixtures, D0-D2 bring-up; every required control is reachable and observed. Debug/profile features claimed for the campaign actually work. |
| G1 - Local medium | Complete C0-C9, D0-D6 and D8 on admitted local projects, held-out assembly cases and required capability matrix; fix defects at their owner | At least three recorded passing local campaigns with varied tasks and cold/warm state, no unresolved known reproduction, exact-byte safety and independent two/three-guest editing. Plain host PTYs cannot satisfy the device rows. Human calibration remains separately required. |
| G2 - Remote preflight | After G1, refresh the authorized host inventory, provision an owned remote benchmark endpoint and stage the exact native artifact | Key-only auth/host identity, profile locality, compatibility, capacity, approved paths/effects and tested rollback/cleanup. Early explicit credential bootstrap/read-only inventory is allowed but is not a workload or G2 pass. No production core replacement. |
| G3 - Remote equivalence | Replay the admitted medium tasks using the independent guests and real remote core, D7, SSH and authorized direct/mixed transports | Distinct-host source/tool locality, rendering, edit/undo/save, recovery, security and measured network behavior. Record mixed-OS VM and physical-client calibration separately; neither loopback nor three local processes satisfies those rows. |
| G4 - Scale and discovery | VS Code, then LLVM, then Chromium; each promoted only after scoped baselines and capacity admission | Full-tree discovery/index-pressure plus real component tasks. Record exact configured units; a subset build/sparse checkout is not whole-project acceptance. Expanded server storage still needs measured workload/headroom admission. |

“Implement all required functionality first” means before G2, not postponing
local end-to-end feedback until every feature is finished. Implement a complete
local slice, exercise it, fix it, then expand the required matrix. Optional
capabilities may remain explicitly unsupported, but cannot be removed from a
required campaign to obtain a pass.

After any Crude change: run focused regression tests and affected local
workflow checkpoints. Before every remote upload: rebuild the final immutable
candidate, run applicable repository gates and the full admitted local
regression matrix. During remote investigation keep stable local medium
sentinels, rerun them on every fix, add a minimized reproduction at the actual
owner, and rerun the original remote failure before declaring it fixed.
Record hardware/infrastructure failures separately; neither failures nor
blocked cases count as successful acceptance.

## Remote campaign safety and capacity

The user designated a benchmark server and subsequently authorized credential
bootstrap. Keep its exact account/endpoint and connection configuration outside
Git. A dedicated Ed25519 key was enrolled and key-only noninteractive SSH was
verified, preserving strict host-key checks. The password was handled through
non-echoing prompts for authentication and the credential audit, not saved as a
password file or placed in a command, recipe, environment variable, memory
record or repository.
An owner-only backup preserves the prior authorized-key preimage; forwarding
restrictions apply only to the new key, with other entries preserved.

Private connection notes and SSH configuration remain in the owner's home,
outside Crude's checkout. The unattended administrator key is unencrypted and
mode 0600, not hardware-backed or protected from a compromised same-user
process. Do not distribute it to test clients. Use distinct revocable guest/
device credentials and keep authorization, pins and revocation provenance.
The password appeared in the conversation; recommend rotating it independently
of the now-verified key login, without disabling an access method automatically.

Read-only inventory observed Ubuntu 24.04 x86_64, 75 visible CPU threads, about
98 GiB RAM. The first observation found only about 62 GiB free on the home/root
filesystem (87% used). Following the user's storage expansion, a read-only
recheck found 266,825,136 KiB free, about 255 GiB, with 46% used.
These are point-in-time observations, not reserved capacity or effective quota
guarantees. QEMU/KVM was not established on the server, so host client VMs
locally by default. No candidate, corpus, VM, account, package, firewall or
production-service change was made there; changes were limited to the
authorized SSH key enrollment/restrictions and its backup.

The prior immediate storage blocker is removed; this is not yet a Chromium
capacity pass. Reserve sufficient measured source, dependencies, index, build
and evidence headroom before that campaign. Do not
delete existing server data, expand LVM or use swap/tmpfs as a disk substitute
without separate authorization. Smaller remote tiers still need their own
capacity budgets, not an assumed pass because RAM is plentiful.

- Inventory read-only first: CPU, RAM/swap, free space, filesystem, limits,
  toolchains and supported containment. Establish an owned run root and
  resource/disk budgets. Do not change system packages, sysctls, firewalls or
  production services as an implicit benchmark step.
- Upload/stage a digest-verified, compatible native image and retain its build
  identity; local shell and remote core need the supported protocol cohort.
  Provision the owned account/SSH endpoint first, assert its remote home/profile,
  then use the supported install/launch flow and exact-image review. Never
  contact the user's live core to discover that isolation was missing. Respect
  blocked handovers.
- Exercise both a local TUI attached with Crude's SSH carrier and a TUI running
  in an SSH terminal on the server; they place rendering/input work differently
  and are separate cohorts. Direct QUIC/TLS needs the existing host-local
  opt-in, certificate pin/pairing and consent. Prefer SSH first; do not expose
  unauthenticated listeners or weaken host verification for convenience.
  Negative tests reject changed host keys/pins and unauthorized enrollment.
  Apply latency/loss faults only to owned namespaces/test connections, never
  the whole server NIC or unrelated sessions.
- Giant-tree budgets are not known from repository names. Chromium's current
  [Linux instructions](https://chromium.googlesource.com/chromium/src/+/main/docs/linux/build_instructions.md#system-requirements)
  give a minimum of 8 GB RAM and 100 GB free space, recommending more than
  16 GB RAM; that is not evidence this server can run Crude, multiple semantic
  engines and the chosen builds comfortably. Measure the pinned checkout,
  dependencies, index, caches and selected build target with headroom and
  stop-before-exhaustion limits. Reduce concurrency/scope visibly or leave
  G4 blocked if capacity is insufficient.
- Targeted component builds/tests are legitimate human workflows. Define them
  before the task and label their scope; do not promise or secretly require a
  complete Chromium debug build for every edit. Preserve full-repository
  discovery pressure as a separate measurement.
- Cleanup owns exact run processes/windows/core generations and disposable
  source/build directories only. Archive failures first, settle unknown
  mutations and retained work, then stop/join owned resources and verify
  cleanup. Never infer that an old or disconnected process is abandoned.

## Remaining implementation work

1. Implement assembly-first contracts and required local IDE features in the
   parent plan; admit pinned project/task manifests and independent oracles.
2. Extend the existing real-CLI/capture workflows into the bounded observe/act
   runner and QEMU device lab, with exact-client input, hidden-oracle separation,
   credentials, evidence sealing, failure retention and D0-D8 scheduling.
3. Run G0/G1 with the required local VM/device and collaboration matrix; fix core
   defects and record scripted/exploratory results. Obtain human and physical
   calibration before making those separate usability/hardware claims.
4. With key login established, refresh inventory, provision the private remote
   endpoint and resolve disk admission. Then run G2/G3 and capacity-admitted
   G4 campaigns; mixed-OS images, physical clients, product/harness work and
   all still-unverified gates remain open until evidence exists.

Project setup sources: [Neovim build](https://neovim.io/doc/build/),
[Ruff development](https://docs.astral.sh/ruff/contributing/),
[pytest contributing](https://docs.pytest.org/en/stable/contributing.html),
[Ghostty development](https://github.com/ghostty-org/ghostty/blob/main/HACKING.md#developing-ghostty)
and [AI policy](https://github.com/ghostty-org/ghostty/blob/main/AI_POLICY.md#ai-usage-policy),
[VS Code development](https://github.com/microsoft/vscode/wiki/How-to-Contribute),
[LLVM setup](https://llvm.org/docs/GettingStarted.html) and the Chromium
instructions above. Re-read the pinned project's instructions at admission;
these links are research inputs, not completed setup or acceptance.
