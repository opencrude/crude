# JSON-RPC request decoding fork

Pinned from `github.com/sourcegraph/jsonrpc2 v0.2.1`, commit
`2cc94179e136b565cb9df7d9d0bff9fc06912ac9`, verified through `go mod download -json`
and the installed module cache. Upstream module sum:
`h1:2GtljixMQYUYCmIg7W9aF2dFmniq/mOr2T9tFRh6zSQ=`.
The MIT LICENSE and all nine root production files are retained. No websocket
subpackage, upstream CI or upstream test corpus is included. The root package
uses only the standard library; the websocket-only requirement and go.sum are
omitted.

Only `Request.UnmarshalJSON` changes production semantics: detached original
params/meta value bytes replace interface decoding/re-marshaling; duplicate
top-level names (including extensions/escaped equivalents), nonobjects and
trailing values refuse atomically. Successful reuse clears stale fields.
Absent/null payloads remain distinct. Upstream method, signed-int64-to-uint64 ID,
null-ID-as-notification, ignored jsonrpc and UseNumber extension behavior remain.
Extension slice order now follows input order rather than map iteration.
Opaque nested duplicates, invalid UTF-8, surrogate escapes and number spelling
are preserved, not validated. Consumers must validate them where required.

Literal regressions cover ownership, reuse, errors, envelope semantics and
actual Content-Length framed Conn delivery. Run `go test ./...`,
`go test -race ./...` and `go vet ./...` here. This fix does not bound initial
frame decode allocations or change marshaling, responses, dispatch, framing,
cancellation or shutdown; tests do not establish native-platform acceptance.

Root and Powernap each replace this module with this exact local fork. Run the
nested suite explicitly; the root `./...` glob does not enter replacement
modules. `make test-lsp` includes its ordinary tests. Lint the local decoder
delta and its new tests against the pinned upstream source; the unchanged
upstream `conn.go` retains four QF1008 warnings. This policy does not waive new
findings or authorize edits to unrelated upstream behavior.
