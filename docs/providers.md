# Providers

jas talks to models through one adapter: any endpoint that speaks the
OpenAI **Chat Completions** API with streaming (`POST <base_url>/chat/completions`,
server-sent events, OpenAI-style `tools`). Anything that serves that API can
be used, whether it is listed here or not.

## How a provider is chosen

Three settings decide where requests go. Each one takes a flag, a `JAS_*`
env var or a `jas config set` value (see the README's Configuration section).

| Setting | What it does |
|---|---|
| `provider` | `openrouter`, `ollama` or `custom` (the default when unset). Picks the preset `base_url` and the name the API key is stored under. |
| `base_url` | The API root; jas appends `/chat/completions`. Overrides the preset. |
| `model` | Sent as-is in the request's `model` field. |

The key comes from `JAS_API_KEY`, or from `jas auth add <provider>` (stored
in `~/.jas/auth.json`, or as a `$ENV_NAME` reference with `--env`). jas sends
it as `Authorization: Bearer <key>`. Only `openrouter` requires a key; the
others run without one when none is set.

jas sizes its context from a built-in catalog of model ids. A model it does
not know gets 128000 tokens and a warning; set the real window with
`jas config set models.<model>.context_window <tokens>`.

## Support at a glance

**Tested** means the repo has recorded streaming fixtures or live tests for
it. **Should work** means the endpoint is OpenAI-compatible but nothing in the
repo checks it yet.

| Provider | `provider` | `base_url` | Key | Status |
|---|---|---|---|---|
| OpenRouter | `openrouter` | preset `https://openrouter.ai/api/v1` | required | Tested (GLM and DeepSeek fixtures, live tests) |
| ollama | `ollama` | preset `http://localhost:11434/v1` | none | Tested (qwen2.5:0.5b fixture) |
| vLLM | `custom` | `http://localhost:8000/v1` | optional | Should work |
| LM Studio | `custom` | `http://localhost:1234/v1` | none | Should work |
| llama.cpp server | `custom` | `http://localhost:8080/v1` | optional | Should work |
| OpenAI | `custom` | `https://api.openai.com/v1` | required | Should work |
| Microsoft Foundry / Azure OpenAI | `custom` | `https://<resource>.openai.azure.com/openai/v1` | required | Should work, with known gaps (below) |
| Anthropic API (native) | — | — | — | Not supported; use Claude through OpenRouter |

The local servers' URLs are their usual defaults; use whatever host and port
yours listens on.

jas sends `max_tokens` on every request (`max_output_tokens`, default 16000),
lowered to the room left in the model's context window when the window is
known. Endpoints that reject `max_tokens` (OpenAI o-series and gpt-5 on Chat
Completions) need `jas config set max_output_tokens 0`, which omits it.

The fixtures live in `providers/testdata/`. There is no Claude-via-OpenRouter
fixture: the key used to record them only reaches GLM and DeepSeek models
(others return 403), so DeepSeek stands in as the second OpenRouter upstream.

## Recipes

### OpenRouter

```sh
jas config set provider openrouter
jas auth add openrouter                 # or: --env OPENROUTER_API_KEY
jas config set model z-ai/glm-5.3
```

Claude, GPT, Gemini and other hosted models are reachable here by their
OpenRouter ids.

### ollama

```sh
jas config set provider ollama
jas config set model qwen2.5-coder
```

The model must support tool calling. Set its context window if jas warns
that it is unknown; ollama's own default context is often smaller than the
model's maximum.

### vLLM, LM Studio, llama.cpp and other local servers

Every endpoint gets a name of its own — there is no special `custom`
label. Set a base URL under `providers.<name>.base_url`, store a key
under the same name, and address its models as `<name>/<model>`:

```sh
jas config set providers.vllm.base_url http://localhost:8000/v1
jas config set model vllm/<model id the server reports>
jas config set models.<model id>.context_window 32768
```

If the server wants a key, store it with `jas auth add vllm`. Tool calling
must be enabled on the server (vLLM, for example, needs its tool-call parser
flags).

With several providers configured, `--model openrouter/z-ai/glm-5.3` and
`--model vllm/qwen3-coder` mix freely, and `/model` lists them qualified;
**Ctrl+S** in the picker also stores the choice as the config default.
Switching a live chat to a model on *another* provider needs `/new`: the
provider and its key are wired at session start.

The text before the first `/` picks the provider when it names one, so
avoid naming a provider after an OpenRouter vendor (`qwen`, `deepseek`, …):
with OpenRouter as the default, `qwen/qwen3-coder` would then go to
`providers.qwen`, not OpenRouter. jas warns on stderr when that happens;
write `openrouter/qwen/qwen3-coder` to stay on OpenRouter.

### OpenAI

```sh
jas config set providers.openai.base_url https://api.openai.com/v1
jas auth add openai                     # or: --env OPENAI_API_KEY
jas config set model openai/<model id>
```

The older flat form still works and stays the default provider (reserved
name `default`; `custom` remains an ordinary alias for it):

```sh
jas config set provider custom
jas config set base_url https://api.openai.com/v1
jas config set model <model id>
```

### Microsoft Foundry / Azure OpenAI

Use the Azure OpenAI **v1** endpoint. It needs no `api-version`, and it
accepts the resource key as a Bearer token, which is what jas sends.

```sh
jas config set provider custom
jas config set base_url https://<resource>.openai.azure.com/openai/v1
jas auth add custom                     # the resource's key
jas config set model <deployment name>
jas config set models.<deployment name>.context_window <tokens>
```

- `model` is your **deployment name**, not the model's name. Deployment
  names are your own, so jas cannot look up the window; set it.
- `https://<resource>.services.ai.azure.com/openai/v1` is the same route.
  OpenAI models and the other models Azure sells on this route (DeepSeek,
  Grok, MAI) all work the same way.
- The older `/openai/deployments/<name>/chat/completions?api-version=...`
  URLs do not work: jas cannot add a query string, and those URLs want an
  `api-key` header.
- Claude in Foundry uses Anthropic's Messages API (`/anthropic`), so it does
  not work.

Known gaps, until jas has a dedicated `foundry` provider:

- **gpt-5.6 deployments need `reasoning_effort: off`.** On Chat Completions
  they reject requests that include tools unless `reasoning_effort` is
  `"none"`, and jas always sends tools. Set `reasoning_effort` to `off`
  (jas sends `"none"` to non-OpenRouter endpoints); not yet tried live.
- **Filtered answers look finished.** When Azure's content filter cuts a
  reply short, jas treats the partial reply as complete.
- **Rate-limit waits.** Azure sends its wait hint in `retry-after-ms`; jas
  reads only `Retry-After` and falls back to its own 1/2/4 s backoff.
- **Untested.** No fixture or live test covers Foundry yet.

The full findings, with sources and open questions, are in
[research/microsoft-foundry-provider.md](research/microsoft-foundry-provider.md).

## What makes an endpoint work

An endpoint works with jas if it:

- serves `POST /chat/completions` under `base_url`, with no required query
  parameters;
- accepts `Authorization: Bearer <key>`, or no key at all;
- streams with `stream: true` as OpenAI-style server-sent events, ending in
  `data: [DONE]`;
- supports `tools` and returns tool calls as streamed `tool_calls` deltas.

If any of these is missing, the provider needs its own adapter.
