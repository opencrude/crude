# Workspace Trust Prompt and Plain Status

> Status: IN PROGRESS - design agreed, implementation not started. Implementation
> starts only after the user approves this plan. It comes before further
> [autonomous code intelligence](autonomous-code-intelligence.md) work.

## Problem

A user opened the Crude repository and saw this:

- Editor header: `Syntax shared · Diagnostics pending · hidden`.
- Footer: `Problems · pending`.
- Workspace health: `no workspace execution grant: Static only; grant Project
  execution allowed in Workspace settings to run language servers` and a row of
  zero counters.

Nothing was broken. The workspace was not trusted, so Crude correctly did not
start language servers. But Crude never asked the user to trust the folder.
Instead, it hid the decision in Workspace settings and showed internal states
as if work was in progress. Results never arrive while the folder stays
untrusted, so "pending" is false. "hidden" and "Syntax shared" are internal
facts. The trust labels "Static only" and "Project execution allowed" use
implementation terms.

The cause is a process gap, not one bad string. Each surface builds its own
text from raw fields, and no rule told contributors how to talk to the user.
The [user language](../reference/user-language.md) reference now holds that
rule.

## Decisions

These decisions are agreed with the user:

1. **Ask on open.** When a workspace opens and no trust decision exists for
   that folder, Crude shows a dialog. A real IDE does this; it does not assume
   that the user knows about trust.
2. **Restricted mode allows editing.** If the user does not trust the folder,
   the user can still open, read and edit files. Crude starts no tools that run
   project code. This matches VS Code Restricted Mode and JetBrains Safe Mode.
   The editor is not read-only.
3. **Ask once for each folder.** Crude remembers the choice for the folder. It
   asks again only if the folder is replaced (its native directory identity
   changes), because the old decision does not apply to new content.
4. **Esc does not decide.** Esc opens the folder in restricted mode for now and
   does not store a choice, so Crude asks again on the next open.
5. **The user can change the choice later** in Workspace settings. The dialog
   says where.

## Design

### Dialog

The dialog uses the shared confirm frame from the
[dialog design language](../reference/dialog-design-language.md). Draft text:

- Title: **Trust this folder?**
- Question: **Do you trust the authors of the files in this folder?**
- Detail: path, then: "Trusted folders can run project tools, such as
  language servers. These tools run code from the folder. In restricted mode,
  you can edit files, but Crude does not run project tools. You can change
  this later in Workspace settings."
- Buttons: **Trust folder** and **Open in restricted mode**. Restricted mode
  has the default focus.

The dialog opens after the workspace attaches. It does not block the core
attachment, other shells or running agents. It waits behind a higher modal
(for example the AI setup reminder) and is never shown twice at the same time
in one shell.

### Stored decision

Today the stored trust has two values, and the default value is the same as
"not decided". The settings diff erases an explicit `static_only` choice
(`OverridesFromDiff` in `types/settings.go`). The decision must become an
explicit three-state value in the workspace overlay:

| Stored | Meaning | Result |
|---|---|---|
| absent | No decision | Restricted, and ask on open |
| restricted | User chose restricted mode | Restricted, do not ask |
| trusted + root identity | User trusted this folder | Project tools may run |
| trusted + other identity | Folder was replaced | Restricted, and ask on open |

The workspace preference owner keeps sole authority: the dialog sends the same
workspace preference edit as the Settings page, through the same save path that
binds the root identity. There is no second trust store and no shell-side
decision. The answer reaches the shell as the existing effective-settings
projection plus one derived fact: "trust decision needed".

### Names

Visible names change. Stored values and IDs may change incompatibly, because
the project keeps no backward compatibility during active development.

| Today | New |
|---|---|
| Static only | Restricted |
| Project execution allowed | Trusted |

### One status, one owner

The language-server owner derives one user-facing status for the workspace and
one for each document. Every surface renders it; no surface builds its own
text:

| State | Editor header | Footer pill | Health panel |
|---|---|---|---|
| Not trusted | Restricted mode | Problems: off | "Language servers do not run in restricted mode." + **Trust folder** |
| No server for the language | (nothing) | Problems | "No language server for Go. Set one up in Language servers settings." |
| Server starting | Checking… | Problems | "Starting gopls." |
| Results for this file | 2 errors, 1 warning | 3 Problems | counts |
| Server failed | Language server failed | Problems: error | the reason and the next step |

Normal states show nothing: "Syntax shared" and the zero-counter row go away.
The detailed facts (fact IDs, source revision, context) stay available in the
diagnostic details reader, not in status lines.

## Acceptance

- Opening an untrusted folder shows the dialog once. **Trust folder** starts
  language servers without a restart. **Open in restricted mode** starts none,
  and the next open does not ask again.
- Esc opens in restricted mode and the next open asks again.
- Replacing the folder at the same path asks again.
- A second shell attached to the same workspace sees the stored decision and
  does not ask again.
- The screenshot case above shows "Restricted mode" in the header, a plain
  footer pill and one health sentence with a **Trust folder** action. No
  internal word from the [user language](../reference/user-language.md) list
  is visible.
- Regression tests drive the real open path and assert the rendered text.
  Native captures at 160x40 and 80x24 cover the dialog, both choices and the
  status surfaces.
- Gates: focused and affected packages, race, build, lint, design audit, docs
  check, bench gate.

## Also in scope

- Audit the other status and error text that the screenshot exposed (Problems
  panel, Status Overview health rows, the 220 unread notices) against the user
  language reference, and fix what this change touches.

## Not in scope

- Gating agents or terminals by trust. They already have their own permission
  model; this plan does not change it.
- Prediction, budget meter and the other remaining
  [autonomous code intelligence](autonomous-code-intelligence.md) steps.
