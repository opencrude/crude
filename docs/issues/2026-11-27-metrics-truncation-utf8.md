# Metrics events split multi-byte UTF-8 at the truncation boundary

> Status: Fixed in source. The running shell uses the fix after it is rebuilt and restarted.

## What we saw

An event whose `Label` (or `Detail`) was a multi-byte UTF-8 string longer
than its byte ceiling was truncated at a byte boundary, splitting a
multi-byte character and leaving an orphaned continuation byte. When the
snapshot was JSON-marshaled, `encoding/json` replaced the invalid byte
sequence with U+FFFD, so dashboard and inspect consumers saw a label or
detail ending in `\ufffd` instead of the real (truncated) text.

`metrics.Note` is called with `detail = err.Error()` from many places
(LSP, core, agent provider), and error strings often carry file paths,
which can contain non-ASCII characters.

## Cause

`internal/metrics/metrics.go` `recordEvent` truncated with
`event.Label = event.Label[:160]` and `event.Detail = event.Detail[:400]`.
A byte slice at the ceiling can land in the middle of a multi-byte rune:
`strings.Repeat("世", 100)` is 300 bytes, so `[:160]` leaves the 53rd
rune (`世` is 3 bytes, `e4 b8 96`) split at its first byte `e4`.

## Fix

`recordEvent` now truncates at a rune boundary using a new
`truncateUTF8(s string, max int) int` helper that walks back to the last
`utf8.RuneStart` so the slice stays valid UTF-8. The event label and
detail that exceed their byte ceiling are cut at a rune boundary instead
of mid-rune.

## Regression checks

`internal/metrics/metrics_test.go` adds a test that fails on the
previous implementation (the label ends in an orphaned `\xe4` byte and
`utf8.ValidString` reports false) and passes after the fix. It records an
event with a >160-byte multi-byte label and a >400-byte multi-byte detail
and asserts both stay valid UTF-8 and are truncated below their full
length.
