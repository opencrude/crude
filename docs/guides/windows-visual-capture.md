# Windows Visual Capture

> Status: Current guide for native Windows Terminal TUI acceptance.

Use [`scripts/tuishot.ps1`](../../scripts/tuishot.ps1) to build Crude, launch
it in a dedicated Windows Terminal window, drive keyboard or mouse input, save
PNG evidence, and close only the window created by that capture.

Do not use WSL to reproduce a Windows rendering or input defect. WSL changes
the terminal, process, clipboard, filesystem, and input path under test.

## Prerequisites

- PowerShell 7 (`pwsh`)
- Windows Terminal (`wt.exe`)
- NirCmd (`nircmd.exe`) on `PATH`, or its absolute path in `NIRCMD`
- The normal Windows Crude build dependencies

Example when NirCmd is not on `PATH`:

```powershell
$env:NIRCMD = 'C:\Tools\nircmd\nircmd.exe'
```

## Font profile for icon acceptance

For Crude's patched rail and file icons, prefer the `JetBrainsMono NF` family
over `JetBrainsMono NFM` in Windows Terminal. Both variants have the same
JetBrains Mono text advance, cap height, line height, and baseline. The `NFM`
variant additionally scales every patched icon into one text-cell ink box,
which makes the rail glyphs look like small dots. `NF` keeps their natural
outlines while Crude's terminal-width calibration owns the resulting grid
advance.

The capture script deliberately does not edit the terminal profile. It records
the terminal and font configuration the user actually selected.

## Basic capture

From the repository root:

```powershell
pwsh scripts/tuishot.ps1 .\tmp\crude-shots
```

The script saves `01-boot.png`, prints the exact HWND and client geometry, and
closes the dedicated window before returning.

Use a prebuilt binary to avoid rebuilding between visual checks:

```powershell
$env:CRUDE_BIN = 'C:\path\to\crude.exe'
pwsh scripts/tuishot.ps1 .\tmp\crude-shots
```

## Keyboard workflow

Steps use the same vocabulary as the Linux
[`scripts/tuishot.sh`](../../scripts/tuishot.sh) workflow:

```powershell
pwsh scripts/tuishot.ps1 -Cols 160 -Rows 40 .\tmp\settings `
  'key:ctrl+alt+p' `
  'type:application settings' `
  'key:Return'
