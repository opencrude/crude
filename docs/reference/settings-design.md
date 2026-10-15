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
  Agents         ● │ ▎Label                 [ ‹ On  › ]      ← focused: accent edge + label
  …                │
                   │  Label                 [ 300      ]  ms
                   │
                   │  Label                  Errors Warnings Info Hints
                   │ What the hovered (else focused) setting does.
 ──────────────────┼─────────────────────────────────────────────────
  Application  Workspace │ Page notice            ● Unsaved  [ Save ]  [ Close ]
```

- **Header (three rows)**, painted by the surface for every page, list or form:
  title, subtitle, one breathing row. Editors show a breadcrumb trail
  (`Language Servers ▸ Go (gopls)`): parents are muted and clicking the first
  segment goes back; the current page is accent and bold. Collections show their
  size as a muted count after the title. Catalogs never paint their own title.
  Detail pages use the subtitle for their install state and description.
- **Body**: one row per field with one breathing row between fields, two before
  a section heading and one after it. Every settings form is built by
  `newSettingsForm`; no page sets its own gap or description policy.
- **Field help** reads in the body's last row, the breathing row directly
  above the footer rule (`WorkspaceSettingsSurface.paintHelp`): the hovered
  field's description, otherwise the focused field's, or its validation errors
  in error ink. One row, ending with `…` when longer; it takes no body rows and
  nothing moves when it changes.
  Write one useful sentence that fits an ordinary 80-column content pane;
  retain behavior and important constraints without turning help into a
  paragraph. Narrow panes elide at their actual width.
- **Footer (rule + one row)**, owned by `WorkspaceSettingsSurface.layoutFooter`
  and resolved per page by `footer()`: the Application/Workspace switch, a `│`
  separator continuing the rail divider (joined to the rule with `┼`), the page
  notice (a pending decision, an error, an operation's progress), then
  `● Unsaved changes`/`Saving…`/the save error and the buttons. Field help is
  never footer content.
- **Decision buttons live in the footer**, and `[ Close ]` is always the
  rightmost one, on every page; closing with unsaved work opens the discard
  decision. Preference pages show `[ Save ]` (enabled only when dirty) before
  it. Every editor or detail page puts its own decisions there instead of Save,
  primary first and Back last: `[ Save changes ] [ Back ]` (account),
  `[ Apply agent ] [ Back ]`, `[ Restore default ] [ Done ]` (language server,
  edits apply live), `[ Install ] [ Back ]` (capability detail). Page operations
  (Read file, Connect, Disable, …) stay in the body above the footer.
- **Rail**: categories grouped under quiet headings. A page holding unsaved edits
  shows a warning-ink `●` at its right edge, so work is never lost off-screen.
- **Navigation** has one path, `openCategory`, shared by rail clicks and search
  Enter: a clean editor closes, a dirty one asks first and the navigation resumes
  after the decision. Hover and wheel over the rail never navigate.

## Controls

Every control is a box that starts on the form's value column; text inside a box
starts one cell in. Rows are never filled: focus is an accent `▎` edge plus an
accent bold label, and hover/focus light only the control's own box. Inputs,
dropdowns, steppers, drill-ins, tables and text areas share one width (capped at
60 columns), so their right edges line up; numbers are short boxes; switches and
segments are content-sized. Multi-line editors start on their label's row.

| Value | Control | Example |
|-------|---------|---------|
| On/off | `BoolFieldState` / `ToggleFieldState` | `‹ On ›` / `‹ Off ›` — a compact box in the stepper's language, sized to its widest state. Never `●`/`○`. Clicking anywhere on the row flips it |
| 2–6 short choices | `SegmentedFieldState` | ` Errors  Warnings  Info ` — one pill; the selected segment is accent-filled with contrast ink; a click selects that segment |
| Many choices | `SelectFieldState` dropdown, or a `‹ value ›` stepper when previewing live (theme) or stepping an ordered scale | `‹      30s (default)      ›` — value centered, arrows on fixed columns |
| Number | `NumberFieldState` (unit) or `Numeric` | `[ 300      ]  ms` — short box, muted unit |
| Text | `TextFieldState` | shared wide box |
| Name → value list | `KeyValuesFieldState` with `Placeholder` (append row) and two `Columns` (captions) | captions on the table's own fill, `+ Add variable` |
| Drill-in | `FieldNav` | a box: `11 of 12 on                 ▸` |

Rules:

- Steppers are ordered scales: the left half steps back, the right half forward,
  and the ends **hold** (the spent arrow dims) — Left/Right keys too. Ladders are
  built in natural order with the default at its own value and Unlimited last
  (`dialFieldOptions`), so `‹` always means less.
- Clicking beside a stepper or segments (the label column) only focuses the row.
- Segments fall back to the stepper automatically when they do not fit.
- `DescriptionImportant` (a description painted under its control) is reserved
  for exceptional per-field state — a denied tool, a catalog disagreement.
  Routine explanations are the focused field's inline help.
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
- Filter chips (Installed) are padded labels that take the selection fill and
  accent ink while on — no glyph.
- Tables share the list ink: a focused or selected row carries every cell in the
  row's own ink, so secondary columns stay readable on the fill.
- Tab strips: a click selects the page and hands the keyboard to it; the heavy
  focus baseline appears only after deliberate keyboard navigation.

## Model capabilities

The model catalog is installed only in the core process. Agent editors therefore
prefetch their account's model list through the core when they open and whenever
the account changes (`Model.prefetchModels`); capability rows fill when it
arrives, without opening the dropdown. A catalog update refetches loaded lists
through the core instead of re-deriving cards in the catalog-less shell.

## Collections (list → detail)

Accounts, Agents, Language Servers and the Skills/MCP/Sources store share one
rhythm: header, toolbar row (search first, then filters), the collection, a
status row, then a left-aligned action row (`[ Add … ]` primary first,
destructive last). Opening an item replaces only the right pane and adds a
breadcrumb; Esc, the breadcrumb parent and `[ Back … ]` return with selection,
query and scroll retained.

Collection pages do not reserve form-help rows: Accounts, Agents, Skills,
MCP, Language Servers and Sources anchor their last action row immediately
above the shared footer rule. Status stays above the actions, never below
them; wrapping actions on narrow panes does not introduce blank bottom rows.

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
  While that search runs the surface reports `NeedsAnimation`, which moves the
  shell to frame-rate ticks (`wantsFastTick`); a caret blink alone never does.
- **Toolbar**: search field, then the `Installed` filter chip (`ToggleButton.SetChip`)
  and `[ All sources ▾ ]  [ Most relevant ▾ ]` choice buttons on the same row
  when wide, wrapped below when narrow.
- **Cards**: selection is a quiet selection fill with the `FocusEdge` glyph and
  accent title; the card's own `[ Install ]`/`[ Manage ]` stays legible. One
  breathing row separates the results from the toolbar and from the status row.
- **Detail pages**: read-only text aligns each run of `Key: value` lines on one
  value column; an empty Tools/Resources/Prompts collection says why it is empty.

## Removed on purpose

- Font settings are removed: a terminal application cannot choose the host
  terminal's font. Icon glyph compatibility stays on the Nerd Font toggle.
- `LSPSettings.Servers`/`ServerArgs` were a second, extension-keyed server
  authority beside per-language definitions; Languages is the only one.
- The Code Intelligence page's raw "Custom servers / advanced JSON" editor is
  replaced by the Language Servers list and per-server editor.
