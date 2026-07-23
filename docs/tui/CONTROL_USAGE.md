# Control Usage Map

Maps current interactive control categories to their implementations and the
target replacement under `tui/ui/`.

## Text entry

| Category | Current implementations | Target |
|----------|------------------------|--------|
| Full input engine | `tui/components/text.go` via chat, composer, overlay, find/replace | `ui/control.TextField` / `TextArea` |
| Project search query | native `ui.SearchField` in `tui/screens/findinfiles` | same |
| Form field editor | `tui/form/item.go` TextItem; native workspace/session/question/background-detail fields | `ui/form` field over shared engine |
| Hand-built prompt | `tui/widgets/textprompt.go` | `PromptField` |
| Append/backspace filter | palette, launcher, settings, list filter | `FilterField` / `SearchField` |
| File/workspace picker filter | native `ui.TextField` in `tui/screens/filepicker` | same |
| Document editor | `tui/editor/` PieceTable | Specialized `DocumentEditor` (kept) |

## Buttons

| Current | Target |
|---------|--------|
| `components.ButtonGroup` | `ui/control.Button` + `ButtonGroup` |
| `form.Form` button row | `ui/form.ActionBar` |
| `PopupButton` / `PopupButtonRow` | same Button + ActionBar |
| Agent question actions | native `ui.ActionBar` in `tui/screens/question` | same |
| Tool confirmation / YOLO actions | native PanelDeck `ui.ActionBar` with typed quick keys and Escape actions | same |

## Collections

| Current | Target |
|---------|--------|
| `components.List` | `ui/collection.List` |
| Command palette list | sectioned VirtualList |
| File tree | `ui/collection.Tree` |
| Message list virtlist | `ui/collection.VirtualList` |
| Context menu | `ui/overlay.ContextMenu` |
| Tabs | `ui/control.Tabs` |
| File picker entries | native `ui.List` |
| Find in Files results | native `ui.VirtualList` + `ui.ScrollbarControl` |
| Launcher workspaces | List + SearchField |
| LSP popup lists | List |
| Agent question choices | native Form select/multi-select collection | List-backed form collection |

## Scrollbars

| Current | Target |
|---------|--------|
| `tui/scrollbar.ScrollBar` | `ui/collection.Scrollbar` (themeable) |
| Find in Files inline glyphs | delete |
| Launcher inline glyphs | delete |
| File picker | native `ui.ScrollbarControl` |
| Find in Files | native `ui.ScrollbarControl` over `ui.VirtualList` |

## Chrome

| Current | Target |
|---------|--------|
| `widgets.PopupChromeLines` | `ui/overlay.WindowFrame` |
| `wm` decoration (deleted) | `ui.WindowFrame` |
| Hand-drawn ╭─╮ in LSP popups | same WindowFrame |