```

Supported named keys include arrows, Enter/Return, Escape, CapsLock, Tab, Backspace,
Delete, Home, End, PageUp/PageDown, Space, Insert, and F1-F12. Chords accept
`ctrl`, `alt`, `shift`, and `win`/`super`.

Acceptance must verify the received event, not just the requested step. In the
2026-10-18 native environment, `key:shift+Left` reached a standalone Bubble Tea
probe as plain Left; a raw console probe already received unmodified `ESC [ D`
with zero modifier state. Scan-code and paced-chord trials did not change it,
so those driver changes were removed. This environment does not establish native
Shift+arrow acceptance. Rendered App/component journeys cover the intended
modifier, while real pointer captures cover native selection; do not report the
Shift screenshots as a pass or infer Shift inside individual text controls.

Key names identify the injected Windows virtual key, not the action after the
user's keyboard remapping. The 2026-10-18 environment swaps Caps Lock and Esc:
inject `key:capslock` to exercise the application's Escape action there. An
earlier standalone probe received no Escape from `key:Escape`; that was not a
product failure or a general capture-driver limitation. Do not change the user's
mapping to run acceptance. The same probe decoded Ctrl+Alt+Enter as Ctrl+Alt+J;
that injected chord does not establish replace-all acceptance. Use the pointer
replace-all control and real App key-event journeys rather than inferring that
the modal received a key it did not receive.

`type:` uses Unicode `SendInput`; it does not modify the clipboard.

## Mouse workflow

Mouse positions are terminal cell coordinates, not desktop pixels:

```powershell
pwsh scripts/tuishot.ps1 -Cols 120 -Rows 30 .\tmp\mouse `
  'move:3,7' `
  'click:3,7' `
  'wheel:35,20,4' `
  'drag:45,12,70,12'
```

Pointer movement uses native `SendInput` motion, not only `SetCursorPos`.
The latter can move the desktop cursor without delivering terminal hover/drag
events. A standalone Bubble Tea probe verifies press, held motion and release;
a screenshot of the pointer's new position alone does not prove a drag occurred.

Available steps:

| Step | Meaning |
|---|---|
| `key:<spec>` | Send one key or chord. |
| `type:<text>` | Send literal Unicode text. |
| `move:<col>,<row>` | Move the pointer to a cell. |
| `click:<col>,<row>[,<button>]` | Click left (1), middle (2), or right (3). |
| `drag:<c1>,<r1>,<c2>,<r2>` | Capture once while held and once after release. |
| `dragburst:<c1>,<r1>,<c2>,<r2>,<steps>` | Flood captured pointer motion without sleeps. |
| `wheel:<col>,<row>,<n>` | Vertical wheel; negative values move up. |
| `hwheel:<col>,<row>,<n>` | Horizontal wheel; negative values move left. |
| `sleep:<seconds>` | Wait without capturing. |
| `shot[:label]` | Save an additional screenshot. |

Windows Terminal is launched in focus mode with `--size <columns>,<rows>`.
This removes tab-strip ambiguity and lets the script map the client rectangle
directly to the requested terminal grid.

## Safety and ownership

Windows Terminal commonly hosts several windows in one process. Process IDs
therefore do **not** identify the window that a capture owns.

The script instead:

1. Takes a snapshot of existing top-level Windows Terminal HWNDs.
2. Launches a uniquely named `CrudeShot-<GUID>` window.
3. Accepts only the new HWND carrying that exact unique title.
4. Activates and verifies that exact HWND before every input or capture.
5. Discards a screenshot if another window takes focus during capture.
6. Closes the exact HWND it created, never every `WindowsTerminal.exe`.

An exclusive lock file rejects concurrent Windows captures and releases
automatically if the owning process exits. Do not bypass it: terminal focus and
desktop input are process-global resources.

The default is to close the window on success or failure. For an explicit
interactive review:

```powershell
$env:KEEP_OPEN = '1'
pwsh scripts/tuishot.ps1 .\tmp\review
```

The script prints the retained HWND. Close that dedicated window after review
and clear `KEEP_OPEN`; never leave a `CrudeShot-*` window running.

## Configuration

Parameters may be passed directly or supplied through environment variables:

| Parameter | Environment | Default |
|---|---|---:|
| `-Cols` | `COLS` | 160 |
| `-Rows` | `ROWS` | 40 |
| `-PaintWait` | `PAINT_WAIT` | 5 seconds |
| `-StepWait` | `STEP_WAIT` | 0.7 seconds |
| `-CrudeBin` | `CRUDE_BIN` | build a temporary binary |
| `-CrudeCwd` | `CRUDE_CWD` | repository root |
| `-CrudeArgs` | `CRUDE_ARGS` | empty |
| `-KeepOpen` | `KEEP_OPEN=1` | off |

Crude stderr is retained at `%TEMP%\crude-tuishot\crude.log`.

## Clipboard-image acceptance

Windows Terminal normally owns Ctrl+V and Ctrl+Shift+V as terminal paste
actions. An image-only clipboard produces an empty bracketed paste
(`ESC[200~ ESC[201~`), not necessarily an application Ctrl+V keypress.
Crude's chat composer handles that event by reading the system clipboard image
on its existing asynchronous worker and persisting an ordinary attachment.
Changing the terminal's paste bindings is not required.

`TestConnectedClipboardImageReachesProviderExactly` verifies the connected
composer, framed inline/chunked transport, binary storage and exact provider image
payload. `TestConnectedNativeClipboardImage` additionally uses the real OS reader
when `CRUDE_SYNTHETIC_CLIPBOARD=1`; run it only while owning a synthetic 2×2 PNG
clipboard fixture, preserving/restoring the previous clipboard in memory. Never
enable it against an arbitrary user's clipboard. Native Windows acceptance passed;
the shared Linux build compiles, but native Linux clipboard acceptance is separate.
The 4.8 MB uncompressed image passes ordinary execution; its race-instrumented
end-to-end test still exceeds the existing eight-second journey bound during
context preparation. No bound was relaxed and that gate remains open.

For a native check, save the original clipboard, put a small synthetic bitmap
on it, open a new session without submitting a prompt, and send `key:ctrl+v`.
Wait for the attachment chip and verify the existing draft stays intact. Repeat
with `key:ctrl+shift+v`, then restore the original clipboard. Never use private
clipboard contents as a test fixture.

Separately test ordinary text paste: it must insert the terminal-delivered text
once, without importing an image or re-reading unrelated clipboard text. Empty
pastes in the launcher, file tree, editor, popup fields or blocking decks must
not attach an image to a hidden composer. Session changes while an image read
is in flight must reject the stale result.

## Limits

- The physical desktop must be available and unlocked.
- A user focus change during a capture intentionally fails closed.
- NirCmd captures the active window, so exact foreground verification is part
  of the safety contract.
- The workflow validates the user's configured Windows Terminal profile and
  font. It does not alter terminal settings or infer a font from the operating
  system.
