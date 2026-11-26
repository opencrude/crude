# Provider simulator

> Status: Current reference. One simulator serves seven conversation protocols and three fill-in-the-middle dialects. The [offline conformance guide](../guides/protocol-conformance.md) distinguishes schema checks, recordings and the independent MCP referee.

## Why

Before migration, about 209 test files started their own HTTP server, and 168 contained
provider stream text written by hand. The author of a request encoder also
writes the fake that accepts it, so they agree by construction. In November
2026 this hid real defects and produced false failures:

- a fake read the compaction probe from top-level `instructions` after Crude
  correctly moved it into `input` (false failure);
- fakes counted request size without the declared tools, so the checkpoint
  chunk-budget bug stayed invisible until real runs failed (0ee97c8f);
- no test could see the whole-history cache miss after a settings change,
  because no fake modeled a prompt cache.

The MCP tests already avoid this: they run the official MCP Go SDK server.

## The rule

`internal/providersim` is one in-process simulator for seven conversation wires
and three explicit fill-in-the-middle (FIM) dialects. Keep the wire evidence independent
of `agent/provider`:

1. pinned official specs and official SDK types, compiled into JSON Schema
   bundles with a manifest (URL, revision, SHA-256, license, generator);
2. offline go-vcr v4 replay (the checked-in cassette is synthetic and independently authored);
3. optional, explicitly authorized capture of a fixed synthetic turn on a real account.

`internal` packages cannot import `agent`, so the layout gate enforces the
independence. A simulator fact that no source establishes is marked
`synthetic` in code and in the wire table below.

## Sources (pinned by the research report of 2026-11-25)

