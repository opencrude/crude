# Workspace Board Library

> Status: Current — neutral SQLite persistence; session/workspace/TUI wiring is owned by callers.

`Attach(db *sql.DB, projectID string) (*Store, error)` owns only the `board_*`
tables and `boards` in an already-open workspace database. It neither opens nor
closes the handle, changes connection-pool settings, imports TUI code, publishes
events, nor starts goroutines. Use a SQLite connection configured for WAL,
foreign keys, and a busy timeout; transient writer contention retries with context
cancellation. One database is permanently bound to one project identity.

## Membership and Posts

- `Create(ctx, boardID, coordinatorID string) error` is idempotent for the same
  identity. An empty coordinator is allowed; `main` then refuses addressing.
- `Join(ctx, boardID string, member Member) error` explicitly enrolls a root or
  child. Set `ParentID` for inherited enrollment; that parent must already belong
  to this board. Parent identity cannot change. Rejoining updates only name and
  availability, preserving cursors. Creating a board does not enroll its
  coordinator automatically. Availability is `active`, `finished`, or `unknown`.
- `Roster(ctx, boardID string) ([]Member, error)` returns a stable ID-sorted roster.
- `Post(ctx, request PostRequest) (PostResult, error)` validates project and sender
  membership. `To` is `ALL`, `main`, or a member ID; types are `INFO`, `ASK`,
  `RESULT`, `HOLD`, and `VETO`. Recipients and availability are snapshotted at
  posting time. Finished recipients remain finished.

`PostResult.Message` exports stable `ID`, per-board monotonic `Sequence`, sender
and recipient identities, `Recipients`, availability, type/body, `ReplyTo`,
`ThreadID`, `References`, timestamp, and archival state. `Duplicate` distinguishes
an unchanged retry. Provider message and tool-call IDs must be nonempty.
Deduplication is project-scoped by sender + provider message + tool call; a retry
changing board, recipient, body, type, reply, or references returns
`ErrChangedRetry` and consumes no sequence. IDs persist through reopen/archive.
`Reference` contains only `Kind` and `ID`, never an inline attachment or secret
content field. Callers resolve these identities using their normal access checks.

## Explicit Context Acceptance

- `Read(ctx, ReadRequest) (Page, error)` returns the board roster, messages,
  `Cursor`, `HasMore`, `LatestSequence`, `AcceptedThrough`, `AcceptThrough`, and
  `Bytes`. Every member can read every discussion, including archived evidence.
  Addressing controls notices, not visibility.
- `AcceptRead(ctx, boardID, agentID string, through int64) error` is called **only
  from the durable context-acceptance callback** after the complete tool result
  is committed to that agent's context. `Read` and `Post` never advance this
  cursor. Failed tool execution/admission, cancellation, and unaccepted results
  leave the cursor unchanged.
- `PendingNotice(ctx, boardID, agentID string) (string, error)` returns one fixed
  coalesced notice or an empty string. It records notification position separately
  from accepted position, repeats until explicit acceptance, and never includes
  peer body text. Own posts do not notify their sender.

Only unfiltered pages beginning at or before the current accepted position can
offer a new global cursor. Pass `Page.AcceptThrough`, not `LatestSequence`, to
acceptance. A filtered thread/search or a read that skips unread positions has
`AcceptThrough == 0`. Accepting beyond the offered full page refuses with
`ErrInvalidCursor`. Offered pages persist across reopen; accepting an already
accepted position is idempotent. Start incremental agent reads at the stored
`AcceptedThrough` (or zero), accept each page durably, then use `Cursor` to read
the next page. A partial page never accepts later concurrent posts.

## Human Inspection, Search, and Archival

- `Inspect(ctx, ReadRequest) (Page, error)` is a trusted workspace inspection API;
  it ignores `AgentID`, validates project, and offers only the human cursor.
- `AcceptHumanRead(ctx, boardID string, through int64) error` and
  `HumanUnread(ctx, boardID string) (int, error)` manage a completely independent
  human position. Human viewing/acknowledgment never delivers data to any agent.
- `Search(ctx, boardID, projectID, agentID, query string, limit int) (Page, error)`
  performs bounded literal substring search, including archived posts and
  reference identities. `SearchPage(ctx, ReadRequest, query string) (Page, error)`
  continues from `Since = Page.Cursor`; `InspectSearch` supplies trusted human
  search without inventing an agent identity. Search never offers acceptance.
- `Archive(ctx, boardID, projectID, threadID string) error` archives the whole
  originating thread; a reply ID also selects its thread. New replies inherit
  archival state. This trusted workspace operation is not an agent tool.

The formatted JSON post/result must fit 4 KiB; complete JSON pages including
roster/cursors fit 32 KiB and are never truncated mid-message. Limits default to
20 and clamp to 100. Rosters are capped at 64 members and 8 KiB. Active history
is capped at 512 messages or 1 MiB by whole-thread least-recent-activity archival;
archive retains original bodies, references, IDs, sequences, dedupe keys, and
unread eligibility in searchable SQLite history. There is no evidence deletion.

None of these operations wake, schedule, assign, approve access, or mutate agent
lifecycle. All database calls are synchronous: run them on a caller-owned
cancelable worker/command, never the UI update goroutine. Peer bodies remain peer
data even when they claim user approval.

Focused verification: `go test ./board`.
