# Core protocol v2: full-duplex wire, streaming state and remote connections

> Status: IMPLEMENTED, ACCEPTANCE PARTIAL — local gates pass; native cross-host/platform and full visual-matrix acceptance remain open.
> Current contracts are the [core client protocol](../reference/core-client-protocol.md),
> [shell port](../reference/shell-port.md), [in-app terminals](../reference/in-app-terminals.md)
> and [network transport](../reference/network.md). This plan retains acceptance
> thresholds and the per-feature visual/fuzz matrix; implementation is not proof
> that those gates passed. Do not delete the plan while any matrix row is open.

## Goal

One wire for every client (the TUI today, any other client later) and every
feature (agents, panels, terminals, editors, files, Git, settings). It must:

- stay snappy on a loopback socket, a LAN or a lossy intercontinental link;
- move large updates with minimal CPU, allocation, GC and bytes;
- never let a slow peer, bulk transfer or burst block input or other features;
- survive disconnects, roaming and restarts without losing control or state;
- serve any number of clients on any number of machines at once;
- run any program in its terminals regardless of the outer terminal's
  capabilities (kitty or legacy keyboard, grapheme or legacy width, synchronized
  output or not, Windows ConPTY or Unix PTY).

Rules that do not change: typed closed dispatch, durable mutation receipts and
the no-replay rule, owner/epoch fencing, document revision fencing, write-only
secrets, one authority per state. Pre-alpha policy applies: v2 replaces v1
wholesale; no adapters, dual stacks, legacy readers or compatibility branches
remain in Crude's own machinery.

## Original pre-refactor baseline (2026-10-23, Linux, i7-10750H)

| Observation | Measurement |
|---|---|
| Terminal `rows` page 80×24 / 150×45 / 200×50 | 412 KB / 1.4 MB / 2.1 MB JSON; ~14 / ~50 / ~70 ms decode; ~200 bytes per cell (200×50 visible text ≈ 10 KB) |
| `q` in htop, pointer off the pane | 0.06–0.12 s |
| `q` in htop after 300 pointer motions over the pane | 9.3 s, reproduced three times; with the pointer off the pane the same motions cost nothing |
| Shell CPU during that flood | 52% `corewire.strictJSON`, 27% `rows` unmarshal, 12% GC, 6% paint |
| Core CPU during that flood | under 1% |
| 400,000-line `seq`, then `echo` | visible after ~1 s; no resync observed |
| Idle shell with a terminal open | ~3.6% of one core: full shell repaint per blink/tick plus a JSON `rows` read |
| Tilix (VTE 0.76) replies | DEC 2026 permanently reset, mode 2027 unrecognized, no kitty keyboard |

A headless Xvfb comparison did not reproduce a Tilix-only slowdown (typing ≈ 60 ms,
htop redraw ≈ 100–110 ms in both Tilix and Alacritty). The defects below affect
every outer terminal; Tilix remains a named acceptance target.

### Original root causes (historical, not current contracts)

1. **Streams forced through request/response.** Core events are invalidations;
   one shell fetch worker then pulls sessions, the session list, `agent.changes`,
   status, settings, terminal inventory and LSP serially, each a JSON round trip
   (`shellport/remote/remote.go`).
2. **Terminal input serialized behind full-screen reads.** Every key, mouse
   event, paste, focus and resize waits for the previous one and is followed by a
   full viewport `terminal.rows` read (`tui/viewmodel/terminal_client.go`). The
   shell subscribes to every pointer motion, so a pointer over a mouse-tracking
   program queues one full page per motion.
3. **JSON decoded twice.** Every frame is strictly scanned and then unmarshalled
   again (the deleted corewire/frame.go); payloads nest JSON inside JSON.
4. **Whole values instead of deltas.** `Conversation.ChangesSince` sends the whole
   current message per change (`workspace/conversation_delta.go`), so streaming a
   reply costs O(length) per flush; text reaches shells on the workspace actor's
   100 ms tick (`core/runtime.go`).
5. **Terminal output coupled to the workspace actor.** Each PTY read emits one
   `TerminalChangedMsg` (`workspace/terminal_service.go`) that bumps the workspace
   revision and fans out to every shell; the PTY reader blocks on the actor's
   queue, shared with agent and file events.
6. **Slowness disconnects.** Subscription overflow publishes a resync that the
   client treats as connection failure; it reconnects with 100 ms–5 s backoff and
   reloads full snapshots. A full server output queue or a 17th in-flight request
   closes the connection (`coreclient/client.go`, `core/server.go`,
   `core/runtime.go`). Detach revokes terminal leases, so after reconnect the pane
   silently ignores input.
7. **Presentation round trip.** The shell composes cells, serializes them to an
   ANSI string, and Bubble Tea parses that string back into cells before diffing
   to the outer terminal; cursor blink repaints the whole shell.
8. **Backend choice is a CLI flag.** `--ssh HOST --workspace PATH`
   (`app/shell_client.go`) is the only route to another machine; there is no
   connection manager, network transport or client-owned connection store.

## Original proposal (design intent, not current implementation truth)

The principles, designs and execution order below record the original proposal.
They are not a second current contract or claims that every proposed mechanism
landed (for example future WebSocket/QUIC transport capabilities). Follow the
linked current references for implemented carriers, authentication, source-cohort
admission, acknowledged topics, transfer bounds and lifecycle rules. The acceptance
thresholds and complete per-feature matrix remain binding and unchanged.

### Design principles

1. **One wire model for everything.** Every interaction is a command, a topic
   or a stream. No feature-specific transport, polling loop or side channel.
2. **The Go domain types are the schema.** The codec is generated from them; no
   parallel wire structs, no conversion layer, no hand-written field mapping.
