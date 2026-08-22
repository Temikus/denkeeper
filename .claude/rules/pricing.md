---
name: pricing
description: Cost tracking, pricing registry, model price config, lookup priority.
paths:
  - internal/llm/**
---

## Cost Tracking & Pricing

`internal/llm/pricing/` — registry with bundled defaults for ~70 models. `TokenCost(resp, reg)` returns `(cost, source)`; source becomes the `pricing_source` OTel attribute. Unknown models log a warning. `TokenUsage.CachedPrompt` from Anthropic `cache_read_input_tokens` / OpenAI `prompt_tokens_details.cached_tokens`.

Config: `[costs] default_rate_per_1k_tokens` (fallback when model unknown; 0 = $0 + warn); `[costs.model_prices.<model>]` with `input`/`output`/`cached_input` in $ per million tokens (`cached_input` 0 = same as input).


## Invariants

- **Pricing lookup priority**: provider-reported > registry exact > registry longest-prefix > `[costs]` fallback > $0 (with warning).
