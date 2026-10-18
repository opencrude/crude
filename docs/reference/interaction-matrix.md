# Interaction Matrix

> Status: Current. Phase 0 baseline of keyboard/mouse contracts the component system must preserve.

Phase 0 baseline of keyboard/mouse contracts that the component system must
preserve or intentionally improve.

## Global

| Action | Binding (current) | Notes |
|--------|-------------------|-------|
| Command palette | Ctrl+Alt+P | Not Ctrl+P; tmux Shift issue |
| Find in Files | Ctrl+Alt+F | Not Ctrl+Shift+F under tmux 3.4 |
| Replace in Files | Ctrl+Alt+H | Sibling of Find in Files; not Ctrl+Shift+H under tmux |
| Toggle sidebar | Ctrl+B | Hides/shows the sidebar body; the 8-col activity rail (outer gutter plus padding before the panel left border) stays |
| Files / Agents / Terminals / Git | Ctrl+Shift+E / A / U, Ctrl+Alt+G | Files and Agents are rail destinations; Terminals and Git are bottom-dock destinations (footer pills). Rail and panel bodies expose destination-aware context menus |
| Recent Workspaces | Rail below Agents; palette **Recent Workspaces** | Shared sidebar icon/search/count chrome and command rows. Most recently accessed first (successful core attachment, not session creation or background activity). First click selects, double-click/Enter requests opening; every different-workspace route (home, rail/context menu, folder picker, cross-workspace session resume) previews the exact destination with Open workspace / Cancel and Cancel focused. Approval proceeds through dirty-file/draft safety; Esc or outside clicks never silently switch. Current is a no-op. New Workspace opens the folder picker; Refresh retains usable rows on failure. Short rails keep it reachable through More and the palette. |
| Application Settings | activity-bar gear | Bottom rail gear opens Application Settings (VS Code manage control); right-click also offers Workspace Settings |
| Rail counts | Upper/lower-right of destination icons | Normal-size bold digits keep the script-like placement without tiny Unicode glyphs: Every destination uses the top row of its three-row band. Counts above nine use `+` at the same level. Badges sit diagonally beyond the measured icon, never widen the rail or overwrite the panel rule, and remain inside their destination's whole-band click/hover target. Zero counts disappear. |
| New terminal | Ctrl+Shift+` | Ghostty-pinned PTY; docks in the workspace terminal dock |
| Summon / leave terminal | Alt+F12; Ctrl+` and Ctrl+Alt+T aliases | Focuses the last-used PTY in its existing dock/window placement, creates one if none exists, and returns focus to content on repeat. The function-key chord avoids layout-specific backtick/AltGr encoding. Never bypasses modal/dirty decisions. Shifted `~` encoding also activates Ctrl+Shift+` New Terminal. |
| Notices → Terminals | Bottom-row pills | Changes the painted panel even when a new terminal's create receipt arrives before its inventory. Notices restore on cold core start and new workspace notices reach all connected shells |
| Composer | Alt+C | |
| Modified Enter | Semantic chords, captions resolved per terminal | Ctrl+Enter / Shift+Enter exist only where the terminal disambiguates them (Windows console, kitty keyboard protocol); tmux and legacy terminals send plain Enter, and Windows Terminal takes Alt+Enter for fullscreen. Every caption comes from `ui.FormatShortcut` of a chord token and every matcher from the same token: **commit** (`CommitChord`) accepts Ctrl+Enter and Alt+Enter; **new line** (`NewlineChord`) accepts Shift+Enter, Alt+Enter and Ctrl+J (LF, delivered everywhere); **previous match** accepts Shift+Enter and Shift+F3; **replace all** accepts Ctrl+Alt+Enter and Alt+Enter. A caption always names a key this terminal delivers; `TestNoHardCodedModifiedEnterCaptions` rejects hard-coded ones. |
| Quit application | Ctrl+Q | |
| Close active editor tab / empty pane | Ctrl+W | Editor uses the dirty-close Save / Don't Save / Cancel prompt; an empty focused group alone is removed without closing neighboring resources; terminal focus keeps the key as PTY input |
| Cancel/back | Esc | Never Ctrl+C for back; in editor focus, Back closes the tab after transient editor UI is dismissed. On a tool permission dock, Esc stops the turn without denying permission; Deny / `n` is the explicit rejection path. In a text control Esc first drops the selection; the chat composer's ladder is selection → draft → cancel the turn. Cancelling requires live work (turn, pending prompt, compaction) or a recovery parked for retry: Esc with nothing to stop does nothing, a stopping turn is never re-entered, and palette Cancel Agent on an idle agent says "Nothing to stop". |
| Previous / next tab | Ctrl+[ / Ctrl+] | Within the focused main group, outside terminal key ownership, including editor focus |
| Main tab actions and focus | Click tab / ×; tab context menu; close shortcut | Pointer selection and close return keys to the selected editor/chat, or workspace home when the last tab closes. Hover and overflow browsing never pin keyboard focus to the strip. Deliberate Ctrl+T + arrow/Home/End navigation retains strip focus until Enter/Down/Tab/Esc commits or leaves it; closing ends that navigation. Dirty-close Save / Don't Save / Cancel keeps its safe modal and never restores stale strip focus. Background tab updates preserve the current focus. |
| Attach file / choose current folder | Ctrl+O | Opens Attach File from non-terminal shell views; a focused Open Workspace folder picker owns the same key locally and selects its current folder |
| Copy | Ctrl+C / Ctrl+Shift+C | Copies the selection of the chat input, chat messages, editor, overlay editors and every inline text control. A focused terminal is the deliberate exception: Ctrl+C always belongs to the child, even with a retained selection; Ctrl+Shift+C copies and never falls through to interrupt when nothing is selected. Unhandled Ctrl+C clears the chat draft, and Ctrl+C never navigates back. |
| Terminal controls | Ctrl+C/D/Z/\\/S/Q and application key bindings | The focused PTY owns these before shell/editor selection handling. Ctrl+C interrupts when the child enables processed input/ISIG; Ctrl+D is EOF under Unix canonical input, Ctrl+Z is Unix suspend or Windows child-specific EOF, Ctrl+\\ is Unix quit, Ctrl+S/Q are child/TTY flow control. Crude forwards keys, never guesses Unix signals on Windows or kills unrelated processes. Raw/extended-keyboard children receive their negotiated key encoding. Only exact Shift+PgUp/PgDn/Home/End scroll local primary-screen history. |
| Cut | Ctrl+X | Text controls: cuts the selection, or the whole value when nothing is selected (masked fields refuse it) |
| Drag selection | drag over text | Editable text (composer, fields, filters, overlay inputs, editor) keeps the dragged selection after release for typing over, Ctrl+X, Delete or Ctrl+C; release never writes the clipboard. Reading surfaces (chat transcript, read-only areas and documents, Git review, plan reader, terminals) copy on release and drop the highlight. Double-click selects a word, triple-click the line, Shift+click extends, in text controls and the editor alike. |
| Keyboard text selection | Shift+arrows / Home / End | Insertion-boundary semantics everywhere: at `abc` then caret then `def`, Shift+Left selects `c`, Shift+Right selects `d`, despite the block cursor covering `d`. Whole graphemes, stable anchor through reversal, plain Left/Right collapse to directional edge. Pointer hits retain their complete original source unit for backward drags; keyboard ranges never acquire pointer capture. |
| Whole-text Copy/Cut | right-click on a text control | Context menu Copy/Cut act on the entire value; the composer menu (Paste / Copy / Cut / Clear input) acts on the draft. Cut runs through the owning control so composite models stay in sync. |
| Paste | Bracketed paste primary (the terminal's own paste: Ctrl+V on Windows Terminal, Ctrl+Shift+V on Linux terminals, Cmd+V on macOS); Ctrl+V / Ctrl+Shift+V fallback | Routes to the focused control; launcher/tree paste never leaks into hidden chat input. Overlay text fields (find/replace, palette, tree prompt) take it through the same owner path as their keys. A Ctrl+V clipboard read re-enters through the exact control that asked (its composite sees it like a bracketed paste: searches run, forms track it) and is dropped if that owner is gone. Where the system clipboard is unreadable (SSH, headless tmux) the error names the terminal paste shortcut. Windows Terminal image-only clipboard pastes arrive as empty bracketed pastes: only the chat composer asynchronously reads and attaches the clipboard image. Non-empty text remains exact; an empty paste without an image is a silent no-op, never a read of unrelated clipboard text. |
| Copy target | System clipboard; OSC 52 fallback | When the system clipboard is unreachable (no wl-copy/xclip/pbcopy) or the session is SSH, copies also go out as an OSC 52 terminal clipboard write, which local terminals and tmux (`set-clipboard`) deliver to the user's clipboard. |
| Large text paste | Substantial overflow, not a line count alone | Attach at 4 KiB; also at eight logical lines with at least 512 bytes of trimmed payload, or ten wrapped rows with at least 1 KiB of trimmed payload. Wrapped evaluation uses the real text column with a 40-column floor. Short prose/small lists stay literal even on a tiny grid. Terminal and image-first clipboard routes share the policy; the full Pasted text attachment loads off Update. Pending storage preserves paste order and blocks Send; owner/draft changes invalidate late insertion and failures never dump the payload. |

Clipboard images use binary `CreateRequest.Data` through local and connected
reference stores; `Content` is text, never a Base64 image transport. Inline and
chunked RPCs preserve original bytes. MIME/signature validation rejects encoded
text before publication. Faulty stored image evidence produces an exact exclusion
and warning while retaining the original message and good media, without editing
history. Permission, ownership and storage failures remain fail-closed.

## Main view panes

Current ownership, lifecycle and limits: [Main View Panes](main-view-panes.md).
Groups are recursive side-by-side/stacked leaves with independently selected
tabs. One resource identity moves, never duplicates. Layout is workspace-scoped
in memory for the current process only, not restored after restart; switching
workspace resets it. Header telemetry follows the focused group.

| Action | Binding / control | Notes |
|--------|-------------------|-------|
| Split right / down | Ctrl+Alt+L / Ctrl+Alt+D | Creates a focused empty group; Ctrl+Alt+Right/Down aliases may be intercepted by the OS |
| Previous / next pane | Alt+[ / Alt+] | Visual leaf order; remains available in compact presentation |
| Grow / shrink pane | Ctrl+Alt+= / Ctrl+Alt+- | Shared split sizing with minimum bounds |
| New Session in Pane | Group +; Ctrl+Alt+N; Ctrl+T then N/+ | + sits after content-sized tabs, pinned at the trailing edge during overflow |
| Populate empty pane | New Session / Open File; N / O | Tab/Shift+Tab chooses and Enter/Space activates; Ctrl+W closes only that empty group |
| Move / reorder tab | Drag tab to body center or group strip | Existing identity moves; strip drop inserts before hit tab or appends; empty source collapses |
| Edge split preview | Drag to left/right/top/bottom body edge | Theme ColorHover preview only until release; Esc or release outside a valid target cancels; floating windows are not drop targets |
| Divider resize | Drag shared SplitDivider | Root Host capture retains motion/release outside divider |
| Split/move/merge/open/new/resize/focus/close | Command palette; tab context menu for split/move/merge | Registry-backed operations retain modal and terminal input ownership |
| Too-small terminal | Automatic compact layout | Preserves the recursive tree and selection; shows focused group only, restores all groups on enlargement |
| Close agent tab | Tab × / close action | Minimizes, never terminates; explicit Terminate is separate |

Main chats retain independent projections, including per-main-agent inline
subpane embedding. A session family has one composer/deck owner even with root
and child visible together. Focus aliases are bindings to the authoritative
group, not shadow state. Settings outside-click Ignore policy remains unchanged.
Main-resource floating pop-out/composition remains deferred. Git and local
read-only comparisons participate in the main groups; their aligned split reader
and immutable snapshot lifetime are documented in Main View Panes.

## Chat and Workspace

| Action | Binding / control | Notes |
|--------|-------------------|-------|
| Inspect active plan | Hover the chat activity line | Passive, exact-pane WindowFrame reader with complete steps and native scrolling. Moving into it keeps it open; dragging over its text selects and copies it on release, and a click enables keyboard navigation/Ctrl+C for the whole plan. Leave/Esc/outside, a modal, hidden owner or completed plan dismiss it. No composer/draft/session switch. |
| Jump to latest chat | Centered `↓ Latest` above activity while scrolled up; existing End with empty composer | Uses the existing reserved gap, never adds transcript height. Reaches the actual virtualized tail and re-arms stream following for that pane only. |
| Composer growth/scroll | Automatic one-to-six text-row viewport; wheel or cursor navigation | The full composer is also capped at one third of the available content column, leaving a readable transcript. Excess draft text scrolls internally; a quiet `↑↓ first–last/total` range appears beside its rule when it fits. Wheel browsing survives repaint, hit testing uses the painted offset, and navigation/edits reveal the caret. Typing/restoring/resizing never converts the draft to an attachment or trims content. |
| Browse prompt history | Up / Down at composer boundaries | Prefix-filtered recall preserves text and attachment snapshots; Down past the newest match restores the original draft. File I/O runs outside Update, with session and input-generation checks. |
| Search prompt history | `/history` | Searchable single-line summaries show time, prompt preview, and attachment count. Click or Enter expands full text and attachment details; **Use in composer** restores without sending. Missing media fails closed and leaves the draft intact. |
| Turn capabilities off/on for a session | `/skills`, `/mcps`; palette **Session Skills** / **Session MCP Servers** | Separate Skills and MCP servers views (no tabs) with a padded heading, search and a list of self-contained cards. Switch click or Space toggles for the selected session and all its agents (live, next request); clicking a card body or Enter expands/collapses its full details in place; empty views explain where to start; Esc closes. Preserves the composer and registrations, never launches MCP or grants access. **Install & manage** opens Settings; `/skill`, `/mcp` and the composer capability control are removed. |
| Open attachment | Click its filename chip or a linked history attachment | File-backed media opens with the OS default application; inline text opens the native reference editor. History previews use session-owned copies, never writable history assets. |
| Inspect/edit pasted text | Click its Pasted text chip; Space on the text token; attachment menu Preview | Inline and file-backed pasted text open the same movable/resizable, terminal-bounded native text window with the composer retained underneath. Reads/saves run off Update; full text stays available within the 64 MiB attachment read bound. Ctrl+S saves against exact reference/content preimages, Cancel/Esc leaves content unchanged, and a save error retains the editor draft. Copy never closes the editor. |
| Reveal attachment | Attachment context menu > Show in file manager | Windows Explorer selection, macOS Finder reveal, or the containing folder in the Linux file manager. Paths are arguments, never shell commands. |
| Remove attachment | Separate three-cell `x` target on the chip | Detaches tokens from the draft without deleting references owned by sent messages. Overflow and wheel navigation keep hidden chips reachable. |
| Session account / model | `/settings` or `/model` | Account labels only, no template groups. Model discovery uses that account's resolved connection and cannot update another session's deck. |
| Compact context and queue input | `/compact`, then Enter in the composer | Both connected shells show the exact operation and pending input rows. New prompts queue durably and run in order after compaction/natural completion; only the submitting shell's unchanged draft is consumed. Stop cancels extraction without running the queue; cold resume restores it passively. |
| Continue unfinished run | Automatic transport/DNS cooldown; inline **(Retry now)**; retained `/retry`, palette **Continue Agent Run**, context **Continue run** | Transient failures continue without human polling. Retry now shortens the exact incident/generation's cooldown; no draft consumption, silent tab switch or historical tool replay. Needs attention is an owned pause, not completion. Passive restart remains explicit. |
| Recovery disclosure | Compact header/chevron or ordinary chat disclosure shortcut | Expands the actual full reason/remediation; clock updates change only the red square/countdown header. Drag selection/copy takes precedence over disclosure and Retry now. Settled incidents have no retry target. |
| Network controls | Application Settings > Network (rail's last group); palette **Network Settings** | Connection / Proxy / Certificates share application authority. Workspace values are read-only. Save validates material off Update, keeps the busy form mounted and retains the draft on error. |
| Session default agent | `/settings` > Default agent | Pick Main Agent, a built-in role, or an enabled named agent for future root turns. Its account stays pinned to this session, switching choices retains the other root settings, and disabled/removed selections fail closed. |
| Auto Review Agent | Application/Workspace Settings > Agents | Select an explicit reviewer account/model, edit bounded extra restrictions, and configure thinking, response format, timeout, retries, token growth/ceiling, denied/unresolved behavior, and request/recovery bounds on the Review tab. There is no review-count cap. Every exact non-hard-blocked call, including unknown commands, can be reviewed. Each root and child has an independent reviewer lifecycle. YOLO bypasses the owning session tree only. Unresolved can ask or safely deny for unattended continuation, never approve. Security > Auto Review Policy is a synchronized view of the same persisted policy. |
| Permission completion | Allow / Deny / Always / cancel | An accepted decision clears that exact owner's pending approval in the same frame as the deck/modal closes. Deny rejects the operation, not the turn; command and file paths publish correlated completion before their rejected tool result, so subsequent frames/tab switches cannot revive “Needs approval”. A late completion cannot clear a newer request. Esc stops without fabricating denial. Completion never clears or rewrites composer text, attachment references, selection, undo state, or history position; only explicit send/clear actions consume a draft. |
| Permission presentation | Agent or child requests confirmation | Any painted member of the owning root family uses that family's retained dock. Concurrent family requests receive exact-owner tabs only while two or more exist; tab close denies that exact call. Leaving the family hides without discarding it, and returning restores selection. A background family, or a request received while an editor/file is painted, uses the FIFO global modal without switching tabs or sessions. Owner/subtree termination removes its active and queued requests. |
| Breadcrumb navigation | Click an individual ancestor label | Settings editors retain discard confirmation; session settings retain staged role edits. File-tree ancestors reveal their row, and file-picker ancestors navigate directories. Separators do not navigate. |
| Close Settings | Window × or footer Close/Cancel | Closes the entire Application/Workspace Settings window at every depth, never Back. Unsaved preferences and editor drafts require one explicit discard decision; Keep settings open preserves the current page and draft. Breadcrumbs/Back/Esc own navigation. |
| File-tree search and deep folders | Type while the tree is focused, or click its search field | Both paths edit the same retained filter and request the async path projection. Backspace on the final letter clears it; late results cannot restore the query. Every folder remains a row. Shared offscreen ancestry is compacted into a clickable breadcrumb; when visible rows reach another root branch, the normal top-row breadcrumb stays while absolute guides return. Narrow breadcrumbs keep the nearest folders visible. |
| Workspace details | Footer **Workspace** | Explicit Open Agents, Open Files, and Previous Sessions sections. Collapsed resources occupy one row; click, Enter, or Space toggles details; Right expands and Left collapses. Saved sessions expose an explicit Resume action inside their details. |
| Open Tabs details | Click header model / context telemetry | Same single-row disclosure pattern, restricted to visible tabs. Identity, current model, known call/cost totals and lifecycle share the row; less important facts yield first at narrow widths. Expanded details expose an Open action. |
| Terminate background command | Its row's Terminate button; compact close glyph on narrow grids | Only running rows expose the action. Click selects the exact command; Enter/Space activates a focused button, Up/Down or Tab moves between buttons, and Esc returns to the composer. Handle, PID, start time and owning session must still match. Finished/stale/replaced rows never signal a process, and the session and other jobs remain alive. |

Submitting text captures an immutable input identity and returns an off-UI
admission command. Only its exact session, agent, owner epoch, turn generation,
draft revision and workspace receipt can consume that submitted draft. Text,
references, cursor, selection, scroll and undo/edit ABA changes all invalidate
that consumption. A floating composer cannot consume the separate shell draft
or close a changed/reopened composer. Duplicate acknowledgement repeats neither
chat nor prompt-history effects.

Stop preserves the accepted original once in immediate chat and durable future
context without returning it to the composer. A newer unsubmitted draft remains
byte-for-byte and interaction-state intact. Rejected steering creates no human
chat row or accepted history. Exact-owner late semantic stream/tool evidence is
retained while stopping and after terminal acknowledgement, but never reactivates
animation; only a genuinely new runtime generation can start new work.
Ordinary stopped boundaries and cancelled recovery notices remain durable but
have no transcript rows or height; the fixed strip says only `Stopped`. Authentic
recovery actions and interrupted tool evidence are not hidden.

Prompt history owns content-addressed media beside `history.json` with
owner-only permissions. Multimodal entries never merge by text similarity.
Restoration imports fresh session references and rewrites only the recorded
chip spans; literal bracketed prose is not treated as an attachment. Imported
media is session-owned, so pruning the bounded history cannot break a resumed
session. Individual binary reads are limited to 64 MiB. Capture failures are
reported and marked unavailable rather than silently recalling text-only
versions. Application shutdown flushes pending history before closing sessions.
After terminal exit, idempotent UI teardown flushes private composer drafts and
accepted editor commands, retains unresolved editor recovery, and closes client
views/collectors. Connected project and lazy document watchers belong to the
retained core, not shells. Local/test adapters still close their UI-owned watchers.
Shell detach never closes shared sessions, LSP servers or PTYs.

Composer ownership changes at session activation, never during rendering. The
destination reference store is bound before recalled attachment IDs are installed,
including text-only recalls that must not capture same-named media. Warm sessions
retain the complete editor (text, attachment selection, cursor, selection, undo and
history navigation); cold drafts load off Update with activation/revision fences.
A recall intent outranks an earlier disk load, and pointer edits invalidate a late
recall just like keyboard edits. An unmodified loading projection must not save
an empty draft over unread persisted content. Teardown flushes inactive queued
drafts too. Stale paste completions never consume another session's pending paste
or release its Send barrier merely because numeric paste IDs coincide.

Regression journeys cover mixed image/text/file/MCP references, exact image bytes
reaching a local provider after Up → edit → Enter, literal-label collisions,
missing media, session switching with undo, cold-load races, failed saves, and
opposite completion orders for two sessions' pending pastes. Existing text editing,
capability selection, clipboard and attachment suites remain required gates.

Windows background-command termination uses the retained Job Object for both
pipe and ConPTY commands; normal completion cleans descendants before waiting
for inherited output pipes. Native acceptance remains a Windows-machine check:
`go test ./fileio -run 'TestWindows(KillRef|CompletedCommand|InteractiveCommands)|TestKillRef|TestSettledCommand' -count=1`.
These tests do not skip missing/broken process ownership: failures include
`TODO(Windows)` and the exact ownership, child-cleanup, unrelated-process or
ConPTY behavior to investigate. Linux cross-compilation is not a native pass.

## Editor context

Dirty state means an exact byte difference from the accepted disk savepoint,
not a different revision number. Manual reversal, undo/redo, and an in-flight
save acknowledgement all use the same immutable piece-span checkpoint. Restoring
saved bytes cancels pending autosave deadlines and closes without a save prompt.

The Git gutter uses an immutable HEAD baseline plus an exact incremental live
diff. Additions, modifications and deletion boundaries update on the edit, as
do change inspection and next/previous-change navigation. There is no speculative
local marker or separate settled representation. Background refreshes of the
same HEAD update metadata without replacing live hunks; branch/HEAD changes
establish a new baseline. Shared views and grouped undo beyond the edit-log
window derive from current bytes, not replayed change guesses.

| Action | Binding | Notes |
|--------|---------|-------|
| Undo / redo | Ctrl+Z / Ctrl+Y or Ctrl+Shift+Z | Atomic editing commands, adjacent typing groups; exact bytes and private caret/selection where valid. Pending shared history preserves every gesture and subsequent input order |
| Drag to scroll | Hold text/word/line selection at a viewport edge | Repeats with a stationary pointer in both axes; whole-line gutter drag works too. Release stops immediately and retains editable selection. Cancel, tab unmount, resize, terminal blur and modal takeover stop stale repeats |
| Move line/selection | Alt+Up / Alt+Down | One undo step each |
| Duplicate line/selection | Ctrl+Shift+D | Kitty/CSI-u terminals |
| Delete line(s) | Ctrl+Shift+K | Kitty/CSI-u terminals |
| Toggle line comment | Ctrl+/ (legacy Ctrl+_) | Prefix from language map / chroma |
| Indent / outdent selection | Tab / Shift+Tab | Shift+Tab outdents the current line without a selection |
| Smart Home | Home | Toggles first non-blank column and column 0 |
| Auto-close pairs | ( [ { " ' | Type-over closers; openers wrap an active selection; Backspace deletes an empty pair |
| Close tab / contextual back | Ctrl+W / Esc | Both use the dirty-safe close path; Ctrl+W is the explicit Close Tab command |
| Git line edits / blame | (gutter + header + inspector) | `+` added / `~` modified in the gutter; branch and compact current-line blame are in the header; full blame and Copy blame commit remain in editor detail/context actions |
| Horizontal scroll | Two-finger horizontal / wheel-left/right; Shift+vertical wheel fallback | Scrollbar extent is the widest line in the whole document and does not change with vertical viewport |
| Text cursor | Shared 400 ms phase after 800 ms activity hold | Muted reverse-video block; idle shell remains at 2.5 FPS so blink never freezes |
| Compare with disk / saved version | Editor context menu or palette | Complete read-only main tab, native split divider, synchronized old/new vertical scroll, unified fallback, find/hunk navigation. Refresh replaces only the exact snapshot; Close never consumes source edits. |
| Private editor drafts / discard backups | Palette **Private Editor Drafts** | Metadata-only private profile/workspace listing, explicit Preview/Copy/Open draft/Compare, default Close/Esc. Exact CAS protects unread/newer recovery. An already-open draft is compared, never replaced; reopening retains unknown command evidence without replay. |
| Go to / search result landing | LSP navigation, Find in Files Open | A target already visible does not shift the viewport. Pre-render jumps wait for actual geometry; short files stay at the top. A visible, non-editing landing range clears on input/edit; typing never replaces the destination as a selection. |

Find in Files retains query Home/End, routes Tab through result/preview/actions,
and supports preview scroll, selection and copy. File preparation runs off Update
and preserves the selected pane/resource and newer buffers. Old-query activations
are rejected; an edited target is used only if the exact range survives or has
one unambiguous occurrence on the same line. File-only matches preserve an
already-open document's cursor. Fuzzy results retain useful identifier/path
abbreviations but reject loose accidental subsequences; no good match shows an
explicit empty result instead of unrelated suggestions.

## File tree context

| Action | Binding | Notes |
|--------|---------|-------|
| Search files | click persistent search, `/`, or any printable char | Hint is `Search in <workspace>`; trailing badge shows aggregated git + diagnostics like the Agents count; fuzzy subsequence; ancestors auto-expand; Esc clears; mouse focus and type-to-filter share one field |
| Refresh tree | Ctrl+R | Relocated from `r` (typing now filters) |
| Toggle hidden files | Ctrl+. | Relocated from `.` |

Connected trees project the core Git snapshot and versioned LSP diagnostics,
including status arriving before a directory page and collapsed or filtered
descendants. File/index changes refresh status without opening Git; clean state
clears old markers. Shells never add a local Git resolver or project watcher.

## Git panel (bottom dock)
| Action | Binding / control | Notes |
|--------|-------------------|-------|
| Open / focus / hide | Footer **Git** pill, `Ctrl+Alt+G`, header Git identity menu | Opening focuses the change list; repeating with focus inside hides the dock |
| Move / preview | Up/Down, PgUp/PgDn, Home/End, click | Selection drives the live hunk preview; double-click or Enter opens the full review at that file |
| Stage / unstage | Space, row `+` / `−`, section **Stage all** / **Unstage all**, `a` / `-` | Unsaved editor bytes are refused; after staging the cursor stays in place |
| Discard | Row `↶`, `d` | Danger confirmation with Cancel focused, exact preimage, Local History recovery; untracked files are never deleted |
| Open file | `o` | Opens at the first changed line |
| Generate message | `[ Generate ]` beside Commit; `[ Stop ]` or Esc in the message while writing | Aeron (the narrow-task model role) drafts from exactly what Commit would record; text streams into the message under an animated progress rule; Stop, failure or timeout restore the previous draft. Commit is disabled while writing |
| Resize list/preview | Drag the divider (or focus it and use ←/→) | Same shared split control as every other pane; the review's navigator/document divider works the same way |
| Commit | **Commit** button, commit chord (Ctrl+Enter, or Alt+Enter where Ctrl+Enter is not delivered) in the message, `c` focuses the message | Commits the shown staged set via Git (hooks/signing apply); nothing staged → "Commit all N" stages exactly the listed paths; a different staged set or a conflict fails closed |
| History / Branches | Tabs, `1` `2` `3` | History previews a commit read-only, Enter opens it in the main area; a branch row's menu offers Switch (after confirmation) and Merge into current |
| Full commit message | **Edit…** beside the message | Dialog with the whole message, Save / Commit / Cancel and the repository's commit conventions |
| Fetch / Pull / Push | Status-row buttons (**Fetch**, **Pull ↓n**, **Push ↑n**, **Publish** without upstream); **Stop** while running | Pull merges (`--no-rebase`); Push is never forced. Authentication is never prompted on the terminal; failures name the cause |
| Merge conflicts | Status row **Resolve n**, **Abort**, **Complete** during a merge | Resolve opens the merge resolver: per conflict choose ours, theirs or both, preview, Save. Each save takes a Local History snapshot; Complete is enabled only when no conflicts remain |
| Collapse a side | Divider ◂ ▸ controls, `[` / `]` | Temporarily closes Changes or History; the same control restores it |
| Refresh | Ctrl+R | Explicit refresh shows "refreshing…"; watcher reconciliation stays silent |

## Git review (main area)
| Action | Binding / control | Notes |
|--------|-------------------|-------|
| Navigate | Up/Down/PgUp/PgDn/Space, `]` `[` (Alt+→/←) file, `n` `p` (Alt+↓/↑) hunk | The file navigator follows the reading position; clicking a file scrolls to it |
| Fold | Click a file header, `z` current file, `Z` / **Collapse all** every file | Click a `⋯ N unchanged lines` gap to expand it |
| Per-file actions | Header Stage / Unstage / Mark Resolved / Discard / Open; `s` `u` `d` `o` | Stage/unstage records the painted bytes and fails closed when they changed |
| Per-hunk actions | Hunk header Stage hunk / Unstage hunk / Discard hunk; `+` (stage, or unstage when staged), `-` unstage, `x` discard | Targets the hunk at the reading position; only that hunk's lines change in the index or on disk. Discard asks first and captures Local History; any newer change fails closed |
| Select / copy | Drag (copies on release), Shift+↑/↓ whole lines, Ctrl+A current file, Ctrl+C, Esc clears | Copies source text only: no gutters, markers or split alignment rows; split selections stay on the side they started |
| Find | Ctrl+F, Enter / Shift+Enter or F3 / Shift+F3, Esc | Case-insensitive across every shown line; folded files and gaps are not searched |
| Open a line | Double-click any line | Opens the editor on that line (deleted lines land on the nearest surviving line) |
| Presentation | `v`, **Split** / **Unified** link | Split needs 100 columns; narrower reverts to unified and keeps the preference |
| Tabs | Every review is its own main tab: all changes (`[ Review all ]`), one file (Enter / double-click in the panel), a folder or file (Explorer/editor **View Changes**), one commit (History Enter) | Any number stay open, move and split like documents; reopening the same scope or commit activates its tab. A scoped tab offers `[ All changes ]`; Esc returns to the tab it came from |

## Pane windows

| Action | Binding / control | Notes |
|--------|-------------------|-------|
| Open a tab in a window | Tab context menu **Open in Window**, palette **Open Tab in Window** | Moves the same tab (editor, agent, surface, review) into a movable, resizable window |
| Open a file / agent in a window | File tree **Open in window**; Agents panel **Open in window**; **⇱ window** on a Started-agent row | Sub-agents never open by themselves; **⧉ open** opens a main tab |
| Open a dock surface as tab / window | Dock header **⧉** / **⇱**; palette Open Panel as Tab / Window | The surface leaves the dock; closing its tab returns it there |
| Focus | Click inside the window; click the main area or sidebar to leave | Keys follow the focused window's editor or composer; popups opened from it keep it focused |
| Return to the main area | Title **⇲**, window **✕**, tab menu **Move to Main Area**, palette **Move Window to Main Area** | Tabs are never closed by closing the window; a lone dock surface returns to the dock |

## Agents panel

| Action | Binding / control | Notes |
|--------|-------------------|-------|
| New Agent | `+ New Agent` command row | Same single-click button pattern as New Terminal. Starts a session from the sidebar (also palette `agent.newSession` / launcher New Session via `launcherNewSession`). **Must add a window tab and make it the active/painted view immediately**, even when editor tabs are already open — closing those editors must never be required to see the new agent. Background `syncAgentWindowTabs` (tick) still creates missing agent tabs but must not steal the editor; only explicit user actions call `activateAgentWindowTab`. Guarded by `tui/viewmodel/agent_new_session_focus_test.go`. |

## Terminal panel

| Action | Binding / control | Notes |
|--------|-------------------|-------|
| New / float / close | `+` `⇱` `✕` header icons | Panel header, right of the tabs (VS Code style) |
| Select terminal instance | Compact numbered tabs; picker `≡` for overflow | Tab labels and terminal actions share one row; selecting retains the same PTY and focuses its body. All tabs use the shared baseline and active-tab scrolling. |
| Browse overflowing tabs | Horizontal trackpad/wheel, wheel up/down over strip, `‹` / `›` | Browsing never changes the active PTY or snaps back on repaint. Labels are `Terminal 1` / `Terminal 2`, adapting to `T 1` / `T 2` when cramped; picker numbers match and placement is named, never `0`. |
| Dock a floating terminal | `⇲` title-bar button | Same PTY, no restart |
| Scrollback | Wheel, Shift+PgUp/PgDn/Home/End | Primary screen only; typing snaps to bottom |
| Selection / copy | Drag; double-click word; triple-click line | Copy-on-release + Ctrl+Shift+C; Shift bypasses app mouse tracking |

Workspace, Problems, and Notices put search and More/Maximize/Hide on the same
header row. Memory and Board put their tabs or detail title beside those host
controls; browse search and local actions share a row. Narrow panels replace the
local buttons with a same-row More button; its action list preserves all commands
and restores the retained search/query on Back or Esc. Extremely constrained
slots retain a vertical action fallback rather than clipping buttons.

## Focus precedence (current)

1. Floating window (WM)
2. Blocking session Panel Deck
3. Focused workspace terminal dock
4. Launcher
5. File tree
6. Tab bar
7. Editor
8. Chat input

Passive background-job panels update their owning session deck without taking
focus or displacing an active interactive panel. Failed and killed jobs request
tab attention until the panel is activated or closed.

## Click-outside policies (current)

| Behavior | Surfaces |
|----------|----------|
| CloseAndRoute | context menu, LSP popups, diagnostics |
| KeepAndRoute | find/replace, find-in-files, replace-in-files, composer, palette, textoverlay, floating terminals |
| IgnoreAndConsume | dirty prompt, workspace-switch confirmation, tree prompt, tree delete, filepicker, workspace settings |

## Collection navigation baseline

| Control | Up/Down | Page | Home/End | Wrap | Enter |
|---------|---------|------|----------|------|-------|
| Command palette | yes | yes | no | clamp | activate |
| File picker | yes | yes | yes | clamp | open |
| Find in Files | yes | yes | yes | clamp | open; Cc/.* sit on the right of the query; Cc click toggles case, .* click or Ctrl+R toggles regex |
| Replace in Files | yes | yes | yes | clamp | same search surface; Replace/All on the right of the replace row; the commit chord replaces all |
| File tree | yes | custom | no | clamp | expand/open |
| Context menu | yes | no | no | wrap | activate |
| Launcher | Up/Down | viewport | yes outside the search editor | clamp | open exact resource; Tab/Shift+Tab visit original action controls, Skills, MCP, search, resources and Retry; hover never redirects keyboard activation |

### Workspace home

The Recent Workspaces rail is an independent navigation surface, not a route
back to home. Its two-row resource targets and shared list/scrollbar remain
available while any main-pane topology or floating editor is open. Both views
consume one asynchronous, cancelable catalog, including after home unmounts.
Search, selected identity, scroll and width survive sidebar destination changes.
Right-click offers exact-path Open/Copy and New Workspace/Refresh; old-menu
callbacks cannot switch a replacement attachment. Home keeps its existing
recoverable Delete operation separately.

Connected switching keeps the old attachment, tabs and processes until the
destination is ready. Unsaved shared editors offer Save / Close view / Cancel;
Close view retains shared unsaved bytes and authorizes only that exact revision.
Save completes before the same switch intent continues. Cancel, failed saves,
peer/local edits during preparation and failed attachments keep the current
workspace and drafts. Pending local edits and history settle through the existing
document coordinator, never a blocking UI-thread flush. Opening/preparing status
and Esc-to-cancel remain visible; cancellation invalidates the exact sequence even
when the backend ignores its context. Prepared but unadopted attachments have a
single cleanup owner through cancellation, timeout and shutdown. Switching away
does not terminate the old core's agents or PTYs.

- Failed New Session/Resume opens a native recovery modal with exact reason,
  useful repair guidance, and explicit Settings/account repair action. Notices
  are independently registered but optional; error text under New Session or a
  “go check Notices” instruction is prohibited. Close/Esc keeps the launcher and
  never automatically retries, resets, or selects another account.

- On wide grids, the original four-row New Session control (cycling sparkle,
  project path and Enter hint) spans the complete top section. Beneath it, the
  original folder-icon New Workspace control starts the left pane. There is no
  additional workspace heading or duplicate Settings, Help, palette or Sessions
  control. On cramped grids the two action controls stack with exactly one blank
  row between them. Retry belongs to the Recent workspaces heading row and never
  shortens the dedicated search row.
- Skills and MCP retain their named current resources and independent scrolling
  in the wide left pane. After one blank row below New Workspace, they divide all
  remaining height equally. Clicking a resource opens its exact source-qualified
  detail in the existing manager. The left pane, including both capability panels,
  is hidden in cramped mode.
- Recent workspaces occupy the right pane, never a full-width resource grid.
  Each name and path share a two-row hit target, with one quiet, non-interactive
  row between resources. The last resource has no trailing spacer. Search always
  owns a separate full-width row below the heading and remains fixed while
  scrolling. Its navigation guide is pinned to the bottom of the pane regardless
  of resource count. Below 72 available columns Recent workspaces takes the full
  content width beneath the stacked actions.
- Loading uses a thin `ProgressRule` ink sweep, not blinking dots or fake
  percentages. The last successful listing stays usable during refresh. Only
  the current owner/generation can settle loading; errors expose Retry.
- Capability state is initialized without starting a session. Superseded refresh
  cancellations cannot paint failure over a newer result. Last usable skill/MCP
  rows stay accessible on observation failure; configuration attention outranks an
  idle transport, and cached discovery alone never claims connected.
- Right-click a workspace name or path offers Open, Copy path and Delete
  workspace. Delete opens an asynchronous review with Cancel focused. It moves
  saved workspace/session data into recovery storage; project files are never
  removed. Undo in the completed dialog refuses a later replacement workspace.
  Recovery location is recorded in Notices and an on-disk manifest; after the
  dialog closes, recovery is manual. The bare D key never deletes saved data.

## Known defects to fix during migration

1. TextPrompt cursor byte offsets. **FIXED**: prompts use shared grapheme-aware TextField/TextEdit geometry.
2. Form TextItem shift-selection. **FIXED**: shared TextField/TextArea own selection, drag capture, and shortcuts.
3. FindInFiles reimplements input paint. **FIXED**: native `tui/screens/findinfiles` uses `ui.SearchField` surface paint and debounce identity.
4. Multiple filter strings without selection/word motion/paste.
5. Scrollbar colors hard-coded outside theme.
6. Three chrome systems disagree on padding and close buttons.
7. Modal=true combined with CloseRoute on some popups (contradictory).
8. Blocking PanelDeck pointer shielding. **FIXED**: while a blocking panel is
   active, clicks/wheel/motion/releases outside its slot are consumed before
   root-Host hit testing. Deck-owned drags may still release outside; stale
   captures owned by underlying shell controls are canceled. Guarded by
   `TestPanelDeckViewmodelContractTable/blocking_panel_shields_pointer_outside_deck_rect`.

## Connected shared-editor decisions

Editor open, edit, undo/redo, save, format and close use exact core document
identities/revisions with attachment epochs. Each shell retains cursor, selection
and viewport; author undo does not undo another shell's accepted group. Dirty
unsaved text is shared, not silently written to disk. Rename preserves document
identity and dirty text; conflicted delete fails closed.

Resolve Shared Editor Changes in the palette opens the native modal for parked
or recovered drafts. Keep draft (default/Esc) changes nothing. Compare with
shared opens immutable, read-only shared/local snapshots and does not consume the
draft. Use shared text requires explicit discard confirmation and durable private
backup, then clears only the exact private recovery record by tombstone. It never
rewrites collaborators' accepted text. Pending/unknown commands require original
receipt reconciliation first, never replay. External disk conflicts need separate
comparison/reconciliation; accepting shared text is not permission to overwrite
external changes. Failure keeps the draft and actionable reason visible.

## File tree filter and guide geometry

- Connected trees project the header/panel's one core Git snapshot and retained
  diagnostics, including unloaded descendants and filtered search matches. Both
  ordinary file and Git metadata changes arm reconciliation. Directory response
  order cannot erase status; clean snapshots clear it. Never add client resolvers
  or project watchers to repair missing replica metadata.
- `shelltree.FilterField` is the single text owner for clicked input AND
  type-to-filter while the tree has focus. `FileTreeModel` may derive its
  in-memory projection when no async path-search service is installed; the
  product's async search never writes legacy model filter text over the input.
  Clearing the final character cancels the old generation, so late results
  cannot resurrect a removed query.
- The shared `filetree.NewTreeRowPainter` adjusts the preferred whitespace
  between ancestor rails using the visible tree's cached maximum depth and
  viewport width. The shell computes the deepest offscreen ancestor shared by
  every painted row. Its clickable breadcrumb owns that common prefix, and the
  row painter omits ONLY those repeated ancestor guide columns. A viewport
  crossing branches rebases to their common ancestor. When it crosses the
  workspace root, the first visible row keeps its normal breadcrumb and ALL
  absolute guides return; do not hide the breadcrumb in that transition.
  narrow breadcrumbs keep the nearest parents and a non-clickable `…` prefix.
  Local guides keep one column per remaining ancestor even on narrow grids;
  directory rows, disclosure, scroll and path ownership stay intact. Focus and
  selection alone never change guide alignment. Do not replace this with
  single-child path flattening or row-local indent math that makes rails jump.
