# ADR 0002: Billing-expression pricing overrides legacy ratios in the NewAPI decoder

Status: accepted

## Decision

When a NewAPI `/api/pricing` model entry carries a `billing_expr` (and its quota type is not per-request fixed), the decoder (`internal/pricing/newapi.go` + `internal/pricing/expr.go`) derives the displayed price from the billing expression alone:

- `pickTierBody` selects the tier representing the standard price: the `tier("base", …)` call when present, else `tier("standard", …)`, else the first `tier(...)` call; a bare expression without a `tier()` wrapper is used verbatim. Conditional wrappers (`len <= N ? … : …`, `hour(...)`, `param(...)`) around the tier calls are ignored — runtime-conditional pricing cannot be a single number, and the standard tier is what the board's per-token price should reflect.
- `parseLinearExpression` reduces the tier body to linear coefficients `{p, c, cr, cc, …}` in the expression's native "USD per 1M tokens" semantics, supporting `+ - * /`, parentheses, and constant factors such as `(p * 4 + c * 20) * 0.5`. `fixed(N)` bodies become per-request fixed prices.
- `ModelPrice` persists `expressionBilling: true` and, when reduction succeeds, `exprCoefficients`. `displayPrice` builds the quote from coefficients × group multiplier × exchange rate and never consults `model_ratio`/`completion_ratio`/`quota_per_unit` for such models.
- If the expression cannot be reduced (non-linear terms, unknown functions, variable products), the model gets `expressionBilling: true` with no coefficients and **no group price is offered**. The legacy ratio fields are never used as a fallback for expression-billing models.
- `quota_type: 1` (per-request fixed) takes precedence: `billing_expr` is ignored for per-call models even when both are present, matching the NewAPI contract where `quota_type` is the primary billing-type flag. Sites do ship this contradictory combination (hkai's `step-5-preview` declares per-call `model_price: 0.02` alongside a length-tiered expression); changing precedence there is a deliberate future decision, not a decoder fix.

## Rationale

NewAPI supports two billing models per site: legacy ratio fields and tiered-expression billing (`billing_mode: "tiered_expr"`). Sites using expressions routinely leave `model_ratio` at the platform fallback (observed: 37.5) because billing never reads it. Deriving input/output from ratios then produced prices that diverged from the source site by 12×–250× (e.g. dudu公益站 and HappyCoding's `deepseek-v4.1-flash` displayed $75/M against a real $2/M and $0.3/M). A 2026-09-26 sweep found 77 affected models across 13 sites. The prior decoder only regex-extracted `cr`/`cc` coefficients from the expression for cache prices, which is why cache prices looked right while token prices were wildly wrong, and why the bug escaped the existing test: its fixture coincidentally aligned `model_ratio`/`completion_ratio` with the expression coefficients.

For expression models, no price is strictly better than a wrong price: cards render fine without a quote, while a fabricated number misleads users comparing sites.

## Alternatives considered

- Regex extraction of `p`/`c` coefficients (extending the previous `cr`/`cc` approach): cheapest to ship, but it misprices real payloads with constant factors or nested parentheses, e.g. PigeonW's `(p * 4 + c * 20 + cr * 0.4) * 0.5` would show $4/M instead of $2/M. Rejected.
- Full expression evaluator honoring `hour()`, `param("stream")`, and `len` conditions: perfect fidelity is impossible in a single per-1M number anyway, and the parser would grow a condition/AST layer for shapes the board cannot display. Rejected; the standard tier covers all 77 observed models.
- Fall back to legacy ratios when the expression is unparseable: keeps some price visible, but on expression-billing sites those fields are unconfigured fallbacks, so the visible number is guaranteed wrong. Rejected.

## Revisit when

Sites begin using non-linear or runtime-conditional shapes *inside* their base tier body (then a real evaluator or a "price range" display is needed), or NewAPI replaces `billing_expr` strings with a structured pricing API the decoder can consume directly.
