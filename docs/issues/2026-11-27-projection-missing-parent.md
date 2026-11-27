# validateChanges reported the wrong agent as the orphaned one

> Status: Fixed in source. The running shell uses the fix after it is rebuilt and restarted.

## What we saw

When `WorkspaceView.validateChanges` rejected a malformed agent tree
because an agent's parent was missing, the resync error named the wrong
agent. For a chain `A -> B -> C` with `C` removed, the error said
"missing parent B for A" — naming `B` (which is present) as the parent and
`A` (which is fine) as the agent — even though the actually-missing node
was `C`, the parent of `B`. The tree was still rejected, so no invalid
state leaked, but the user-visible `ErrResync` reason pointed maintainers
and users at the wrong agent.

## Cause

`core/coreclient/projection.go` walked each agent up its parent chain and
checked whether the current node existed in `parents`:

```go
for id, parent := range parents {
    for p := id; p != ""; p = parents[p] {
        ...
        if _, exists := parents[p]; !exists {
            return fmt.Errorf("%w: missing parent %s for %s", ErrResync, parent, id)
        }
    }
}
```

`parent` and `id` are the outer-loop variables — the parent and id of the
agent the loop started from — not the node `p` whose parent is actually
missing. The check also keyed off the wrong thing: it tested whether `p`
itself was a known key, so the walk ran one step past the orphan before
failing, and the message referenced the outer agent instead of the
orphaned one.

## Fix

Check the parent, not the node. At each step, read `parent := parents[p]`;
if it is non-empty, verify that parent exists in `parents`. If it does
not, the orphaned agent is `p` (its parent is the unknown node), so report
"missing parent for <p>". This names the agent whose parent is actually
missing, regardless of how deep in the tree the break is.

## Regression checks

`core/coreclient/projection_test.go` adds
`TestWorkspaceViewMissingParentNamesTheOrphanedAgent`. It builds a chain
`A -> B -> C` with `C` absent and asserts the error is `ErrResync`,
names the orphaned agent `B`, and no longer contains the old wrong
"missing parent B for A" message. It fails on the previous implementation
(error is "missing parent B for A") and passes after the fix.
