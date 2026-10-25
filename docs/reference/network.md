# Network transport

> Status: Current reference — outbound HTTP policy and authenticated remote-core access.

## Product surface and authority

Application Settings has one **Network** group, ordered last in the rail after
the AI, Security, and IDE groups:

| Page | Controls |
|---|---|
| Connection | TCP connect timeout, TLS handshake timeout, TCP keep-alive idle/interval/probes, idle-pool lifetime, idle connections per host, HTTP/2 compatibility and health checks |
| Proxy | Environment / Direct / Custom URL, credential-masked proxy URL, proxy bypass |
| Certificates | Minimum TLS version, additive custom CA file, exact leaf-certificate exception, exact-host mTLS identity |
| Remote access | Host-local direct-access opt-in, QUIC/TLS listen addresses, retained-session grace, fingerprint, pairing and device revocation |

The palette's **Network Settings** opens Connection directly. Workspace Settings
shows the application values read-only; it cannot introduce proxy credentials,
trust exceptions, or client identities. These settings persist under `network`
in the application `config.json`, whose permissions are owner-only. Proxy
credentials are masked and refuse clipboard commands. They are not encrypted
inside that file. Certificate/private-key bytes are never persisted in settings;
only their paths and exact destination hostname are stored.

`network/` owns outbound HTTP transport and scalar settings. Remote-core carriers
have the separate mandatory pinned TLS 1.3/strict SSH trust boundary below; HTTP
proxy or certificate settings cannot weaken core authentication. `app/` owns
persistence and publishes its validated immutable default snapshot. Account connections retain their own
request/progress/attempt budgets; they do not implement another proxy/TLS stack.
New clients capture current policy. In-flight clients keep their snapshot, so a
save cannot change TLS or routing halfway through a request. A retained MCP
connection uses its captured policy until explicitly reconnected.

Preferences Save runs certificate validation and persistence off the UI thread.
Its exact retained form is busy until the owning workspace/overlay result arrives.
Failure keeps the same draft open. A late result cannot close a replacement
Settings window or redirect a workspace save into another project's database.

Invalid saved policy does not stop the IDE shell. Startup shows a recovery dialog
and blocks new outbound clients locally until a valid explicit repair. Malformed
application configuration also blocks outbound activity rather than silently
using defaults. No direct fallback, reset, or trust widening occurs.

## Connection policy

### Shell-to-core link health

The persistent **Crude** wordmark is the shell's quiet connection-health control.
The former always-visible Local/connected/carrier/RTT string is removed. Healthy
connections keep the ordinary static accent wordmark; connecting a destination
workspace or reconnecting uses the shared `ui.TextTide` ink sweep. Reconnecting
and measured round trips of at least 250 ms add a static warning mark; unavailable
connections show a static error mark. Only connection work requests fast ticks;
healthy and settled attention states keep the existing idle cadence.

Hover/click opens the shared native **Connection to core** popover, also reachable
through the palette's **Connection Details**. It shows current connection name,
state, carrier, observed round trip and plain-language guidance, plus safe
transport notices and **Connections** / **Close** actions. Sub-millisecond samples
read `<1 ms`; unknown RTT is **Not measured yet**, never fabricated zero latency.
A retained sample after loss is labelled **last observation**. This is link
responsiveness, not provider health, throughput, packet-loss detection or proof
that every replica update is applied. Carrier heartbeat/resume remains the only
transport-health owner; the UI adds no probe or retry loop. The Workspace adapter
continues to report reconnecting while its fresh-attachment owner retries after
SDK resume expires, and closed attachments cannot report live telemetry.

Hover is passive, keeps the draft and active pane, and stays open while the pointer
enters the adjacent popover. Read-only text remains complete and scrollable on
narrow grids. Leave, Escape, outside click, modal takeover and attachment changes
dismiss it; explicit click/palette activation permits keyboard reading and Tab to
the native actions. Below the usable popover size, the compact wordmark and
existing Remotes rail/palette navigation remain available.

### Outbound HTTP defaults

| Setting | Default |
|---|---|
| TCP connect timeout | 30 seconds |
| TLS handshake timeout | 10 seconds |
| TCP keep-alive after | 30 seconds |
| TCP keep-alive interval | 10 seconds |
| TCP keep-alive probes | 3 |
| Idle connection lifetime | 90 seconds |
| Idle connections per host | 10 |
| HTTP/2 | Enabled |
| HTTP/2 health check after | 15 seconds |
| HTTP/2 health check timeout | 15 seconds |
| Minimum TLS | TLS 1.2 |

