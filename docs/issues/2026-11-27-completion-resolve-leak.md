# Completion resolve leaked documentation through an error

> Status: Fixed in source. The running shell uses the fix after it is rebuilt and restarted.

## What we saw

A completion `resolve` that added documentation together with malformed
`additionalTextEdits` returned a non-nil error and a partially populated
`Resolution`, so a caller that propagated the error but still read
`r.Documentation` saw documentation for a resolve that was rejected as
invalid. Every other error path in `Resolve` returns the zero
`Resolution`, so this one path broke the documented atomic-refusal
contract.

## Cause

`lsp/lspcompletion.Resolve` populated `out.Documentation` from
`readDocumentation`, then ran `additionalEdits` only when the original
object lacked `additionalTextEdits` and the resolved object added it.
When `additionalEdits` returned an error, the function returned
`return out, err`, carrying the populated documentation back through the
error.

## Fix

`Resolve` now returns `Resolution{}, err` on that path, matching every
other branch in the function and the sibling `lspaction.Resolve` and
`lspinlay.Resolve`.

## Regression checks

`lsp/lspcompletion/completion_test.go` adds a test that fails on the
previous implementation and passes after the fix. It covers a valid
`documentation` paired with four malformed `additionalTextEdits`
shapes: `newText` without `range`, `range` without `newText`, an entry
with three fields, and a non-array value. Each case asserts that
`Resolve` returns a non-nil error and the zero `Resolution`.
