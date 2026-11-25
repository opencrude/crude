# MCP Go SDK

Runtime packages from [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk)
v1.8.0, commit `3f3b699b2b67e1ed033a63d6651671dab53c2d32`. `LICENSE` is
unchanged. Examples, development tools, tests and documentation are not included.

- `mcp/protocol.go` and `mcp/mrtr.go` use `*string` for optional
  `requestState`. `nil` omits the member; a non-nil empty string encodes `""`.
  The original `string` with `omitempty` lost this distinction before a caller
  could inspect `NeedsInput`. No transport or retry behavior was replaced.
- `mcp/client.go` exposes the SDK form schema validator as `ValidateElicitSchema`
  for callers that handle `NeedsInput` themselves. A null titled-enum entry
  reports a missing constant instead of dereferencing nil.

To update, obtain the upstream revision through the Go module checksum database.
Copy `go.mod`, `go.sum`, `LICENSE` and the non-test source of the used packages
(`go list -deps ./agent/mcpclient`), including all supported platform files.
Reapply only the deltas above. Remove the replacement when upstream exposes
equivalent presence-preserving types and form validation. Run the managed MCP
tests, including HTTP and stdio form calls, and the frozen client referee.