For outbound HTTP, TLS 1.3 is an explicit stricter minimum; core QUIC/TLS
carriers always require TLS 1.3 regardless of this HTTP minimum. Disabling HTTP/2 is a compatibility
control, not the default. Connection establishment is distinct from model
response waiting; the provider progress watchdog remains the sole owner of
header/stream silence. An explicit provider/account Connect timeout overrides
both its TCP and TLS establishment budgets. An unset provider Connect timeout
inherits the separate Network budgets.

Dead pooled connections are detected for every protocol: HTTP/2 connections
are pinged after the configured read silence and closed when the ping goes
unanswered; TCP keep-alive probes (idle, interval, count) close dead HTTP/1.1
and HTTP/2 connections alike. A connection that dies during a retry cooldown is
therefore redialed rather than reused by the next attempt. These are
idle-connection probes, not request deadlines.

A provider progress stall retires only the actual failed transport connection,
including a pooled HTTP/2 connection that remains locally established while
traffic is blackholed. The next automatic attempt may redial using the same
captured routing/TLS policy; it does not rebuild the transport or fall back direct.
Retirement can interrupt other streams multiplexed on that failed connection.
Their accepted-output recovery safeguards remain in force. Ordinary cancellation
and overall deadlines do not retire a connection as a progress failure.

