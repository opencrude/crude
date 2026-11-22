# fastwalk fork

Upstream: https://github.com/charlievieth/fastwalk at commit
1d58b22e6ed98b13d701f9a77871e5215d23cc97. Licenses: `LICENSE` and
`GO-LICENSE`. Unlike the other folders here, this one has no `go.mod`: it is a
package of the Crude module (`crude/third_party/fastwalk`), so Crude has no
module dependency on fastwalk.

## Local changes

Crude adds context cancellation, bounded metadata catalogs for the pickers
(`catalog.go`), a hard worker ceiling, entry and path-byte limits,
hidden-directory filtering, directory-only scans, deterministic catalog
sorting, and a path-search adapter that never reads file contents
(`adapters.go`). The directory enumeration itself is upstream code.

## Update

Diff upstream against the commit above, apply the enumeration changes, keep
the extensions, then run `go test ./third_party/fastwalk/...` and `make test`.
