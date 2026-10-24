# Crude quic-go fork

Base: github.com/quic-go/quic-go v0.61.0. Upstream LICENSE is preserved.
Go version and wire protocol are unchanged.

Local delta: `Stream.SetPriority` and `SendStream.SetPriority` select local
STREAM-frame scheduling classes 0–3, weighted 8:4:2:1. Class 2 is the default;
unmodified users retain round-robin behavior among their streams. Scheduling
lives in the existing packet framer, not an application-side queue. QUIC control
frames, congestion control, ACK/loss recovery and flow control are unchanged.
Priorities do not bypass a congestion window or connection flow-control limit.

Priority metadata is atomic. A setter notifies the packetizer through the owning
connection, reclassifying pending frames under its scheduler mutex. It rereads
the atomic value under that mutex to avoid stale concurrent notifications.
Class queues retain at most one entry per active stream; removal, exhaustion and
0-RTT rejection release scheduling metadata. Reclassification/removal scans one
class queue (rare lifecycle operations); steady-state dequeue is allocation-free.

Focused tests and benchmark: `go test . -run 'Test(Framer|Priority)'` and
`go test . -run '^$' -bench BenchmarkPriorityFramer -benchmem`.
