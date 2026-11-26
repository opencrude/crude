# Unified diff counts hid changes whose lines start with "++" or "--"

> Status: Fixed in source. The running shell uses the fix after it is rebuilt and restarted.

## What we saw

When an agent tool wrote or edited a file whose changed lines began with
`++` or `--`, the reported diff block was suppressed and the `Added` and
`Removed` line counts read zero, so a real change was reported as "no
change". The same content reported through `unidiff.IsEmptyDiff` was also
treated as empty. A diff that only added `++i` or removed `--count`
disappeared from the tool result.

## Cause

`text/unidiff.GenerateDiff` and `text/unidiff.IsEmptyDiff` counted added
and removed lines by scanning the rendered unified diff and matching the
`+` and `-` prefixes while skipping the `+++` and `---` file headers.
`go-udiff` renders an added line as `+` followed by the line text, so an
added line whose text begins with `++` (for example `++i`, `+++x`) is
rendered as `+++i` or `++++x`. The prefix check `strings.HasPrefix(line,
"+++")` matched that rendered line and skipped it as a header, so the line
was not counted. The symmetric `--` case (a removed `--count`, a SQL `--`
comment) rendered as `---count` and was skipped too. `++` and `--` are
common line prefixes in real code: increment and decrement operators, SQL
and shell comments, and divider lines.

## Fix

`GenerateDiff` now counts the inserted and removed lines from the
structured `udiff.Lines` edits instead of re-parsing the rendered diff.
`IsEmptyDiff` keeps scanning the rendered text (it takes only the
rendered string), but it skips the two file headers by their full form
(`--- ` and `+++ ` with the trailing space) and then classifies every
remaining line by its first byte, so a rendered `+++i` content line is
read as an added line, not a header. The counts now match the lines the
renderer marks `+` and `-`.

## Regression checks

`text/unidiff/diff_test.go` adds cases that fail on the previous
implementation and pass after the fix: adding `++i`, `+++x` or two `++`
lines; removing `--count` or two `--` lines; replacing a `--` line with a
`++` line. It also keeps the ordinary cases (identical, empty to empty,
single add, unterminated line, block replace, edit with context) and
checks that `IsEmptyDiff` reports the `++`/`--` diffs as non-empty while
identical input stays empty. `TestIsEmptyDiffSkipsHeadersAndCountsRenderedContent`
keeps the file headers in the diff and verifies they do not count as
content lines.
