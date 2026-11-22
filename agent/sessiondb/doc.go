// Package sessiondb stores the session SQLite data: transcript messages,
// recovery checkpoints, context publications and the queue. It reads and
// writes rows; it does not decide when a session publishes or compacts.
package sessiondb
