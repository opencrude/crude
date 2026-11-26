# A saved or renamed file kept its old viewer until the tab was reopened

> Status: Fixed in source. The running shell uses the fix after it is rebuilt and restarted.

## What we saw

On 2026-11-26 the user reported that Markdown and other file types did not
adapt on save. A new buffer saved as `notes.md` stayed a plain editor; only
closing and reopening the tab showed the preview and the other features.

Reproduction tests on the unchanged code confirmed three cases:

- A new buffer saved as `notes.md` showed no Markdown views.
- An open `notes.txt` renamed to `notes.md` showed no Markdown views, and a
  Markdown file renamed to `.txt` kept them.
- `crude new.md` (one new file, opened directly) opened an empty Preview that
  ignored every key, so the first words typed were lost.

A fourth case came from reading the code: an editor kept the problems of its
old name after a rename, so their marks could stay on a file that no longer
has a language server.

## Cause

The editor itself already followed a new name: syntax colors, editing rules,
EditorConfig, the core's language view, semantic colors and inlay hints all
read the current path. The tab view did not. Its Markdown screen was made once,
from the path the tab had when it first appeared, and never looked again. The
problems were installed once at open and then only for the path they arrived
under.

## Fix

`retitleEditorWindowTab` is the one place a tab gets a new name (first Save,
a rename through the core, and the local rename). It now calls
`followFileType`, which re-reads the problems for the new name from the
diagnostics authority and adds or removes the Markdown screen of every view of
the document, as a fresh open would. A rename between two Markdown names keeps
the view the user chose.

Text that is still being written stays on screen: a Markdown file that does not
exist yet, or that just became Markdown, opens Side by side when the setting is
Preview. Single-file windows keep working: no language server starts there, an
existing `crude README.md` still opens in the setting's view, and Save of a new
file keeps its view.

Tests: `tui/viewmodel/file_type_follow_test.go`. The first three fail on the
unchanged code with the user-visible message; the diagnostics test fails with
the old facts still installed.
