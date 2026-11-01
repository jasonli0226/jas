# Microsoft Foundry / Azure OpenAI as a jas provider

**Date:** 2026-11-02
**Written against:** jas `e6aa513` (fix: match skill rows on Windows by cleaning both paths (#86))

Primary sources only: Microsoft Learn, the Azure REST API specs, the official
OpenAI SDKs (openai-python, openai-go), and the OpenAI OpenAPI spec as the
baseline. Each source is listed with its `ms.date`, or the commit date for
GitHub files. "Unconfirmed" means no primary source settles the point; the
note next to it says which live call would. Microsoft renames these products
often (Azure AI Foundry became Microsoft Foundry, and "classic" pages now sit
beside "new" ones), so re-check the URLs before you rely on them.

## 1. Executive summary

**Recommended endpoint.** Use the Azure OpenAI **v1** route,
`https://<resource>.openai.azure.com/openai/v1`. The same route also works
under `https://<resource>.services.ai.azure.com/openai/v1`. It needs no
`api-version`, and jas's existing `BaseURL + "/chat/completions"` works on it
unchanged. The `model` field takes the **deployment name**. The same route
serves non-OpenAI models that Azure sells, such as DeepSeek, Grok and MAI.

**Recommended auth.** Use a static resource key sent as
`Authorization: Bearer <key>`, which is exactly what jas sends today.
Microsoft documents this for v1. `api-key: <key>` is the documented
alternative. Entra ID tokens are not needed for a CLI that holds a key.

**Consequence.** jas can probably talk to Foundry today with
`provider=custom`, `base_url=https://<res>.openai.azure.com/openai/v1` and
`model=<deployment>`. The streaming format and the tool-call shape are
compatible. The real gaps are in how errors and edge cases are handled, not
in the wire format.

**Minimal change list:**

1. **Named provider.** Add a `foundry` provider. Its preset base URL is built
   from the resource name, its key is required, and onboarding asks for the
   resource name and deployment.
2. **Retry wait.** Parse `retry-after-ms` before `Retry-After` in
   `doWithRetry`. Microsoft documents the millisecond header on 429s.
3. **Content filtering.** Treat `finish_reason: "content_filter"` as an
   error, not a normal finish. Give a clear error for the HTTP 400
   `content_filter` response.
4. **In-stream errors.** Read a top-level `error` object inside SSE chunks
   instead of silently dropping it.
5. **Reasoning effort.** Add a per-model `reasoning_effort` setting. On Azure
   Chat Completions, gpt-5.6 deployments reject any request that has `tools`
   unless `reasoning_effort` is `"none"`.
6. **Context window.** Deployment names are user-chosen, so the catalog
   lookup misses. The onboarding flow should set
   `models.<deployment>.context_window`.
7. **Context-length detection.** The existing
   `"context_length_exceeded"` substring probably still works, but only a
   live call can confirm it.

## 2. Endpoint URL shapes

| Shape | Status | `api-version` | OpenAI-style `/chat/completions` appended to base? |
|---|---|---|---|
| `https://<res>.openai.azure.com/openai/v1/chat/completions` | **Current, recommended.** GA since Aug 2025 [A] | Optional: `v1` (default) or `preview` [B] | Yes |
| `https://<res>.services.ai.azure.com/openai/v1/...` | Current. Documented as equivalent [A][C] | Same as above | Yes |
| `https://<res>.services.ai.azure.com/api/projects/<project>/openai/v1/...` | Current. The project route [D] | Not required [D] | Yes. Docs show Entra auth only; key auth is Unconfirmed |
| `https://<res>.openai.azure.com/openai/deployments/<dep>/chat/completions?api-version=2024-10-21` | Legacy, dated versions [E][F] | **Required** [E] | No. The deployment is in the path and a query string is needed |
| `https://<res>.services.ai.azure.com/models/chat/completions?api-version=2024-05-01-preview` | Legacy Foundry Models inference API. **The azure-ai-inference SDK retired on 2026-08-26** [G][H] | Required [H] | No |
| `https://<res>.services.ai.azure.com/anthropic/v1/messages` | Claude models in Foundry. Anthropic Messages API, not chat completions [I][J] | n/a | No |

- v1 removes the dated `api-version` and works with the plain OpenAI clients.
  Preview features are switched on with feature headers rather than API
  versions [A].
- The v1 spec's server is `{endpoint}/openai/v1`, and its only query
  parameter is the optional `api-version` [K].
- The legacy 2024-10-21 spec's server is `https://{endpoint}/openai`, and its
  path is `/deployments/{deployment-id}/chat/completions` with a required
  `api-version` [F].
- openai-python's `AzureOpenAI` builds
  `{azure_endpoint}/openai/deployments/{model}` and always adds
  `api-version` [L].
- openai-go's `azure.WithEndpoint` rewrites `/chat/completions` to
  `/openai/deployments/{model}/chat/completions` [M].
- Neither legacy client is needed for v1. Microsoft's own v1 samples use the
  plain `OpenAI()` client and `openai.NewClient(option.WithBaseURL(...))` [A].
- Whether the `/models` REST route still answers after the SDK retirement is
  Unconfirmed. jas should not target it either way.

## 3. Authentication

| Method | v1 | Legacy `/deployments` |
|---|---|---|
| `api-key: <key>` | Yes (`ApiKeyAuth`) [B][K] | Yes, and it is the only key scheme [F] |
| `Authorization: Bearer <key>` (the resource key as a bearer token) | **Yes.** The spec defines `ApiKeyAuth_` as an apiKey in the `authorization` header [K]. The REST sample says: "Pass your key in the `Authorization` header as a bearer token" [C] | Not documented. openai-go notes that "Azure expects Api-Key instead" and sends `Api-Key` [M] |
| `Authorization: Bearer <Entra token>` | Yes [A][C] | Yes (OAuth2 implicit) [F] |

- **Why Bearer works on v1.** openai-python's plain client always sends
  `Authorization: Bearer {api_key}` [N]. Microsoft documents that same client
  with a resource key against v1 [A][C], so Bearer-with-key is the documented
  happy path, not an accident.
- **Entra scope.** The Learn v1 and Foundry pages say tokens "must be issued
  with scope `https://ai.azure.com/.default`" [C][D]. The v1 OpenAPI spec
  still lists `https://cognitiveservices.azure.com/.default` [K], and so does
  openai-go's default [M]. The migration guide says to change from the
  cognitiveservices scope to the ai.azure.com scope [O]. Whether the old
  scope is still accepted is Unconfirmed; a live token call would settle it.
- **Entra token lifetime.** By default a token lives a random 60–90 minutes
  (75 on average) [P]. A CLI using Entra would need a refresh path, such as
  `az account get-access-token --resource https://ai.azure.com` [C].
  Out of scope for a first cut.
- **What jas should send.** `Authorization: Bearer <key>` against v1, which
  needs no change. Optionally, send `api-key` instead when
  `provider=foundry`; it is the canonical Azure header and also works on v1.
  Never send both.

## 4. Model naming

- On v1 the `model` field is the **deployment name**, not the model ID [A][C].
  The migration table says: "Model parameter … Required (deployment name)"
  [O].
- A mismatch returns 400 or 404, "deployment not found" [Q].
- Non-OpenAI models use the same Chat Completions surface:
  - v1 "allows you to make chat completions calls with models from other
    providers like DeepSeek and Grok" [A].
  - The reasoning-model page calls `DeepSeek-V4-Pro` through
    `/openai/v1/chat/completions` [D].
  - The SDK overview lists "Foundry Models sold by Azure through Chat
    Completions" on `/openai/v1` [R].
- **Partner models** (Mistral, Llama, Cohere, Phi) are listed as type
  `chat-completion` [I]:
  - Whether every partner model is reachable on `/openai/v1` is Unconfirmed;
    a live call per deployment would settle it.
  - Several partner models list "Tool calling: No", including
    Llama-4-Scout, Codestral-2501 and Phi-4 [I]. jas always sends `tools`.
    How those deployments react is Unconfirmed.
- **Claude** in Foundry is `Messages` type on `/anthropic` [I][R], so the
  OpenAI adapter can't serve it.
- **Responses API.** It is recommended for Azure OpenAI models, and a
  deployment that lacks it returns `400 Model not supported` [C]. jas uses
  Chat Completions, which avoids that problem but hits the gpt-5.6 tools
  limit in section 7.

## 5. Streaming SSE vs OpenAI

The baseline OpenAI behaviour comes from the OpenAI spec: `choices` "can also
be empty for the last chunk if you set `stream_options: {"include_usage":
true}`", and `usage` is null "except for the last chunk" [S].

Azure differences:

- **Prompt-annotation first chunk.** It has empty `choices`, empty
  `id`/`model`, `created: 0`, a `prompt_filter_results` array (some samples
  name it `prompt_annotations`) and `"usage": null` [T].
- **Filter annotations on choices.** Choices can carry
  `content_filter_results` [B][K].
- **Asynchronous Filter mode** adds more:
  - "Annotation messages" arrive as `choices[0]` with **no `delta`**, plus
    `content_filter_results` and `content_filter_offsets`. They can arrive
    **after** the `finish_reason: "stop"` chunk [T].
  - A blocked stream ends with a chunk whose `finish_reason` is
    `"content_filter"`, with no delta, followed by `data: [DONE]` [T].
  - The signal comes within about 1,000 characters of the offending text [T].
- **Default filter mode.** Output arrives buffered in chunks rather than
  token by token [T].
- **Last-segment leak in default mode.** "The last segment of a streamed
  response might be returned before the guardrail policy is applied." In
  tests, a blocklisted term near the end came back with
  `finish_reason: "stop"` [T].
- **`stream_options.include_usage`.** Supported. It was added in
  2024-09-01-preview and is in the v1 spec with OpenAI's wording [A][B].
  Usage carries `prompt_tokens_details`, which is how jas reads cached tokens
  [K].
- **`finish_reason` values:** `stop`, `length`, `tool_calls`,
  `content_filter`, `function_call` [K].
- **`[DONE]`.** Present in Microsoft's samples [T].
- **Mid-stream guardrail errors.** In default mode, "a guardrail error is
  returned" mid-stream [T], but the docs give no wire shape for it.
  Unconfirmed: whether it arrives as `data: {"error":{...}}` or as a dropped
  connection. A recorded fixture of a blocked prompt would settle it.
- **Spec gap.** The v1 spec declares only `application/json` for the 200
  response and does not list `text/event-stream` [K]. This is a spec omission,
  not a behaviour.

What this means for jas today:

- `parseChunk` loops over `c.Choices`, so an empty array yields nothing, and
  `"usage": null` decodes to a nil pointer. **jas already tolerates the empty
  first chunk and the delta-less annotation chunks.** No SSE change is needed
  for them.
- `content_filter` sets `finished = true`, and core ignores the finish
  reason (grep shows no consumer outside `providers/`). **A filtered stream
  becomes a silently truncated success.** Truncated tool-call arguments
  would then reach `Accumulator.Done()` and be run or rejected.

## 6. Tool calling

- **Request fields.** `tools`, `tool_choice` (`none`, `auto`, `required` or a
  named function), and `parallel_tool_calls` (default `true`) match OpenAI's
  [B].
- **Custom tools.** `tools` also accepts custom tools [B]. jas sends only
  `type: "function"`.
- **Streamed deltas.** `ChatCompletionMessageToolCallChunk` has `index`
  (required) and optional `id`, `type` (`function`) and `function{name,
  arguments}` [B]. This is the same shape jas's `parseChunk` and
  `Accumulator` already handle.
- **Reasoning interactions.** Parallel tool calls are not supported when
  `reasoning_effort` is `minimal` [U]. gpt-5.6 can't combine reasoning with
  tools on Chat Completions; see section 7 [U].

## 7. Reasoning models

- **Token limits.**
  - Reasoning models accept only `max_completion_tokens` on Chat Completions;
    `max_tokens` is unsupported [U].
  - `max_tokens` is deprecated in v1 and "not compatible with o1 series" [B].
  - jas sends neither field, so there is nothing to fix.
- **`reasoning_effort` values.** `none`, `minimal`, `low`, `medium`, `high`,
  `xhigh` [B]:
  - Models before gpt-5.1 default to `medium`.
  - gpt-5.1 defaults to `none` [B].
  - gpt-5-pro supports only `high` [U].
- **gpt-5.6 blocker.** On Chat Completions, a request with `tools` fails with
  "Function tools with reasoning_effort are not supported for gpt-5.6-sol in
  /v1/chat/completions…". It fails "even when you don't send
  `reasoning_effort`, because these models default to `medium`". The
  workarounds are `reasoning_effort: "none"` or the Responses API [U]. **jas
  sends `tools` on every turn, so every gpt-5.6 deployment fails until jas can
  send `reasoning_effort`.**
- **Unsupported sampling parameters.** Reasoning models other than GPT-6
  Astra reject `temperature`, `top_p` and the penalties [U]. jas sends none
  of them.
- **System messages.** Treated as developer messages on o-series. Don't send
  both a system and a developer message [U]. jas sends one `system` message,
  which is fine.
- **Reasoning text.**
  - `delta.reasoning_content` is "an Azure-specific extension property" in the
    v1 stream delta [B].
  - DeepSeek-style models return `message.reasoning_content` [D].
  - jas already reads `reasoning_content` as a fallback.
  - Azure OpenAI's own models return no raw reasoning on Chat Completions;
    reasoning summaries are Responses-only. Trying to extract raw reasoning
    "may result in throttling or suspension" [U].

## 8. Errors and limits

- **Error envelope.**
  - The legacy spec defines `{"error":{"code","message","param","type",
    "inner_error":{"code":"ResponsibleAIPolicyViolation",…}}}` [F].
  - The v1 spec's default response lists `code`, `message`, `param`, `type`
    and `inner_error` **without** the `error` wrapper [K].
  - Microsoft's prose examples show the `{"error":{…}}` wrapper [D][V].
  - Unconfirmed whether v1 actually wraps. jas matches substrings, so it
    doesn't matter for detection.
- **Prompt blocked by content filter.** HTTP 400 with `"code":
  "content_filter"`. The doc example has `"param": "prompt"` and
  `"status": 400` [V]. The inner code `ResponsibleAIPolicyViolation` means
  "the prompt violated one or more content filter rules" [F].
  - Don't retry the same prompt [D].
  - jas treats 400 as non-retryable, which is correct, but shows a raw
    body.
- **Output blocked.** HTTP 200 with `finish_reason: "content_filter"` [V][T].
  Both the prompt and the generated tokens are billed [T].
- **Filter unavailable.** HTTP 200 with
  `content_filter_results.error.code = "content_filter_error"`. The content
  passes unfiltered [V]. jas can ignore this.
- **429s and headers.**
  - Every response carries `x-ratelimit-limit-requests`,
    `x-ratelimit-limit-tokens`, `x-ratelimit-remaining-requests`,
    `x-ratelimit-remaining-tokens`, `x-ratelimit-reset-requests` and
    `x-ratelimit-reset-tokens` [W].
  - 429 responses include **`retry-after-ms`** ("recommended wait time (in
    milliseconds)") [W].
  - There are three kinds of 429: rate limit exceeded, system capacity
    throttling, and temporary rate-limit adjustment. All three are retried
    with `retry-after-ms` [W].
  - openai-python checks `retry-after-ms` first, then `retry-after` (seconds),
    then an HTTP date [X]. openai-go checks `Retry-After-Ms`, then
    `Retry-After` [Y].
  - Microsoft does not document a standard `Retry-After` header on Azure
    429s. jas reads only `Retry-After`, so today it falls back to
    exponential backoff (1 s, 2 s, 4 s).
- **Context overflow.** No Microsoft Learn or spec page documents the code.
  - The only evidence is a user report on the Azure SDK issue tracker
    (2025-05-08, azure-ai-inference, GPT-4.1). It shows code
    `context_length_exceeded` with "This model's maximum context length is
    128000 tokens…" [Z]. So the substring jas checks for has been seen on
    Azure.
  - **Unconfirmed for v1, and for non-OpenAI deployments** (DeepSeek, Grok,
    MAI), which may return a different message. A live oversized request on
    v1, one per model family, would settle it.
  - Requests rejected for input length (HTTP 400) still count toward rate
    limits [W].

## 9. What jas would need

Ordered by file. Nothing here changes code yet.

### `settings/auth.go`

- Add `ProviderFoundry = "foundry"`.
- `KeyRequired` should return true for `foundry` too.

### `settings/config.go`

- Add `foundry` to the `provider` `oneOf`.
- Consider a new key, `azure_resource`, validated as a DNS label. `base_url`
  can then be derived when it is unset. Keep `base_url` as the override for
  `services.ai.azure.com` and project routes.
- Add a per-model `models.<name>.reasoning_effort`, validated as
  `none|minimal|low|medium|high|xhigh` and empty by default. It sits next to
  the existing `models.<name>.context_window` pattern in `File.Set`.

### `settings/run.go`

- `Build()`:
  - When `provider=foundry` and `base_url` is empty, build
    `https://<azure_resource>.openai.azure.com/openai/v1`.
  - If neither `base_url` nor `azure_resource` is set, error with a clear
    message.
  - Don't put a static entry in `Presets`, because the host varies per
    user.
- Reject a `base_url` that contains `/deployments/` or a query string, and
  say "use the v1 endpoint …/openai/v1". The legacy shape needs
  `api-version` and the `api-key` header, and `send()` would produce
  `…?api-version=x/chat/completions`.

### `providers/openai.go`

- **Headers.** Keep `Authorization: Bearer` (documented on v1). Optional: add
  an `AuthHeader` field (`"bearer"` or `"api-key"`) so `foundry` can send
  `api-key`. Leave `api-version` out entirely.
- **Retry.** `parseRetryAfter` should check `retry-after-ms` (integer or
  float milliseconds) before `Retry-After`, matching both SDKs [X][Y]. Keep
  the 60 s `maxRetryWait` cap. Optionally log `x-ratelimit-remaining-tokens`
  at debug level.
- **Context length.** Keep the `"context_length_exceeded"` substring check,
  then confirm it with a live fixture. If non-OpenAI deployments differ, add
  the observed string as a second matcher. Don't widen the check to all 400s.
- **Content filter, before the stream starts.** Add
  `ErrContentFiltered`. On a 400 whose body contains `"content_filter"` or
  `ResponsibleAIPolicyViolation`, return it wrapped with the redacted body.
  It must not be retried.
- **Content filter, mid-stream.** In `run()`, when a `FinishReason ==
  "content_filter"` event is seen, return `ErrContentFiltered` after
  forwarding events. This stops core from committing a partial assistant
  message or truncated tool calls. A narrower alternative is to make core
  treat it as an error.
- **Errors inside chunks.** Add `Error *struct{Code, Message string}
  \`json:"error"\`` to `oaiChunk`. When it is set, return an error (or
  `ErrContentFiltered` when `code == "content_filter"`) instead of
  `ErrStreamTruncated`. This depends on the Unconfirmed in-stream shape in
  section 5.
- **Reasoning effort.** Add `ReasoningEffort string
  \`json:"reasoning_effort,omitempty"\`` to `oaiRequest`, fed from a new
  `Request.ReasoningEffort`. jas needs this before any gpt-5.6 deployment
  works with tools.
- **No change** for `max_tokens`/`max_completion_tokens` (jas sends neither),
  `stream_options.include_usage`, tool encoding, or `reasoning_content`
  parsing.

### `providers/sse.go`

- No change. `[DONE]`, comment lines and blank lines are already handled, and
  empty `choices` is a parse-level concern that already works.

### `providers/accumulate.go`

- No change, provided `openai.go` errors out on `content_filter` before
  `Done()` runs.

### `providers/types.go`

- Add `Request.ReasoningEffort string`.
- Optionally add `ErrContentFiltered` here if it should be provider-neutral.

### `core/` (session.go, session_compact.go)

- Handle `ErrContentFiltered` without auto-compacting or retrying. Surface it
  as a distinct stop reason, for example in the exit-code contract.
- Unchanged: the `ErrContextLength` → compaction path.

### `cmd/jas`

- **`auth_cmd.go`.** Accept `foundry` in the provider switch and usage text.
- **`config_cmd.go` (`configInit`).** Add `foundry` to the provider prompt.
  When it is chosen, prompt in turn for:
  - The resource name. Accept a full `https://…/openai/v1` URL as an
    alternative.
  - The key, through the existing hidden or `$ENV` flow.
  - The deployment name, saved as `model`.
  - Its context window, saved as `models.<dep>.context_window`, since the
    OpenRouter catalog can't resolve user-chosen deployment names.
  - Optionally, `reasoning_effort`.
- **`chat.go` (`Models()`).** For `foundry`, don't offer the OpenRouter
  catalog. Configured models and overrides only.
- **`setup.go`.** Pass the auth-header style and the reasoning effort into
  `providers.OpenAI`.

### Not needed for a first cut

- Entra ID token acquisition and refresh.
- Responses API support. This is the long-term fix for reasoning with tools
  on gpt-5.6.
- Content-filter annotation display.
- Legacy `/deployments` and `/models` routes.

## 10. Test plan

**Offline tests.** Run in CI; hand-written fixtures under
`providers/testdata/`.

- **`azure_prompt_annotations.sse`.** An empty-`choices` first chunk with
  `prompt_filter_results`, then text deltas, then a `stop` chunk with
  `content_filter_results`, then a usage chunk with empty `choices`, then
  `[DONE]`. Assert that the text and usage are correct and that no spurious
  events appear.
- **`azure_async_filter_after_stop.sse`.** Delta-less annotation chunks
  before and after `finish_reason: "stop"`. Copy the sample from [T].
- **`azure_content_filter_midstream.sse`.** The blocked sample from [T].
  Assert `ErrContentFiltered` and that no tool calls are returned.
- **`azure_tool_calls.sse`.** Two parallel calls with split arguments. This
  mirrors `two_parallel_calls.sse` with an Azure prompt-annotation preamble.
- **HTTP-level tests** with `httptest`:
  - A 429 with `retry-after-ms: 1500` and no `Retry-After` should wait
    1.5 s, using the injected `Sleep`.
  - A 400 with the content-filter body [V] should return
    `ErrContentFiltered` and make no retry.
  - A 400 with a `context_length_exceeded` body should return
    `ErrContextLength`.
  - The request must carry `Authorization: Bearer` (or `api-key` when that
    is configured), with no `api-version` query and the path
    `/openai/v1/chat/completions`.
- **Settings tests.**
  - `provider=foundry` plus `azure_resource=foo` should derive
    `https://foo.openai.azure.com/openai/v1`.
  - A `base_url` containing `/deployments/` or `?api-version` should be
    rejected.
  - `KeyRequired("foundry")` should be true.
  - Onboarding should write the deployment's `context_window`.

**Recorded fixtures.** These need the user's own Azure resource and key.

- **Repo convention.** OpenRouter live tests use only `z-ai/glm-*` or
  `deepseek/*`. That rule doesn't apply here: a Foundry fixture can only be
  recorded against the user's own resource, deployment and key.
- **Build tag and variables.** Add a `//go:build live` test next to
  `providers/live_test.go`. It skips unless `AZURE_FOUNDRY_API_KEY`,
  `AZURE_FOUNDRY_BASE_URL` and `AZURE_FOUNDRY_DEPLOYMENT` are all set.
- **Calls to record and redact:**
  1. A plain text stream with `include_usage`. This captures the real
     first-chunk shape and checks that usage arrives.
  2. The two-step tool loop from `TestLive_GLMMultiTurnToolsWithoutReasoning`.
  3. An oversized prompt, to get the exact context-overflow status and
     body. Run it on an OpenAI deployment and on a DeepSeek deployment.
  4. A prompt that trips the default filter, to get the exact 400 body.
  5. If a gpt-5.6 deployment exists, a request with `tools` and no
     `reasoning_effort`, then one with `reasoning_effort: "none"`.
- **Redaction.** Strip keys and `apim-request-id` before committing the
  fixtures, and save them as `providers/testdata/foundry_*.sse`.

## 11. Open questions for a live call

1. **Overflow error.** What exact status, code and message does v1 return
   for context overflow, on OpenAI deployments and on DeepSeek, Grok and MAI
   deployments?
2. **Mid-stream errors.** What is the wire shape of a mid-stream guardrail
   error in default filter mode: an SSE `data: {"error":…}` line, or a
   dropped connection?
3. **Error envelope.** Does v1 wrap errors in `{"error":{…}}` (as the prose
   shows) or return them flat (as the spec shows)?
4. **Retry header.** Do 429s on v1 also send a standard `Retry-After`
   header, or only `retry-after-ms`?
5. **Project route.** Does the project route
   (`/api/projects/<p>/openai/v1`) accept a resource key, or only Entra?
6. **Partner models.** Are partner models (Mistral, Llama) reachable on
   `/openai/v1/chat/completions`? How do "Tool calling: No" deployments
   respond when `tools` is present?
7. **Late filter verdict.** In Asynchronous Filter mode, can a
   `content_filter` verdict arrive after a `stop` chunk?
8. **Entra scope.** Is the `https://cognitiveservices.azure.com/.default`
   scope still accepted on v1? This matters only if Entra is added later.

## Sources

- [A] Azure OpenAI in Microsoft Foundry Models v1 API, ms.date 2026-05-13 —
  https://learn.microsoft.com/en-us/azure/foundry/openai/api-version-lifecycle
- [B] Microsoft Foundry REST Reference, Azure OpenAI Chat (API version v1),
  ms.date 2026-05-27 —
  https://learn.microsoft.com/en-us/rest/api/microsoft-foundry/azureopenai/chat
- [C] Endpoints for Microsoft Foundry Models, ms.date 2026-07-31 —
  https://learn.microsoft.com/en-us/azure/foundry/foundry-models/concepts/endpoints
- [D] Microsoft Foundry reasoning models, ms.date 2026-08-26 —
  https://learn.microsoft.com/en-us/azure/foundry/foundry-models/how-to/use-chat-reasoning
- [E] openai-python `AzureOpenAI` requires `api_version` (see [L]).
- [F] Azure REST API specs, legacy inference 2024-10-21 (stable) —
  https://github.com/Azure/azure-rest-api-specs/blob/main/specification/cognitiveservices/data-plane/OpenAIInference/stable/2024-10-21/inference.json
- [G] Supported programming languages (classic), retirement notice,
  ms.date 2025-11-21, updated 2026-10-19 —
  https://learn.microsoft.com/en-us/azure/foundry-classic/foundry-models/supported-languages
- [H] azure-ai-inference README (REST API version `2024-05-01-preview`),
  commit 2025-03-01 —
  https://github.com/Azure/azure-sdk-for-python/blob/main/sdk/ai/azure-ai-inference/README.md
- [I] Foundry Models from partners and community, ms.date 2026-09-21 —
  https://learn.microsoft.com/en-us/azure/foundry/foundry-models/concepts/models-from-partners
- [J] see [R] (Anthropic SDK endpoint row).
- [K] Azure REST API specs, v1 OpenAPI (`azure-v1-v1-generated.json`), last
  commit 2026-06-01 —
  https://github.com/Azure/azure-rest-api-specs/blob/main/specification/ai/data-plane/OpenAI.v1/azure-v1-v1-generated.json
- [L] openai-python `src/openai/lib/azure.py`, main @ 2026-10-31 —
  https://github.com/openai/openai-python/blob/main/src/openai/lib/azure.py
- [M] openai-go `azure/azure.go`, last commit 2026-10-01 —
  https://github.com/openai/openai-go/blob/main/azure/azure.go
- [N] openai-python `src/openai/lib/_client_auth.py`, main @ 2026-10-31 —
  https://github.com/openai/openai-python/blob/main/src/openai/lib/_client_auth.py
- [O] Migrate from Azure AI Inference SDK to OpenAI SDK, ms.date 2026-06-04 —
  https://learn.microsoft.com/en-us/azure/foundry/how-to/model-inference-to-openai-migration
- [P] Access tokens in the Microsoft identity platform, ms.date 2025-05-14 —
  https://learn.microsoft.com/en-us/entra/identity-platform/access-tokens
- [Q] Work with chat completion models, ms.date 2026-08-24 —
  https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/chatgpt
- [R] Get started with Microsoft Foundry SDKs and endpoints, ms.date
  2026-10-06 —
  https://learn.microsoft.com/en-us/azure/foundry/how-to/develop/sdk-overview
- [S] OpenAI OpenAPI spec (`CreateChatCompletionStreamResponse`), v2.3.0,
  commit 2026-10-31 — https://github.com/openai/openai-openapi
- [T] Content Streaming in Azure OpenAI, ms.date 2026-09-08 —
  https://learn.microsoft.com/en-us/azure/foundry/openai/concepts/content-streaming
- [U] Azure OpenAI reasoning models, ms.date 2026-10-23 —
  https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/reasoning
- [V] Content filtering for Microsoft Foundry Models (classic), scenarios,
  ms.date 2026-08-04 —
  https://learn.microsoft.com/en-us/azure/foundry-classic/foundry-models/concepts/content-filter
- [W] Manage Azure OpenAI quota (rate-limit headers, 429 types), ms.date
  2026-05-04 —
  https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/quota
- [X] openai-python `_base_client.py` `_parse_retry_after_header`, main @
  2026-10-31 —
  https://github.com/openai/openai-python/blob/main/src/openai/_base_client.py
- [Y] openai-go `internal/requestconfig/requestconfig.go` (retry headers) —
  https://github.com/openai/openai-go/blob/main/internal/requestconfig/requestconfig.go
- [Z] Azure/azure-sdk-for-python issue #40986 (user report, 2025-05-08; not
  a spec) — https://github.com/Azure/azure-sdk-for-python/issues/40986