3. **Bytes are the unit of work.** Encode once into pooled buffers, share across
   subscribers, decode into reused values, pass bulk payloads zero-copy, batch
   writes into one syscall. Steady-state hot paths allocate nothing.
4. **State, not history, for presentation.** Viewers receive the newest state
   paced to their link and display; intermediate presentation states are skipped.
   Ordered logs (input, edits, transcript appends) are never skipped.
5. **Credit, not disconnection.** Flow control throttles; only liveness loss or
   protocol violation closes a carrier, and a session outlives its carrier.
6. **Carrier independence.** QUIC, local sockets, SSH and WebSocket all present
   the same session API; everything above it is identical.

## Established designs this plan follows

| Concern | Reference | What we take |
|---|---|---|
| Internet transport | QUIC (RFC 9000/9001/9002, datagrams RFC 9221) via quic-go (Caddy, Syncthing, Traefik, cloudflared) | Independent streams without TCP head-of-line blocking, TLS 1.3, 0-RTT resumption, connection migration |
| SSH-authenticated fast path | mosh: SSH bootstrap, then a direct UDP session keyed over SSH | SSH authenticates and hands over a one-time QUIC credential; SSH stdio remains the fallback carrier |
| Multiplexing over a byte stream | SSH connection protocol channels (RFC 4254), HTTP/2 streams | Per-stream credit windows, small frames, priority classes |
| Viewer pacing | mosh State Synchronization Protocol (frame interval 20–250 ms in `transportsender.h`) | Diff from the viewer's acknowledged state to the newest, interval from RTT and display rate; predictive local echo on slow links |
| Terminal grid updates | Neovim `ext_linegrid` (`grid_line` runs of `[text, hl_id, repeat]`, `hl_attr_define`, `grid_scroll`, `flush`) | Style table per connection, run-length rows, scroll ops, frame commit |
| Mux render state | WezTerm mux (dirty lines, sequence numbers, zstd above a threshold) | Sequenced render state, dirty-row tracking, compression only where it pays |
| Wire format | MessagePack (standard, implementations in every major language) generated by tinylib/msgp v1.6.5 | Zero-reflection generated codecs on the Go types, positional tuple encoding, append-style encoding into reused buffers |
| Stream compression | zstd with context takeover (klauspost/compress v1.20.3) | One streaming compressor per stream on network carriers; off on local sockets |
| Remote development UX | VS Code Remote-SSH (Remote Explorer, SSH config targets, matching server installed on the remote, host indicator) | Remotes view, automatic matching core install, header backend indicator |

Versions verified against the Go module proxy on 2026-10-23: quic-go v0.61.0 is
the newest release that builds on Crude's Go 1.25 (v0.62.0 requires Go 1.26);
msgp v1.6.5 and klauspost/compress v1.20.3 require Go 1.24 or earlier.

## Architecture

```
 client (TUI, later web/GUI)                               core (per user, any host)
┌──────────────────────────────┐                        ┌──────────────────────────────┐
│ presentation (direct cells)  │                        │ domain owners (actors,       │
│ replicas (topic state)       │                        │  terminal engines, documents)│
│ client SDK                   │                        │ topic hubs: encode once,     │
│  commands · topics · streams │   session (resumable)  │  per-viewer pacing + credit  │
│  connection store + keychain │ <====================> │ command dispatch + receipts  │
│ carrier: QUIC | stream mux   │  QUIC / socket / SSH   │ carrier: QUIC | stream mux   │
└──────────────────────────────┘                        └──────────────────────────────┘
```

### Carriers and the session layer

- **Session API** (one interface, two implementations): open/accept typed
  streams with a priority class; per-stream credit; heartbeat with RTT and
  jitter estimates; carrier-level close; resume.
- **QUIC carrier** for every network path. Each stream is a QUIC stream, so loss
  on one topic never stalls another. 0-RTT resumption and connection migration
  keep sessions alive across Wi-Fi changes and sleep.
- **Stream-mux carrier** for reliable byte streams: local Unix socket / Windows
  named pipe (existing ownership and ACL rules), SSH stdio (existing strict host
  key rules), TCP+TLS when UDP is blocked, WebSocket for future browser clients.
  Frames are `varint stream · type byte · varint length · payload`, at most
  16 KiB each, so a keystroke waits behind at most one bulk chunk. One writer
  per carrier drains streams by priority (control > input > interactive state >
  bulk) and batches ready frames into a single vectored write.
- **Sessions outlive carriers.** A session has an ID and resume secret; after
  carrier loss the core keeps subscriptions, sequence positions, controller
  leases and unacknowledged input for a grace period (default 15 minutes,
  configurable). The client resumes over any carrier (QUIC after a roam, SSH if
  UDP disappeared); topics continue from the last applied sequence and reset
  only if their journal no longer covers the gap.
- **No slowness disconnects.** Credit throttles producers; state topics conflate,
  log topics reset explicitly. Only missed heartbeats or protocol violations close
  a carrier.

### Encoding

- MessagePack, generated by msgp from the existing Go types with positional
  tuple encoding. The survey of every type reachable from `coreprotocol/` found
  373 named types in 19 packages, no interface, function or channel fields and no
  non-string map keys, so generation needs no special cases.
- The 17 `json.RawMessage` fields carry real JSON documents (tool JSON Schemas,
  settings override sections, control inputs) and travel as opaque bytes, not
  re-encoded. The v1 `Request.Payload`/`Response.Payload` envelopes disappear:
  each message is its own typed value.
- The one external type, the LSP `WorkspaceEdit` from the powernap fork, keeps
  its own standard: it travels as its JSON bytes through a msgp shim, with no
  Crude mirror type.
