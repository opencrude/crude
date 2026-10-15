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
  Ctrl+V (KeyPressMsg) is a fallback that emits a `tea.PasteMsg`.
- **Ctrl+C is copy, NOT back.** Never reuse Ctrl+C for navigation.
  Esc is the universal back.
- **One owner for text selection and copy**: `ui.TextClipboardTarget` +
  `textClipboardChord` in `tui/ui/text_clipboard.go`. Every text control
  (TextArea, TextField, and the composites that wrap them) accepts Ctrl+C and
  Ctrl+Shift+C (copy the selection), Ctrl+X (cut the selection, or the whole
  value when nothing is selected), Esc (drop the selection, then fall through),
  and copy-on-release after a mouse drag — never keep a pointer-owned selection
  after the release. Hosts add policy on top: the chat composer clears its
  draft on an unhandled Ctrl+C, and its Esc ladder is selection → draft →
  cancel the turn.
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
- Submitted-input receipts also fence draft/editor state, owner epoch, generation
  and workspace. Cursor, selection, scroll, references and undo/edit ABA changes
  protect a newer draft from consumption. Stop preserves accepted input in chat
  and context, never in the composer. Exact-owner late semantic evidence remains
  static after terminal acknowledgement; only a new runtime generation reactivates
  work. See `docs/reference/interaction-matrix.md`.
