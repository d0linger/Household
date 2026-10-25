# Repeatable planning purchases — MP17 planned contract

Status: persistence, complete-scope reader and checkout/lifecycle foundation implemented with focused local PostgreSQL evidence; MP17 remains in progress. No public MP17 scope-registration or purchase-sync API is available. MP14's current PWA preview remains selection-scoped and read-only. Its shortage must not be used as an instruction to append purchases independently. The existing mealsToShopping path still creates independent positions; replacing that path is part of MP17 acceptance, not a second parallel integration. See the [foundation evidence and remaining gates](planning-shopping-verification.md).

## Shared physical coverage

Two independent sources needing1kg each must not both subtract the same free0.5kg and purchase only1kg in total. Product locks alone cannot solve this: the first Shopping sync changes neither physical stock nor a PA12 lease.

The initial safe model uses one explicitly reviewed active household planning scope. Each preview includes the newly selected whole meals plus every already-active requirement, including requirements with zero net purchase and no Shopping item. Scope is bounded to42 whole meals and500 ingredients across all included plans; refuse an excess instead of truncating it. The UI names added existing sources and every affected destination list before confirmation. Unchecking a source in a new selection does not silently deactivate a previously accepted requirement.

Activation and ending apply to whole meals, including every ingredient of every component. Detaching an item ends its Shopping management only; it does not remove that ingredient from the active meal demand. Ordinary source deletion or mapping loss leaves a blocked active identity until an explicit whole-meal end. Actual Mealplan module purge deliberately removes all current source/scope payload before truncation; minimal operation proofs and independent Shopping records survive.

Requirement activity is independent of the last purchase's disposition. A stored purchase clears pending procurement coverage, but its still-planned meal remains an active requirement until cooked/cancelled or explicitly ended. Otherwise the newly stored goods could immediately cover a second meal while the first meal disappears from the shared calculation.

All active requirements are read in one consistent preview. On mutation, serialize changes to the active scope with a household-specific nonblocking transaction advisory lock before reading it. This includes empty/zero-purchase scopes: concurrent first registrations must not both start from an empty set. Normal stock/checkout writers retain their existing household barrier and targeted contention behavior.

## Quantity rules

1. Confirm current frozen ingredient identities and current Pantry mappings. Unknown/incompatible inputs block a complete initial sync; they are not zero.
2. Group demand by Pantry product/base unit before calculating eligible physical coverage. Claims outside the full active scope remain unavailable; claims inside it are counted once. Forecast pooling does not release or transfer any manual PA12 lot lease.
3. Distribute physical coverage deterministically by planned date, explicit meal-slot order, meal UUID, component position/UUID and ingredient position/UUID. Source net need N_i is computed before considering its existing procurement Q_i.
4. Compare the stable desired purchase against Q_i: delta=N_i−Q_i and additional=max(delta,0). Repeating the same state must not shrink the target or create another item.
5. Check every positive per-source purchase with pantryExactAmount, even if the product total is bookable. Three separate1/3 requirements cannot become three rounded positions merely because their aggregate is1.
6. Initial mutation rejects non-bookable source targets. A later explicit quantity-decision step may offer ceiling to six decimal places or whole base units, show the exact original fraction and surplus, and retain that decision only while its source/unit/exact need remains applicable. It must never silently round. Out-of-range targets remain blocked.
7. Zero net need still registers the active requirement. An unchanged managed open item reduced to zero is explicitly removed under review; zero is not written into a positive-only Shopping quantity column.

Equal unlinked names, copied lists and another source's purchases provide no automatic source coverage. Pantry identity and Shopping catalogue identity remain separate concepts.

The allocation helper now feeds the read-only complete-scope Store preview. It validates source ownership/order, exact per-product coverage, whole-meal/ingredient bounds and stable N-before-Q differences. The shared transaction reader reads all plans/components together and evaluates claims with one instant. It registers no scope. The [schema decision record](planning-shopping-schema.md) describes the current development migrations; they have been exercised only in the disposable test database. Later MP17 evidence is separate from the frozen MP14 candidate.

