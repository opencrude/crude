package coreprotocol

import (
	"time"

	captypes "crude/types/capability"
)

// A session starts on the first stream of a carrier (class control): the
// client sends Hello, the core answers Welcome and keeps the stream open for
// the carrier's lifetime. Closing it cleanly ends the session; losing the
// carrier keeps the session for its grace period so any carrier can resume it.
//
// Hello and Welcome both begin with the frozen pair [Schema, Build]. Every other
// field may change between builds; those two positions never move, so any two
// builds can read each other's identity (HandshakeIdentity) and report a typed
// mismatch instead of a tuple decode error.
type Hello struct {
	// Schema must equal the core's exactly; any difference is a different
	// protocol and is refused before application traffic.
	Schema string `json:"schema"`
	// Build is the exact source cohort, independent of native executable bytes.
	// Both Schema and Build must match before any application traffic.
	Build    string `json:"build"`
	ClientID string `json:"client_id"`
	// Resume continues an existing session; nil starts a new one.
	Resume *SessionResume `json:"resume,omitempty"`
	// Credential authenticates network carriers (device or bootstrap
	// credential). Local and SSH carriers are authenticated by the OS.
	Credential string `json:"credential,omitempty"`
	// Pair is the restricted enrollment handshake, never a session admission.
	// The peer must verify the certificate fingerprint before sending its code.
	Pair *RemotePairRequest `json:"pair,omitempty"`
	// SSHRelay can only narrow an OS-authenticated local connection to SSH
	// authority. A network carrier can never use this to authenticate itself.
	SSHRelay bool `json:"ssh_relay,omitempty"`
}

type SessionResume struct {
	SessionID string `json:"session_id"`
	Secret    string `json:"secret"`
}

type Welcome struct {
	// Schema and Build are the frozen identity prefix; see Hello.
	Schema string `json:"schema"`
	Build  string `json:"build"`
	// Owner is the session identity. It stays the same across resumes, so
	// leases and attachments survive carrier loss within the grace period.
	Owner     Owner             `json:"owner"`
	SessionID string            `json:"session_id"`
	Secret    string            `json:"secret"`
	Resumed   bool              `json:"resumed"`
	Grace     time.Duration     `json:"grace"`
	Fault     *Fault            `json:"fault,omitempty"`
	Paired    *RemoteCredential `json:"paired,omitempty"`
	// LocalHost is immutable admission provenance, not a carrier guess. SSH and
	// direct sessions never gain local host administration when they resume.
	LocalHost bool `json:"local_host"`
}

// SessionEnd is the core's last message on the control stream: the session
// is over (the core is shutting down or replaced it) and cannot be resumed.
type SessionEnd struct {
	Reason string `json:"reason"`
}

// StreamKind is the role of a stream, named by its first message.
type StreamKind uint8

const (
	// StreamCommand carries one request and its reply.
	StreamCommand StreamKind = 1
	// StreamTopic carries a subscription: snapshot, then ordered updates.
	StreamTopic StreamKind = 2
	// StreamInput carries ordered, unreplied input under a lease.
	StreamInput StreamKind = 3
	// StreamBulk carries credit-controlled digest-verified chunks.
	StreamBulk StreamKind = 4
)

// Open is the first message on every stream after the control stream. A
// command's request value follows Open in the same message.
type Open struct {
	Kind        StreamKind `json:"kind"`
	ID          string     `json:"id,omitempty"`
	WorkspaceID string     `json:"workspace_id,omitempty"`
	Method      string     `json:"method,omitempty"`
	Topic       string     `json:"topic,omitempty"`
	Key         string     `json:"key,omitempty"`
	// Since is the last sequence the client applied, when resuming a topic or
	// an input stream; zero asks for a snapshot or a fresh stream.
	Since             uint64 `json:"since,omitempty"`
	DisplayIntervalNS int64  `json:"display_interval_ns,omitempty"`
}

// Reply is a command stream message: zero or more progress notes, then one
// final reply whose result value follows it in the same message.
type Reply struct {
	Progress string `json:"progress,omitempty"`
	Final    bool   `json:"final,omitempty"`
	Revision uint64 `json:"revision,omitempty"`
	Fault    *Fault `json:"fault,omitempty"`
}

// TopicHeader precedes every topic value. Reset marks a full snapshot that
// replaces the subscriber's replica; it follows a subscribe, a resume whose
// gap the journal no longer covers, or a journal overrun.
type TopicHeader struct {
	Seq   uint64 `json:"seq"`
	Reset bool   `json:"reset,omitempty"`
}

// TopicAck travels client to core on a topic stream. Paced topics send the
// next update only after recent ones were applied, so a slow viewer receives
// fewer, newer states instead of a backlog.
type TopicAck struct {
	Seq               uint64 `json:"seq"`
	DisplayIntervalNS int64  `json:"display_interval_ns,omitempty"`
}

// Topic names.
const (
	// TopicSession delivers core-initiated requests to this session
	// (shell control, capability host requests and private clipboard offers).
	TopicSession = "session"
	// TopicWorkspace delivers one attached workspace's state and events.
	TopicWorkspace = "workspace"
	// TopicTerminal delivers one terminal's grid frames (Key = terminal ID).
	TopicTerminal = "terminal"
	// TopicTerminalInput is the input stream for one terminal.
	TopicTerminalInput = "terminal.input"
)

// SessionEvent is one core-initiated request for this session.
type SessionEvent struct {
	WorkspaceID       string                `json:"workspace_id"`
	Control           *ControlEnvelope      `json:"control,omitempty"`
	CapabilityHost    *captypes.HostRequest `json:"capability_host,omitempty"`
	TerminalClipboard *TerminalClipboard    `json:"terminal_clipboard,omitempty"`
}

// TerminalClipboard is an explicit copy offer to the exact terminal controller,
// never a workspace invalidation or an automatic desktop clipboard write.
type TerminalClipboard struct {
	TerminalID string   `json:"terminal_id"`
	Generation uint64   `json:"generation"`
	Clipboard  [][]byte `json:"clipboard"`
}

const SessionPrivateMaxOutstanding = 16
const SessionPrivateMaxBytes = 1 << 20
