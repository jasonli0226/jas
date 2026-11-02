# Multi-provider models — design

Date: 2026-11-02 (Asia/Tokyo). Status: **draft, owner-approved direction.**

jas today has one active provider: flat `provider` + `base_url` + `model`
config keys (`docs/providers.md`), so trying a second endpoint means
rewriting global config. pi addresses models as `provider/model` with every
configured provider's models in the `/model` selector
([research/pi-interactive.md](../../research/pi-interactive.md)). jas adopts
that, and drops the special `"custom"` pseudo-provider: **every provider
gets a name.**

## Goals

- Configure any number of named providers; select per model as
  `provider/model` (flag, `JAS_MODEL`, `jas config set model`, `/model`).
- Named local/self-hosted endpoints instead of `"custom"`:
  `vllm/qwen3-coder`, `lmstudio/…`, `foundry/gpt-5.6`.
- No change to the wire layer: providers are still endpoints that speak
  Chat Completions; only `base_url`, key name and model id differ.

## Config

New optional `providers` map in `~/.jas/config.json`:

```jsonc
{
  "providers": {
    "vllm":    { "base_url": "http://localhost:8000/v1" },
    "lmstudio": { "base_url": "http://localhost:1234/v1" },
    "foundry": { "base_url": "https://<resource>.openai.azure.com/openai/v1" }
  },
  "model": "openrouter/z-ai/glm-5.3"   // may be qualified or bare
}
```

- `jas config set providers.vllm.base_url http://localhost:8000/v1` — the
  `models.<name>.context_window` pattern key already shows the shape.
- Provider presets stay built in: `openrouter` and `ollama` get their
  preset `base_url` without configuration; the `provider`/`base_url` flat
  keys keep working as the default provider, spelled as the reserved name
  `default` internally. `jas config get` prints the resolved view.
- Reserved names: `default`. A provider named like a model id segment is
  the user's problem; first `/` splits.

## Model ids and resolution

`resolveModel(ref, cfg)` → `(providerName, baseURL, keyName, modelID)`:

1. `ref` contains `/` and the segment before the first `/` names a
   configured provider (built-in preset or `providers.` entry): that
   provider is used; the rest after the first `/` is the model id. So
   `openrouter/z-ai/glm-5.3` → provider `openrouter`, model
   `z-ai/glm-5.3` (OpenRouter's own slash ids compose fine).
2. Otherwise the whole `ref` is a model id on the current default
   provider — bare `glm-5.3` keeps working.
3. Key resolution is unchanged and already per provider name:
   `jas auth add <name>` entry → `$ENV_NAME` → none. Unknown window still
   warns with the same `models.<model>.context_window` fix, keyed by the
   bare model id (provider prefixes are stripped, as `settings.ContextWindow`
   already does).

## CLI and interactive surface

- `jas --model openai/gpt-5.6`, `jas config set model …` — resolve at
  session start.
- `/model <ref>` resolves immediately; bare `/model` opens the picker with
  models **grouped by provider** (provider name as the section label, every
  configured provider that has keys or a base_url listed). Picking returns
  the qualified id, so the status line shows where requests go.
- `/model vllm<tab>` completes provider names first.

## Live switching across providers

`Session.SetModel` stores a string; the provider is wired at creation.
Two stages:

- **Stage 1 (this spec):** a switch to a model on the *same* provider works
  as today. A switch to a *different* provider mid-session is refused with
  "`<provider> needs a new session — /new first`" (same posture as
  `context_full`). Nothing is half-migrated.
- **Stage 2 (follow-up):** `model_changed` carries the resolved
  `(base_url, key_name)` and the session rebuilds its provider at the turn
  boundary. Touches `core.Session` + `Live`; the event-log seam already
  exists. Build it only if mid-session cross-provider switching turns out
  to matter.

## Acceptance

- [ ] Two named providers configured; `--model vllm/qwen3-coder` and
      `--model openrouter/z-ai/glm-5.3` both work in one config, no flags.
- [ ] `/model` picker groups by provider; picking routes requests to the
      right `base_url` with the right key (fake-provider test).
- [ ] Bare model ids and the flat `provider`/`base_url` keys keep working
      (existing tests stay green).
- [ ] Cross-provider mid-session switch refuses loudly; same-provider
      switch still works (`model_changed` logged).
- [ ] Docs: `docs/providers.md` rewritten around named providers;
      `"custom"` documented as a legacy alias for the default provider.
