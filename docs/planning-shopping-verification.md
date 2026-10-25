# MP17 foundation verification

Status: in progress. Checklist: **52 verified / 2 in progress / 126 open**. This records local foundation evidence, not complete MP14/MP17 acceptance or a live upgrade. The unchanged MP14 PWA still explicitly discloses unlinked Shopping coverage.

## Implemented foundation

- Core21/Shopping5/Pantry9/Mealplan5: typed current requirements, permanent minimal operations and append-only Shopping-local detail receipts;73 audit policies and55 migration hashes. No foreign keys into independently purgeable optional schemas.
- Deferred reciprocal proof/receipt checks, exact current before/after states, individual privileges and trigger/index readiness. Claimed item effects require actual immutable same-transaction audit fingerprints. Audit retains its existing private-value redaction.
- Scope-changing SQL rejects REPEATABLE READ and takes the household planning-purchases mutex. Whole-meal checks validate the final active ingredient set, including an atomic old-source/new-source replacement.
- Pure rational allocation and a transaction-bound complete-scope Store preview. Already-active zero-purchase and stored-but-still-planned ingredients remain in the physical calculation. All selected plans share one stock/claim read and expiry instant. Bounds:42 meals,500 ingredients, existing component/snapshot limits.
- Stable N-before-Q comparisons, exact signed differences, per-source posting bounds and blocked unbookable quantities. A read-only preview never registers a scope or creates a purchase.
- Confirmed per-source basis changes block effects. An unrelated revision to another meal in the same plan does not falsely alter the accepted source. Complete preview fingerprints retain parent plan versions and omit only the read clock itself.
- Actual checkout integrates in its existing transaction: matching/partial intake, other target, no_stock and skip clear current Q while retaining active source demand. Deferred evidence requires the actual same-transaction receipt, actor, journal movement, checkout correlation and stock link. Replay returns before another contribution transition.
- Both merge directions and the existing merge preview protect a managed item. Actual user deletion marks only its contribution missing; the active source and permanent proof survive.
- Real maintenance purges remove current Mealplan payload, invalidate Pantry targets, or clear current/historical Shopping payload respectively. Independent module records and minimal proofs survive as specified.

## Executed development evidence

| Campaign | Result and precise scope |
|---|---|
| Initial corrected schema/effects | PASS, web25.610s: actual create/update evidence, fabricated prior note rejection, unreported effect rollback, old-proof rejection, RR rejection, source deletion/end, genuine checkout correlation and repeat/repair. |
| First expanded schema regression | FAIL, web90.846s: historical Core19 fixture unintentionally selected new Shopping5, which requires Core21. Six historical fixtures now explicitly pin Shopping4. No application assertion cause is inferred from that fixture mismatch. |
| Corrected expanded regression | PASS, web143.656s,20 named contracts: four MP17 schema contracts, nine MP14 demand/binding contracts, actual checkout, repeat/repair and five other affected historical upgrades. |
| Initial global Store read | PASS, web19.818s,three contracts: multiple plans, active zero-purchase scope, no read effects, missing sources and existing lot-scoped demand. This preceded the additional source-basis correction. |
| Checkout and reader campaign | PASS, web84.218s,ten named contracts including five actual checkout dispositions, active stored-source coverage, durable replay, genuine late database failure and complete rollback. |
| Merge/deletion regression | PASS, web19.493s: both managed merge directions and actual destination deletion, plus the two existing Shopping merge contracts. |
| Real purge/source campaign | PASS, web32.386s: all three actual module purge paths, changed portions with a managed item preserved, and another meal in the same plan without a false source conflict. Backup authorization metadata is synthetic; these purge fixtures do not execute a backup/restore. |
| Combined foundation native regression | PASS, web318.738s,35 named contracts: all13 MP17 contracts, nine MP14 contracts, all five Checkout contracts, both Merge contracts, repeat/repair and five other affected prior upgrades. Includes populated immediate-prior Core20/Shopping4/Pantry8/Mealplan4 with existing confirmed mapping, exact stock and frozen source preserved; migration does not infer active requirements. This is a focused regression, not the full current-tree native campaign. |
| Final unit/workflow verification | PASS,15.6% across ./... (1536/9877 statements;14% floor),69 Node checks, module verification, workflow policy and55 hashes. All51 prior migration hashes remain unchanged. |
| Tagged lint / Gosec | PASS: zero tagged lint findings. Explicit cmd/internal Gosec scope111 files/17592 lines, zero issues or Go errors. |
| Scoped Linux race | PASS, web314.150s and Store1.156s: all13 MP17 Web contracts, both existing Merge contracts, actual checkout replay/projections and the named lot-scoped demand contract; all ten matching pure allocation/difference/preview groups. Unmatched packages report no tests; this is not a whole-tree race acceptance. |
| Current vulnerability/dependency review | PASS: zero reachable/imported-package findings, one existing unused module-only advisory GO-2026-5932. Dependency/license inventory and module verification pass; no dependency versions changed. |
| Affected audit regression | PASS, web33.887s, all three Audit contracts: domain census47, attribution/privacy, runtime immutability, rollback/replay, populated upgrade and worker failure evidence. |

The source-only database reviewer identified six material schema/evidence issues: old-proof reuse, invented item effects/counters, final-source replacement, stale RR scope writes, invented previous-row details and reuse of old intake movements. Their corrections are present. A subsequent reader review identified the missing accepted-source comparison; its actual-basis regression and correction are present. Follow-ups report no remaining material finding in their stated source scopes. Reviewers performed no database actions or tests and did not approve the unfinished product workflow.

## Remaining feature and acceptance gates

- [ ] Scope registration/sync service with nonblocking scope mutex before the first read, ordered source/product/Core/Shopping locks, fresh post-lock coverage and current fingerprint comparison.
- [ ] Durable initial-sync/new-key no-op/old-key replay API, explicit whole-meal ending and item detach/adopt/rebind/renew decisions.
- [ ] Replace the legacy mealsToShopping action and its PWA flow; no parallel path that appends untracked ingredients.
- [ ] Public authorization, generation, CSRF, reader/foreign-household and payload-limit checks for the new API.
- [ ] Concurrent disjoint first registrations, changed/overlapping selections, sync-versus-checkout contention and late multi-item sync rollback.
- [ ] Explicit quantity resolution if supported; no silent rounding, subprecision zero or aggregate authorization of unbookable individual positions.
- [ ] Complete current-tree native/race regression, current encrypted recovery and authenticated restored API replay where required.
- [ ] Actual mobile/desktop sync workflow, keyboard/accessibility review, browser regression and scoped independent finish acceptance.

No MP17 public registration or purchase-sync API/UI is available yet. Earlier full-native70.9%,668 browser checks and MP14 synthetic recovery apply only to the frozen MP14 checkpoint82dc5b9/export, excluding this later foundation. They are not current MP17 acceptance. Live8100, sibling applications, hosted Actions, push and publication are outside these local checks.
