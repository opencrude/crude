// Package memory implements crude's durable workspace memory system:
// FTS5/BM25 scored recall with recency, importance and path signals,
// evidence-backed knowledge revisions, scoped episodes, history search,
// exposure and feedback ledgers, and an append-only audit journal.
//
// It is a neutral domain library (like sessiondb): it owns schema,
// storage, and retrieval only. Callers (session, workspace) own LLM
// access, logging, lifecycle policy, and rendering. Records are never
// hard-deleted: "forget" marks a record superseded so the full history
// stays auditable in place.
package memory
