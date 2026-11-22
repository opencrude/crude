// Package clientstate persists client-private composer snapshots independently
// of workspace/session databases. It has no UI or credential dependencies.
// Epochs fence attachments; record revisions fence content writes; request IDs
// identify durable receipts. None of these identities is interchangeable.
package clientstate
