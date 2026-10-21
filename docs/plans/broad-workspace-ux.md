# First Use Acceptance Handoff

> Status: ACTIVE HANDOFF. Linux implementation and acceptance checks are in place. Native-platform and resource acceptance remain open.

## Implemented scope

The first-use introduction, detached project selection, and native AI setup are implemented.
OS-known home, personal folders, and filesystem roots are navigation locations only.
The admission check runs before the workspace factory, including for explicit paths.
No Open anyway action or trust database was added.

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
3. Explicit home and Desktop refusal, invalid paths, and valid child-project recovery.
4. Malformed configuration, byte preservation, issue dismissal, and file-editor access.
5. Exact test-core termination during browsing, followed by explicit navigation and reconnection.
6. Exact shell termination, profile-lock recovery, and reopening the same project file.

All six scenarios passed with no owned process survivors.
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

### Reported memory incident

Reproduce the reported 4 GB workload separately.
Measure startup, retained services, and dense resumed-session behavior before changing service internals.
Do not claim that navigation-only top-level folders fix unrelated project memory growth.
Keep resource-monitor work, broad Settings changes, and general LSP changes outside this task.

Remove this handoff and its index entry after these remaining checks have recorded evidence.
