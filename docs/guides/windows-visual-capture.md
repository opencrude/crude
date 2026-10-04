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
  'type:settings' `
  'key:Down' `
  'key:Return'
```

Supported named keys include arrows, Enter/Return, Escape, Tab, Backspace,
Delete, Home, End, PageUp/PageDown, Space, Insert, and F1-F12. Chords accept
`ctrl`, `alt`, `shift`, and `win`/`super`.

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

## Limits

- The physical desktop must be available and unlocked.
- A user focus change during a capture intentionally fails closed.
- NirCmd captures the active window, so exact foreground verification is part
  of the safety contract.
- The workflow validates the user's configured Windows Terminal profile and
  font. It does not alter terminal settings or infer a font from the operating
  system.
