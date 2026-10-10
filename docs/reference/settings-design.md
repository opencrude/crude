# Settings Design Language

> Status: **Authoritative** for every Application/Workspace Settings page and its
> editors. Reviewers reject a page that invents its own anatomy, control dialect,
> unit spelling or save model. Update this document before introducing an
> exception.

Settings is used rarely and in a hurry. Every page therefore has the same shape,
every value reads as a sentence, and nothing asks the reader to know a wire
format. The reference implementation is `tui/settings/surface.go` (anatomy),
`tui/ui/form_field.go` (controls) and `tui/settings/language_server_catalog.go`
(a list → detail page).

## Page anatomy

```
 Search settings  ⌕ │ Title  12                         ← accent title, muted count
 ───────────────── │ One sentence saying what this page is for.
 AI                │
  Accounts         │ Section ─────────────────────────────
  Agents         ● │  Label                  ● On
  …                │  Label                  [ 300 ]  ms
                   │  Label                  Errors  Warnings  Info  Hints
                   │
                   │ Full explanation of the focused field, wrapped.
 ──────────────────┴─────────────────────────────────────────────────
  Application  Workspace           ● Unsaved changes  [ Save ]  [ Close ]
```

- **Header (three rows)**, painted by the surface for every page, list or form:
  title, subtitle, one breathing row. Editors show a breadcrumb trail
  (`Language Servers ▸ Go (gopls)`): parents are muted and clicking the first
  segment goes back; the current page is accent and bold. Collections show their
  size as a muted count after the title. Catalogs never paint their own title.
- **Body**: one row per field, grouped by separator sections. Descriptions are
  never painted beside controls (they used to be clipped with `…`).
- **Help strip**: the focused field's description (or its validation errors, in
  error ink) is wrapped in full below the body, two lines when height allows.
  Hovering a field previews its help. `Form.HelpField` and `paintFieldHelp` are the
  only implementation; list → detail editors reserve the same strip above their
  action row.
- **Footer**: the Application/Workspace switch on the left; on the right a status
  (`● Unsaved changes` in warning ink, `Saving…`, or the save error) followed by
  `[ Save ]` and `[ Close ]`. Save is enabled only while something is unsaved.
  The second button is always **Close** — closing with unsaved work opens the
  existing discard decision. Accounts keep their own entity transaction and
  replace the footer actions with `[ Save changes ]  [ Back ]` while editing.
- **Rail**: categories grouped under quiet headings. A page holding unsaved edits
  shows a warning-ink `●` at its right edge, so work is never lost off-screen.

## Controls

One affordance per kind; `ui.FieldType` documents the contract.

| Value | Control | Example |
|-------|---------|---------|
| On/off | `BoolFieldState` / `ToggleFieldState` → switch | `● On` (accent) / `○ Off` (muted) |
| 2–6 short choices | `SegmentedFieldState` | ` Errors   Warnings   Info   Hints ` — selected is accent+bold on the selection fill; a click selects that segment directly |
| Many choices | `SelectFieldState` dropdown, or a `‹ value ›` cycle when previewing live (theme) | `‹ dark ›` — `‹` steps back, `›` and the value step forward, Left/Right on the keyboard |
| Number | `NumberFieldState` (unit lifted into the field) | `[ 300 ]  ms` — short, content-sized box; the unit is muted |
| Text | `TextFieldState` | readable-width input (≤ 56 columns), never a pane-wide slab |
| Name → value list | `KeyValuesFieldState` with `Placeholder` (append row) and two `Columns` (captions) | `Variable │ Value`, `Add variable` |
| Drill-in | `FieldNav` | `Language servers      11 of 12 on ▸` — label in the label column, summary + `▸` in the value column |

Rules:

- Segments fall back to the `‹ value ›` stepper automatically when they do not fit.
- A cycle never jumps the wrong way: clicking `‹` always selects the previous
  option. Never paint `←`/`→` arrows across the full control width.
- Units are never written into labels (`Idle timeout (seconds)`); `liftUnit`
  converts any remaining parenthesised unit. Sizes use the unit people think in
  (MB/KB, not bytes) and keep exact stored bytes when untouched.
- Lists are typed the way people write them: shell-style arguments
  (`--stdio "a path with spaces"`, `splitShellWords`/`joinShellWords`),
  comma-separated extensions and markers, name/value tables for environment,
  headers and rules. JSON is reserved for values that are JSON by protocol
  (LSP `settings`, `initializationOptions`).
- Copy: labels are short nouns or verbs ("Recall memories", "Skip files over");
  descriptions say what happens, in the second person where natural. No internal
  identifiers in labels.

## Collections (list → detail)

Accounts, Agents, Language Servers and the Skills/MCP/Sources store share one
rhythm: header, toolbar row (search first, then filters), the collection, a
status row, then a left-aligned action row (`[ Add … ]` primary first,
destructive last). Opening an item replaces only the right pane and adds a
breadcrumb; Esc, the breadcrumb parent and `[ Back … ]` return with selection,
query and scroll retained.

Language Servers edits apply to the settings draft immediately (the rail marker
and Save follow); an incomplete custom server stays open with guidance instead of
being half-saved, and Save refuses an invalid inventory with the reason.

## Skills and MCP store

- **Live search.** Typing arms a 350 ms quiet period (`capabilitySearchInput`,
  `CapabilitySearchTickMsg`); only the newest generation for the exact settings
  model may search. A newer live search supersedes an in-flight one
  (`CapabilityRequestMsg.Supersedes`), so results always match the query. Enter
  still searches immediately. Changing the Source filter or turning Installed off
  re-arms the search; Installed-only never touches the network.
- **Loading** uses the shared `ui.ProgressRule` beneath the toolbar — the same
  ink tide as the launcher — and the empty state reads "Searching your sources…".
  The window stays frame-sensitive only while that search runs.
- **Toolbar**: search field, then the `○ Installed` chip (`ToggleButton.SetChip`)
  and `[ All sources ▾ ]  [ Most relevant ▾ ]` choice buttons on the same row
  when wide, wrapped below when narrow.
- **Cards**: selection is a quiet selection fill with an accent edge (`▎`) and
  accent title; the card's own `[ Install ]`/`[ Manage ]` stays legible.

## Removed on purpose

- The Font page (no consumer read it) is no longer offered.
- `LSPSettings.Servers`/`ServerArgs` were a second, extension-keyed server
  authority beside per-language definitions; Languages is the only one.
- The Code Intelligence page's raw "Custom servers / advanced JSON" editor is
  replaced by the Language Servers list and per-server editor.