- Encoding appends into pooled per-stream buffers; decoding fills reused values;
  byte fields reference the receive buffer until the consumer copies or
  releases them.
- Hard limits: frame size, message size and collection counts are checked
  against the remaining bytes before any allocation. If msgp's generated
  decoders do not already guarantee this, Crude carries the guard in a forked
  generator template under `third_party/`, like its other forks.
- Hot payloads use purpose-built packed layouts inside byte fields: terminal
  grid rows, transcript text appends, document operations and file chunks.
  Decoding them allocates nothing per cell, character or chunk.
- Compression: zstd with context takeover per stream on network carriers, above
  a small threshold; disabled on local carriers where CPU matters more than bytes.

### Interaction model

| Pattern | Semantics | Used by |
|---|---|---|
| **Command** | Typed request and response on a command stream; any number in flight within credit; durable receipts, owner fencing and no-replay unchanged | All mutations and one-shot reads: startup, workspace attach/delete, sessions, settings, accounts, capabilities, control, Local History, references, prompt history, memory, Git and file mutations, LSP requests |
| **Topic (log)** | Subscribe returns snapshot at sequence S, then ordered deltas; never skipped; resumable from a sequence; explicit reset if the journal no longer covers a gap | Agent transcripts (per session, subscribed only while shown), live documents (revision-fenced operations), file tree changes, notices |
| **Topic (state)** | Same subscribe/resume, conflated to the newest value at the viewer's pacing | Status, settings projection, session list, terminal inventory, LSP diagnostics and health, Git snapshot, Board, presence, terminal grids |
| **Input stream** | Ordered, fire-and-forget, no per-item reply; failures arrive as typed events on the stream | Terminal key/mouse/paste/focus/resize, editor cursor/presence |
| **Bulk** | Chunked, credit-controlled transfer in either direction with digest verification | File reads and uploads, media, large payloads (replaces `payload.read` polling and the 8 MiB mutation ceiling) |

Topic hubs live with each domain owner. A hub encodes each delta once and hands
the same bytes to every subscriber; per-viewer work is limited to pacing and
credit.

### Terminals

- **One engine, out of the workspace actor.** libghostty-vt in the core stays the
  only parser and reply source. Terminal state and output never touch the
  workspace revision or the agent/file event queue.
- **Grid topic.** Per subscriber the core remembers the last acknowledged frame
  sequence. At each pacing tick it sends the rows changed since then as runs of
  `[grapheme text, style id, repeat]`, `scroll` operations for scrolled regions,
  cursor and modes, then `flush` with the new sequence. Styles are defined once
  per connection; per-cell truecolor content (video, image-like output) uses
  inline colors. Ghostty's render-state dirty tracking supplies changed rows;
  DEC 2026 is honored in the core before a frame becomes eligible.
- **Pacing:** interval = clamp(RTT / 2, the client's declared display interval,
  250 ms). Local viewers get display-rate frames; distant ones get fewer frames,
  never slower input. PTY bytes are never dropped.
- **Input stream** under the controller lease: keys, paste, focus and resize are
  strictly ordered. Pointer motion is coalesced on the client to the latest
  position per frame and sent only when the child enabled motion tracking.
- **Predictive local echo** (mosh-style) when RTT exceeds a threshold: printable
  input is drawn immediately with a subtle underline and confirmed or rolled back
  by the next frame. Off on local carriers.
- **Scrollback** pages are fetched on demand with the same packed row encoding.
- **Outer-terminal independence.** In-app terminals never depend on the outer
  terminal's protocols. Grapheme widths come from the shell's single width
  profile (existing probe); keyboard input arrives as neutral key events whether
  the outer terminal speaks kitty or legacy; the core encodes for the child with
  Ghostty's encoder. One outer-terminal adapter in the shell owns capability
  negotiation (synchronized output, keyboard protocol, width probe, color depth)
  and nothing else reads raw capabilities.
- Budget: an ordinary 200×50 text frame is a few KB; a full truecolor 200×50
  video frame is about 80 KB before zstd (30 fps ≈ 2.4 MB/s), which is comfortable
  locally and on a LAN and is paced down on slower links.

### Transcripts, documents and the rest

- Transcript appends travel as `append {message id, offset, text}`, idempotent by
  offset; structural changes (tool rows, status, reorder) remain upserts. Text is
  published as it arrives and paced per viewer instead of the 100 ms actor tick.
- Documents publish revision-fenced operations on a log topic; edits stay commands
  with receipts; author undo and conflict rules are unchanged.
- Status, settings, Git, LSP, inventory and Board become state topics; the fetch
  worker and every pull-after-invalidate path disappear.

### Presentation engine (shell)

- The compositor draws cells directly into the renderer's buffer (the Bubble Tea
  and Ultraviolet forks under `third_party/` expose a drawable surface), removing
  ANSI string generation and re-parsing. The renderer diffs cells and writes the
  outer terminal off `Update` as today.
- Repaint is driven by component invalidation; cursor blink and terminal frames
  invalidate their own regions instead of a whole-shell pass.
- Presented frames are paced by measured flush time, so a slow emulator gets
  fewer complete frames instead of a backlog. Mutation is never delayed and the
  idle cadence stays at least 2 FPS.

### Multiple clients

- Any number of clients, local or remote, attach to one core concurrently, each
  with its own subscriptions, pacing, credit and session. A throttled client
  never affects another.
- Controller leases bind to sessions. A lease whose session expired is
  released; viewers see an explicit **Take control** action, never silently
  dropped keys. Terminal size follows the controlling client.
- The core remains single-user: every client acts as the user who owns the core.

### Remote access and authentication

