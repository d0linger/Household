# Pantry minimum, target and pack review — PA11

Status: PA11 locally verified. The complete corrected browser chain passes584 checks, including32 replenishment checks/six clean axe scans. Full native70.1%, seven final focused contracts, scoped Linux race and synthetic encrypted restore passed at the scopes recorded in pantry-replenishment-verification.md. No live8100 upgrade or hosted CI run is claimed.

## Product behavior

- Preserve the nullable existing minimum. Below that minimum, suggest replenishing toward a separately configured target. At the minimum, do not automatically suggest purchase. Zero and unknown remain distinct.
- A missing target explicitly falls back to the minimum for older products. A configured target requires a known minimum and must be at least that minimum. Both amounts use the product's base unit. Do not infer a pack, brand or unit from a label.
- Show current physical stock, minimum, effective target, exact shortage and the reason for or against a suggestion. Count every physical lot consistently with the stock board, including archived locations; name that scope. This is not a promise of unreserved availability. Meal reservations and differential planning are separate pending contracts.
- Offer an explicit active pack choice for the current product. Convert pack content exactly to the base unit, then round the shortage upward to a whole pack count using integer/rational arithmetic. Show pack count, original content/unit, base purchase quantity and projected surplus separately. No silent decimal rounding, floating-point quantity arithmetic or implied cheapest pack.
- No pack selected means a base-unit suggestion. Disabled, incompatible or foreign-product packs cannot become a new suggestion. Explain quantities outside a single shopping input's range rather than truncating them.
- Purchasing remains an explicit reviewed action through the existing Core-bound Pantry→Shopping adapter. The preview itself writes nothing and works when Shopping is disabled. Preserve destination generation, authorization, CSRF, source version, duplicate-list checks and idempotent response recovery.

## Boundaries and concurrency

Keep configuration in the existing Pantry product and add a new migration without changing historical migration files. Extend product validation and an explicit CHECK constraint for target/minimum consistency; no trigger is needed for a same-row value relationship. Generic product updates retain optimistic versions and audit redaction.

## Direction contract

THESIS: explain one product's physical shortage and whole-pack purchase before confirmation. This extends the Pantry product/stock workflow; it is not a dashboard redesign.

OWN-WORLD: preserve DESIGN.md, existing local sans-serif, green actions, quiet panel borders, rows, native labelled controls and light/dark tokens. No new shipping raster assets.

STORY: read current stock and the minimum trigger, understand the effective target, choose an optional pack, inspect exact quantity/surplus, then explicitly select a Shopping list and confirm.

FIRST VIEWPORT: existing app navigation, product heading and back link, followed by a stock/minimum/target comparison and pack control. Place the calculation beside its review action; mobile stacks the same fields and puts errors/retry beside confirmation.

FORM: narrow Operate extension of the current product surface, code-led under the approved implementation order. No concept seed is applicable to this precise local extension.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

Use one bounded product review, at most50 configured packs and full stock aggregation; do not aggregate only a page of lots. Bind any confirmed pack to its ID/version and include the reviewed stock basis. Lock the source product and read the stock in a new statement after relevant writer locks. Reject stale stock, product or pack basis with an explicit review path; never recompute a changed quantity while silently posting the old review. Exact retry must preserve the originally serialized body and operation key.

## Interface

Extend the existing Pantry identity and rows. Product configuration labels explain minimum versus target and the legacy fallback. A product's “Nachkauf prüfen” destination shows the product/unit, physical-stock basis, threshold explanation and a short pack selector. A compact comparison shows current→after purchase and any surplus. Shopping-list selection is separate from the read-only calculation. Put validation/conflict/retry messages next to their action, preserve selections, move focus to review results and keep the mobile actions visible without horizontal overflow. Readers can inspect calculations; only authorized writers see purchase confirmation.

## Acceptance checklist

- [x] Exact pure arithmetic: threshold boundary, legacy fallback, zero/unknown, wide stock, fractional units and upward pack rounding.
- [x] PostgreSQL configuration checks, direct invalid runtime writes, existing data upgrade, unchanged physical journal and nullable target restoration.
- [x] Real HTTP user/household/module/generation/CSRF boundaries and explicit foreign/disabled/stale-pack rejection.
- [x] Concurrent stock writer and same-balance journal change against a reviewed purchase; stale review produces no shopping effect.
- [x] Shopping destination disabled/recreated, duplicate open item, genuine late-write rollback and exact response-loss retry.
- [x] Mobile/desktop/light/dark browser and accessibility checks, source-version conflicts and preserved form choices.
- [x] Required Go/unit/integration/race/security/workflow checks with honest execution scope.
- [x] Separate scoped interface finish review and documentation comparison; update README and the180-item checklist only after acceptance.
