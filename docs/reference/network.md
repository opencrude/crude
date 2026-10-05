# Network transport

> Status: Current reference — application-owned routing, TLS trust, and client identity.

## Product surface and authority

Application Settings has one **Network** group, between AI and Security:

| Page | Controls |
|---|---|
| Connection | TCP connect timeout, TLS handshake timeout, TCP keep-alive, idle-pool lifetime, idle connections per host, HTTP/2 compatibility |
| Proxy | Environment / Direct / Custom URL, credential-masked proxy URL, proxy bypass |
| Certificates | Minimum TLS version, additive custom CA file, exact leaf-certificate exception, exact-host mTLS identity |

The palette's **Network Settings** opens Connection directly. Workspace Settings
shows the application values read-only; it cannot introduce proxy credentials,
trust exceptions, or client identities. These settings persist under `network`
in the application `config.json`, whose permissions are owner-only. Proxy
credentials are masked and refuse clipboard commands. They are not encrypted
inside that file. Certificate/private-key bytes are never persisted in settings;
only their paths and exact destination hostname are stored.

`network/` owns the transport and scalar settings. `app/` owns persistence and
publishes its validated immutable default snapshot. Providers retain their own
request/progress/attempt budgets; they do not implement another proxy/TLS stack.
New clients capture current policy. In-flight clients keep their snapshot, so a
save cannot change TLS or routing halfway through a request. A retained MCP
connection uses its captured policy until explicitly reconnected.

Preferences Save runs certificate validation and persistence off the UI thread.
Its exact retained form is busy until the owning workspace/overlay result arrives.
Failure keeps the same draft open. A late result cannot close a replacement
Settings window or redirect a workspace save into another project's database.

## Connection policy

| Setting | Default |
|---|---|
| TCP connect timeout | 30 seconds |
| TLS handshake timeout | 10 seconds |
| TCP keep-alive | 30 seconds |
| Idle connection lifetime | 90 seconds |
| Idle connections per host | 10 |
| HTTP/2 | Enabled |
| Minimum TLS | TLS 1.2 |

TLS 1.3 is an explicit stricter minimum. Disabling HTTP/2 is a compatibility
control, not the default. Connection establishment is distinct from model
response waiting; the provider progress watchdog remains the sole owner of
header/stream silence. An explicit provider/account Connect timeout overrides
both its TCP and TLS establishment budgets. An unset provider Connect timeout
inherits the separate Network budgets.

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
