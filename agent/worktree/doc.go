// Package worktree owns explicit managed execution artifacts. It never changes
// the logical checkout's branch, index or files. Integration is a neutral plan:
// the workspace document and Local History owners must commit it transactionally.
package worktree