Source review compares the actual accepted per-meal/component/mapping basis with its current value. A changed portion, source or date blocks automatic item effects. Unrelated changes to another meal in the same plan do not change that basis; parent plan versions remain in the complete preview fingerprint. Missing sources retain blocked identities rather than silently releasing their physical coverage.

Actual checkout now writes a typed contribution transition in its existing transaction, after its actual checkout receipt and intake effects exist. It preserves active meal demand and distinguishes matching/partial storage, another target, no_stock and skip. Managed items are protected in both merge directions, including preview. Deletion invalidates the destination while retaining the active source. This foundation is not an available initial registration/sync workflow.

## Minimal persistent records

| Record | Required contract |
|---|---|
| Core current requirement/contribution | Stable UUID; unique household/Mealplan generation/component/ingredient; immutable source identity/context; version; separate requirement activity and procurement disposition; confirmed source/mapping basis; Pantry product/generation/unit; destination Shopping generation/list/item; last applied quantity, item version/fingerprint and operation ID. Retain active zero-purchase requirements. At most one current contribution manages an item. No runtime DELETE. |
| Core operation proof | Immutable household/actor/request-key identity and canonical request hash; module generations; result/receipt UUID and bounded counts. No human names, notes or full response payload. Retain across all module purges and ordinary receipt cleanup. |
| Shopping receipt | Append-only bounded snapshot of reviewed scope, previous/next contributions, explicit decisions and item before/after states. No foreign key to a deletable item or another optional schema. Deferred completeness prevents a proof-only commit. |

Core21/Shopping5/Pantry9/Mealplan5 implement the reviewed columns and narrow guards. Runtime grants and readiness check each required privilege, trigger identity/mode/owner/search_path and durable unique index. Shopping item audit events add only bounded exact-row fingerprints while preserving the existing private-value redaction. Deferred receipt checks compare those immutable same-transaction effects; checkout evidence also requires the actual actor/correlation and same-transaction intake movement. General resource_links do not own these records or their lifetime.

## Lifecycle and conflict decisions

- Untouched open item: update the same item under expected version/fingerprint; preserve unrelated data.
- Manual edit or check→uncheck: preserve the user's record; explicitly re-review/adopt the current state before another managed update. A version mismatch is never silently overwritten.
- Checked item: preserve purchased history; no automatic repeat or quantity update.
- Actual matching Pantry intake: atomically record the purchase disposition and remove its pending procurement coverage. Keep the underlying planned requirement active. Partial intake permits only a newly reviewed residual need.
- Historical checkout references are separate from an open destination. A completed archived list does not block a source whose current net need is zero. Require a reviewed open list only for a new positive contribution.
- no_stock: bought without physical stock evidence. Keep this distinct and prevent automatic reorder; an explicit decision must settle how the already-bought source quantity is represented or separately recorded.
- skip: not bought and no stock coverage. A new purchase requires an explicit renewed decision.
- Intake into another Pantry product/unit/generation: preserve honest intake and mark the planning relationship for explicit resolution; do not claim it covers the original ingredient automatically.
- Item/list deletion: a targeted hook marks only the affected contribution missing, without inverse plan/product locks. Missing is a conflict, not permission to recreate it silently.
- Merge: protect both participants using the typed Core contract. Explicitly detach a managed position before merging; resource-link cleanup cannot erase its proof.
- Source/mapping/destination changes or disappearance: show the changed basis and explicit keep/end/rebind decision. Do not silently delete or replace existing purchases.
- Module purge: separate payload cleanup from minimal proof retention before TRUNCATE. Source purge removes active source payload; Pantry purge invalidates target mappings; Shopping purge clears destination/pending coverage. Preserve independent module records and explain lost linkage.

