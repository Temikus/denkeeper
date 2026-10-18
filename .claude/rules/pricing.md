---
name: pricing
description: Cost tracking, pricing registry, model price config, lookup priority.
paths:
  - internal/llm/**
---

## Cost Tracking & Pricing

`internal/llm/pricing/` — registry with bundled defaults for ~70 models. `TokenCost(resp, reg)` returns `(cost, source)`; source becomes the `pricing_source` OTel attribute. Unknown models log a warning. `TokenUsage.CachedPrompt` from Anthropic `cache_read_input_tokens` / OpenAI `prompt_tokens_details.cached_tokens`. Anthropic's `input_tokens` excludes both cache reads and writes: `Prompt` = input + `cache_creation_input_tokens`, and `CacheWritePrompt` is that write share (a **subset** of `Prompt`, so `computeCost` adds only the 0.25x `cacheWriteSurcharge`; `CachedPrompt` is separate from `Prompt`).

Config: `[costs] default_rate_per_1k_tokens` (fallback when model unknown; 0 = $0 + warn); `[costs.model_prices.<model>]` with `input`/`output`/`cached_input` in $ per million tokens (`cached_input` 0 = same as input).


## Invariants

- **Anthropic caching is requested only on tool-bearing requests** (top-level `cache_control`, automatic breakpoint, 5m TTL): a write costs 1.25x and pays off only when the prefix is resent within 5 minutes. The router attaches tools to every non-final call, so a reply with no tool call pays the premium for nothing; break-even on Sonnet 5.5 is roughly 1 in 4 writes being read. Tool-less calls (`CompleteFinal` wrap-up and compaction, supervisor review, eval judge) stay unmarked.
- **Pricing lookup priority**: provider-reported > registry exact > registry longest-prefix > `[costs]` fallback > $0 (with warning).
