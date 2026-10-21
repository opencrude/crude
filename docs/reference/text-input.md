# Text Input and Composer

> Status: Current reference — shared text controls, cursor, clipboard, composer pastes, attachments and prompt history.

Every non-document text control (fields, areas, the chat composer, popup
filters) shares one editing, cursor and clipboard contract; add behavior to the
shared control, never to one popup.

## Text controls

- `ui.TextEdit`, `ui.TextField`, and `ui.TextArea` own the non-document editing
  contract: selection replacement, undo/redo, word/document/page movement,
  Ctrl/Super shortcuts, copy/cut, bracketed paste, owner-keyed async Ctrl+V,
  multi-click selection, and captured outside drag with autoscroll. Add behavior
  there, never to one popup.
- Overlays whose text outlives their rebuilt frames (editor find/replace, the
  command palette filter, tree create/rename prompts) bind the frame's field to
  their retained `TextEdit` with `TextField.BindEdit`; keys and bracketed pastes
  go through `routeOverlayText` and presses through `overlayFieldPointer`, and
  `ComponentWidget` hosts keep window pointer capture for the drag. Never mirror
  overlay text into a frame copy or hand-roll its keys (the removed
  `HandleTextEditKey` path lost clipboard, undo, selection and paste).
- TextArea cursor geometry and paint share one logical-to-wrapped-segment mapping.
  A cursor inside continuation whitespace omitted by WordWrap maps to the next
  segment's column zero without changing the draft or cursor offset; never slice
  from a visible segment start past a logical cursor in that omitted gap.
- `Form` is a composite pointer owner because its retained fields are not Host
  children. Once an inner input starts a drag, `Form.pointerField` must route
  every captured motion/release to that same field before hover-focusing.
- Child bounds stay parent-local during paint as well as layout. A FormField
  passes its own origin unchanged to TextArea; pre-adding the textarea's Y=1
  shifts its background into the description row.
- Text drag selection uses theme `TextSelection`/`TextSelectionFg`, distinct from
  list/chip Selection. Apply readable ink across input/chat/editor/diff/terminal
  consumers. Programmatic editor jumps reveal only when necessary after actual
  viewport geometry; landing highlights are not destructive edit selections.

### Selection semantics (all editable text)

- A block cursor visualizes an **insertion boundary before** its covered
  grapheme, not a selected character. At `abc|def`, Shift+Left selects `c`;
  Shift+Right selects `d`. Extensions retain the original boundary through
  reversal; returning to it collapses. Plain Left/Right collapses a non-empty
  selection to its corresponding edge without an extra move. The document
  editor, TextField, TextArea, composer bridge and popup inputs follow this
  same rule. Never add the covered character to keyboard selection merely
  because the caret is drawn as a block.
- A plain pointer press instead retains the **hit source unit**. Backward
  dragging includes the originally pressed grapheme; forward dragging starts
  at its leading edge. Reversal projects from those immutable bounds. Shift
  pointer gestures extend the existing caret/selection boundary instead.
  Double/triple-click-hold retains whole-word/line units. Release applies its
  final coordinate, and only the initiating button can finish the gesture.
- User character movement, selection and deletion cross complete graphemes
  (combining sequences, flags and ZWJ emoji), never a partial source rune.
  Cursor/storage offsets remain rune-based in non-document inputs and
  document-source-based in the editor. Source units and terminal columns are
  never interchangeable. Atomic composer references remain indivisible.
- Pointer capture and a retained keyboard selection are different lifetimes.
  Hover/release cannot extend or clear a keyboard range; typing/paste, new
  content, owner replacement and capture cancellation end only the pointer
  gesture. Every composer mount forwards to TextArea's gesture owner; there
  is no parallel press/motion implementation in the input model.

