# Control Usage Map

Maps current interactive control categories to their implementations and the
target replacement under `tui/ui/`.

## Text entry

| Category | Current implementations | Target |
|----------|------------------------|--------|
| Full input engine | `tui/components/text.go` via chat, composer, overlay, find/replace | `ui/control.TextField` / `TextArea` |
| Input + custom paint | Find in Files query renderer | `SearchField` |
| Form field editor | `tui/form/item.go` TextItem | `ui/form` field over shared engine |
| Hand-built prompt | `tui/widgets/textprompt.go` | `PromptField` |
| Append/backspace filter | palette, launcher, settings, file picker, list filter | `FilterField` / `SearchField` |
| Document editor | `tui/editor/` PieceTable | Specialized `DocumentEditor` (kept) |

## Buttons

| Current | Target |
|---------|--------|
| `components.ButtonGroup` | `ui/control.Button` + `ButtonGroup` |
| `form.Form` button row | `ui/form.ActionBar` |
| `PopupButton` / `PopupButtonRow` | same Button + ActionBar |
| Confirm/Yolo inline buttons | same |

## Collections

| Current | Target |
|---------|--------|
| `components.List` | `ui/collection.List` |
| Command palette list | sectioned VirtualList |
| File tree | `ui/collection.Tree` |
| Message list virtlist | `ui/collection.VirtualList` |
| Context menu | `ui/overlay.ContextMenu` |
| Tabs | `ui/control.Tabs` |
| File picker entries | List/Tree |
| Find in Files results | VirtualList |
| Launcher workspaces | List + SearchField |
| LSP popup lists | List |

## Scrollbars

| Current | Target |
|---------|--------|
| `tui/scrollbar.ScrollBar` | `ui/collection.Scrollbar` (themeable) |
| Find in Files inline glyphs | delete |
| Launcher inline glyphs | delete |

## Chrome

| Current | Target |
|---------|--------|
| `widgets.PopupChromeLines` | `ui/overlay.WindowFrame` |
| `wm` decoration (deleted) | `ui.WindowFrame` |
| Hand-drawn ╭─╮ in LSP popups | same WindowFrame |