All automatic cooldowns, including rate-limit hints, use the independent
[five-minute retry contract](run-recovery.md#shared-cooldown-contract). A connect,
request, progress, or maintenance-job timeout is not a retry cooldown.

## Proxy protocols

Environment mode snapshots `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY`, preferring
uppercase over lowercase. It preserves CGI protection for `HTTP_PROXY`; it does
not implicitly interpret `ALL_PROXY`. Direct mode ignores proxy routing. Custom
mode uses one configured URL, subject to the explicit bypass list.

| Scheme | Destination DNS | Authentication |
|---|---|---|
| `http` | Proxy resolves the destination | URL-userinfo Basic |
| `https` | Proxy resolves the destination; proxy hop is TLS | URL-userinfo Basic |
| `socks4` | Local IPv4 lookup | SOCKS user ID; passwords are rejected |
| `socks4a` | Proxy/remote lookup | SOCKS user ID; passwords are rejected |
| `socks5` | Local lookup | RFC 1929 username/password when configured |
| `socks5h` | Proxy/remote lookup | RFC 1929 username/password when configured |

Bypasses accept comma-separated exact hosts, domain suffixes, IP addresses and
CIDRs, with supported optional ports. Custom SOCKS modes deliberately distinguish
local from remote DNS; they are not aliases. HTTPS destinations use an owned,
cancelable CONNECT tunnel with separate proxy TLS and origin TLS configuration.
Proxy failure never falls back to a direct connection. Proxy errors redact
credentials, and cancellation closes a stalled handshake.

The current HTTPS tunnel uses HTTP/1.1 CONNECT. An exclusively HTTP/2-CONNECT
proxy is unsupported. These are HTTP client settings, not a subprocess sandbox:
external browsers, shell tools, LSP servers and stdio MCP programs retain their
own networking configuration.

## Trust and mutual TLS

System chain and hostname verification is on by default. A **Custom CA file**
adds PEM issuers to system trust; ordinary chain, hostname, time and minimum-TLS
checks remain in force.

An **Exact certificate exception** requires both an exact hostname and the
SHA-256 of the peer's leaf DER certificate. Matching that exact certificate
deliberately replaces chain, hostname and validity-period verification for that
configured hostname only. This permits a reviewed self-signed, expired or
otherwise invalid certificate without a global “trust everything” option.
The pin is a requirement, not a fallback: certificate rotation or a mismatch
fails closed until the user reviews and updates it. Verify the fingerprint
through a trusted channel, not solely from the connection being repaired.

An **mTLS identity** requires certificate file, key file and exact destination
hostname together. It is offered only to that origin, never to unrelated
redirects or HTTPS proxies. Hosts are canonicalized for case, trailing dot and
IDNA; matching is exact, not wildcard, and currently applies across ports.
The current settings surface supports one client identity and one leaf exception.

PEM reads are bounded at 16 MiB per file. Missing, malformed, mismatched or
oversized material fails Save/connection construction. No fallback transport
silently ignores a bad CA, key, pin or proxy.

## Coverage

The shared policy is used by provider chat/model discovery, provider OAuth,
public model catalogs, web/arXiv tools, capability sources/downloads, MCP HTTP
connections and MCP OAuth. Local callback listeners are not outbound clients.
Model-catalog refresh captures new policy per request; successful refresh cadence
is independent of its capped failure-retry schedule.

`network/` tests real HTTP/HTTPS forwarding and CONNECT, all SOCKS handshakes and
DNS variants, cancellation, redaction, custom trust, mTLS redirect/proxy isolation,
leaf-pin isolation, TLS minimums and HTTP/2. Provider integration tests exercise
saved proxy changes, immutable in-flight policy, custom CA use and fail-closed
missing material. App/settings tests cover persistence, scope and busy/failure
ownership. Cross-compilation is not a claim of native-platform acceptance.

## Transport invariants, timeouts and stalled connections

- `network/` owns the shared outbound HTTP transport for providers, OAuth,
  catalogs, web tools, capability downloads and MCP HTTP/OAuth. Network settings
  are application-only Connection / Proxy / Certificates pages; Remote access
  separately administers host admission. Workspaces
  cannot grant trust or client credentials. Secure system trust, exact-host leaf
  exceptions and exact-host mTLS stay isolated from proxies/unrelated origins.
  Proxy failure never falls back direct, and invalid material fails closed.
  Certificate reads and preference persistence run off Update with retained
  form/workspace ownership; an unchanged request keeps its captured policy.
  See `docs/reference/run-recovery.md` and `docs/reference/network.md` for the
  authoritative contracts, coverage and explicit limits.
- **Account timeouts are one shared transport policy.** Accounts
  expose Request/Connect timeout, initial/minimum/maximum progress timeout, and
  attempt/backoff budgets directly in Connection tuning. There is no template
  inheritance or Override dialer switch. Progress defaults
  are 60/45/300 seconds and require minimum <= initial <= maximum. Streaming and
  non-streaming requests honor the overall deadline across retries, even when
  the caller already has a longer deadline; close/completion/error releases the
  stream timer. Unlimited overall duration does not disable progress detection.
  An explicit provider/account connect setting also owns its TLS handshake
  budget; unset values inherit the separate Network TCP/TLS defaults. Neither
  is a competing response-header timeout.
- **Progress-stall recovery retires the exact failed connection.** HTTP/2 stream
  cancellation alone can leave a blackholed pooled connection eligible after
  cooldown. Provider watchdogs capture `httptrace.GotConn` and close only that
  connection when a typed upload/header/download stall wins; ordinary stop and
  overall deadline do not flush the pool or change routing/TLS. Sibling streams
  on the failed connection may be interrupted and retain normal accepted-output
  continuation safeguards. Real same-client and root/child upload tests must prove
  automatic redial plus complete payload delivery, not just a finished countdown.

## Remote core access

Remote-core access is distinct from provider/web HTTP policy. `app/` owns both
application configuration and listener lifecycle; `core/` owns authenticated
admission, retained sessions and device checks. The application prepares and
validates new listener sockets before committing configuration under the existing
settings lock, publishes admission/listener ownership only after durable Save, and
aborts only newly reserved sockets on failure. It does not publish a setting that
failed to bind, nor retire established carriers just because the listen address
changed. Unexpected listener failure is surfaced, not silently treated as saved
and healthy.

**Network → Remote access** is application-owned and administrable only from the
selected core's authenticated host-local session. It exposes direct-access opt-in,
QUIC UDP and TLS TCP listen addresses, session grace (default 900 seconds), host
fingerprint, paired-device status, explicit refresh, Generate pairing code, Hide
code and confirmed Revoke. Workspace scope and SSH/direct attachments are
read-only. Codes are private, non-clipboard administrative evidence for the exact
retained form; expiry, close, replacement and workspace generations remove/fence
it. Pairing and revocation are explicit operations, not background form polling.

QUIC and TLS/TCP require TLS 1.3 and an exact SHA-256 leaf-certificate fingerprint
learned through trusted host-local review or authenticated SSH. The certificate
pin is mandatory on every handshake; no system-CA, hostname exception or insecure
retry bypasses a mismatch. Device/session authentication remains separate from
server-certificate identity. Direct Auto can try QUIC and then pinned TLS to the
same saved authority; this never falls back to an unauthenticated connection.
Local IPC retains OS same-user security. SSH retains strict system host-key
verification, fixed helpers, no forwarding and no shell interpolation of project
paths.

`Hello.Pair` is a restricted enrollment handshake, answered by
`Welcome.Paired` and then closed. The client verifies the host pin before sending
the code; the pairing code is never a normal session credential or resume secret.
Codes are random, single-use, expire after five minutes and have bounded online
attempts. The host stores device-secret hashes, not reusable plaintext credentials,
in its private current-format registry. Revocation commits durably and immediately
closes that device's active sessions; a resume rechecks the retained device under
the same admission boundary. Disabling direct access revokes active paired-device
sessions; the host may retain transport endpoints for authenticated one-use SSH
bootstrap without enabling public enrollment.

Requested direct listen addresses remain user configuration, not a claim that a
socket is bound. A cold disabled core reserves independent ephemeral loopback
UDP/TCP listeners for authenticated SSH bootstrap; separate private profiles do
not contend for the configured default port. The host publishes actual live
endpoints atomically with admission policy, and bootstrap advertises those facts,
never requested addresses or port zero. Disabled address edits preserve healthy
listener identity and set future direct-access intent. Disabling after an enabled
host retains its healthy endpoints (including a prior nonloopback listener) while
revoking paired admission; it is not a listener-closure isolation promise.
Accept failure or shutdown withdraws matching actual endpoints without rewriting
configuration. Loopback-only bootstrap may be unreachable from a distinct host;
the pinned direct attempt then retains the verified SSH fallback.

QUIC retirement stops only its immutable accept generation, retaining the one
transport/socket for established carriers. Returning A→B→A prepares a new accept
generation on that retained transport instead of attempting another bind at A.
No application handshake or command is served before settings commit. Abort
stops that staged generation and drains unaccepted handshakes without closing
old carriers; a stale old accept worker cannot withdraw the new endpoint. Host
shutdown still closes each shared resource and joins its workers. Accepted
connections across all generations receive a QUIC `CONNECTION_CLOSE` while the
UDP socket is still writable; only then does shutdown release the transport.
Abruptly closing a transport cannot notify a peer and would make SSH fallback
wait for the liveness timeout. Acceptance and completed-connection cleanup are
joined before the resource close returns.

`Welcome.LocalHost` records immutable authenticated admission provenance. It is not
inferred from a Local label, loopback address or the latest carrier: replacing an
SSH/direct carrier with a local one cannot grant host administration.
`Hello.SSHRelay` only narrows an OS-authenticated local bridge to SSH authority;
a network peer cannot use it to authenticate or gain local rights. SSH bootstrap
credentials are short-lived, single-use and scoped to their reviewed purpose.

### Client connection manager

The **Remotes** activity rail is the shell-host connection manager, independent of
the selected core's settings. It retains Local and saved SSH/direct connections,
connection state/carrier/RTT, errors, explicit Test/Connect/Disconnect and remote
workspace browsing. Add/Edit uses shared native forms. SSH config aliases may be
listed as suggestions; this does not import SSH passwords or disable host checks.
The globe is fourth in the default rail, immediately after Recent Workspaces.
Both sidebar resource lines target the exact connection's context menu, including
Connect/Open, Test, Edit/Pair and Remove; Local omits saved-connection operations
and the current remote cannot be removed. Refresh lives in the context menu, not
a dedicated list/footer control. It uses the same retained activity-list
ProgressRule and visible-only animation cadence as Workspaces: keep usable rows
during reads and failures, reject stale completions, and clear the refresh error
on the next attempt. Menu callbacks are fenced to their manager and workspace
lifetime; they never start I/O on Update.
Switching backends prepares the destination and applies existing dirty-editor,
pending-command and private-draft safeguards before replacing the old attachment.
Failure leaves the old backend usable; no peer work is stopped. Late results are
fenced by exact backend/form generation.

Connection metadata (host/user/port/identity path, carrier, pin, recent workspace
facts) lives in the shell client's private `remotes/connections.json`, not the
selected remote host's config. Device credentials use the OS keychain when
available. Writing an unencrypted private 0600 credential file requires explicit
fallback consent and a persistent visible notice; owner-only permissions are not
encryption. Connection tests refuse to send a saved credential to changed
host/port/fingerprint authority merely because a display ID matches. Changed authority requires new
pairing. Pairing is never automatically retried. Removing a client entry removes
its client credential, not a substitute for host-side device revocation.

Both `Hello.Schema` and the full 64-hex source cohort `Hello.Build` must match
before application traffic. Different native images of that same cohort are
admitted; image hashes are installation identities, not authentication. See the
[identity and generation contract](core-client-protocol.md#wire-format-and-bounded-delivery).

A mismatched/missing SSH image uses a fixed user-consented exact-image helper,
with expiring host/image review, digest recheck and safe staging. A running older
core is not killed or replaced to make the new connection succeed. Unsupported
platform/artifact sources fail explicitly; no backwards-protocol adapter is
introduced. See [core transport and trust](core-client-protocol.md#trust-secrets-and-platform-limits).

These ownership and trust rules are not evidence of native Windows, physically
distinct-host SSH, complete install-process or resource acceptance. Those gates
are recorded separately by the active implementation/validation plans.
