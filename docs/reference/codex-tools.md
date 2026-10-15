# Codex tool adapters

> Status: Current — native patch and command interfaces integrated with Crude.

## Selection and ownership

`codexio/` is first-party code copied from `fgn/applypatch` commit
`95ed66f6d36a575b00001f2e27aedc1ec7455812`, adapted against OpenAI Codex
`aa380897f67b91e1a47d530d7286d497b6726d3f`. There is no external Go module or
vendor directory. The package retains upstream MIT/Apache-2.0 notices, attribution,
upstream tests, and the 1,558-case recorded Rust-reference corpus.

A case-insensitive `gpt` in the effective model ID selects the native profile.
Each child resolves its own model. `write_file` and `edit_file` are replaced by
one complete `apply_patch` tool. Other models keep their existing editor tools.
Explicit tool and filesystem policies remain separate from the patch language:
a missing standalone `move_file` does not remove move-with-edit from patches.
Read-only roles do not gain filesystem writes. All write-capable roles use the
same engine, not smaller role-specific implementations.

Crude does not embed or select vendor behavioral prompt templates. The root
`prompt/` package supplies the same canonical policy to GPT, Claude, Gemini,
routed and local models; escaped user profile instructions are a lower-priority
overlay. The owner map and development checklist live in
[model prompts and project guidance](prompt-architecture.md). Do not restore
a codexio/prompts/ tree, model-family base-prompt selection, or an `agent/` forwarding
API to implement a native tool. Codex-specific behavior is limited to the tool
and wire interfaces in this document. The native patch grammar and tool
description remain authoritative when `apply_patch` is offered. Prompt caching
remains workspace-scoped and is cleared when the workspace changes.

## Patch contract

On Responses and Codex endpoints, `apply_patch` is a custom/freeform tool with
the upstream name, description, and Lark grammar. Chat Completions, Anthropic and
Ollama endpoints, including third-party gateways, receive an ordinary `apply_patch`
function with a required string `input` parameter; these endpoints reject
`tools[n].custom`. The Chat adapter wraps patch text in JSON on the wire and
unwraps it on receipt. Execution and persisted history still use raw patch text,
so switching between Chat and Responses does not corrupt replay. Chat function
calls do not enforce the upstream Lark grammar server-side; the host validates
the patch before mutation. Other unsupported wire protocols fail explicitly.

The engine supports add, delete, update, move-with-edit, destination overwrite,
multiple operations on the same path, multiple hunks, context/EOF markers,
lenient whitespace and Unicode matching, no-op success, and partial application.
Both upstream reconstruction modes are available; the integrated tool selects
`PreserveLineEndings`. `NormalizeToLf` retains the upstream algorithm, including
its trailing-empty-line sentinel. Syntax is validated before the first mutation.
Incomplete streamed calls cannot execute merely because their text happens to
form a syntactically complete patch.

`fileio.Editor.ApplyCodexPatch` owns the filesystem application under the shared
mutation lease. Paths resolve against the session workspace without rewriting
patch input. Absolute paths are supported. Every committed write/delete records
its exact preimage and postimage under `apply_patch`, including both sides of
move-overwrite and operations preceding a later failure. The existing history
service supplies scoped undo. Unrelated changes and source preconditions remain
protected. Existing symlink/hardlink and tracked-size policies still apply;
remote environment IDs require an actual configured environment rather than
being silently redirected to the local filesystem.

Patch results retain the upstream success summary and add real multi-file diffs
for the transcript. Partial failures display both the error and committed diffs.
No-op successes remain ordinary visible results, not fabricated diff cards.
Human permission grants cover every operand and operation; a grant for an
update does not implicitly approve deletion or an outside move destination.

## Runtime integration

GPT command-enabled agents also receive native `exec_command` and `write_stdin`
interfaces. These use the existing per-agent command manager, with shell/login
selection, pipes or PTY, numeric session IDs, yielding, stdin continuation,
output budgets, cancellation, and shutdown ownership. Unix uses PTY and Windows
uses ConPTY; Windows runtime behavior requires Windows verification beyond
cross-compilation. Existing command tools remain available. Requests for
escalation and interactive input pass through the permission system.

GPT `update_plan` uses the upstream step/status snapshot schema. The adapter
writes the existing declarative plan store; IDs/revisions are host-owned and
removed steps remain cancelled in the audit record. The model does not have to
invent Crude-specific plan IDs to follow its upstream guide.

`multi_tool_use.parallel` uses the same execution and permission pipeline as
individual tools. Chat Completions, Responses, Codex, Anthropic and Ollama all
encode it as the
ordinary function `multi_tool_use_parallel`; OpenAI reserves the `multi_tool_use`
namespace. The adapters map this exact function name to the canonical execution
identity on receipt, including streaming and replay. No raw JSON rewriting or
protocol fallback is involved, and switching protocols never rewrites persisted
calls. Actual application tool namespaces remain supported by Responses.
Independent reads and read-only commands execute
concurrently; permission-producing calls serialize their prompts. Batch results
remain ordered, durable, and disclose child errors rather than claiming success.

## Verification and limits

Run `go test ./codexio ./fileio ./provider ./agent ./session ./tools ./workspace`
and the TUI projection suites. Tests cover source preservation, strict-mode
absence, multi-file moves/overwrites, partial failure, raw tool replay, mixed
model profiles, child ownership, permission denial/grants, durable snapshots,
and production message-scoped undo.

The optional live reference tests accept `CODEX_APPLY_PATCH_BIN`, an executable
accepting the patch as its sole argument. A wrapper around a Codex CLI binary
can invoke `codex --codex-run-as-apply-patch "$@"`. Run:

```sh
CODEX_APPLY_PATCH_BIN=/path/to/apply_patch PARITY_CASES=5000 PARITY_SEED=726492 \
  go test ./codexio -run 'TestCodexParityLive|TestNormalizedModeLiveParity' -v
```

The recorded corpus is deterministic and runs without Rust or network access.
Live differential tests compare success/failure and final bytes, not model
quality. This integration does not claim to implement every optional Codex
service (remote environments, hosted search, plugins, or model-side services),
or guarantee identical performance across different models/providers.
