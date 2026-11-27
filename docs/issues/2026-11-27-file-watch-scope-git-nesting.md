# Scope file watcher misclassified nested .git paths as non-git

> Status: Fixed in source. The running shell uses the fix after it is rebuilt and restarted.

## What we saw

While the recursive file watcher is still being promoted, Crude uses a
scope watcher to publish file changes. That scope watcher classified
only `.git` itself and its direct children as git metadata:

```go
git := rel == ".git" || filepath.Dir(rel) == ".git"
```

A change nested deeper than one level — `.git/refs/heads/main`,
`.git/objects/pack/...`, `.git/logs/HEAD` — was reported with
`FileChange.Git == false`. The sibling recursive watcher
(`core/file_watch.go`) classifies the same paths as `Git == true`, so the
two code paths in the same package disagreed. As the watch matured from
scope to recursive, the same `.git` change could arrive with a different
`Git` flag, and any consumer that filters or routes git-internal events
by that flag saw inconsistent results.

## Cause

`core/file_watch_scope.go` `publish` computed `git` from
`filepath.Dir(rel) == ".git"`, which is true only for direct children of
`.git`. The reference implementation in `core/file_watch.go` does it
correctly with `strings.HasPrefix(relKey, ".git"+sep)`.

## Fix

Extract the git classification into a shared `coreWatchIsGit(rel string)
bool` helper in `core/file_watch.go` that covers `.git` and every path
nested under it (`.git/refs/heads/main`, `.git/objects/pack`,
`.git/logs/HEAD`). Both the recursive watcher and the scope watcher now
call it, so the two code paths can never disagree on a `.git` change.

## Regression checks

`core/file_watch_test.go` adds `TestCoreWatchIsGitCoversNestedGitPaths`,
a table test over `.git`, `.git/HEAD`, `.git/refs/heads/main`,
`.git/objects/pack/pack-abc.pack`, `.git/logs/HEAD` (all git), and
`README.md`, `src/main.go`, `foo/.git` (all non-git). It fails on the
previous behavior (the helper reports the three nested paths as
`false`) and passes after the fix.
