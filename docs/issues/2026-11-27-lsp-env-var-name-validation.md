# LSP environment variable names with "0" or "x" silently turned servers off

> Status: Fixed in source. The running shell uses the fix after it is rebuilt and restarted.

## What we saw

A saved LSP language whose `environment` map had a variable name containing
the digit `0` or a lowercase `x` (for example `PORT_8080`, `VERSION_10`,
`MY_VAR_x`) silently failed validation, so `ResolveServer` returned "not
available" and the language's server never started.

## Cause

The environment-name check used `strings.ContainsAny(name, "=\\x00")`. In
a Go double-quoted string `\\` is one backslash and `x00` is three literal
bytes (`x`, `0`, `0`), so the second argument was the five-byte string
`=`, `\`, `x`, `0`, `0` rather than the intended three-byte set `=`, `\`,
NUL. `strings.ContainsAny` matches any byte in that set, so any name
containing `0` or `x` was rejected, and a name that actually contained a
NUL byte slipped through because the real NUL byte was never in the set.

## Fix

`types/setting/language_servers.go` now passes `"=\x00\\"` to
`strings.ContainsAny`: `=`, the NUL byte, and a backslash. Names are
rejected only when empty, or when they contain `=`, `\`, or a NUL byte,
which is the form shell quoting and the kernel can carry through. Ordinary
names that happen to contain `0` or `x` are accepted, and a real NUL byte
in a name is now caught.

## Regression checks

`types/setting/language_servers_test.go` adds a test that fails on the
previous implementation and passes after the fix. It accepts ordinary
names (`PORT_8080`, `VERSION_10`, `MY_VAR_x`, `EXTRA_PATH`, `HOME`,
`PATH`) and rejects `""`, `A=B`, `WITH\SLASH` and `A\x00B`, checking both
`Validate` and `ResolveServer` so a future regression that only breaks
one path is still caught.