- **SSH (default):** the user's SSH setup authenticates (keys, agent,
  `~/.ssh/config`, ProxyJump) with the existing strict host-key checks. If the
  remote core build differs, the client offers to install the matching build into
  the remote's Crude data directory over the same SSH connection. The core then
  hands the client a one-time QUIC credential and certificate fingerprint over
  the authenticated stream; the client switches to QUIC and keeps SSH stdio as
  the automatic fallback when UDP is blocked. SSH passwords are never stored.
- **Direct (opt-in on the core host):** the core listens on a configured UDP
  address (QUIC) and TCP fallback. Pairing shows a one-time code on the core host;
  the client pins the certificate fingerprint (trust on first use) and receives a
  per-device credential. Devices are listed with last-seen time and are
  individually revocable; pairing attempts are rate-limited and audited.
- **Local:** the per-user socket or pipe, always available.

### Client connection store

- Client-owned, beside the existing `clientstate` store (`clientstate/store.go`):
  connections (name, carrier preference, host, user, port, identity file, pinned
  certificate fingerprint), per-connection recent workspaces, last used backend.
- Device credentials live in the OS keychain (macOS Keychain, Secret Service,
  Windows Credential Manager), falling back to a 0600 file in the client data
  directory with a visible notice where no keychain exists.
- Every client machine keeps its own store; nothing is shared through the core.

### Product surfaces

- **Remotes** activity-bar entry (`tui/screens/activitybar/bar.go`) with the
  standard activity list: Local plus saved connections, each with state
  (connected, reconnecting, offline, needs install), carrier and RTT, expandable
  to recent workspaces. Actions: Connect, Open workspace, Disconnect, Edit,
  Remove, Pair.
- **Add connection** dialog in the shared dialog language: SSH host (offering
  SSH config hosts) or Direct (address and pairing code), with a live connection
  test before saving.
- **Backend indicator** in the header: current backend, carrier and link
  quality; clicking opens the switcher. Palette: "Connect to…", "Switch backend".
- **Settings › Network › Remote access** on the core host: direct access toggle,
  listen addresses, pairing, device list with revoke, session grace period.
- Switching backends is a workspace-switch generation boundary using the
  existing dirty/draft/preparation lifecycle; the destination attaches before the
  current one is released, and a failed destination leaves the current one usable.

### Client-agnostic contract

The core client protocol reference documents carriers, stream types, topics,
commands and the tuple layout of every message, generated from the same Go types
as the codec. Hello negotiates the exact build; a mismatch offers the matching
install rather than a partially compatible session. The Go client SDK
(`coreclient/` and `shellport/remote/` today) owns carriers, sessions, topics and
the connection store; presentation code consumes only replicas, commands and
streams.

### Persistence

Only wire-adjacent persistence changes: durable receipts store typed response
bytes. Human-edited configuration stays JSON. Session database blob formats remain
unchanged; a later change requires the documented backup and manual alignment
procedure for existing user data.

## CPU, allocation and GC discipline

- Pooled frame and stream buffers sized by class; no per-message goroutines.
- One writer goroutine per carrier with vectored writes; one reader per carrier
  decoding in place.
- Decode into reused values; zero-copy byte fields with explicit release.
- Encode-once broadcast for every topic delta.
- Allocation gates in benchmarks: zero allocations per steady-state terminal
  frame encode/decode and per transcript append; bounded allocations per command.
- GC CPU under a terminal flood measured and gated.

## Acceptance gates

Gates were specified against the original failing code and are intended to join
`make bench-gate` or the real-process journey suite. Ceilings come from repeated
measurements and are never raised to land work.

