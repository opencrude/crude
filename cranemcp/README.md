# Crude MCP stdio adapter

> Status: Implemented adapter and CLI route; official SDK pipe/core acceptance is covered by `server_test.go`. Native platform and distinct-host SSH acceptance remain separate.

Launch a read-only, workspace-bound MCP server:

```sh
crude --mcp --workspace /absolute/project
```

The adapter uses the existing private local core connection. Remote use follows
the same strict system-SSH bridge as the interactive shell:

```sh
crude --mcp --ssh configured-host --workspace /remote/project
```

`--mcp-effects read,write,execute` explicitly fixes the permitted effect set for
this connection. Supported values are `read`, `write`, `delete`, `execute`, and
`communication`; the default is only `read`. Authority and human-only operations
cannot be granted. There is no tool to widen this launch grant.

UI operations require an exact registered shell identity, selected by the user
with `--mcp-shell CLIENT_ID --mcp-shell-epoch EPOCH`. `crude_shells` lists bounded
informational identities but never selects or changes the binding. No shell is
automatically chosen, even if only one is registered. A detached/replaced shell
must be explicitly selected at a fresh launch.

## Tools and results

- `crude_commands`: discover semantic commands and their exact schemas.
- `crude_control`: supply a discovered command, object arguments, optional
  session/resource/execution target, and inspected revision/open-epoch fences.
  Workspace and shell cannot be changed by tool arguments. Generic shell control
  resolves its actual effect through the target shell before execution; its
  arguments include the exact inspected `expected_action`.
- `crude_shells`: list up to 128 registered identities in the bound workspace.
- `crude_receipt`: observe an original `request_id`, without repeating an effect.

Structured content contains owner-provided descriptors, control results or exact
receipts. Text gives a short state summary, not a raw JSON envelope. Admission,
pending decision, partial and uncertain outcomes are not completion. An effect
gets one random request identity, also used as its RPC identity. After a lost
reply the adapter queries the original receipt once; it never blindly resends
the effect. If the transport is unavailable or the receipt remains pending or
uncertain, retain the returned request identity for reconciliation. A fresh MCP
invocation is a new request, not an automatic retry of that identity.

The adapter contains no model loop, domain state owner, HTTP listener or alternate
core socket. MCP exclusively owns stdout; launch errors go to stderr. It exits
without entering the interactive application path. Closing MCP detaches its own
core connection, not another shell or retained session.

## Evidence limits

The official SDK communicates through real newline-delimited `IOTransport` pipes
in tests. These tests attach an actual `core.Runtime`, register two shell
endpoints, discover tools, read/change real settings, deny read-only writes,
verify exact-shell routing and forbidden target widening, and recover a deliberately
lost completed mutation reply by durable receipt with one execution. The shell
endpoints are controlled test responders, not physical TUI paint acceptance.
Complete cancellation/disconnection timing, CLI subprocess stdio, native Windows
console setup and distinct-host SSH still require integration acceptance.