Replay checks current actor authorization and generation boundaries before reading the durable proof. A valid proof does not require the original item still to exist. Detail receipts are shown only under permitted current module access; unavailable details do not recreate effects.

## Mutation order and integration points

Use one transaction-bound demand reader shared by the preview and mutation; do not call the public RR method through a second connection during commit. Mutation uses explicit READ COMMITTED. Lock household/modules, acquire the active-scope mutex, then lock plans, meals and components in deterministic order; Pantry products FOR UPDATE; Core bindings/contributions; Shopping lists/items. Secondary contested locks fail with NOWAIT. Read stock/claims in fresh statements after locks and use one common expiry instant.

The inverse checkout path locks the relevant contribution with NOWAIT and updates it in the same actual checkout transaction. It must not wait on a planning writer that already holds a product and is trying to acquire the Shopping row. Source/target cleanup hooks remain narrow invalidations rather than broad orphan sweeps.

Required call sites: integrations.go mealsToShopping and its actions.go branch; generic Store.Write/Delete and check/uncheck; shopping_merge.go plus merge preview; shopping_checkout.go checkoutLine; cleanupOrphans and maintenance.Purge. Old action payloads without the new full review must be rejected with a useful path to the new workspace.

## Required evidence before completion

The quantity/lifecycle tests must include these concrete cases (canonical source A precedes B):

| Case | Input and required result |
|---|---|
| Disjoint registration | A needs1kg against0.5kg free stock: register A with N_A=0.5. Adding B needing1kg rechecks A+B and produces N_A=0.5,N_B=1, not two0.5kg contributions. |
| Covered source still occupies forecast stock | A needs0.4kg against0.5kg: N_A=0 but A is persisted active. Adding B needing0.4kg produces N_B=0.3. |
| Stored but still planned | A needs1kg; its0.5kg purchase raises physical stock from0.5 to1kg. Its pending Q becomes0, but active A remains. Adding B needing1kg requires1kg new purchase. |
| Product total does not authorize per-source rounding | Three separate1/3kg requirements total1kg with no stock. Each per-source target still requires an exact explicit quantity decision. |
| Below precision is positive | N=1/3000000kg is never0. If explicit ceiling is supported, reviewed purchase0.000001kg shows exact surplus1/1500000kg. |
| Lot undercoverage | Lot1 has physical0.25, own claims0.6 and external claims0.3; lot2 has eligible1. For1.2kg demand, usable1kg and shortage0.2kg coexist with lot undercoverage0.65kg. |
| Deleted managed article | Existing Q=1 but its item is absent: return a conflict with no new position until explicit resolution. An old request proof cannot recreate it. |
| Replay after successor | Original request K applied version1; a later request changes or ends the contribution. K returns the immutable original proof alongside any separately labelled current state and never reapplies version1. |
| No-stock purchase | Bought quantity remains distinct from physical stock and cannot cause an automatic repeat purchase. Settlement requires the named explicit decision. |
| Shared expiry instant | A claim expiring at the selected read instant is excluded consistently from every product/source calculation. Mutation rechecks using one fresh post-lock instant. |

- Changing, overlapping and disjoint selections, including two first syncs against shared free stock, active zero-purchase sources and stored-but-still-planned sources.
- Stable repeated/new-key no-op behavior, exact old-key replay, ordinary receipt pruning and both actor races.
- Fractions, below-precision and whole-unit quantities; no implicit rounding; explicit oversupply decision if that extension is shipped.
- Both merge directions, manual edits, check→uncheck, real item/list deletion and explicit detach/end.
- Matching/partial/other-target intake, no_stock/skip and checkout-versus-sync contention.
- A genuine late database failure rolls back every item, contribution and receipt; no proof-only commit.
- Actual source/Pantry/Shopping purges, prior populated upgrade, encrypted restore and current grant/readiness failures.
- Full native regression, scoped Linux race, real mobile/desktop review, browser chain, security/workflow checks and honest checklist evidence.