The keyboard boundary/collapse model follows established
[CodeMirror character selection](https://github.com/codemirror/commands/blob/main/src/commands.ts)
and [VS Code cursor movement](https://github.com/microsoft/vscode/blob/main/src/vs/editor/common/cursor/cursorMoveCommands.ts).

## Cursor

- Every product text cursor is a blinking reverse-video block overlay. Never
  paint `|`, `│`, a bar, or an underline cursor.
- The cursor preserves the grapheme under it and toggles `cell.AttrReverse`;
  at end-of-input it reverses the existing blank cell. It never inserts a
  glyph, shifts placeholder text, or consumes layout width.
- Native TextField/TextArea and editor paths share `ui.PaintBlockCursor`
  semantics. Embedded terminal applications may control blink/steady timing,
  but DECSCUSR shape requests are normalized to the same block geometry.
- Cursor visibility derives from the App-owned Unix-millisecond frame clock;
  floating Hosts receive the same frame through `ComponentWidget`. Focused
  frame-sensitive windows alone invalidate on idle ticks. `ColorCursor` maps to
  the softer muted palette before reverse-video, never full foreground white.

## Clipboard

- **Bracketed paste (`tea.PasteMsg`) is the primary paste mechanism.**
  Ctrl+V (KeyPressMsg) is a fallback: the control emits `ActionTextPaste`, the
  clipboard is read off Update, and `nativePasteDelivery` re-enters the text as
  a `tea.PasteMsg` through the exact component that asked (dropped when it is
  unmounted or replaced), so composites observe Ctrl+V exactly like a bracketed
  paste. Never insert into the leaf directly: wrappers would miss the change.
- Copies write the system clipboard; when it is unreachable or the session is
  SSH they also emit an OSC 52 terminal clipboard write
  (`rendering.CopyToClipboardCmd`, the async write lane). A failed read names
  the terminal's own paste shortcut (`ui.ClipboardReadUnavailable`).
- **Ctrl+C is copy, NOT back.** Never reuse Ctrl+C for navigation.
  Esc is the universal back.
- **One owner for text selection and copy**: `ui.TextClipboardTarget` +
  `textClipboardChord` in `tui/ui/text_clipboard.go`. Every text control
  (TextArea, TextField, and the composites that wrap them) accepts Ctrl+C and
  Ctrl+Shift+C (copy the selection), Ctrl+X (cut the selection, or the whole
  value when nothing is selected) and Esc (drop the selection, then fall
  through). A mouse drag ends pointer capture on release; an editable control
  keeps the selection for editing and never writes the clipboard, while a
  read-only control is a reading surface that copies on release and drops the
  highlight (like the chat transcript and terminals). Hosts add policy on top:
  the chat composer clears its draft on an unhandled Ctrl+C, and its Esc ladder
  is selection → draft → cancel the turn.
- **Line breaks and commits use the semantic chords** in `tui/ui/chords.go`:
  where Enter sends (the composer, single-line areas) the newline chord
  (Shift+Enter, Alt+Enter, Ctrl+J) breaks the line; in multi-line areas the
  commit chord (Ctrl+Enter, Alt+Enter) submits. Captions come only from
  `ui.FormatShortcut(token)`, resolved from the terminal keyboard profile, so a
  hint never names a key the terminal swallows.
- **Whole-text Copy/Cut in context menus** comes from
  `App.textClipboardMenuSection` (`tui/viewmodel/text_clipboard_menu.go`): the
  composer menu acts on the draft, and any text control under a right-click
  (popup forms, filters, search fields, question-deck write-ins) gets the same
  section. Cut must flow through the owning component's `Handle`, never by
  mutating the inner editor directly, so composite models stay in sync.
- **Masked controls (`TextSecret`) refuse every clipboard command**, so a
  password/API-key field can never be exfiltrated through the clipboard.

## Composer

- Composer height is a viewport, never the draft's total row count: grow from
  one to six text rows, apply the host's tighter one-third-column budget on short
  grids, and scroll internally without trimming text. Paint a quiet visible-row
  range when clipped. Unchanged layout must not reset wheel scroll; cursor
  navigation/edits and real geometry changes alone reveal the caret.
- Composer pastes attach at 4 KiB, or eight logical lines with at least 512 bytes
  of trimmed payload, or ten wrapped rows with at least 1 KiB of trimmed payload.
  Wrapped checks use the actual text column with a 40-column evaluation floor so
  a tiny terminal cannot promote a little text. Short prose and small lists stay
  literal; typing/resizing/restoring a draft never promotes it automatically.
  Bracketed paste and image-first Ctrl+V share this policy.
  Persist large pastes off Update, preserve gesture order, block Send until
  persistence completes, and fence results to session/agent/workspace/draft.
  A failed or unbound store never falls back to dumping the wall into the input.
  Inline and file-backed pasted text share Pasted Text labels and native
  click-to-view/edit; keep the composer mounted underneath. Reference loads and
  saves are off-UI, complete within the 64 MiB read bound, epoch/preimage-checked,
  and keep edits visible on failure. Projection locks never cover attachment I/O.
- Clipboard images use binary `CreateRequest.Data` throughout the connected/local
  capture path, never Base64 text in `Content`. Validate MIME/signature before
  storing. Faulty already-stored image captures are explicitly excluded from
  preparation with durable context evidence and a user warning, preserving the
  original input, good attachments and files while work continues. Ownership,
  permissions and storage failures remain fail-closed; never replay old effects.
  Connected image admission reuses its already-committed recovery envelope at run
  start. Token estimation decodes each multipart message once and never counts
  Base64 payload as text. Ctrl+V and bracketed paste are exercised with both inline
  and multi-megabyte chunked images under the race detector, retaining exact bytes
  once in the provider request and the existing user-visible completion deadline.
- Attachment paint and hit testing share `ReferenceChips` geometry. Filename
  bodies open; separate three-cell remove targets only detach the draft.
  External opens run off the UI thread through `fileio.OpenDesktopPath` (native
  Windows associations, macOS `open`, Linux `xdg-open`). History media is copied
  into the session before external opening, so viewers cannot edit snapshots.
- Prompt history entries retain exact chip spans and immutable attachment
  snapshots, not session-local labels alone. `history.Store` is the sole recall
  source; input emits owner/generation-keyed recall events, and
  `workspace.ReferenceStore.RestorePrompt` imports session-owned media off the UI
  thread. A late result never overwrites a newer draft. Missing media fails
  closed; history eviction must not delete a session's restored attachments.
- Composer session activation owns reference binding and draft restoration,
  never Paint. Warm sessions retain their complete editor; cold loads are
  activation/revision-fenced and cannot overwrite explicit history recall.
  Bind the destination store before recalled IDs, preserve literal bracketed
  prose, flush inactive queued drafts, and never let a stale paste completion
  consume another session's numeric paste ID or release its Send barrier.
- Permission, question and tool-projection lifecycle events never own the chat
  composer. Opening or completing a blocking deck/overlay may move focus but
  must preserve the exact draft text, attachment references, cursor, selection,
  undo state and history position. Only explicit send/clear/cut actions consume
  those values; `blocking_complete` must never reset the input model.
  Docked agent questions/confirmations are pending continuation, not global shell
  modality. Deliberate composer focus may edit a future draft, but plain Enter
  cannot send or steer while that prompt remains unresolved. Slash commands and
  newline editing remain available. Question write-ins use the same multiline
  editor; their commit chord advances to review and never bypasses explicit Submit.
- Submitted-input receipts also fence draft/editor state, owner epoch, generation
  and workspace. Cursor, selection, scroll, references and undo/edit ABA changes
  protect a newer draft from consumption. Stop preserves accepted input in chat
  and context, never in the composer. Exact-owner late semantic evidence remains
  static after terminal acknowledgement; only a new runtime generation reactivates
  work. See `docs/reference/interaction-matrix.md`.
- Connected Enter uses one core admission path for both normal input and steering.
  The execution owner chooses steering, queued input or a fresh turn; a lagging
  shell cannot send to an already-completed inbox. Steering acceptance requires
  the exact immutable event/run/epoch receipt, and storage/transport uncertainty
  never causes an alternate send or consumes a newer draft.