| Gate | Target (unchanged) | Measured evidence and remaining scope |
|---|---|---|
| `q` in htop after 300 pointer motions over the pane | under 100 ms (today 9.3 s) | **Focused pass:** actual htop q→native exit 1.077293 ms after 300 motions; isolated core/SDK/PTY journey, not the separate key→PTY gate. |
| Key to PTY write, local, p99, during a 60 fps full-screen redraw | under 3 ms | **Pass, final R16 three-run cohort:** seccomp-traced p99 2.430815 / 2.904142 / 2.699153 ms, 100/100 PTY writes and exact child bytes each run, source 59.994–59.999 Hz. Tracing overhead is included; no competing acceptance workload. Earlier failing cohorts remain recorded. |
| Ordinary 200×50 text frame | under 16 KB on the wire, under 100 µs decode, 0 allocations | **Pass:** 10,607–10,608 complete local carrier bytes, including mux/application framing; packed dictionary decode 72.157–73.279 µs, 0 B / 0 allocations. Row-buffer reuse is distinct from allocating an initial SDK viewport. |
| `mpv --vo=tct` 200×50, local | sustained 30 fps presented, input p99 under 20 ms | **Local pass, final R16:** exact enqueue→PTY-write p99 2.367363 ms (100 keys); native Alacritty capture decodes 289 distinct source-frame identities in 8 seconds, 36.125/s, with actual-PTS one-second bins [40,32,32,37,34,34,40,40]. Ten-bit tags agree at top/middle/bottom before a sample counts. There are 339 actual recorded frames (not the nominal 480); 16 mixed-tag and five ambiguous samples are excluded. This verifies the three bands, not every pixel or atomicity of every raster sample. mpv's default line buffering does not provide DEC 2026 frame boundaries. |
| Same over 50 ms RTT, 1% loss (netem), QUIC | key echo ≈ RTT (predicted echo immediate), no stalls from other topics | **Pass, private namespaces (`scripts/protocol_netem.sh`, 2026-10-24):** real shell port and QUIC carrier in two netns joined by one veth, netem 25 ms + 1% loss on each end; 100 typed keys per phase, two runs. SRTT 50.1–50.3 ms: echo p50 idle 60.6 / flood 63.1–63.9 / 4 MiB bulk download 63.1–66.1 / both 68.8–71.4 ms; worst sample 189 ms, none over 1 s. Gate: p50 ≤ SRTT + mosh send interval (75 ms), max < 1 s. Predicted echo follows mosh's adaptive trigger (SRTT/2 > 30 ms), so it is correctly off at 50 ms RTT; at 160 ms RTT (80 ms each end) 100/100 keys predicted immediately, echo p50 170.5–199.8 ms. The run found and fixed two defects: the pacer ticked every RTT/2 even when idle (now mosh `SEND_MINDELAY` scheduling; idle echo p50 66.3 → 60.6 ms) and prediction used mosh's 80 ms underline trigger as its show trigger. Namespaces on one host, not physically separate machines. |
| Streaming a 20 KB reply | total wire bytes under 1.5× the text; 0 allocations per append | **Pass:** 20 KiB in forty separately applied 512-byte publications uses 23,766 complete carrier bytes (1.1604×); warmed source AppendToken is 0 allocations. Publication/receipt work is not claimed allocation-free. |
| Carrier killed mid-session, resume within grace, including QUIC→SSH fallback | control retained, no full snapshot, no lost or duplicated input | **Partial/local integration:** three SDK clients, 12 cuts, 48 exact marker bytes, retained lease; QUIC shutdown/SSH fallback race integrations pass. Covered-journal topic tests verify incremental resume, but the cut harness does not prove zero full resets for every cut. Random owner/resume/grace and terminal-lease/input fuzzers cover rejection/deduplication. Distinct-host and roaming network acceptance remain open. |
| GC CPU during a terminal flood | under 5% of shell CPU | **Pass, final R16 three-run cohort:** 3.987291 / 4.071365 / 4.166104% of active shell runtime CPU. R14's 5.0609% failure remains retained, not rounded down or waived. Core GC is excluded from the shell denominator. |
| Ten idle shells | each under 1% CPU | **Native local pass:** all ten shells <1% over the harness's five-second sample; additional 60.004-second sample 0.8833–0.9499% per shell, core 0.4500%. Five provider requests belong to preceding active stream/question/stop/zero-shell journeys; the result is not a zero-total-request claim. |
| Three clients (two remote) on one core, one throttled | others unaffected within gate latencies | **Partial:** three local SDK peers, one withheld ACK, 12 carrier cuts, latest resume 16.76–27.49 ms; local native ten-shell sharing also passes. Two genuinely remote machines and gate latencies on them are unverified. |
| Receipt, document, Local History and safety journeys | unchanged results | **Local pass:** full normal and exhaustive sharded full-race suites at R16; deterministic late-control admission regression and repeated connected Stop/steer/recover/editor journeys pass, as do stale-restore refusal/safety/inverse checks. |
| Tilix, Alacritty, kitty, Windows Terminal; desktop and constrained sizes | visual capture acceptance of the journeys above | **Partial:** 16 feature phases in each Linux emulator at 160×45 and 60×20, sequential native R16 captures reviewed. These are local framed-core fixtures, not every journey in every matrix cell. Native Windows Terminal, remote/netem combinations, and uncaptured destructive/authority journeys remain open. |

## Integration checkpoint (2026-10-24; acceptance scope explicit)

The refactor is implemented in the working integration: generated tuple codecs,
source-cohort admission, resumable carriers, pushed workspace/seven-state/document/
terminal topics, direct-cell presentation and remote connection/access surfaces.
Private session delivery contains only control/host/clipboard requests; progress
is command-scoped `Reply`. Safe handover, measured writer pacing, Update-owned
terminal ACK completion and detached control-glyph normalization are described in
the current references, not duplicated as another design contract here.

Source cohort: `d4840108df7f9503e75714d1719b43400e2c777640cb3317072940bcd3e27841`.
Parent-run Linux native evidence is retained in session scratch under
`acceptance-r16` (normal/race/build/lint/native-fuzz logs and native captures),
with older profiled/negative measurements retained under their original cohorts.
These paths identify the measurement run, not durable shipped product assets.

- `go build ./...`, source-cohort freshness, full normal `go test ./...`, lint,
  design audit, docs check and benchmark gate pass (51 allocation ceilings).
  The full race runner passes: every package plus five session and four viewmodel
  shards, including every discovered top-level Test/Example/Fuzz case without
  name exclusions. Normal opt-in/dependency skips are not universal path coverage;
  the separately recorded native journeys supplement them.
- Thirty-eight distinct instrumented native fuzz targets each completed a bounded
  two-second/two-worker campaign, including nested Bubble Tea/UV modules. Longer
  focused campaigns include pairing (311 cases) and full-frame state (50 cases).
  These are smoke/focused bounds, never a claim of exhaustive fuzzing or a hosted
  CI run. Verify is wired for native campaigns; repository Actions is disabled.
- Windows/darwin amd64 cross-builds with cgo disabled pass. They validate Go
  platform alternatives only, not libghostty, ConPTY, Windows Terminal or native
  macOS behavior. This host has no matching native cross-cgo toolchain.
- The new semantic targets found input receipt mutation before rejection,
  literal SQLite path parsing, dense catalog open contention, stale admission
  context rollback, banked recovery wakes and last-menu/focus repaint defects.
  The production owners are corrected; minimized checked-in seeds and targeted
  original-failure regressions preserve the evidence. Clean terminal input still
  delivers every key without forcing a full compose/snapshot; stable Host layout
  reuses its registry maps with zero allocations.
