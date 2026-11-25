# Windows emoji input: duplicated glyphs and two block carets

> Status: RESOLVED. Reproduced on Windows Terminal 1.25.3131.0 (Windows build 26200).
> Fixed in the shared terminal renderer and Windows console reader.

## What the screen showed

The user reported a second emoji and a second grey caret when inserting an
emoji with the Windows virtual keyboard. A native Windows Terminal capture
reproduced the visible defect **with a correct stored draft**. Two separate
Unicode keyboard inputs produced three faces on screen. After typing
`é☀️👩‍💻`, two reverse-video blocks appeared with a stray sun between them.
An ASCII key advanced the real insertion point without editing the stale
suffix.

The composer painted one block at the insertion point. During an incremental
row change, Ultraviolet overwrote part of a suffix before inserting more
cells in front of it. A write that ended on the primary of a wide glyph had
already replaced *every* column of that glyph. Moving the insertion boundary
back afterwards shifted a second copy of the physical ink. Comparing the
renderer’s own “previous frame” with the new buffer missed the failure:
it had copied the intended row, not what its operations left on the terminal.

The renderer now shifts only a complete, unchanged suffix, at grapheme
boundaries and before any write could change it. It overwrites the changed
span when a shift is not appropriate. This is a shared row-diff rule; it
does not change emoji widths, redraw the frame or add another widget caret.

## A separate input error

The Windows Console VT reader used to decode a surrogate pair independently
on key press and key release, forwarding **both** as typed UTF-8. Native
input-record fixtures with both edges produced `😂😂` for one `😂`, including
inside a bracketed paste. Ignoring all releases would also be wrong: a native
headless ConPTY sent unmappable input as a bare Alt press and an Alt release
carrying **each** UTF-16 unit. The previous code happened to retain supplementary
scalars there but lost BMP units, including the ZWJ and variation selector.
With console echo disabled (`input mode 0x288`), sending `x😂😂a👩‍💻☀️!`
produced `x😂😂a👩💻!`.

The reader now selects text-producing records before decoding their single
UTF-16 stream. Normal releases carry no new text; an Alt-code release can.
A pending lead surrogate survives read boundaries. This preserves deliberate
repeated characters without timing-based or value-based deduplication.
The same ConPTY check now returns the exact input.

## Regression checks

- `third_party/ultraviolet`: `TestVTInputConsoleText`,
  `TestVTInputAltReleaseText`, `TestVTInputTruncatedPairPreservesNextKey` and
  `TestVTInputPreservesWindowsControlChord` exercise both kinds of release,
  split UTF-16 records, repeated input, Ctrl shortcuts and paste boundaries.
- `tui/screens/shellinput`: `TestComposerIncrementalWideText` sends separate
  key and paste events, paints the real composer, replays the emitted VT in
  libghostty-vt with Unicode Core mode, and checks its text and reversed cell
  after each edit and on idle blink. `TestIncrementalWideTextBeforeSuffix`
  checks insert, delete and replace at the first column and before an existing
  wide/ASCII suffix, with and without ICH. The old output fails for **both**
  capability profiles.
- `TestWindowsConsoleEmojiReachesEachFocusedControl` sends UTF-8 into a private
  ConPTY and routes its actual decoded key events to the composer, document editor
  and find field. All three lost part of `x😂😂a👩‍💻☀️!` on the old reader. All three
  retain the exact text after the fix. `TestEditorAndFindPresentEachWideCharacterOnce`
  separately checks the emitted terminal cells and reverse-video caret during edits.
- Native capture: build `./tui/viewmodel` with `go test -c` in the native
  cgo environment. Set `CRUDE_VISUAL_EMOJI=1` and point `CRUDE_BIN` at
  the disposable test executable with `CRUDE_ARGS=-test.run=^TestWindowsComposerEmojiVisual$`.
  Run `scripts/tuishot.ps1` at 112×25 with an isolated `CRUDE_HOME` and
  these steps (keep `e` + U+0301 decomposed):

  ```text
  key:Enter   key:alt+c   type:😂   type:😂   type:é☀️👩‍💻   key:x   sleep:1   shot:idle   quit:F9
  ```

  Enter chooses the selected restricted-mode option for the disposable
  fixture. The fixture exercises a session with `TERM` unset. Before and
  after, it asserted the same final draft, `prefix 😂😂é☀️👩‍💻x`, on quit.
  After the fix, the screenshots showed one face for the first gesture, two
  for the second, and no extra glyph or second caret during typing or idle.
  The script used native `KEYEVENTF_UNICODE` gestures, **not** clipboard paste
  or a click in the OS-owned emoji picker. It does not test the picker's
  uncommitted composition overlay.

## Upstream context

[Ultraviolet commit 8ecbcf0](https://github.com/charmbracelet/ultraviolet/tree/8ecbcf0e188ce205bcf9e2e3818fd1a8dbd8eb8a)
retained the two-edge VT surrogate decode and the post-write wide-cell
insertion adjustment at investigation time. Windows Terminal itself has had
[composition overlay bugs](https://github.com/microsoft/terminal/pull/17213)
and [touch-keyboard-specific input bugs](https://github.com/microsoft/terminal/issues/10551).
Those reports are not evidence of a fault in the user's terminal. The native
draft assertion and physical output replay above distinguish the defects in
Crude's dependencies from an OS-managed composition overlay.
