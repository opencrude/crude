# Ctrl+V pasted an older clipboard entry on KDE Wayland

> Status: Fixed in source. The running shell uses the fix after it is rebuilt and restarted.

## What we saw

On 2026-11-26 testers on KDE Plasma under Wayland reported that Ctrl+V in
Crude pasted the wrong text, mostly the second entry of the clipboard
history.

## Cause

Crude tried clipboard tools in a fixed order: `wl-paste`, then `pbpaste`,
then `xclip`. When `wl-paste` was not installed, or reported that the
clipboard held no text, Crude asked `xclip`. In a Wayland session `xclip`
reads XWayland's copy of the clipboard, and KWin can leave an older entry
there, so Crude pasted that entry. This also broke the rule that an empty
clipboard never replays an older copy.

## Fix

`internal/clipboard` now reads the clipboard of the session's own display
server, the way Neovim, Helix and atotto/clipboard choose a tool: Wayland
through wl-clipboard, X11 through xclip or xsel, macOS through pbpaste and
Termux through termux-api. A Wayland session never reads XWayland, and the
tool's "nothing copied" answer is final. Copies may still go through
XWayland, which the compositor forwards. When the session's tool is missing,
Ctrl+V says which package to install; the terminal's paste shortcut still
works. Windows is unchanged.

This fixed stale XWayland reads but not the reported no-helper Ghostty paste.
Crude now includes a native Wayland and X11 client. KWin offers data-control, so
Crude never reads XWayland there. GNOME's Mutter has no data-control but updates
XWayland's selection as soon as a Wayland client copies, so Crude uses XWayland
on GNOME. Without either, a local terminal can answer an explicit paste.
See [text input](../reference/text-input.md#clipboard) for current selection ownership.

## Regression checks

`internal/clipboard/clipboard_unix_test.go` puts fake `wl-paste`, `xclip`
and `xsel` tools on `PATH` and checks that a Wayland session never reads
`xclip` (for text and images, with a current, empty or image-only
clipboard), that a missing wl-clipboard is named, that X11 uses xclip or
xsel, and that a session without a desktop leaves the copy to the terminal.
`TestPasteNamesTheMissingClipboardTool` checks the message Ctrl+V shows.
A live round trip with the real `xclip` passed on an X11 desktop. KDE
Wayland was not available for a live check.
