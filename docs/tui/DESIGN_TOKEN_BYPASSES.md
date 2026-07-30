# Design Token Bypasses

Allowlisted locations that currently bypass the theme system. Phase 0 freezes
this list: CI rejects new matches outside this allowlist. Each migration removes
entries until the list is empty.

## Hard-coded colors (representative)

| Location | Issue |
|----------|-------|
| `tui/theme/theme.go` | Some styles hard-code hex instead of Colors fields |
| `tui/diffview/style.go` | Full independent palette |
| `tui/filetree/icons.go` | Fallback icon colors (migrated to theme tokens; no longer on hex allowlist) |
| `tui/chatprojection/` | message row paint uses theme tokens (replaces deleted `widgets/chatmessage.go`) |
| `tui/editor/render.go` | raw ANSI diagnostic underlines |

## Parallel style pipelines

1. Theme `lipgloss.Style` fields
2. Inline `lipgloss.NewStyle()` from tokens
3. `cell.Color` / `cell.Attrs`
4. Raw ANSI strings
5. Chroma token styles
6. Glamour Markdown styles

## Glyph inconsistencies

| Role | Competing glyphs |
|------|------------------|
| Close | `×` vs `✕` |
| Cursor | `█` vs `▏` |
| Scrollbar | `░`/`█` vs `│`/`┃` |
| Status | `●`/`○`/`✓`/`✗` variants |

## Migration rule

New code must use semantic tokens only. Temporary adapters require an explicit
manifest entry and a deletion phase. See `bypass_manifest.json`.
