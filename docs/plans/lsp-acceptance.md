# LSP Acceptance

> Status: IN PROGRESS - lazy lifecycle and observable owned group/Job teardown are implemented; native-platform containment acceptance, installed-server workflows and the remaining gates are open.

The current contract is in [editor and language servers](../reference/editor.md#language-server-transport)
and [settings](../reference/settings.md#lsp). This plan lists only what is still
required before LSP is accepted as complete.

## Implemented and guarded

- Enabling LSP on an unused workspace starts no server and sends no `didOpen`
  (`TestLSPEnabledUnusedWorkspaceDoesNotOpenSources`).
- Settings Save → replicated idle guidance → source open → rendered diagnostics,
  including enabling with an already-open source, is guarded by
  `TestConnectedLSPEnableSaveExplainsIdleAndStartsOpenSource`. Idle, disabled,
  paused, manual-start and unsupported-file explanations are manager-owned
  (`workspace/lsp_idle_test.go`).
- Document lifetimes are lazy; stop/restart retains open documents and rejects
  stale diagnostics; the server budget waits for owned shutdown; disable cancels
  a pending start; idle servers are evicted; roots follow language markers inside
  the workspace boundary (`workspace/lsp_lifecycle_test.go`).
- Nested process/transport closers preserve joined errors and exact identities;
  the workspace retains unverified process reservations instead of releasing
  them. `make test-lsp` covers the otherwise excluded replacement module.
  Root startup/stop/pipe regressions and focused race checks cover failure
  propagation. Unix group escape limits are explicit; cross-compiling the
  Windows/macOS owners is not native behavioral acceptance.
- The catalog covers Go, Rust, Python, TypeScript/JavaScript, HTML, CSS, C/C++,
  C# and Zig plus custom stdio servers (`types/language_servers.go`).
- A dead connection is marked failed and needs an explicit restart.
- Agent code intelligence: the session-bound `lsp` tool and post-edit
  diagnostics notice, editor/probe document holders, progress-settled
  diagnostics, and human-owned launch settings. Real `gopls` coverage:
  `workspace/lsp_probe_gopls_test.go` (skipped when `gopls` is absent);
  session wiring: `session/code_intelligence_test.go`.

## Remaining

1. **Native process-tree acceptance.** Exercise the existing owner and reported
   failures on supported native platforms. Verify stopping/reaping the owned
   group or Job, neighbor isolation, cancellation, natural exit and partial
   startup cleanup, matching the
   background-command contract in [background commands](../reference/background-commands.md).
2. **Installed-server acceptance.** With real servers installed, verify semantic
   errors appear and clear after correction, completion/navigation/rename work,
   resource usage stays within the configured limits, and teardown leaves no
   processes. Bound the test load so it does not overload the host.
3. **Diagnostics columns.** Confirm incoming diagnostics are converted from UTF-16
   exactly once (`Editor.SetDiagnostics` performs the conversion) across every
   delivery path. Nonidentity tab/CJK/combining/emoji fixtures now cover both
   source-line endpoints, raw caches, three connected shells, cached open,
   first-error/F8, async definition/reference/status navigation and later-focus
   preservation. Keep real installed-provider/carrier and native acceptance
   evidence distinct from those deterministic rendered/App regressions.
4. **Visual acceptance** of Settings → Code Intelligence and the Language Servers
   list → detail pages at desktop and constrained sizes.

Delete this plan when all four are complete and the references describe the
result.
