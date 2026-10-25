# Reviewed stock posting after cooking — RE12

Next approved work contract. This document is planning evidence only; no RE12 workflow or schema is implemented by its existence. RE07/RE11 acceptance is tracked separately. Preserve PRODUCT.md and DESIGN.md; extend the existing cooking-history and pantry review surfaces in Operate mode.

## User outcome

Finishing a cooking session continues to record actual servings and history without consuming stock. The completed cooking record offers a separate explicit action to review pantry consumption. Read its immutable `actual_servings` and `source_version_id`, including when the live original recipe has been deleted. A freely entered serving count or the current live recipe must not replace that source.

Show the source recipe/version, actual servings and each ingredient's rational suggested amount. Require one decision per ingredient: consume or explicitly skip. Unknown quantities, incompatible units and positive amounts below journal precision remain explained; a confirmed actual quantity can differ from the recipe suggestion, with a bounded explanation. A one-time manual product choice does not alter catalogue bindings. Several ingredients may use one product and several lots; aggregate allocations per lot before checking available stock.

No automatic reservations, automatic FEFO booking or silent rounding. The user chooses and reviews lots, actual amounts, before/after balances and any deviations. Cancelling/back navigation creates no final record. Completing all decisions without stock requires a separately named final confirmation; it must not occur as a side effect of closing an empty form.

## Durable module contract

- Add a minimal immutable `core.cooking_stock_postings`: UUID, household, positive Recipes generation, cooking-record UUID, used Pantry generation, posting actor/time and normalized request hash. Unique `(household_id, recipes_generation, cooking_record_id)` excludes both Pantry generation and actor. Different actors, new request keys and a later Pantry generation cannot book the same source again.
- Runtime SELECT/INSERT only; explicit same-household identities, secure invoker guards and redacted audit policy. Core does not reference optional schema tables through FKs and does not retain recipe notes, preparation text or product names.
- Pantry owns the reviewed ingredient/allocation details and historical stock receipt, with same-household local journal/lot FKs and a permitted reference to the Core posting. Its bounded snapshot records recipe suggestion, explicit actual amount/skip decision, product/lot labels, source provenance and movement identities.
- Both Recipes and Pantry purge retain the minimal Core proof. Pantry purge removes local receipt/journal details; Recipes purge removes the cooking record. Neither operation makes the source newly bookable. Generic idempotency receipts/resource links cannot carry this guarantee because purge and expiry remove them.
- Disabled/purged targets must not be disclosed through historical response replay. A retained minimal proof can explain that posting already happened even when its optional details are unavailable; no old movement can be booked or reversed into a new Pantry generation.

## Transaction boundaries

1. Use the authenticated Recipes mutation boundary: current write permission/session, household lock and source generation. Take a transaction advisory lock based on household/source generation/cooking-record UUID, independent of actor; retain the unique database constraint as the final guarantee.
2. Read the durable proof before considering any new posting. An identical authorized retry returns the original completion; changed reviewed values conflict. Module/generation authorization governs what response details remain visible. Never create effects while replaying.
3. For a new posting, require current active Pantry, validate the immutable household cooking record and completed state, and read the captured recipe source. Do not acquire UPDATE locks on runtime read-only cooking records.
4. Bound source/decision/allocation counts before locking. Validate explicit catalogue-based selections against current identity/version; one-time manual selections are separately identified. Use bounded NOWAIT parent locks consistent with catalogue/binding/product deletion paths.
5. Sort unique lot UUIDs; lock lots for UPDATE and their products/locations for SHARE with NOWAIT. Read projection balances and movement counts in a separate statement after locks are held. Expected lot/product/location versions alone are insufficient for concurrent journal changes.
6. Validate every decision and aggregate all allocations per lot before writing anything. Actual and aggregated amounts must be exactly bookable at six decimal places, within numeric(18,6), integer when required and within current stock. Future reservation work must extend all consumption paths consistently.
7. Write one negative `consume` journal movement per aggregated lot with shared correlation identity, Pantry details/stock operations as appropriate, the Core posting, audits, projections and any short-lived request receipt in one transaction. Reuse `afterWrite`; never write projections directly or call several separately committing `MutatePantryStock` operations.
8. Journal time is the current server time; cooking date remains separate provenance. Do not backdate physical stock history to `cooked_on`.

Initial resource limits: at most500 source ingredient decisions and500 total allocations, existing1MiB request maximum, bounded reason/snapshot text and explicit transaction deadlines. Duplicated source decisions/allocation identities are rejected. Product and lot selection remain paginated; a small FEFO preview never stands in for complete physical stock.

## Corrections and recovery

Use exact journal counter-movements linked through `reverses_movement_id`. Existing individual reversals can make a cooking posting partially or wholly reversed; derive that state from the journal rather than falsely presenting a permanently completed total. Full reversal does not reopen the original cooking record for another automatic posting. Any future corrected reposting requires its own explicitly linked contract.

A Pantry movement can still be reversed after Recipes purge because its reviewed receipt owns the necessary history. Pantry purge makes old movement details unavailable; it cannot authorize a reversal in the new generation. The permanent Core key survives backup/restore and generic receipt pruning.

## Required evidence

- Immutable actual servings/source version; live recipe deletion does not prevent valid review.
- Two actors/new keys race one cooking record: one atomic posting and one movement bundle.
- Reused lots across ingredients, several lots/products, exact unit conversion, unknown quantities, thirds, sub-micro values and integer quantities.
- Concurrent quick consumption, generic journal insert and transfer: correct fresh review or full conflict, with no partial effects.
- Invalid final allocation rolls back every prior movement, projection, receipt and durable proof.
- Replay after generic receipt pruning and actual source/target purge cannot create stock effects again.
- Partial/full individual reversal, duplicate/concurrent reversal and old-generation reversal denial.
- Reader/anonymous/CSRF/foreign-household boundaries, immutable grants, disabled-module response redaction and scoped audit.
- Populated upgrade, encrypted restore preserving proof/amounts, native integration, Linux race, static security and browser/accessibility acceptance.
- Independent database review and Impeccable finish/documentation; update README, schema, checklist and implementation ledger only with executed results.

Useful existing patterns: `CookingRecord` reads completed history without a live recipe; `ingredientDemand` retains exact rational suggestions; `pantryExactAmount` validates actual bookable quantities; `lockPantryLot` demonstrates fresh post-lock projection reads; shopping checkout demonstrates one atomic multi-row completion; current real-purge fixtures exercise maintenance cleanup. `RecipePantryComparison` is a read model, not a ready posting source because it assumes a live recipe and freely chosen servings.
