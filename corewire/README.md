# Core wire transport

This package is a neutral transport, not a core runtime, RPC dispatcher, or usable
attach feature. Its only wire values are JSON data. Applications explicitly
dispatch a closed typed method set and validate identities, epochs, revisions,
receipts and payload schemas; no reflected method dispatch exists here.

## Public API

- `ProtocolVersion = 1`, `MaxFrameSize = 8 << 20` and closed `Kind*` constants.
- `Frame` and `Fault`: JSON envelopes with raw JSON application payloads.
- `NewFramer(context.Context, io.ReadWriteCloser) *Framer`;
  `Read() (Frame, error)`, `Write(Frame) error`, `Close() error`.
- `(*Framer).NegotiateClient(clientID string, maximum int) (int, error)` and
  `NegotiateServer(maximum int) (clientID string, maximum int, err error)`.
- `LocalAddress(dataDir string) (string, error)`;
  `ListenLocal(context.Context, string) (net.Listener, error)`;
  `DialLocal(context.Context, string) (net.Conn, error)`.
- `SSHOptions{Host, User, Port, IdentityFile}` and
  `DialSSH(context.Context, SSHOptions) (io.ReadWriteCloser, error)`.

Frames have a four-byte big-endian unsigned length followed by exactly that many
JSON bytes. The hard cap applies before allocation, and negotiation can only
reduce it. Envelopes reject unknown fields, invalid UTF-8, duplicate JSON keys,
trailing values, depth over 128, unknown kinds and any version except 1. Errors
close the connection. Reads and writes are independently serialized. There is no
output queue; a slow peer blocks its own write until connection-context
cancellation or close. Callers must isolate each client from the domain owner.

Negotiation helpers must run once before other traffic without competing reads
or writes. They are explicit entry points, not an automatic Framer state machine.
Connection context, including any handshake deadline, belongs to the caller.
Local dial context governs establishment; wrap established connections in a
Framer with the full connection lifetime context.

The data directory must already exist. Unix uses its canonical path and a private
0700 `corewire` child containing a 0600 socket and persistent owner lock. Existing
endpoints, even stale crash sockets, fail closed: external explicit cleanup is
required. Close removes only its observed socket identity, never a replacement.
Same-user access is full core access, not isolation from malicious processes of
that user. Long Unix socket paths fail with the native OS error.

Windows uses the canonical data directory and current user SID in a hashed pipe
name. The existing go-winio dependency creates an exclusive first pipe, rejects
remote clients and installs a protected DACL denying network users and allowing
only the current SID. Windows code has cross-compilation coverage; native ACL,
concurrent ownership and cancellation behavior require Windows acceptance.

SSH uses system configuration and known_hosts, explicit strict host-key checking,
no PTY, no forwarding, no connection sharing, noninteractive authentication and
the fixed remote command `crude --core-stdio`. Arbitrary SSH options and remote
paths are not accepted. Cancellation kills and joins the owned ssh process;
it cannot guarantee termination of remote application work or arbitrary children
spawned by user-configured ProxyCommand. Stderr is discarded (bounded memory), so
the current API reports stream/exit errors without detailed SSH diagnostics.
The remote CLI bridge must be implemented by the application. No real remote SSH
or live user core is used by the tests.