- Sequential R16 Linux captures cover Git changes/review/history, editor/search,
  workspace/status/notices/Board, performance overview/core/resources, capability
  empty state, Local History timeline/preview, and saved workspace settings.
  There is no external inference, capability activation or history restore in
  that fixture. Earlier connected agent/terminal/control captures and normal/race
  journeys supplement it; none substitutes for the missing cross-product matrix.
- Final-source mpv presentation is measured from native pixels, not writer flush
  counts or the recording's nominal rate. A lossless 60 Hz source repeats its
  ten-bit frame identity in three raster bands; 36.125 distinct agreeing identities
  per second are visible, with every complete second above 30. The 20-second
  instrumented run has 2.367363 ms input p99 and 4.971892% shell GC. The eight-second
  capture has 339 actual frames with 16–34 ms PTS steps; decode uses passthrough,
  not ffmpeg's duplicated constant-rate output. It excludes 16 mixed-tag and five
  ambiguous samples from its 289-identity count; it is neither a
  promise of atomic output from an unsynchronized child nor an all-emulator run.

**Explicit unverified list:** distinct-host SSH/direct QUIC, roaming and
two-remote-client visual/latency runs; native Windows Terminal/ConPTY and native
cross-platform cgo; hosted fuzz CI; every uncaptured journey combination below.
Semantic coverage is bounded: integrated carrier/grace/lease random interleavings,
all capability authority/host-channel lifetimes, complete bulk upload/read source
lifecycles, and arbitrary Local History plans are not exhaustively fuzzed.
Existing draft CAS and Local History stale/restore/inverse oracles are covered;
do not list their entire families as missing merely because the target name is
broader. No threshold or required journey is removed by these scope notes.

### Implementation-intent audit

The original design is not a claim that every aspirational mechanism shipped.
The following maps its requirements to current owners, evidence and explicit
differences. The performance and feature tables remain the acceptance authority.

| Requirement group | Current implementation and acceptance boundary |
|---|---|
| One wire and typed domain schema | `corewire` streams plus generated domain tuple codecs; exact shape/collection limits and all-message decoder seeds. Commands, pushed workspace/state/document/terminal topics, input and verified bulk replace the old JSON envelope/invalidation path. Explicit recovery/inspection commands remain, not normal polling. |
| Credit, priority and independent peers | Byte-stream mux and the quic-go fork schedule 8:4:2:1 classes; tests cover credit overrun, short/blocked writes, fairness, control floods, input overtaking bulk and slow subscribers. Stream windows/admission and retained-result bounds refuse excess work explicitly; they do not promise unlimited in-flight storage. |
| Compression and allocation | Network directions retain one standard zstd frame/history; local IPC is uncompressed. Decoder and append zero-allocation gates pass at their stated reuse boundaries. SDK immutable viewport construction, changed-row exports and command publication still allocate; the original blanket zero-allocation wording is not achieved end to end. Mux writes a reusable contiguous batch, not vectored zero-copy I/O. |
| Carrier-independent retained sessions | Exact owner/resume secret, applied cursors, input deduplication, grace and clean-close receipts; local/QUIC/TLS/SSH integration and random ownership tests pass. Application sessions resume after reconnect; QUIC 0-RTT application admission is not implemented, and explicit roaming/migration acceptance is unverified. WebSocket is not implemented (future browser transport). |
| Terminal authority and outer independence | One core libghostty parser/reply owner, packed rows/style dictionaries/scrolls, DEC 2026 hold/timeout, asynchronous lease input and packed history. Motion conflates without dropping ordered keys; prediction is provisional and disabled locally/unsafe modes. Native Linux, Unicode/synchronization and replica-oracle tests pass. Native Windows and the full outer-emulator/remote matrix are not inferred from them. Unsynchronized child line output has no invented frame boundary. |
| Ordered transcripts/documents and latest state | Canonical mutation is immediate; viewer publication/ACKs pace independently. Document revisions, author history, receipt recovery and exact context publication survive tested cuts. Seven independently derived state topics do not serialize input behind Git/settings; source-generation resets and journal eviction have focused regressions. |
| Direct-cell presentation | Host retained-region paint, immutable publication transfer and off-Update renderer writer replace ANSI generation/reparse. Full-repaint differential fuzz, nested renderer output fuzz, dense region/idle/pointer tests and six Linux native visual runs pass; no claim is made for every uncaptured UI journey. |
| Remote trust, client store and surfaces | Strict SSH, pinned TLS 1.3 QUIC/TCP, one-use bootstrap/pairing, durable device revoke, consented exact-image install and shell-local Remotes/Network surfaces are implemented and tested. Client-profile metadata and keychain/fallback consent remain private. Native OS keychains/ACLs, distinct-host installation/roaming and complete consent/switch visual journeys remain unverified. No authority is inferred from a carrier label or source hash. |
| Persistence and lifecycle safety | Durable typed receipts retain exact preimages; unknown effects are observed, never replayed. Session blobs have no v1 compatibility reader. Shared documents/history preconditions, stale asynchronous results, backend/workspace changes, Stop/recovery and shutdown are covered by the final normal/full-race suites. This is not an uninterrupted-core-crash guarantee. |

## Original execution order (not a completion checklist)

This records the proposed sequence, not completed gate evidence.
One continuous refactor, not approval-gated phases. Each step leaves the product
working, deletes the code it replaces in the same change and passes the gates
that exist so far.

1. Gate harness and baselines (benchmarks, tmux and real-process journeys,
   netem/SSH rigs).
2. Session layer: session API, stream-mux carrier, QUIC carrier, credit,
   heartbeat, resume.
3. Codec: msgp generation for the protocol types, typed messages on command
   streams; delete JSON framing, `strictJSON`, envelopes and fixed in-flight caps.
