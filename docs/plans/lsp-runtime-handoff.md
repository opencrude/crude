# LSP and runtime repair handoff

> Status: ACTIVE HANDOFF — LSP work paused by the user for urgent agent-runtime repair on 2026-10-08.

## Current priority

Human steering `event_3bf33effa5a354b42990451ee252f32e4847cd05853724264d217a654de2bebf`,
`event_5f35b3a359867f834a9c422d0b3c50f766b4e3aff1adc1f25bbc4391a543256f`, and
`event_10291eaa35d81827766cd927467dfa62076bcb5607a9ce8094b91f203b362e19` addressed
run `run_aef93ecda3633c4678596fc8f3caf6ed`: fix child checkpoint/reporting failures
and related lifecycle defects first, prepare a restart, then continue LSP work.

Runtime fixes live under `agent/`, `session/`, `sessiondb/` and `prompt/`, with
contracts in [adaptive context](../reference/adaptive-context.md),
[agent orchestration](../reference/agent-orchestration.md), and
[run recovery](../reference/run-recovery.md). Regression coverage includes real
SQLite checkpoint publication for native and ordinary parallel rounds, passive
interruption, exact late controls, admission failure, mandatory child reporting,
and model-native tool selection.

The original restart artifact is `build/runtime-repair/crude`; its adjacent README records
the exact restart command, source archive/hash manifest, validation and limitations.
That build preserves the earlier tab-focus fix but excludes unfinished LSP edits.

The 2026-10-08 temporary source-build repair adds the missing launch spec, settings
clone, existing-catalog resolver and bounded resource defaults. `go build ./...`
and the agent/session/sessiondb/prompt suites pass in the main worktree again;
`go run .` no longer requires the isolated artifact. Keep LSP disabled until the
unfinished feature below is validated. No session data or child runs were changed.

## Preserved unfinished LSP work

The worktree intentionally retains an incomplete LSP integration. Do **not** claim
the LSP feature is complete or treat the temporary source-build repair as LSP
acceptance. The expanded catalog/settings and lifecycle tests remain unfinished.
The isolated runtime repair build excludes these edits while retaining the
pre-existing tab-focus fix.

- `workspace/lsp_manager.go`, `lsp_types.go`, `lsp_lifecycle.go`, `lsp_roots.go`,
  `lsp_load_regression_test.go`, and LSP portions of `workspace.go`: draft lazy
  manager, serialized process admission, open-document tracking, resource limits,
  idle eviction, project-root selection and lifecycle fencing.
- `tui/viewmodel/lsp_runtime.go`, `lsp_handlers.go`, `launcher.go`, and LSP hunks
  in `app.go` and `editor_tabs.go`: draft integration. The latter two also contain
  unrelated pre-existing tab-focus changes that must be preserved.
- `third_party/powernap/pkg/lsp/client.go`, `tui/editor/lsp_integration.go`,
  `types.go`, `editor_test.go`, `lsp_protocol_test.go`: Luke's partial protocol
  implementation. Review before integrating; incoming diagnostics currently
  risk double column conversion because `Editor.SetDiagnostics` expects UTF-16.
  Subprocess-tree containment remains unfinished.
- Clara produced no catalog/settings files before the checkpoint failure. The
  temporary source repair supplies `types.LSPServerSpec`, `CloneLSPSettings`,
  `ResolveServer`, and resource fields using the existing language catalog only.
  Existing LSP tests still target the old runtime factory/manager internals and
  block the full test/lint gates; integrate them during the separate LSP work.

### Child evidence and identities

- Luke: `agent-cfb6f7ccdddb70324e81a687f8377d0e`, cancelled run
  `run_5e3d2238c631fd6a1f7a20f0954c77f3`, suspended availability.
- Clara: `agent-288f7f61b06dc65ac1d166e5354b89f9`, cancelled run
  `run_9f4ddb274311a9b0b98427ca0aa2081e`, suspended availability.
- Their databases and accepted transcripts remain under this session's
  `subagents/` directory. Both SQLite quick checks passed after pause. Do not
  relabel these cancelled attempts as completed or automatically replay them.
- June's completed research report is
  `artifact_40cee101fffe12357b332916a990cac82cf3e6b7faab3ce03f21abf6de65e5ca`, revision 1.
  It includes official server configuration sources and explicit limitations.

## LSP acceptance still required

1. Reproduce enabling an unused workspace without any server startup or fabricated
   `didOpen`. The original implementation opens every source file (1,775 Go files
   in this repository, including replaced modules and alternate-platform sources).
2. Finish central language catalog and persisted native settings for Python,
   Zig, C/C++, C#, Rust, Go, JS/TS, HTML/CSS and custom external stdio servers.
3. Review open/close/start/stop/settings races, worker shutdown, diagnostics clearing,
   max-server admission and idle policy. Do not lose retained documents when stopping
   one server; Stop/Restart must work without reopening tabs.
4. Correct scoped initialization/configuration and root/toolchain/environment
   detection. Never globally exclude `third_party`, which contains Go replacements.
5. Verify actual semantic errors, corrected errors, completion/navigation, resource
   usage and teardown with installed servers; bound tests to avoid host overload.
6. Run full build/test/lint/race, documented visual checks for settings, and update
   current LSP documentation. Remove this handoff when integration is complete.
