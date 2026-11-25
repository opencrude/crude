# Offline protocol conformance

> Status: Current guide. Local synthetic replay and the official MCP client referee are separate checks. Neither is live model certification.

## Provider recordings

The isolated module in `tools/providerfixtures` pins [go-vcr v4.0.8](https://github.com/dnaeon/go-vcr/tree/v4.0.8).
It supports one literal `OK` streaming turn for each of `chat`, `responses`,
`codex`, `messages`, `gemini`, `interactions`, `ollama`, `openai-completions`,
`deepseek-fim` and `codestral-fim`. It does not record Crude sessions. Capture
has no custom prompt, arbitrary request body, tool list, gateway or account
discovery. Ollama uses the default local endpoint; the others use fixed HTTPS
provider origins.

```bash
cd tools/providerfixtures
go mod download                    # dependency setup, not a provider request
GOPROXY=off go test -count=1 ./...
go run . replay -wire chat -dir ../../internal/providersim/recordings
```

`internal/providersim/recordings/chat.yaml` is an independently authored,
synthetic go-vcr cassette for the pinned OpenAI Chat chunk contract. It is
**not** traffic from a provider account. The default test replays it and
compares it with a fresh simulator turn. All ten wires also have local
capture → replay → comparison plumbing checks. These latter checks do not
independently establish provider behavior.

An operator can opt in to capture **one potentially billable request**. Give
the tool a new directory, supported wire, actual model ID and a token on
standard input. Use an explicitly supplied API key, or an already available,
authorized short-lived bearer for Codex. No keyring, OAuth refresh, login,
stored application accounts, browser or provider-key environment variables
are used. Ollama needs no token. Do not pass a token as a command argument or
type it into a visible terminal. For example, from the tool module:

```bash
# Only after authorizing a real request. The input file contains only the key.
go run . capture -live -wire messages -model "$MODEL" \
  -dir "$NEW_DIRECTORY" < "$PRIVATE_TOKEN_FILE"
# Offline. NEW_DIRECTORY must be an absolute path when passed to the Go test.
CRUDE_FIXTURE_DIR="$NEW_DIRECTORY" CRUDE_FIXTURE_WIRE=messages \
  GOPROXY=off go test -run '^TestRecordedTurn$' -count=1
```

The recorder rejects redirects, HTTP errors, bodies over 1 MiB, unexpected
fields, free text and incomplete turns. Only fixed request bytes, normalized
model/URL, protocol headers and sanitized response frames go into YAML.
Credentials, cookies, response headers, user metadata and SSE comments are not
saved. Body IDs and opaque strings become deterministic aliases. An unfamiliar
successful response can still be rejected: review the public contract before
expanding the allowlist. No failed raw body is saved for inspection. Review
each cassette before sharing it and separately label its origin; do not call a
simulator output a provider observation.

Replay uses go-vcr **replay only**, with no network fallback. `serve` accepts
the same flags as `replay`. It prints a loopback base URL and serves the
recorded scenario's exact POST body, path/query and protocol headers from
memory until stopped. `TestRecordedTurn` derives the text deltas from the
recording, scripts the simulator with those deltas and compares the ordered
event names, terminal, field presence and types, discriminators, indexes and
ID relationships. An extra optional field can be reported as a difference:
investigate it rather than blessing a new snapshot. Usage counts and timings
are not equality checks. Go-vcr buffers the body; there is no evidence here
about latency, network packet boundaries, backpressure, error streams, or
replay of a provider-signed item against its issuing service.

## MCP client

Use Node 22 and the Go toolchain from the root module. `prepare` is the only
networked setup step. It builds the production manager driver and installs the
locked npm dependency with lifecycle scripts disabled in a **new directory
outside the checkout**. No `npx` or workspace npm install is needed.

```bash
python scripts/mcp_conformance.py prepare "$NEW_TOOL_DIR"
python scripts/mcp_conformance.py quick "$NEW_TOOL_DIR"  # tools_call; Crude must see 8
python scripts/mcp_conformance.py full  "$NEW_TOOL_DIR"  # all 2026-07-28 requirements
```

The driver uses `agent/mcpclient.Manager`, not the upstream SDK's example
client. It has a disposable root and credential directory. It acts as the
synthetic user at the referee's loopback sign-in flow, follows no external
redirect and uses only the manager's discovered definitions and results.
Runtime dependencies are installed before tests; the dispatch-only **Offline
protocol conformance** workflow runs the actual referee and provider replay
tests in a Linux network namespace with only loopback. Each invocation keeps
the referee's `checks.json`, stdout and stderr in a fresh `results-*`
directory. CI uploads the checks (including failures); it calls no provider.

Crude pins the [same referee release as Go SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/.github/workflows/conformance.yml):
`@modelcontextprotocol/conformance@0.2.0-alpha.11`, npm revision
[`c321dd3`](https://github.com/modelcontextprotocol/conformance/tree/c321dd32035556e6769d3724a8ee97d87c3faaac).
The older stable 0.1.16 does not know the new stateless lifecycle. The full
command runs the official [frozen 2026-07-28 client requirements](https://github.com/modelcontextprotocol/conformance/blob/c321dd32035556e6769d3724a8ee97d87c3faaac/requirements/2026-07-28.yaml),
not a handpicked subset. `scripts/mcpconformance/baseline.yml` has no waivers.

### Local evidence, 2026-11-25

The integrated native Windows run produced **39 check files**. All **32 scored
client scenarios passed** with an empty waiver list. Of the seven non-scored
scenarios, JSON Schema preservation passed. Six optional authorization extension
scenarios did not: client credentials (basic and JWT), enterprise-managed
authorization, DPoP, DPoP with a nonce, and workload identity. Across all scenarios
the referee counted **455 successful checks, 16 failures and one warning**. All
failures and the warning belong to those six extensions. Five individual wire
observations were marked skipped by the referee; no scenario was omitted. These
are wire checks, not counts of distinct requirements or of hosted CI runs.

Four scored scenarios had not passed in the initial run (446 successful checks,
20 failures and three warnings). The integrated run also confirmed each of these
four reached the driver's expected result, not just the expected request:

| Scenario | What passed |
|---|---|
| `auth/basic-cimd` | The public metadata URL was the client ID. |
| `auth/pre-registration` | The synthetic issuer-bound secret was used at its token endpoint. |
| `auth/scope-step-up` | A deliberate second **Sign in** retained the earlier scope and requested the challenged one. The driver made a fresh call only after sign-in. |
| `sep-2322-client-request-state` | All five referee assertions passed. The driver checked the four tool results, including an unrelated request and an omitted result type. The [MRTR contract](https://modelcontextprotocol.io/specification/2026-07-28/basic/patterns/mrtr) governs opaque state. |

The driver uses a disposable vault and loopback fixture identities. The negative
cases have narrower meaning: `auth/resource-mismatch` observed no authorization,
but the client stopped at registration (HTTP 404); `auth/scope-retry-limit`
observed one attempt, but the client stopped on a rejected handshake. These
upper-bound checks do not establish *why* the client stopped. This gate does
not certify the server role, live provider compatibility or every negotiated MCP
revision.
