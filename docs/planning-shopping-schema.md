# MP17 persistence decision record

Status: development migrations and focused PostgreSQL foundation tests exist; complete MP17 acceptance and live deployment remain pending. This record follows the source-only database-pattern reviews. The current PWA still uses MP14's read-only selection preview. A separate Store-only complete active-scope preview and actual checkout transition are implemented; an initial scope-registration/sync API and UI are not yet available. See [executed evidence](planning-shopping-verification.md).

## Current requirements and contributions

One Core row identifies one frozen ingredient within a whole active meal. A separate stored scope header is unnecessary: current active rows, versions and the canonical scope fingerprint define the scope. Every scope registration/end takes the household scope mutex before reading rows, including the empty case.

| Fields | Invariant |
|---|---|
| UUID id, household_id, mealplan_generation, plan_id, meal_id, component_id, ingredient_id | Stable identity; unique household/generation/component/ingredient. No foreign keys into optional module schemas. |
| source_hash, reviewed_basis_hash |32-byte frozen-source fingerprint and last explicitly reviewed basis. Ordinary portion/date/mapping changes require a new current basis; they do not overwrite source identity. |
| version, active, review_reason | Version advances on actual changes. Activation and ending cover all ingredients of the meal. Missing sources remain active and blocked until explicit whole-meal end. Ending remains possible without the source row. |
| binding_id, binding_version, product_id, pantry_generation, unit_code | Confirmed current mapping basis, with complete nullable target group for invalidation. No FK to the independently cleaned-up MP14 binding. |
| shopping_generation, list_id, item_id, item_version, item_fingerprint, pending_quantity | Current managed contribution Q only. One item belongs to at most one current contribution. Expected item version/fingerprint detects ordinary edits and check→uncheck. Zero Q has no required current item or open destination. |
| procurement_disposition | Distinguishes no purchase, managed, stored, no_stock, skipped, missing, detached and unresolved. Activity and procurement disposition are independent. |
| last_sync_operation_id, last_checkout_generation/id/item_id | Sync evidence and historical checkout references are separate. Purchased quantities remain in immutable checkout receipts; they are not current Q. |
| created_by, changed_by, created_at, updated_at | Actor/time evidence through the existing authenticated audit transaction. Runtime SELECT/INSERT/UPDATE only; no DELETE/TRUNCATE/TRIGGER grant. |

All positive Q values must fit numeric(18,6), exclude NaN and satisfy the reviewed base-unit/whole-quantity rule. Exact demand is derived from the frozen source with rational arithmetic; it is not rounded into the stored Q. Active fully covered ingredients persist with Q=0. Empty or unknown ingredient sources cannot be registered as a successfully covered meal.

## Durable minimal operation proof

The immutable Core proof identifies the household, actor and request UUID, with a unique constraint for that combination. It also records the canonical SHA-256 digest of the request, current Mealplan/Pantry/Shopping generations, scope fingerprint, receipt UUID, bounded source/change/result counters and trusted insertion time. It contains no human names, notes or full response. Runtime SELECT/INSERT only. Ordinary receipt housekeeping and all module purges preserve it.

Replay first checks current authorization and generation boundaries, then the proof. A valid old key returns its original minimal result without needing its former item, destination or current contribution to exist. Any separately returned current state is labelled separately. A successor cannot cause the original operation to run again.

## Shopping-local detail receipt

The append-only Shopping receipt has a stable UUID, household, unique Core operation identity and a bounded server-built JSON snapshot. The snapshot includes the whole reviewed scope, explicit decisions and source/item/contribution before-and-after states. It may reference the Core proof; it has no FK to deletable articles or optional schemas. Source identities are unique within the receipt, and result counters agree with its actual decisions. Ordinary successful no-op syncs permit zero changes and emit no item versions or outbox effects.

Deferred checks on both new proof and receipt require the exact matching counterpart, identities, generations, request/scope hash and counters. A proof plus an unrelated or malformed receipt cannot commit. A deliberately late failure rolls back purchases, contributions and both evidence rows. Removing detail payload through an actual Shopping purge must not remove the durable Core proof or cause replay to recreate effects.

## Lifecycle boundaries

| Trigger | Required transition |
|---|---|
| Matching actual Pantry intake | Q→0, disposition stored, requirement remains active; actual amount comes from checkoutLine's returned stock evidence. A partial intake needs a fresh review before a residual purchase. |
| no_stock | Q→0 and explicit unresolved purchase state. Initial version blocks automatic substitute coverage/reorder; settlement requires a named explicit decision. |
| skip | Q→0, skipped; renewal requires explicit confirmation. |
| Intake into another target/unit/generation | Preserve actual stock truth; mark planning relation unresolved rather than attributing it to the old product. |
| Item/list deletion | Invalidate only the targeted destination, mark missing and require review. Do not end the meal or silently recreate an item. |
| Manual edit/check/uncheck | Preserve the user's row. Current item version/fingerprint differs from the reviewed managed basis; explicit adoption/detachment is required. |
| Detach | Stop item management and preserve the independent item. Whole-meal need remains active. |
| Ordinary source deletion/mapping loss | Preserve blocked active identity; never omit it from the next global scope read. MP14 binding deletion does not own the MP17 row lifetime. |
| Actual Mealplan purge | Maintenance removes current source/scope payload before TRUNCATE; independent items and minimal proofs survive. |
| Actual Pantry purge | Invalidate current target/basis, retain active requirements and independent purchases. |
| Actual Shopping purge | Clear destination/pending payload; retain requirement activity and minimal proof. No implicit replacement purchase. |

Checkout acquires the relevant contribution with NOWAIT after its existing list/item locks; it must not wait inversely on a planning writer. A checkout that changes scope activity also takes the same nonblocking scope mutex. Both merge preview and commit protect either participant when managed. Source/target cleanup performs targeted invalidation without locking parents in reverse order.

This foundation reuses the existing durable proof/replay, authenticated checkout, audit, purge barrier and narrow readiness patterns. Focused tests exercise fresh/repeated migrations, individual readiness failures, whole-source replacement, actual checkout rollback and all three real module purges. The remaining registration/sync, concurrency, full-current regression, restore and PWA evidence are required by planning-shopping.md; source review alone does not satisfy them.