| Wire | Request/response authority | Stream authority | Notes |
|---|---|---|---|
| OpenAI Chat | `openai/openai-openapi@abe304d1e5f1264fd3a11499ab57c0d17f5c1df3` `openapi.json` (OAS 3.1, MIT) | `chat.completion.chunk` + `data: [DONE]` | Unknown top-level argument: 400 `invalid_request_error`. |
| OpenAI Responses | same spec: `CreateResponse`, `/responses/compact`, `context_management` | typed events (openai-python `response_stream_event.py@aff223d`) | `response.failed` and top-level `error` events differ. |
| ChatGPT Codex | `openai/codex@ebd0765` `codex-rs/codex-api/src/common.rs` (`ResponsesApiRequest`) | `sse/responses.rs` | `compaction_trigger` is an INPUT item; the stream returns exactly one `compaction` output item (codex commit 0322ac3). No stored responses over HTTP. |
| Anthropic Messages | `anthropics/anthropic-sdk-python@f1edcd7` types, incl. beta compaction types | six events + `ping` + `error` | Cache total = read + creation + input. On-demand compaction: `compaction:{type:"summarize"}` + beta `compact-2026-09-04`; threshold: `context_management.edits[compact_20260112]` + beta `compact-2026-01-12`; the two are exclusive. |
| Gemini generateContent | `googleapis/googleapis@4b6ea83` protos; `googleapis/python-genai@36e2970` `types.py` | repeated `GenerateContentResponse`, `?alt=sse` | Errors: numeric `code` + `status`; 429 may carry `RetryInfo.retryDelay` in `details`. |
| Gemini Interactions | python-genai `_gaos/types/interactions@36e2970`; AI Studio OAS 3.0.3 snapshot (hash and date recorded) | `interaction.*`, `step.*`, `error` | Errors use string `code` (`parameter_unknown`, `rate_limit_exceeded`). |
| Ollama native | `ollama/ollama@924cc28` [openapi.yaml](https://github.com/ollama/ollama/blob/924cc28/docs/openapi.yaml) (OAS 3.1, MIT), `api/types.go` | NDJSON; `{error}` line mid-stream | Unknown top-level fields ignored. `prompt_eval_cached_count` optional. |
| OpenAI legacy completions | Same OpenAI pin, `CreateCompletionRequest` / `CreateCompletionResponse` | `choices[].text`, finish choice, optional usage-only chunk, `[DONE]` | `CompletionStreamChunk` permits the null `usage` required by `include_usage` on ordinary chunks. The non-stream root is unchanged. |
| DeepSeek FIM | [FIM Completion API (Beta)](https://api-docs.deepseek.com/api/create-completion.md) | `choices[].text`; usage on the finish choice, even without `include_usage`; `[DONE]` | `deepseek-flash` or `deepseek-v4-pro`. Required cache hit/miss counts when usage is present. |
| Codestral FIM | [Mistral FIM endpoint](https://docs.mistral.ai/api/endpoint/fim), `mistralai/client-python@6e9e31a` `completionresponsestreamchoice.py` | `choices[].delta.content`; nullable `finish_reason`; `[DONE]` | JSON response uses `choices[].message.content` and requires usage. Stream chunks allow usage. No `stream_options` request field. |

FIM documentation pins (2026-11-25, SHA-256 of the fetched bytes, not
redistributed snapshots): DeepSeek completion page
`9e5e344c07b51c478eb205eb857d4a59bfb111c56279372481087a98ae11f757`,
Mistral FIM page
`0b599b1b43255f1b2dc90af4295d78053a91f83ccf1c93018bbc101d9f44544b`.
Fault status/format references: [DeepSeek errors](https://api-docs.deepseek.com/quick_start/error_codes.md)
and [Mistral error glossary](https://docs.mistral.ai/resources/error-glossary).

Schema bundles are generated by `scripts/providersim_schemas.py` (pinned
Python packages; OpenAPI extraction, pydantic JSON Schema for SDK types, and
Protobuf descriptors for the Gemini model list)
into `internal/providersim/schemas/` with `manifest.json`. A Go test verifies
every bundle digest against the manifest. Validation uses
`github.com/google/jsonschema-go` (already a dependency; Draft 2020-12).
Strictness comes from each wire's documented server behavior (for example,
rejecting unknown top-level fields), applied explicitly per wire. Never inject
`additionalProperties:false` globally.

For Gemini model listing, the generator compiles the Apache-2.0 Google APIs
`model.proto` and `model_service.proto` at `4b6ea83bc2d87be6f3b8ce19f070b649bd7f4638`.
It verifies the `GET /v1beta/models` binding and projects all 13 `Model`
properties and both `ListModelsResponse` properties. The manifest records the
source and import hashes, compiler and descriptor-reader pins, and
`googleapis.license.txt`. The generator does not use a live Discovery response
or redistribute one.

### Synthetic facts

No source establishes these; code marks each one `synthetic`.

| Fact | Simulator choice |
|---|---|
| Token count | `ceil(UTF-8 bytes / 4)` per rendered block. |
| Cache rendering | Block boundaries and order per wire (Chat: tools, then messages). |
| Cache profile | Per model; Chat default minimum 1024, step 128; no TTL. |
| Faults | Error messages, IDs, timing and the position of an in-stream fault. The OpenAI spec allows a 200 Chat stream to carry `error` and end without `[DONE]`; using `ErrorResponse` for that frame is a choice. |
| Chat reasoning | The public Chat spec has no reasoning output. A model profile can enable DeepSeek's documented `reasoning_content` (https://api-docs.deepseek.com/guides/thinking_mode.md) as a named gateway extension. |
| IDs, timestamps, signatures | Deterministic per server. Signatures, sealed compaction items and Messages signed blocks are server-local HMACs, not provider encryption. |
| Responses and Codex | Render order tools, then instructions, then items. Public Responses uses a 1024/128 cache profile; Codex needs `WithCacheProfile`. Writes are the newly eligible prefix minus reads (`Prefix.CacheWriteTokens`). Compaction summaries in `Effective` are lossless. |
| Messages | Reads only at a published breakpoint inside the 20-block lookback; 5m/1h TTL tiers; unknown models get a 1024 minimum and no compaction. HTTP 413 and the model-list page envelope are checked against the docs, not an SDK root. |
| Gemini and Interactions | Unknown models have caching off until a profile opts in. The in-stream Gemini error frame, Interactions context-overflow code and retry headers are synthetic. Interactions replays completed interactions only. |
| Ollama | Reads only (`prompt_eval_cached_count`); Ollama reports no cache writes. |
| FIM | Single literal prompt and suffix; synthetic token counts, chunk boundaries, IDs and timestamps. Codestral stream usage is placed on the finish choice. Fault text and injection positions are synthetic. DeepSeek uses an OpenAI-compatible error object; type/code choices are synthetic, not recordings. Codestral 408/502 type pairings are synthetic. |

## Contract (Go API)

```go
sim := providersim.New(t)                   // one server, every wire
base := sim.BaseURL(providersim.Responses)  // what a Crude account stores
sim.Script(providersim.Text("done"))        // FIFO turns, any wire
sim.Respond(func(r providersim.Request) providersim.Turn { ... })
req := sim.Requests()                       // the journal
```

- Conversation `Wire` constants: `Chat`, `Responses`, `Codex`, `Messages`, `Gemini`,
  `Interactions`, `Ollama`. Each wire owns its routes, including model listing
  and the compaction operations.
- `Turn` is provider-neutral: text, reasoning (with signature or encrypted
  content), tool calls, refusal, usage override, finish reason. A wire codec
  encodes it in that wire's streaming or non-streaming form.
- `Turn{}.WithToolArgumentDeltas(id, name, parts...)` appends a call with
  `Arguments = strings.Join(parts, "")`. Chat sends `function.arguments`;
  Responses and Codex send `response.function_call_arguments.delta` for functions
  and `response.custom_tool_call_input.delta` for declared free-form tools;
  Messages sends `input_json_delta.partial_json`; Interactions sends
  `arguments_delta.arguments`. `WithGateAfter` counts each such event.
  Empty fragments and splits inside an argument string or JSON escape are
  allowed. Each fragment must be valid UTF-8: one JSON string per SSE frame
  cannot encode half a UTF-8 scalar. Gemini's object/typed `partialArgs` and
  Ollama's object-valued arguments cannot express arbitrary serialized
  fragments. These scripts report misuse rather than silently joining them.
- `FIMOpenAI`, `FIMDeepSeek` and `FIMCodestral` are separate wires so
  `BaseURL`, FIFO scripts and the journal require no new dispatch API. Append
  `completions`, `completions`, or `fim/completions` to the respective account
  base paths `/v1`, `/beta`, or `/v1`. FIM has no inventory or chat routes.
  Use `Text`, `WithTextDeltas`, `WithFinish`, `WithUsage`, gates or cuts.
  The journal exposes `Prompt`, `Suffix`, `Model`, `OutputLimit`, `Stream`
  and Codestral's optional `MinOutputLimit`. Unsupported fixture parameters
  (such as batch prompts and logprobs) and non-text turns report misuse.
  Each dialect accepts the HTTP fault kinds. For in-stream faults OpenAI
  accepts a legacy error object. DeepSeek and Codestral support `Overload`
  via the native `insufficient_system_resource` or `error` finish choice.
  Use an HTTP fault for other kinds or custom error wording on those two.
- Faults are turns: HTTP error of a wire-specific kind (rate limit with
  `Retry-After`, `retry-after-ms` or `RetryInfo`; context overflow; overload;
  auth), an error event inside a 200 stream after N events, a cut stream, and
  a gate that holds events until the test releases them. No sleeps in tests.
- `WithProxyBodyLimit(bytes)` puts a reverse proxy in front of every wire, as
  nginx's `client_max_body_size` does: a larger request gets 413 with the
  proxy's HTML page and never reaches the model or the journal.
  `ProxyRejections()` counts those requests.
- `Request` is the journal entry: raw bytes and headers, the wire, and a
  neutral decode written from the spec (system, messages with typed parts,
  tools, tool choice, output limit, stream, store, previous response, cache
  markers). `Effective` is the conversation the model sees after stored
  responses and compaction artifacts are resolved.
  `Request.Reasoning` retains the exact Chat `reasoning_effort` or Responses
  / Codex `reasoning` value, including explicit `null`; nil means omitted.
- Every request is validated. A violation fails the test with the schema path
  and gets the wire's real 400 response.
- Prefix analysis: each request reports the longest rendered prefix (in the
  wire's cache order) it shares with earlier requests of the same model, and
  where it first differs. Cache counters derive from it with a per-wire
  profile (minimum size, granularity, explicit breakpoints) and are labeled
  synthetic. Tests assert prefix stability; that is the property Crude owns.
- State: Responses stores responses (`store` default true) and resolves
  `previous_response_id`. Use `ExpireResponse` to expire a real stored ID without
  consuming the next turn; a fabricated ID is still a fixture error. Public Responses
  and Codex accept an explicit final `compaction_trigger`; the pinned Codex client does
  not define a standalone compact endpoint. Messages serves credential-specific model
  lists and detail with beta capabilities. Compaction artifacts are sealed so a changed replay
  is rejected exactly as the real API does; Codex, Anthropic threshold and
  on-demand compaction follow the table above.
- Replay: a go-vcr cassette can be served as a script, and a conformance test
  checks that the simulator's encoding of an equivalent turn has the same event
  sequence and required fields as the recording.

## Where to use it

Use `providersim` for ordinary provider replies in `agent`, `core`, `workspace`
and `tui` tests. `agent/provider/wire_suite_test.go` exercises 15 behaviors on all
seven conversation wires through the real client. An inapplicable cell names the
protocol reason; it is not a passing observation. The first run exposed Codex
input typing, Ollama thinking replay and context-error classification, plus an
inaccurate simulator error message. The session tests cover all 49 wire transitions,
the first post-checkpoint request, and stored and stateless resume.

Keep raw bytes only when generating a valid ordinary reply would defeat the test:

- **Exact codec and gateway behavior:** duplicate or missing fields or stored IDs,
  reduced usage rows, conflicting terminal snapshots, interrupted patch arguments,
  out-of-order items around a seal, pagination, a nonstandard media part, and a
  gateway-only operation or quota code. Examples are `agent/provider/text_once_test.go`,
  `agent/turnloop/response_continuation_policy_test.go` and `agent/session/usage_integrity_test.go`.
- **Transport behavior:** first-byte and write stalls, early EOF, HTTP/2 upload
  backpressure, redirects, and cancellation after a partial reply. A transport wrapper
  may alter just the part under test and forward ordinary requests to the simulator.
- **Other protocols:** Decisions and System One are not chat; OAuth authorization,
  the MCP SDK server, registries and file downloads need their own protocol fixtures.

Do not add an accepting fake to work around a schema violation. Correct the client
or the simulator against the cited wire contract. Assert the emitted request or
user-visible outcome, not just how a responder was scripted.
