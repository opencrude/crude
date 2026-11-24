# Immediate action: unreliable LSP completion

> Status: IN PROGRESS. The lost manual request is reproduced. No fix has landed.

On 2026-11-23 the user reported that suggestions sometimes disappear entirely.
Even **Ctrl+Space** can do nothing when it worked a second earlier. This is not
only a question of when automatic suggestions open or how the editor paints them.

## Reproduced: Crude drops Ctrl+Space while an edit is in flight

A temporary test used the real editor key route with a recording completion port.
It turned off automatic suggestions to isolate the shortcut:

1. Press Ctrl+Space on acknowledged text. One request reaches the port.
2. Type a character, hold back its edit receipt, then press Ctrl+Space again.
   Nothing reaches the port while the document is unacknowledged.
3. Deliver the receipt without moving the caret. The request count stays at one:
   Crude never sends the second invocation. Press Ctrl+Space again at the same
   caret and the count increases.

The test expected two requests after step 3, before the final keypress, and
failed with one. This proves an editor-side loss before any server can answer.
It does not identify every intermittent miss. The existing connected Pyright
test passed for ordinary names and member access once the server was ready;
it does not cover that manual timing boundary.

[Manual completion](../../tui/viewmodel/lsp_completion.go) requires the visible
source to be acknowledged at the instant the key arrives. It returns if the
source is not ready. [Automatic completion](../../tui/viewmodel/lsp_completion_automatic.go)
already keeps a bounded input intent until its exact edit is acknowledged.

## Other findings

- **Both paths can discard a reply after an unrelated workspace event.** The
  [core completion fence](../../workspace/lsp_completion_request.go) and
  [editor presentation](../../tui/viewmodel/lsp_completion.go) require the
  workspace-wide LSP context generation to match. A watched-file event or
  another server stopping can advance that generation even when the invoking
  document, caret and provider are unchanged. The context advance can retire
  the request or the visible list. This path exists in the code; its frequency
  in the reported workload is not measured. A dependency change within the
  same server and root can still make an answer stale.
- **Document synchronization ignores the server's change mode.**
  [Crude's LSP integration](../../lsp/lspclient/lsp_integration.go) always
  sends whole-document changes. Installed gopls, Pyright, TypeScript Language
  Server and clangd all advertised incremental changes, using both forms of
  `textDocumentSync` (numeric and options object). Direct wire probes got
  completion for the new symbol after both a whole-document change and a ranged
  change on each server. That shows tolerance in these fixtures, not that the
  protocol gap is harmless under load or that it caused this report.
- **Completion negotiation is narrower than the VS Code LSP client.** Crude
  [advertises static completion only](../../third_party/powernap/pkg/lsp/completion.go),
  [rejects lists containing shared item defaults](../../lsp/lspcompletion/completion.go),
  and accepts only documentation and detail from `completionItem/resolve`, not
  deferred additional edits such as imports. Servers that honor the advertised
  capabilities should not send unsupported list defaults. These are
  interoperability gaps, not evidence that any of them caused the reproduced
  timing failure.

References: LSP 3.17 [document synchronization](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#textDocument_didChange)
and [completion](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#textDocument_completion);
VS Code [document synchronization](https://github.com/microsoft/vscode-languageserver-node/blob/main/client/src/common/textSynchronization.ts)
and [completion capability negotiation](https://github.com/microsoft/vscode-languageserver-node/blob/main/client/src/common/completion.ts).
Crude's current behavior is in the [editor reference](../reference/editor.md#completion-documentation-and-original-insertion).

## Immediate action and exit test

1. Retain one explicit completion intent in the existing editor path. Send it
   once when the exact edit is acknowledged and sent to the correct server.
   Cancel on Escape or when the editor, caret, source, or provider that owns the
   request is superseded. Do not query speculative text or allow a stale edit.
2. Reproduce the shared freshness failure with an unrelated root or server
   event, then compare with a real dependency change. Keep the guard for a
   changed source or provider and for relevant project inputs. Do not weaken
   the insertion guard to make a list appear.
3. Audit synchronization mode and the missing completion capabilities with
   real servers before advertising new support. Use the shared protocol path,
   not per-language branches. Keep protocol gaps and proven causes separate.

The regression must type, press Ctrl+Space before the acknowledgement, withhold
and release the acknowledgement, and show suggestions without a second
keypress. It must also show that a superseded invocation never appears or
accepts an edit. Use installed servers through the real editor to verify the
wire order and the visible result for both automatic and manual requests.
Distinguish no request sent, no candidates returned, and candidates rejected
or hidden. Keep the automatic list passive and the editor responsive.
