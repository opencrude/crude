# First Use Acceptance Handoff

> Status: ACTIVE HANDOFF. Linux implementation and acceptance checks are in place. Native-platform and resource acceptance remain open.

## Implemented scope

The first-use introduction, detached project selection, and native AI setup are implemented.
**Decision change (2026-11-25).** A plain launch in a home, personal or root
folder keeps the welcome page. **New workspace** opens the picker. An explicit
choice opens the top-level folder with Local History and language servers off
by default. `crude PATH` can also open one file in its parent workspace with the
file tree closed. See [workspace entry](../issues/2026-11-25-composer-entry-and-errand-setup.md).

Established project launches keep the current project experience.
The Crude repository, empty folders, and non-Git projects remain valid projects.
LSP implementation and the current workspace-switch confirmation are unchanged.

The shared picker owns the corrected movement rules.
Enter changes the current root.
Right Arrow expands, and Left Arrow collapses.
Backspace never navigates from search.
Workspace confirmation uses the highlighted directory, or the current root without a highlight.
Esc clears search, then the highlight, then closes.

## Current contracts

Use the current references, not this handoff, to change behavior:

- [Startup admission and protocol](../reference/core-client-protocol.md#startup-before-workspace-attachment)
- [First-use choices and AI setup](../reference/settings.md#first-use-profile-choices-and-ai-setup)
- [Shared picker movement](../reference/interaction-matrix.md#shared-folder-picker-movement)
- [Surface catalog](../reference/surface-catalog.md)
- [Shell port](../reference/shell-port.md)
- [Validation](../guides/validation.md)

Changed prose follows ASD-STE100 Issue 9.
The requested label **Let's go!** remains the explicit contraction exception.
UI text uses no em dashes.

## Observed Linux acceptance

The native product was run with isolated profiles at 160x40 and 80x24.
The introduction, project selection, reminder, and direct Add Account appeared in the required order.
The existing project home remained available without AI setup.
Captures verified readable startup actions and empty-list guidance.

The complete native account route also used a loopback model endpoint.
Account Save opened Main Agent without applying a model.
Apply agent staged the explicit model, and Settings Save persisted it.
Setup created no session and sent no inference request.

`scripts/startup_acceptance.py` checks six real-process scenarios:

1. Fresh introduction, folder selection, direct Add Account, and cancellation.
2. Established project restart and persistent reminder suppression.
3. Implicit chooser, explicit home and Desktop opening, invalid directory recovery, direct file opening and missing-file Save.
4. Malformed configuration, byte preservation, issue dismissal, and file-editor access.
5. Exact test-core termination during browsing, followed by explicit navigation and reconnection.
6. Exact shell termination, profile-lock recovery, and reopening the same project file.

All six scenarios passed on Linux after the November 25 entry change, with no
owned process survivors. This includes opening home and Desktop explicitly,
saving a previously missing file and reconnecting after loss during a browse.
The script retains bounded text captures and structured results.
Text captures are not screenshots.
Use the repository capture workflow for visual acceptance.

Focused integration tests also cover failed saves, stale results, concurrent preference edits, and workspace boundaries.
The connected account-to-model test verifies failed model Save, retained draft, retry, and explicit session creation.
Existing file attachment and workspace-switch tests remain part of repository acceptance.

## Remaining acceptance

### Native Windows

Run the native Windows build and product, not WSL.
Verify Known Folder relocation, case-sensitive paths, drive roots, and UNC roots.
Exercise first use, suppression, invalid paths, picker movement, and save failures.
Capture the native surfaces with the repository Windows workflow.
Cross-compilation does not establish native-platform acceptance.

### Distinct-host SSH

Run a shell against a separate host with different accounts and profile choices.
Verify that classification, browsing, setup, and resume use the selected core's data.
Test disconnection during selection and explicit recovery without replaying a save.
Verify host path handling, including shells and cores with different operating systems.

### Native broad-folder resource check (2026-11-25)

With an isolated profile, native Windows opened the real user's home (182
immediate entries) without Local History or LSP. Before demand-driven startup,
attach took 94 ms; recursive watch setup and the text-index build were still
running after one minute. At that point the process used 297 MB RSS and the Go
heap held 175 MB. With directory watches and indexing on demand, attach took
45 ms; the initial watch was ready within 48 ms of the request and there was
no index build. At one minute RSS was 46 MB and the Go heap held 8.4 MB.
The shell prepared the root listing off Update in 5.41 ms. Installing it on
Update took 1.14 ms. A 160 × 40 resize took 0.61 ms in Update and 1.61 ms
to paint. At 80 × 24, Update was below clock resolution and paint took
0.54 ms. These are individual observations from this host, not performance limits.

### Reported memory incident

Reproduce the reported 4 GB workload separately.
Measure startup, retained services, and dense resumed-session behavior before changing service internals.
Top-level service defaults do not establish a fix for unrelated project memory growth.
Keep resource-monitor work, broad Settings changes, and general LSP changes outside this task.

Remove this handoff and its index entry after these remaining checks have recorded evidence.
