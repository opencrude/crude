# Client remote connections

`remoteconn` is client-machine state, never selected-core state. `DefaultPath()`
resolves `$CRUDE_HOME/<buildinfo.DataDir>/remotes/connections.json`, adjacent to
`client-state.db`. The dedicated directory is private (0700 on POSIX), documents
and lock files are 0600. Existing broad modes or symlinks fail closed, not chmod
or reset. No format migration, legacy reader, or corrupt-file fallback exists.

## Host integration

1. Open metadata with `Open(path)` and secrets with
   `OpenSecrets(filepath.Dir(path), SecretOptions{...})`, off the UI goroutine.
2. Generate a stable, client-local connection ID. Save only `Connection` metadata.
   Direct connections require an explicitly confirmed, lowercase SHA256 leaf
   fingerprint and explicit port. SSH aliases retain system SSH configuration;
   a zero SSH port means inherit. SSH passwords are not part of this API.
3. Save `RemoteCredential.Bearer()` separately with `Secrets.Set`. OS keychain
   entries are scoped by the absolute client-profile directory. File fallback
   requires both explicit `AllowFileFallback` and a `Notice` callback that shows
   a persistent notice. Get also shows that notice. A temporarily inaccessible
   existing keychain entry never silently becomes a file credential.
4. Call `RecordUse` only after successful backend/workspace attachment. It retains
   twenty recent workspaces per backend (including `local`); backend paths are
   opaque strings, not client filesystem paths.
5. On Remove, delete the credential first. If deletion fails, keep the connection
   visible so the user can retry. Metadata removal alone cannot revoke a core
   device: invoke the core host's Revoke action independently.

All reads return detached values. File mutations use an OS file lock, reread the
latest complete document, and atomically replace it with file+directory syncing
through `fileio`. `fileio.WriteCommitted(err)` means replacement committed before
a durability warning; do not replay a stale mutation blindly. Native keychain
writes and local JSON publication are not a cross-system transaction: a storage
failure after keychain success can leave an orphan keychain item. Preserve the
connection ID for repair; do not infer failed Set guarantees no external effect.
The callbacks must not synchronously reenter the same store. Keychain APIs may
block at the OS boundary; request cancellation fences file operations but cannot
cancel a platform keychain call already executing.

POSIX mode checks are enforced; Windows permissions inherit the user's profile
directory ACL (Go mode bits are not Windows ACLs). Native macOS/Windows keychain
and ACL behavior requires platform acceptance beyond package tests.

## Core authentication integration

`core.OpenRemoteAccess` uses `corewire.LoadIdentity` and a separate private
`devices.json` registry. App settings remain the sole configuration persistence
owner: pass `RemoteAccessConfig` on construction/`SetConfig`, restart listeners
under the application owner's lifecycle, and pass `GracePeriod()` into sessions.
Zero grace means fifteen minutes. Direct access is off unless explicitly enabled.

Administrative RPCs (`remote.pairing.create`, `remote.device.revoke`) require
host-local authority; `remote.ssh.bootstrap` requires a verified SSH carrier.
Neither a durable remote credential nor SSH bootstrap grants host-local admin.
The service deliberately owns no session or RPC authority decisions. The runtime
must enforce these checks rather than trusting a client-supplied carrier label.

Pairing code is crypto/rand 128-bit base64url text, five-minute expiry, one use,
five failed attempts per code, and five global attempts per minute. Restart
invalidates outstanding codes. SHA256-only device credentials persist; successful
authentication updates last seen. `View` excludes hashes/codes/tokens. Revocation
is durable across service handles. `CheckDevice` lets the runtime fence connected
sessions; runtime must close revoked carriers and reject resumed sessions too.

Use `coreclient.PairRemote` for the pre-session `Hello.Pair`/`Welcome.Paired`
exchange after fingerprint confirmation. It dials pinned QUIC or TLS1.3 and
returns no usable session; it never automatically replays enrollment. The core
must close this path after pairing, before installing sessions/leases/topics.
Authenticate normal network `Hello.Credential` with `AuthenticateCredential`.
Raw credential is `deviceID.token`, with exact canonical base64url lengths.

`BootstrapSSH` yields an in-memory, one-use, sixty-second credential plus endpoint
and pin over the existing authenticated typed command stream. Its accepted
principal has `SSHBootstrap=true`, not a durable device ID. The runtime must bind
that provenance to the resumable session; carrier replacement uses session resume
authority without reusing the spent bootstrap. SSH remains the fallback carrier
when UDP upgrade fails. Never interpolate these values into a remote shell
command. TLS transport and pin verification are owned by `corewire`, not this
package.