4. Topics: hub machinery with encode-once fan-out and resumable sequences;
   move transcripts, documents, files, status, settings, Git, LSP, inventory and
   Board; delete the fetch worker, invalidation events and the text tick.
5. Terminals: grid topic, input stream, pacing, motion coalescing, predictive
   echo, terminal state out of the workspace actor; delete `terminal.rows`
   polling and the client command queue.
6. Presentation engine: direct cell drawing, region invalidation, flush pacing;
   delete the ANSI string round trip.
7. Remote access: SSH bootstrap to QUIC with fallback, matching core install,
   direct pairing and device registry, client connection store with keychain,
   Remotes view, Add connection dialog, backend indicator and switching.
8. Final acceptance: every gate, native captures on Linux and Windows, reference
   docs rewritten to describe the result.
9. **Per-feature validation and fuzzing, after the plan is otherwise complete.**
   Every feature is validated separately and visually and is fuzzed; see
   [Per-feature validation and fuzzing](#per-feature-validation-and-fuzzing).
   The plan is deleted only when every row of that matrix passes.

## Per-feature validation and fuzzing

Passing the aggregate gates is not enough. After the refactor is complete, each
feature below is validated on its own through the real product path and fuzzed
at its wire, parser and interaction boundaries. A failure reopens the feature,
not the whole plan.

**Visual validation, per feature.** Run the feature's journeys with the
repository capture workflow (`scripts/tuishot.sh` on Linux,
`scripts/tuishot.ps1` on Windows): one capture at a time, every capture window
closed. Each journey is captured:

- at desktop and constrained sizes;
- in Tilix, Alacritty, kitty and Windows Terminal;
- locally, over SSH and over direct QUIC with 50 ms RTT and 1% loss;
- with two clients attached at once, plus a disconnect and resume in the middle.

The person reviewing reads every capture: correct content, no stale or partial
frames, focus and cursor right, loading, empty and error states legible.

**Fuzzing, per feature.** Go native fuzz targets (`go test -fuzz`) with their
corpora checked in and run in CI for a bounded time:

- **Wire:** frame parser, stream-mux framing, credit accounting, msgp decoders
  for every message type (truncated, oversized, wrong-type and hostile
  collection headers), zstd stream boundaries.
- **Sessions:** resume and reconnect state machines driven by random sequences
  of carrier loss, duplicate and out-of-order resume, expired grace and lease
  races; invariants: no lost or duplicated input, no double-applied receipt,
  exactly one controller.
- **Topics:** random interleavings of deltas, resets, subscribe and unsubscribe
  per topic kind; the replica always equals the authority's state at the same
  sequence.
- **Terminals:** random and corpus VT byte streams (legacy, grapheme, kitty
  keyboard, synchronized output, malformed sequences) through the engine, grid
  encoder and client replica, compared against a direct engine snapshot; random
  key, mouse, paste and resize input streams.
- **Presentation:** random component invalidation and resize sequences; the
  direct-cell frame equals a full repaint of the same state.
- **Feature commands:** random valid and invalid command sequences per family
  against invariants (receipts never replay, document revisions never fork,
  Local History previews match applied results).

| Fuzz requirement family | Native R16 evidence and boundary |
|---|---|
| Wire | All reachable generated message types have valid/hostile seeds; message fragmentation/limits, compressed records, mux frame/state credit and QUIC class targets each ran natively. Arbitrary protocol-invalid bytes fail closed; bounded smoke campaigns are not exhaustive protocol proofs. |
| Sessions | `FuzzCoreSessionOwnership` uses the production session owner with a virtual grace clock; input admission, command receipts and raw terminal-controller state machines have separate independent oracles. Full integrated random carrier/grace/terminal interleavings remain unverified. |
| Topics | Workspace transcript/inventory replica and remote state-fence targets randomize reset/delta/owner-generation application. Real document/terminal journal and slow-peer/resume tests supplement them; every topic kind's subscribe/unsubscribe lifecycle is not one comprehensive native fuzzer. |
| Terminals | Native VT byte streams through engine → grid encoder → SDK are compared with a direct engine snapshot; input key/paste/focus/resize ordering and terminal leases use independent effect checks. Packed row/dictionary fuzzers and malformed Unicode/control/synchronized seeds run too. |
| Presentation | `FuzzAppFrameState` drives real Update/View with empty/dense history, terminal keys, menu unmount, geometry/theme/selection and focus; the oracle clears/full-paints the same Host. Nested direct-renderer resize/conflation and terminal-output differential targets also ran natively. |
| Feature commands | Runtime-backed document, filesystem/Local History, draft CAS, settings/secrets/reset, input receipts, Git reviewed mutations, LSP requests, capabilities, startup and remote-authentication targets ran. Their bounded action models and independent disk/effect checks are explicit in source; metrics-command and arbitrary bulk-source/authority-host lifecycle state machines remain uncovered. |

All original journeys remain required. The last column records what was actually
exercised; **partial** does not accept the entire row. Every row still shares the
unrun Windows/SSH/netem/two-host visual combinations specified above.

| Feature | Visual journeys (unchanged) | Fuzz focus (unchanged) | Available evidence and unverified remainder |
|---|---|---|---|
| Agents and chat transcript | create, stream, steer, stop, recover, fork, dense resume, two viewers | transcript append/upsert topic, `agent.*` commands | **Partial visual:** connected two-viewer stream/Stop/steer/recover/passive-resume journeys and earlier control captures; full dense/fork native capture matrix unverified. Native `FuzzConversationStreamReaders`, malformed offsets, workspace replica/state fences, input admission receipts cover ordered text and no-replay. |
| Composer and drafts | type, paste, attach, submit, steer while running, reconnect mid-draft | submit receipts, draft CAS | **Local journey pass, partial visuals:** connected follow-up/steering and private draft survival through failure, Stop and reconnect; all attachment visual combinations unverified. `FuzzCoreFeatureDraftCAS` checks private store CAS, exact/changed receipt preimages and epoch takeover; input-receipt fuzzer checks command admission. |
| Editors and live documents | open, edit, undo/redo across authors, save, format, external disk conflict | document operation log, revision fencing | **Partial visual:** editor/open/search in all six Linux R16 captures; real-process editor acceptance and multi-author/lost-response journeys supplement it. `FuzzCoreFeatureDocuments` plus document-update atomic boundary check edits, author undo/redo, savepoints and stale revisions. Every conflict/format visual transition is not captured. |
| In-app terminals | shell, htop, vim/nvim, `git commit`, `mpv --vo=tct`, scrollback, selection and copy, dock/float, many terminals | VT streams, grid topic, input stream | **Partial:** actual htop/mpv and shell/control/dock/float journeys; randomized native VT→authority→packed replica, input admission, raw-PTY lease and full-frame presentation targets. Named nvim/commit/selection/copy/many-terminal visual combinations are not all verified; final latency/GC rows above are separate. |
| File tree and files panel | browse, create, rename, delete, search, large directories | file-change topic, bulk reads and uploads | **Partial visual:** file tree/editor/search and workspace files panel in six Linux captures; process/safety tests cover mutations. `FuzzCoreFeatureFilesHistory` checks independent filesystem bytes; workspace state and bulk receiver fuzzers cover delivery. Complete random upload/read source lifecycles and every large-directory visual path remain unverified. |
| Git panel and review | stage, unstage, hunks, rename, commit, history, branch switch | Git snapshot topic, reviewed mutations | **Partial visual:** changes, review and history in all six Linux captures. `FuzzCoreGitReviewedMutations` uses real reviewed mutation preimages; state-topic fuzz guards replication. Commit/branch-switch and every destructive/hunk interaction are not all natively captured. |
| LSP diagnostics and navigation | diagnostics, hover, completion, rename, references | diagnostics topic, LSP requests | **Partial:** local LSP request/diagnostic tests and `FuzzCoreLSPRequests`, state-topic fences; R16 Problems captures the explicit no-matching-server state, not live completion/navigation. Installed-server native visual journeys remain open. |
| Notices, status, Problems, Board | publish, dismiss, resync after reconnect | state topics | **Partial visual:** retained notice, Problems and empty Board rendered in all six Linux captures; random topic resets/deltas/stale owner fences run natively. Populated Board/dismiss/reconnect visual combinations are not all captured. |
| Settings and accounts | every page, save, reset, secrets keep/replace/clear | settings commands and projection topic | **Partial visual:** workspace Editor settings Save in six Linux captures; account/write-only secret and settings scope/reset tests pass locally. Native settings/secret/session-reset fuzzers run; every page and secret operation is not natively captured. |
| Local History | timeline, preview, restore with safety snapshot, undo | plan/apply commands | **Partial visual:** timeline and exact preview in six Linux captures; no restore is performed by that fixture. `FuzzCoreFeatureFilesHistory` runs stale-plan refusal, safety snapshot, reviewed restore and inverse against independent disk bytes. Arbitrary path/type/restore sequences and the whole visual safety journey remain open. |
| Capabilities, skills, MCP | approve, install, connect, revoke | capability commands, host channel | **Partial visual:** empty Skills and installed built-in metadata, without activation, in six Linux captures. `FuzzCoreCapabilities` covers command state/receipts; host-channel journey tests cover focused approval/ownership. Full install/connect/revoke and all authority-channel lifecycle fuzz/visual combinations remain open. |
| Performance dashboard | overview, core metrics, resources | metrics snapshot command | **Local visual pass for named pages:** overview, core and resources in all six Linux captures; connected metrics/ownership regressions pass. Metrics replies traverse all-wire decoder fuzzing; no dedicated arbitrary metrics-command state-machine target. |
| Startup and workspaces | first use, picker, switch, delete and restore | startup commands | **Partial visual:** startup native process/connected feature/control fixtures plus delete/restore and picker tests. `FuzzCoreStartupWorkspaces` covers real startup/workspace command state; literal path and dense metadata regressions protect catalog identity. Every first-use/switch/delete/Undo native combination is not captured. |
| Remotes and connections | add, pair, connect, SSH-to-QUIC upgrade, UDP blocked fallback, version-mismatch install, switch backend, revoke device | pairing, credential store, carrier switch | **Partial:** local pinned QUIC/TLS/SSH fallback/install and backend-failure tests. `FuzzCoreRemoteAuthentication` checks one-use/expired pairing, durable revoke, reopening, rate limits and bootstrap replay; credential-store documents are fuzzed. Distinct-host and complete native Remotes/consent/install visual paths remain unverified. |
| Multi-client sessions | two shells on two machines, take control, throttled client, grace expiry | session and lease state machines | **Partial:** real local ten-shell sharing, two-viewer journeys and three-SDK throttled/cut test; `FuzzCoreSessionOwnership` advances virtual grace and randomizes carrier replacement/expired resumes, and terminal lease/input fuzzers check exact controller effects. Two physical machines and integrated randomized carrier-grace-terminal interleavings remain open. |

## Decisions made

1. **Session database blobs:** unchanged by this refactor. Receipt wire values use
   the current typed encoding; changing session database blobs is separate work
   with backup/manual alignment, not a compatibility reader.
2. **Direct access:** opt-in direct QUIC/TLS with pairing alongside strict SSH;
   authentication remains mandatory and distinct from source-cohort equality.
3. **Session grace:** 900 seconds (15 minutes) by default after carrier loss.
