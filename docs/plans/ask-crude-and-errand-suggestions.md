# Ask Crude and Errand Suggestions

> Status: IMPLEMENTED 2026-11-18; one acceptance gate open (300 ms visible p50,
> see [Implementation status](#implementation-status)). Design accepted on
> 2026-11-18 with the corrections in [Decisions](#decisions).

## How to work on this plan

1. Read this section, [Decisions](#decisions), [Scope](#scope) and
   [Implementation status](#implementation-status) before any work on Ask Crude,
   editor AI suggestions, prediction context or the workspace text index. They
   override older text further down and older prediction notes in other plans.
2. **One engine for each kind of work.** Ask Crude is a normal agent session
   on the existing agentic loop: same tools, composer, deck, recovery and
   history. Errand suggestions are one-shot model requests through the existing
   Errand role binding: never an agent run, never a tool call, never a second
   loop.
3. **Typing never waits.** No key handler, Update or Paint waits for context,
   a language server, the index or the network. Every asynchronous result has
   an owner and a generation and is dropped when stale.
4. **Knowledge lives in data.** Prompt formats, FIM endpoints, stop tokens,
   model capabilities and language syntax come from provider and catalog data.
   Go code implements generic consumers. No provider-named, model-named or
   language-named branch.
5. **One integrated delivery.** There are no phases or deferred stages. Work
   proceeds through every part in [Scope](#scope); record evidence and open
   gaps in [Implementation status](#implementation-status).
6. **Related plans.** [Autonomous code intelligence](autonomous-code-intelligence.md)
   owns language servers, facts and diagnostics; this plan only consumes them.
   [Complete editor experience](editor-parity.md) owns general editor behavior.
   This plan owns Ask Crude, Errand suggestions, prediction context and the
   workspace text index.

## Decisions

| Date | Decision | From |
|---|---|---|
| 2026-11-21 | Rename the workspace helper to **Errand**. Do not give Errands personal names or show old names in notifications. Keep the same workspace helper and saved identity. | User |
| 2026-11-21 | Use `errand` for the workspace helper throughout the product. Leave old helper sessions on disk without migrating them. | User |
| 2026-11-18 | Add **Ask Crude**: Ctrl+I in the editor (as in VS Code) and an entry in the editor context menu. Give **Errand** the ability to act as an advanced AI autocomplete. Research how other editors do both, and design for very low latency together with real context. Crude has no semantic code index; it has a fast cross-platform fzf-based search that may be made faster. The UI and UX need a deep understanding of Crude. | User |
| 2026-11-18 | Use a shortcut with few terminal conflicts; Ctrl+K is fixed across keyboard negotiation. Copy, Select All and other shortcuts must work after a non-Latin layout switch. Batch shell/Python work with the same care. | User |
| 2026-11-18 | Stop the long implementation for departure. Verify GPT 6.2 and Crude, commit and push a working checkpoint, and leave a handoff. This checkpoint does not accept the unfinished integrated feature. | User |
| 2026-11-18 | Accept the recommendations, except phasing: everything is implemented as one integrated delivery, with special care for the Ask Crude UI and UX. | User |
| 2026-11-18 | No local model installation (no llama.cpp setup by Crude). The user may point the whole Errand at a local server; Crude is responsible only for talking to it. | User |
| 2026-11-18 | Crude decides the thinking level of suggestion requests. Suggestions use the same base as Auto Review: an explicit account, model, thinking and timeout, sent through the normal provider client. | User |
| 2026-11-18 | Ask Crude opens the chat in a split pane with the code attached (Ctrl+K and the context menu), not VS Code's inline box. Existing multiple panes must work without competing authorities, myopic failures or fragile hand-rolled logic. Learn from earlier bugs; keep it consistent and beautiful; do not overengineer. | User |
| 2026-11-04 | Inherited: Errand is a role inside the existing agentic system. No fork, no second loop, no narrower tool belt; the composer and loop are reused as they are. | User |
| 2026-11-04 | Inherited: do not contain or sandbox Errand's execution. | User |
| 2026-11-10 | Inherited: automatic completion stays passive. Keep the caret visible, choose no row, show obvious continuations as faded inline text. Tab accepts visible inline text or a chosen row; otherwise it edits normally. Typing must stay pleasant. | User ([code intelligence](autonomous-code-intelligence.md#decisions)) |

### Resolved questions

| ID | Question | Answer |
|---|---|---|
| D1 | Key where Ctrl+I equals Tab | Ctrl+K, through one ordinary binding on every terminal. |
| D2 | Ctrl+I when an agent already works on the file | Reuse the chat visible beside the code; otherwise a new chat. See [Where the chat opens](#where-the-chat-opens). |
| D3 | Unsaved text | Ask Crude saves the file first. |
| D4 | Inline Keep / Undo marks | Replaced by the split-pane design: review uses Git marks, chat diffs and `/undo`. See [Reviewing changes](#reviewing-changes). |
| D5 | Suggestions model | A separate Suggestions model under Errand, on the Auto Review base. It starts from Errand's account and model; Crude sets thinking to none. |
| D6 | Local model | No installation. Local servers are supported as accounts (OpenAI-compatible chat or completions with a suffix, Ollama). |
| D7 | Errand proposing larger changes in the background | Not a background process. **Finish this change** in the Crude menu sends the user's recent edits to the chat beside, on request. |

## Scope

One delivery. Each line is required.

- Ask Crude: chord, menu, palette, code-selection reference, pane placement,
  save first, prepared prompts, chip open, lifecycle, documentation and tests.
- Errand suggestions: Suggestions tab under Errand in Agents, request path on
  the normal provider client with thinking none, core request and shell
  transport, modes Off / When I ask / As I type, triggers and cancellation,
  context (cursor block, recent edits with authors, diagnostics, completion
  candidates, related snippets), multi-line inline text, checks before
  showing, footer status, workspace consent and hourly cap, latency stages in
  the performance dashboard.
- Workspace text index for related snippets and Find in Files.
- Next edit: nearby edits and the next place, from region-rewrite formats.

## Implementation status

Updated with evidence as parts land. A part not listed here does not exist.

| Part | State |
|---|---|
| Ask Crude | Implemented: Ctrl+K, the editor menu's **Crude** section, the `code_selection` reference through local and connected cores, placement in [main view panes](../reference/main-view-panes.md#ask-crude-opens-the-chat-beside-the-code), chip reuse and click-to-open. Prepared prompts (**Explain**, **Fix**, **Finish this change**) send only after a save that started at or after the ask; a failed save, a disk conflict or a revert sends nothing and says why. **Finish this change** appears after a local edit in local and connected documents. Evidence: `ask_crude_test.go`, `ask_save_order_test.go`, Windows captures (Ctrl+K split, Crude menu). |
| Errand suggestions | Implemented: modes Off / When I ask / As I type with workspace consent (shared confirm frame, Cancel focused) and the hourly limit; insertion, nearby replacement and next-place marker; Tab, Ctrl+Right, Alt+End, Esc with dismissal memory; Tab-Tab follow-up from the same answer without a model call; completion-list precedence; cancel and release on caret, revision, focus or editor change; Suggestions tab (account, model with **Errand's model**, format, endpoint, speed from the protocol, timeout and output limits). Chat binds thinking none; fill-in-the-middle binds no chat overrides. Changes on lines another author changed in the last 10 s are not shown. Evidence: `editor_suggestions_connected_test.go` (four connected journeys), `editor_prediction_followup_test.go`, `ghost_outcome_test.go`, `ghost_projection_test.go`, `agent_suggestions_test.go`; Windows captures of consent, a real multi-line suggestion and its acceptance at 140x36, Settings at 140x36 and 80x24. |
| Measurement | Stages in microseconds (`suggest.context`, `ledger`, `first_byte`, `model`, `check`, `total`), shell `suggest.visible`, outcome counters and the `suggest.outcomes` table per context source, and `suggest.index` memory, curated on the dashboard's Services tab. Context assembly on the 2 MiB worst case: `BenchmarkBuildDense` 1.34-1.38 ms plus `BenchmarkRelated` 0.12-0.15 ms, under the 5 ms target. The 300 ms keystroke-to-visible p50 is observed, not yet a gate: it needs repeated measurements against a hosted model. |
| Workspace text index | Implemented: git ignore precedence (root first, deeper wins, negation), ignore-file changes rebuild, a lost or overflowed watch rebuilds and the index reports itself incomplete until then, idempotent start and close, memory counted over text, chunks and postings against the 64 MiB limit. Related snippets stop while the index is incomplete. Find in Files still reads the disk. |
| Next edit | Region rewrites produce checked hunks; remaining hunks chain after an accept; the marker jumps. |

## Completion record (2026-11-18)

The checkpoint `7571c7a5` listed six resume items. All six are closed in the
commit that follows it:

1. **Connected suggestion journey:** the shell owner reconciles request,
   shown and held state on every update and releases the core view; four
   connected journeys pass with real keys and a local provider.
2. **Settings:** account-specific model fetch with a default option,
   protocol-derived speeds, numeric bounds, consent in the shared confirm
   frame; save, reopen and return to defaults are tested.
3. **Context/index:** see the Workspace text index row above.
4. **Finish this change:** save-before-send ordering, failure, conflict and
   revert, author selection and the connected menu entry are tested.
5. **Projection/performance:** wrap, fold, right-to-left, pointer, scroll,
   undo, multiple carets, replacement paint and marker are tested through the
   real paint path; dashboard stages and outcome counters exist.
6. No legacy readers, local-model installers, speculative fallback or second
   state authority were added.

Defects found and fixed on the way: Tab reported typed-through and accepted;
a disk merge never reached recent edits; Delete and Backspace runs made one
entry per character; Finish was missing for connected documents; Tab applied a
replacement hidden in a fold; right-to-left suggested text painted reversed;
fill-in-the-middle requests failed before the network; the Model choice could
not return to the default; consent focused Turn on.

Known limits: the 300 ms visible p50 is not a gate yet; Windows native
captures used a local stub model, not a hosted one.

## Outcome and acceptance

**Ask Crude.** In any editor, the user selects code (or leaves the caret on a
line), presses Ctrl+K or chooses **Ask Crude** from the context menu.
The agent chat opens beside the code with the code attached and the caret in
the composer. The user types and presses Enter. Accept when:

- With one pane, two panes, nested splits, a floating window or a compact
  terminal, the chat lands in the place that [Where the chat opens](#where-the-chat-opens)
  names, the code stays visible where the layout allows, and no existing tab
  is closed, moved or duplicated.
- The message carries an exact snapshot of the asked code (path, lines,
  text). History and resumed sessions show what was asked even after the file
  changed; clicking the chip opens that place.
- Closing the chat tab never stops the agent or loses the draft.
- Unsaved code is saved first; a conflict never loses either version.

**Errand suggestions.** While the user types, Errand offers the next edit as
faded text at the caret, as a small marked change near the caret, or as a
pointer to the next place to edit. Tab accepts. Accept when:

- Typing frame and mutation latency gates do not change with suggestions on.
- Keystroke to visible suggestion: p50 at most 300 ms on a fast hosted
  connection; reused suggestions (typed through, recycled, chained) appear in
  the next frame. Targets become gates only after repeated measurement.
- Context assembly takes at most 5 ms at p99 on the dense benchmark workspace
  and never waits for a language server.
- Outcome counters (shown, accepted, dismissed, typed through) show whether a
  context source helps; a source that does not is removed.
- Nothing is sent before workspace consent, outside the spending limit, or
  from excluded paths.

## Where Crude stands (2026-11-18)

Historical baseline used to design the work. The implementation status and handoff above supersede this baseline; it is not a current feature inventory.

**Editor ghost text exists.** `tui/editor/ghost.go` projects one literal
single-line candidate at the caret: Tab accepts all, Ctrl+Right one word, typed
characters keep the remainder, Esc dismisses. LSP completion uses it for
obvious continuations (`tui/viewmodel/editor_ghost.go`). Multi-line text,
replacements and jumps are not supported.

**Historical baseline before this checkpoint:** prediction prerequisites existed but nothing called them.

- `workspace/editor_prediction_service.go`: per-view sequence fencing,
  supersede-cancels, source watch, sensitive-path exclusion, a 32-entry cache
  keyed by exact context digest, and measured latency.
- `insight/predictioncontext/context.go`: a prefix/suffix window plus
  diagnostics, bounded to about 2,000 tokens. Scope, imports, signatures,
  recent edits and other files are listed as missing.
- `insight/predictionbudget/ledger.go`: an hourly token ledger. Each request
  runs Reserve and Start transactions before the network call and Settle after
  it. These SQLite writes are on the keystroke path and are not measured yet.
- `agent/session/editor_prediction.go`: binds the Errand role's account and model.
- `agent/provider/fim_once.go` (OpenAI completions, DeepSeek and Codestral FIM) and
  `agent/provider/text_once.go` (OpenAI Chat and Responses only). Both stream on the
  wire but return only at the end.
- `agent/prompt/editor_prediction.go`: a single-line insertion prompt.
- `tui/settings/prediction.go`: Off / Light / Full, an hourly token cap, and
  a protocol and endpoint path that the user must type.
- No core request, shell-port method or editor trigger exists. Automatic
  requests are refused until a manual request has measured latency, and the
  typing pause then equals the measured latency, so a slow connection waits
  longer before it even asks.

**The Errand role uses an agent model.** On this computer it is a thinking
model at high effort. That suits agent work and is the wrong latency class
for keystroke suggestions (D5).

**Ask Crude has no path yet.**

- Ctrl+I is unbound. Kitty-protocol terminals (Ghostty, kitty, WezTerm, foot)
  and the Windows console deliver it; older terminals, and tmux without
  extended keys, send Tab.
  `tui/ui/chords.go` already resolves keys that depend on the terminal.
- The editor context menu (`tui/viewmodel/context_menu_builders.go`) has Edit,
  Lines, Code, Git and View sections and nothing for agents.
- Prompt references (`agent/sessiondb/promptref.go`) cover pasted text, files and
  MCP context, not a code range.
- The real chat composer can be mounted more than once
  (`shellinput.NewWithID` in `tui/screens/shellinput/input.go`).
- Agent edits record operation receipts; selective agent undo exists.
- Agent file tools read the saved file. Unsaved editor text is invisible to
  agents (D3).

**Search.** The fzf matcher (`text/search/fuzzy.go`) is fast. Find in Files
(`workspace/fileio/project_search.go`) walks the tree with one goroutine and reads every
file again for each query, without an index and without ignore files. It
cannot serve retrieval on the keystroke path.

## What other editors do (2026)

| Product | Model and output | Context | Speed techniques | Interaction |
|---|---|---|---|---|
| GitHub Copilot, VS Code | One custom model for ghost text, nearby edits and far edits. Output is a chain of diff patches, also across files. | Recently viewed snippets, current file with line numbers, recent edits as unified diff, and a 3-line cursor block **last** so the provider reuses the cached prompt prefix. | Speculative decoding of the current line; caching follow-up patches for tab-tab-tab; a short delay before showing a cached result; no debounce needed while typing. | The client picks the view for each change (ghost text, nearby rewrite, far rewrite, other file). An ignored suggestion must not come back as a different kind; that one rule moved dismissals by 26%. Progressive reveal of long text; configurable eagerness. |
| Cursor Tab | Custom sparse model; edits around the cursor and jumps. | About 13,000 tokens of editor state. | 260 ms p50 server latency. | Tab accepts, Tab again jumps. |
| Zed | Provider-neutral. Zeta2 (open weights, 8B) rewrites a region around the cursor. | Code around the cursor, fine-grained recent edits, type and symbol definitions from the language server. | Debounce per provider (0 to 150 ms). | Prompt formats are data (Zeta, Sweep, Qwen, Codestral, DeepSeek FIM). Eager or subtle mode. When the completion list is open, Tab belongs to the list. Accept word and accept line. The Inline Assistant (Ctrl+Enter) transforms a selection with one model call, not an agent. |
| JetBrains | Mellum for completion; next edit suggestions. | IDE model. | Global serving for latency. | "Chain suggestions"; "Suggest refactorings" uses IDE actions such as Rename instead of generated text. In-editor prompt (Ctrl+\\) and **AI Actions** in the context menu. |
| VS Code inline chat | General chat models. | Active editor and selection, plus workspace files. | — | Ctrl+I opens a prompt in the editor; result is an inline diff with **Keep** / **Undo**. If the file belongs to an active chat session, Ctrl+I goes to that chat instead. Quick Chat continues in the Chat view. |
| Inception Mercury | Diffusion models, more than 1,000 tokens per second; autocomplete and next-edit endpoints, OpenAI-compatible. | Recent snippets, file, edit history. | Parallel decoding; cheap cached input. | Used through Zed and other clients. |
| Sourcegraph Cody (lessons) | Moved from a chat model to FIM models. | Tree-sitter planning; Jaccard sliding windows over recently viewed files of the same language. Irrelevant context makes results worse. | Stop words and token limits, streaming with early stop, connection reuse, fewer parallel samples, and reuse of an earlier request when the user typed what it predicted (about 1 request in 10). | Use the selected completion-list row to steer the model. |

Sources are listed at the [end of this plan](#sources).

## What Crude can do better

The leaders train their own models. Crude uses the user's own accounts, so it
cannot win on the model. It can win on the system around the model, which
Copilot's own report calls just as important:

1. **IDE facts instead of guesses.** Crude already has language-server
   definitions, completion candidates, diagnostics, fold ranges and exact edit
   history. Feed them in, and let deterministic tools do what they can do
   exactly: when the user edits a declaration name, offer the language
   server's Rename (reviewed) rather than a model's guess at the call sites.
2. **Edit history with authors.** Crude knows which edits came from the user,
   from an agent and from disk. After an agent changes a signature, Errand can
   suggest the call sites the user is fixing. While an agent edits a region,
   suggestions stay out of it.
3. **Context ready before the keystroke.** Warmers prepare definitions and
   related snippets while the caret rests. A request takes what is ready in
   5 ms and never waits.
4. **Check before showing.** A candidate that breaks the parse (where a
   grammar exists), repeats the suffix or reverses the user's last edit is
   dropped before it is painted.
5. **Any provider, honest cost.** Formats and endpoints come from data, a
   **Test speed** button measures the user's real connection, and a visible
   meter shows spending.
6. **Agent power where it pays.** Ask Crude is a full agent (it can read other
   files, run checks and use the language server), not a single model call as
   in Zed's Inline Assistant.

## Part 1: Ask Crude

Ask Crude opens the real agent chat in a pane beside the code, with the code
already attached. There is no inline card: a box over the code is ugly in a
terminal, hides the code it asks about and would be a second chat surface
next to the agent tab (the same reason the Errand sidebar page was rejected on
2026-11-04). The agent tab, its composer, deck, transcript, model picker,
recovery and history are used as they are.

### Entry points

- **Ctrl+I** in a focused editor where the terminal reports Ctrl+I apart from
  Tab (kitty keyboard protocol, Windows console); **Ctrl+K** everywhere. One
  semantic binding (`ui.AskCrudeKey`, Ctrl+K) owns the matcher and the caption, so a
  hint never names a key that the terminal cannot send. In text fields Ctrl+K
  keeps its line-delete meaning; the chord is an editor binding only.
- **Editor context menu**, first section **Crude**:
  - **Ask Crude** (chord caption): code into the chat beside, focus the
    composer, nothing is sent.
  - **Ask in a new chat**: the same, always in a new session.
  - **Explain this code**: sends a prepared prompt at once.
  - **Fix this problem**: only when a diagnostic is at the clicked point;
    sends a prepared prompt with the diagnostic at once.
  - **Finish this change**: only after recent edits by the user in this file;
    sends those edits as small diffs and asks the agent to carry the change
    through the other places that need it.
- **Command palette**: Ask Crude, Ask Crude in a New Chat.

Prepared prompt text lives in `agent/prompt/`. The user sees it as their message.

### What is attached

A new prompt reference kind, **code selection** (`code_selection`):

- The chip label is the location, `[store.go:118-124]` (one line:
  `[store.go:118]`). Clicking it opens the file at that line in the pane that
  asked.
- It stores the absolute path, the 1-based line range and the exact text of
  those lines as shown in the editor, up to 64 KiB. A larger range keeps the
  path and lines only, and the agent reads the file with its tools.
- The agent receives the location, the path and the text. History, resumed
  sessions and hydrated transcripts show what was asked even after the file
  changed.
- **Range**: the selection, extended to whole lines; without a selection, the
  innermost fold range around the caret when it has at most 120 lines
  (with a following line that only closes brackets), otherwise the caret
  line.
- **Unsaved text**: agent tools read the saved file, so Ask Crude saves the
  file first, through the normal save owner with the focus-out reason (no
  format, no "Saved" notice). A conflict uses the existing disk-conflict flow;
  the chip still carries the exact text the user saw.

### Where the chat opens

One function decides, from the current layout, every time; nothing is cached,
so it cannot disagree with the pane owner. Groups in floating windows count
like docked groups.

1. **Reuse a visible chat.** A group other than the editor's whose selected
   tab is an agent with a composer (not the workspace Errand). With several,
   prefer the agent the user focused last, then the nearest neighbor (right,
   below, left, above), then the first in layout order. Ask Crude adds the chip
   to that agent's draft; a running agent receives it as steering when the
   user presses Enter, as in any chat.
2. **New chat.** When no chat is visible, or for **Ask in a new chat**, Crude
   creates a session and places its tab when it arrives:
   1. in a group that shows a chat (a new tab there);
   2. else in the editor's right neighbor (a new tab, like Open to the Side);
   3. else split the editor's pane to the right when it is at least 108
      columns wide (60 for code, 48 for chat);
   4. else split it below when it is at least 28 rows tall;
   5. else in any neighbor of the editor;
   6. else as a tab in the editor's own pane.
   Splits go through the existing placement owner (`Window.PlaceTab` with the
   shared minimum-size check), so pane limits, compact presentation and
   dividers behave as for a dragged tab.
3. **Prepared prompts** go to a visible idle chat; a running or missing chat
   gets a new session, so a prepared prompt never steers a running turn.

After placement the agent's pane gets focus and the composer gets the caret,
after the chip.

### Asynchronous lifecycle

- Session creation, reference creation and the save run off Update. Each
  result carries the workspace generation and the ask's sequence and is
  dropped when stale.
- The new session's tab is placed when its agent reaches this shell (local
  and connected cores), using the layout at that moment. If the editor's pane
  closed meanwhile, placement starts from the focused pane.
- A chip whose agent is no longer the composer owner (the user moved on)
  waits in that agent's draft and appears when its composer is next shown.
- Workspace switch drops pending asks. Closing the chat tab minimizes the
  agent, as for any agent tab; the next Ask opens a new chat or reuses a
  visible one.

### Reviewing changes

The agent edits files with its normal tools, and each edit is a Local History
operation receipt. Review uses the existing owners rather than a second
overlay: the editor's Git change marks and **Inspect change**, the tool rows
in the chat (with diffs), and `/undo` (exact preview, fails closed on later
overlapping edits). There is no separate Keep/Undo overlay: it would be a
second review authority beside Git marks and Local History, and Keep has no
meaning when the files are already written.

## Part 2: Errand suggestions

### What the user gets

- **Modes**: Off · When I ask · As I type. These replace Off / Light / Full.
- **Kinds**, from one request and one output contract:
  1. **Insert at the caret**: faded text, one or more lines.
  2. **Nearby edit**: a change inside a small window around the caret.
  3. **Next place**: the next line to edit in this file (other files later).
- **Follow-through by the IDE**: when the model's next edit is a rename of a
  declaration that the user just edited, Crude offers the language server's
  Rename with its normal review.

### Who does what

- The **Errand role** supplies the connection: its account plus the
  Suggestions model (D5).
- The **workspace Errand agent** is not involved per keystroke.
- The **core** owns the predictor: canonical text, facts, edit history,
  index, provider connection and budget. The shell sends intents and paints
  candidates.

### Request path

```
keystroke ─▶ editor (local echo) ─▶ core document edit
             └─ shell sends predict(view, sequence, revision, caret)   never waits
core: reuse? ──yes──▶ publish at once
      └─no─▶ context snapshot (≤5 ms, from caches) ─▶ render format (data)
             ─▶ provider stream ─▶ first line ─▶ check ─▶ minimal edit ─▶ publish
shell: newest sequence only ─▶ ghost / change marks / next-place marker
```

1. **Intent.** On each edit or caret move in the focused editor, the shell
   sends the existing request identity (source, view, revision, caret,
   sequence) through a new cancellable core request. A newer sequence cancels
   the older one in the core. Partial results stream to the requesting view
   and carry its sequence; the shell paints only the newest.
2. **Reuse first** (no network):
   - typed through: the visible candidate still matches the typed text
     (exists);
   - recycled: an in-flight or recent response is consistent with the
     current text, as in Cody;
   - chained: follow-up edits from the last response wait in a cache; after
     Tab the next one shows at once while a fresh request refreshes it;
   - suppressed: a dismissed suggestion stays dismissed in every view kind
     until the text near the caret changes.
3. **Pause.** As I type sends after a short pause (start at 50 ms, between 0
   and 150 ms), tuned by the share of requests that were cancelled before
   use, never by connection latency. The current rule (pause equals measured
   latency) is replaced.
4. **Prompt order for prefix caching.** Fixed instructions, then related
   snippets, then the file, then recent edits, then the cursor block last.
   Providers that cache prompt prefixes (DeepSeek, OpenAI, Inception) then
   reuse most of the work between keystrokes.
5. **Network.** Keep one warm HTTP connection per suggestion connection. When
   an editor gains focus after the connection has been idle, open the
   connection ahead of the first request. Measure whether this pays.
6. **Streaming.** Show the first complete line when it arrives. Stop the
   stream when the text reaches the suffix, leaves the current scope (by fold
   range), or passes the reveal limit.
7. **Check before showing** (at most 2 ms): the existing literal rules; no
   repeat of the suffix; no reversal of the user's last edit; and, where the
   catalog has a grammar, no new parse error in the edited region (an
   incremental parse of a scratch copy).
8. **Minimal edit.** Crude diffs the model's output against the current text
   and shows each change in its best form, rather than the raw output shape
   (Copilot's lesson).
9. **Budget off the hot path.** Reservation and settlement move to memory with
   a periodic durable write. The hourly limit and meter stay. Measure the
   current three transactions first; change only with the measurement.
10. **Thinking off.** Suggestion requests turn off reasoning for the chosen
    model. If the model cannot, Settings says that suggestions will be slow.

### Context without a semantic index

Each source has a token share, a freshness rule and a measured value. A request
takes what is ready; a missing source stays missing and is never invented.

| Source | What | How it stays ready |
|---|---|---|
| Cursor block | File path, language, the enclosing scope's first line (fold range or document symbol), the editable window around the caret, prefix and suffix | Built per request from the canonical document |
| Recent edits | The last edits across files as small unified diffs, newest last, with the author (user, agent, disk); typing runs merged, fine-grained near the caret | A bounded ring in the core document owner |
| Definitions | Signature and first lines of definitions for identifiers near the caret (called names, receivers, types in the enclosing signature) | A warmer asks the language server when the caret rests (about 100 ms), at background priority through the existing governor; results cached by document revision and identifier |
| Completion candidates | Top labels of the completion list at the caret; the chosen row when there is one | Taken from the completion owner when a list exists |
| Diagnostics | Problems inside the window | Existing facts |
| Related snippets | Best matching windows from open and recently viewed files (same language first) and from the workspace text index | Jaccard or BM25 over identifiers; index queries at most 2 ms |
| Project notes (later) | A short, stable note on project conventions | Only if the corpus shows a gain |

The prompt starts near 2,000 tokens and grows only while measured latency stays
inside the target. Snippets follow the same exclusions as the source file
(sensitive paths, ignored files).

### Workspace text index

This is the answer to "make the search faster". The fzf matcher is fast; the
cost is walking and reading every file for every query. Replace that with an
index that the core keeps current:

- Built from the existing file-watcher facts, walking with the internal
  fastwalk (`third_party/pathwalk/`) in parallel. It honors ignore files and
  the existing skip rules. Open documents are indexed from their live text.
- Content: identifier tokens and trigrams mapped to chunks of about 40 lines,
  per file. Skips binaries and files above 1 MiB. Memory is bounded (start at
  64 MiB) and reported.
- Incremental: one changed file re-indexes one file. Workspace switch and
  shutdown are generation boundaries.
- Queries:
  - suggestion retrieval: identifiers of the cursor block, BM25 over chunks,
    top results in at most 2 ms;
  - Find in Files: candidate files from trigrams, then exact checks with the
    existing matcher, so results and ranking stay the same and only the cost
    drops;
  - later, the agent grep and glob tools.
- Language-neutral by design. Where a catalog grammar has a tags query, that
  data can rank declarations higher; Go code never names a language.

### Presentation

```
 43 │     for _, item := range items {▌
    │         if item.Expired() {                       ← faded rows, not text
    │             continue
    │         }                                    +3 lines
 44 │     }

 57 │     total := price * qty▌                              ⇥ line 61
 61 │     log.Printf("total=%d", amount)    amount → total (marked change)
```

- **Insert**: the first line as faded text after the caret, following lines
  as faded rows that use the shared inlay and fold geometry, up to 8 rows;
  longer text shows the first rows and a muted **+N lines** marker.
- **Nearby edit**: removed text gets the theme's removal tint, added text is
  faded text in place. No popup windows, so code does not jump.
- **Next place**: a muted `⇥ line 61` at the end of the caret line and a
  gutter arrow at the target when it is visible. First Tab moves the caret
  there and shows the change; second Tab accepts.
- **Status**: a quiet **Errand** footer item with one state (ready, working,
  off, limit reached, connection problem). Its menu changes the mode, opens
  Settings and shows the measured speed and the meter.
- **Never**: hide the caret, scroll, open a list, change the selection, show
  with a selection or several carets, show in read-only or conflicted
  documents, or show while an agent is editing that region.

### Keys

| Action | Key | Notes |
|---|---|---|
| Accept all | Tab | Existing order: a chosen completion row first, then visible inline text, then indentation. |
| Accept one word | Ctrl+Right | Exists. |
| Accept one line | A free chord chosen with the interaction matrix | Also an editor action. |
| Dismiss | Esc | First Esc never closes the tab (exists). Dismissal is remembered as above. |
| Ask now | Alt+\\ | Works in When I ask and As I type. |
| Keep typing | — | Matching characters keep the remainder (exists). |

When the completion list is open, Errand shows text only if it continues the
list's single visible or chosen row; otherwise it waits, as in Zed.

### Settings, consent and spending

- **Settings → Agents → Errand → Suggestions** is built like the Auto Review
  Agent's tabs (same base): account and model (empty means Errand's own),
  request format (Chat, or Completions with a suffix for fill-in-the-middle
  servers, with its endpoint path), service tier, timeout and the output
  limit. Thinking is not a choice: Crude sends thinking none, which every
  built-in protocol adapter supports. **Test speed** sends one probe and
  states the result in plain words: "This model answered in about 900 ms.
  Suggestions feel best under 300 ms."
- The request goes through the normal provider client (`providerForCategory`
  and a stream), like Auto Review, so every configured protocol works,
  including local servers that the user set up. Crude installs no model.
- The workspace keeps only the mode and the hourly token cap. The old
  workspace protocol and endpoint fields go away.
- **Workspace consent** stays. The first time suggestions turn on in a
  workspace, one question: "Errand sends nearby code, recent edits and related
  snippets to <provider>. Turn on suggestions in this workspace?"
- **Spending limit**: the hourly token cap stays; when the catalog knows the
  price, Settings also shows an estimate in money.

### Prompt formats and providers as data

Three format families, chosen by catalog data for each model:

| Family | Output | Kinds it can serve | Examples |
|---|---|---|---|
| FIM | Text between prefix and suffix | Insert | DeepSeek FIM, Codestral FIM, OpenAI-compatible completions with a suffix, local coder models |
| Region rewrite | The editable window, rewritten | Insert, nearby edit, next place when the format marks it | Zeta2, Sweep next edit, Mercury Edit |
| Chat rewrite | The editable window between markers, for instruct models | Insert, nearby edit | Any fast chat model; slower and costlier |

Adding a provider or model is a data change plus a live verification, never a
Go branch.

### Measurement

- **Latency stages** for each request: intent to core, context, first byte,
  first line, check, publish and paint, in the existing performance
  dashboard, with p50 and p90 for each connection.
- **Outcome**: shown, accepted (all or part), dismissed, typed through, and
  characters still present after 30 s and 2 min. Stored locally, never sent.
- **Benchmarks**: context assembly on the dense workspace, index build and
  update, Find in Files with and without the index, and the existing typing
  gates with suggestions on.

## Risks

- **Cost with the user's own keys.** As I type can send hundreds of requests
  an hour: about 400 requests of 2,000 input tokens is 0.8M input tokens an
  hour before caching. Reuse, prefix caching, the pause rule, the limit and the
  meter must hold; When I ask stays the default until the user turns on As I
  type.
- **Privacy.** Related snippets widen what leaves the computer. Consent text
  names it; exclusions apply to every source, not only the current file.
- **Tab conflicts.** Tab already serves completion rows, inline text, snippet
  fields and indentation. Every new suggestion kind uses the existing order
  and gets regression tests at each boundary.
- **Noise.** Next-place markers and change marks can distract. Dismissal
  memory and the outcome metrics decide what stays.
- **Agents and unsaved text.** Agent tools read saved files, so Ask Crude
  saves first. Prepared prompts never wait for the save; the chip carries the
  exact text either way.

## Sources

- GitHub Copilot inline suggestions model,
  [part one](https://code.visualstudio.com/blogs/2026/09/16/building-the-github-copilot-inline-suggestions-model-part-one)
  and [part two](https://code.visualstudio.com/blogs/2026/09/23/building-the-github-copilot-inline-suggestions-model-part-two)
  (2026-09); [configurable eagerness](https://code.visualstudio.com/blogs/2026/10/27/configurable-eagerness).
- VS Code [inline chat and Quick Chat](https://code.visualstudio.com/docs/copilot/chat/inline-chat).
- Zed [edit prediction](https://zed.dev/docs/ai/edit-prediction),
  [how Zeta2 was developed](https://zed.dev/blog/how-we-developed-zeta2) and the
  [Inline Assistant](https://zed.dev/docs/ai/inline-assistant).
- Cursor, [a new Tab model](https://cursor.com/blog/tab-update).
- JetBrains [next edit suggestions](https://www.jetbrains.com/help/ai-assistant/next-edit-suggestions.html).
- Inception, [Mercury Edit 2](https://www.inceptionlabs.ai/blog/introducing-mercury-edit-2).
- Sweep, [open 1.5B next-edit model](https://news.ycombinator.com/item?id=46713106).
- Sourcegraph, [the lifecycle of a code AI completion](https://sourcegraph.com/blog/the-lifecycle-of-a-code-ai-completion).
