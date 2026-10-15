# LSP Acceptance

> Status: IN PROGRESS — the lazy LSP manager, language catalog, settings and lifecycle fencing are implemented and tested; process containment and installed-server acceptance remain.

The current contract is in [editor and language servers](../reference/editor.md#language-server-transport)
and [settings](../reference/settings.md#lsp). This plan lists only what is still
required before LSP is accepted as complete.

## Implemented and guarded

- Enabling LSP on an unused workspace starts no server and sends no `didOpen`
  (`TestLSPEnabledUnusedWorkspaceDoesNotOpenSources`).
- Document lifetimes are lazy; stop/restart retains open documents and rejects
  stale diagnostics; the server budget waits for owned shutdown; disable cancels
  a pending start; idle servers are evicted; roots follow language markers inside
  the workspace boundary (`workspace/lsp_lifecycle_test.go`).
- The catalog covers Go, Rust, Python, TypeScript/JavaScript, HTML, CSS, C/C++,
  C# and Zig plus custom stdio servers (`types/language_servers.go`).
- A dead connection is marked failed and needs an explicit restart.

## Remaining

1. **Process-tree containment.** Stopping or reaping a server must terminate its
   whole subprocess tree (Unix process group, Windows Job Object), matching the
   background-command contract in [background commands](../reference/background-commands.md).
2. **Installed-server acceptance.** With real servers installed, verify semantic
   errors appear and clear after correction, completion/navigation/rename work,
   resource usage stays within the configured limits, and teardown leaves no
   processes. Bound the test load so it does not overload the host.
3. **Diagnostics columns.** Confirm incoming diagnostics are converted from UTF-16
   exactly once (`Editor.SetDiagnostics` performs the conversion) across every
   delivery path, including connected shells.
4. **Visual acceptance** of Settings → Code Intelligence and the Language Servers
   list → detail pages at desktop and constrained sizes.

Delete this plan when all four are complete and the references describe the
result.
