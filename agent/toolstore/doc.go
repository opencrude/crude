// Package toolstore owns the bytes of acquired code-intelligence tools.
//
// Each tool lives in one read-only directory per package, version and artifact
// digest under a root that the caller derives from CRUDE_HOME. The directory
// holds an immutable manifest.json and the extracted files. Mutable accounting
// (the workspaces that use a tool and when it was last used) lives in one
// separate usage.json that is replaced atomically. Every install, failed
// install, first use by a workspace and removal is appended to history.jsonl.
//
// The store is not an approval owner: callers pass an approval reference that
// the store records. It knows nothing about languages. It never deletes a tool
// on its own: removal needs a preview, and Remove fails closed when anything in
// the preview changed.
//
// Concurrency: one process owns a store root (the per-user core). Installs of
// the same package and version are serialized by an in-process lock, so they
// download once. The store does not lock across processes; two processes that
// open the same root at the same time can race. Store methods do blocking file
// and network I/O and must not run on a UI goroutine.
package toolstore
