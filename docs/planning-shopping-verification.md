# MP17 implementation and verification

Status: in progress. Checklist: **52 verified / 2 in progress / 126 open**. This records local development evidence, not complete MP14/MP17 acceptance or a live upgrade. The selection-only MP14 comparison links to the new complete active-scope workspace.

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

- [x] Scope registration/sync service with nonblocking scope mutex before the first read, ordered source/product/Core/Shopping locks, fresh post-lock coverage and current fingerprint comparison.
- [x] Durable initial-sync/new-key no-op/old-key replay API, explicit whole-meal ending and item detach/adopt/rebind/renew decisions.
- [x] Replace the legacy mealsToShopping action and its PWA flow; no parallel path that appends untracked ingredients.
- [x] Public authorization, generation, CSRF, reader/foreign-household and payload-limit checks for the new API (two final authenticated HTTP contracts, web15.974s).
- [x] Concurrent disjoint first registrations, changed/overlapping selections, genuine opposite-order resource contention through authenticated HTTP409, and late multi-item sync rollback (full corrected native campaign).
- [x] Exact per-source targets reject unknown/inexact/unbookable quantities; subprecision remains positive. No explicit rounding/oversupply extension is shipped or implied.
- [x] Complete native72.3%, scoped Linux race708.702s and current encrypted synthetic recovery. Authenticated restored-service replay is not part of this executed checkpoint and remains unclaimed.
- [x] Actual mobile/desktop workflow, keyboard/focus,710-check browser chain,42 purchase checks/ten clean axe scans, independent PP-F01–PP-F04 resolved/ship and separate design comparison.

The current development increment now exposes registration/sync/control/history APIs and an embedded PWA workspace. The pending verification gates above are being updated as actual checks execute. Earlier full-native70.9%,668 browser checks and MP14 synthetic recovery apply only to the frozen MP14 checkpoint82dc5b9/export, excluding this later foundation. They are not current MP17 acceptance. Live8100, sibling applications, hosted Actions, push and publication are outside these local checks.

## MP17 registration, synchronization and lifecycle workflow — in progress

Current development schema is Core22/Shopping6/Pantry10/Mealplan6,73 audit policies and59 migration hashes. Core22 adds only a bounded meal-history lookup index; forward guards preserve exact source/binding/reason/history fields during detach and reject reopened completed Shopping destinations. Existing committed migration files are unchanged.

The transaction service registers whole sources including zero purchases, compares the complete active household scope under its mutex and ordered NOWAIT locks, and performs exact create/no-op/update/remove effects with durable original-key evidence. Current generations and actor/session authorization precede replay. Coverage locks include locations and active claim roots; dependency overflow is rejected. Historical receipt details contain the reviewed scope and named decisions, while permanent Core proofs retain only minimal identities/counts.

Explicit accept/rebind/adopt, renewed purchase and whole-meal reactivation are separate reviewed decisions; checked, completed, unit-mismatched and unbookable item states stay blocked. Independent source review found two P2s: preserving managed disposition during accepted removal and losing ended-meal intent after complete component replacement. Both are corrected, with real reduction-to-zero and actual SavePlanningMeal replacement regressions. The reviewer closes both at source scope and executed no tests.

Recovery uses authenticated Core transactions and remains available with all three modules disabled or the original component missing. End covers the whole meal; detach preserves active demand. Both retain independent items unchanged. The first four-case run failed only during test cleanup after its context was canceled; the corrected cleanup and expanded contracts pass. Initial decision testing also had an incorrect fixture effect expectation, corrected to the actual1kg physical stock. The corrected three-case decision/API run passes(web20.380s).

All23 focused native purchase/legacy contracts then pass shuffled(web180.288s), covering schema/evidence/readiness, all actual checkout dispositions, purges, prior upgrade, zero-source/concurrent first syncs, durable replay, explicit decisions, missing/disabled recovery, genuine late control rollback and authenticated API/CSRF/reader/history behavior. Unit store/web pass(1.100s/1.966s); two additional exact-display/whole-meal-key Node tests pass. Later UI/test-tooling edits are not covered by that compiled native candidate.

The PWA is now connected at /planning-purchases, with the legacy append-only plan action rejected and linked to complete review. Browser, independent UI finish, full current-tree native/race, security/workflow and refreshed encrypted recovery remain pending. The isolated review server is being prepared on8101;8100 is untouched. Checklist remains52verified/2inprogress/126open. No hosted Actions, push or publication occurred.

### MP17 current workflow verification

The final23-contract purchase/legacy native campaign passes(web180.288s). Three later one-connection/late-two-item-rollback/location-and-reservation-lock/no-op-quantity contracts pass together(web21.841s); the final two authenticated HTTP contracts, including actual Shopping purge, stale mutation generation, minimal history, malformed/oversized input, forbidden free quantity and unknown targets, pass(web15.974s). A complete native candidate is running with seed1792967872847 and a25-minute cumulative package budget; no full result is claimed yet. A later test-only explicit product-rebind regression was added after compilation and will run separately.

Initial purchase browser review passed32checks/seven clean axe scans. One batched correction tightened mobile quantities, week alignment, singular copy and stale-preview clearing. The first20-component chain passed all prior19components(668checks) but its20th authentication hit the real per-account rate limit; that campaign failed, with no application limiter change. The test suite now shares a real authenticated session only between its final two components in a private temporary `.local` directory, cleaned in finally. The corrected complete20-component chain passes704checks; purchasing has36checks/nine clean scans/nine opened light/dark mobile/desktop captures. It also verifies the post-sync API attributes0.2 purchase with zero delta/additional demand. The review server is stopped. Fresh independent finish review is in progress; no UI acceptance verdict is claimed yet.

Final unit/workflow verification passes15.0%(1557/10395 statements across ./...),71Node tests and59migration hashes. Tagged lint reports zero issues. Gosec scans120application files/18682lines with zero findings and no Go errors. GOVul reports zero reachable/imported-package findings and the same unused module-only GO-2026-5932 advisory. Dependency/license, Actionlint and Compose checks pass. These are local checks, not hosted Actions.

The current runtime candidate is household:planning-purchases-review,sha256:fae0178d218999fe9a7bc74f8fd09938fd12c598a9d893ca10eab23c7606e0e2,40668336bytes,user10001:10001. Matching backup helper is sha256:3e9f74fafc410aa1071d906c810dc30344e4cd7cbff2a23a34f11c86f131587e,user10001:10001. Enhanced encrypted synthetic recovery passes, preserving two complete active zero-purchase identities, exact source/mapping/version, permanent request/scope hashes, detail rows, prior data and two file hashes; duplicate proof insertion returns0. Source review found a too-weak inner-join receipt assertion; its corrected complete-set/LEFT-JOIN assertion and read-only duplicate/unknown-source counterexamples pass(actual:true/duplicate:false/unknown:false). Final backup6.76s/restore-and-verify22.43s supersedes the earlier5.16/24.22s run. Fixed synthetic hashes establish persistence, not an authenticated restored service replay or production RTO/RPO.

MP14/MP17 remain in progress pending full native, scoped race and independent interface/documentation acceptance. Checklist52verified/2inprogress/126open. Live8100 remains untouched; no push, hosted workflow or publication occurred.

### MP17 independent UI correction

Fresh independent finish review returned fix for PP-F01 (actual item note), PP-F02 (detach item/list/quantity identity), PP-F03 (stale quantities after failed comparison) and PP-F04 (focus after destination review). All four corrections are implemented with browser regressions prepared. The database source reviewer additionally found that actual moved items must expose their present list rather than the saved association; the household-bound actual-list join is corrected, with a real A-to-B move/detach regression. The initial corrected note/privacy/explicit-rebind campaign passes(web40.632s); the latest actual-list cases and corrected browser captures are pending.

The full native candidate with seed1792967872847 was deliberately stopped after these source corrections made it obsolete. It is superseded and supplies no full-suite acceptance. Earlier704browser checks and runtime/backup image identities describe the preceding candidate. Final native/race, rebuilt images/recovery and scoped independent correction verdict follow. Checklist remains52verified/2inprogress/126open.


### MP17 final correction acceptance and running regression

The four independent UI findings PP-F01–PP-F04 are scored resolved/disposition ship at their correction scope. The same ten corrected screenshots are opened and valid; the complete20-component chain passes710checks, including42purchase checks/ten clean axe scans. The separate design documenter records the incumbent-system comparison in planning-purchases-design-verification.md; DESIGN.md remains byte-identical and its sidecar absent. The actual-list source P2 is closed source-only; its two focused native cases pass(web14.631s).

The new opposite-order contention test first used an overly narrow Store-error assertion: PostgreSQL55P03 already maps to HTTP409. The corrected regression verifies real authenticated HTTP409 in both directions, no partial checkout/sync proof, and successful retry after lock release. It and the strengthened actual-note assertion pass together(web14.465s). The full corrected native campaign now runs with seed1792969038332 and a25-minute package budget; scoped Linux race follows serially. No result from either is claimed yet.

Final local verify passes15.0%(1557/10395),71Node cases and59migration hashes. Tagged lint is clean; Gosec120files/18687lines finds0issues/noGoerrors; Actionlint passes. Runtime household:planning-purchases-review is sha256:d6696e7054b12d6099a34329af8ed69f9cd5bfd8dd24de4fbd147268e1e5f250,40668364bytes,user10001:10001. Matching backup helper is sha256:24fd57e6c3a6eb2a031e9e4d7a98dd8c6af547878895f462024eb17ad9329f37,521315528bytes,user10001:10001. Its encrypted synthetic recovery passes backup8.26s/restore-and-verify38.04s with the stated complete scope/proof/detail/counterexample/prior-data/two-file scope; authenticated restored API replay and production RTO/RPO remain unclaimed. These image identities and710checks supersede the preceding candidate.

Checklist remains52verified/2inprogress/126open until complete native and scoped race pass. Review8101 is stopped, live8100 untouched, no push/hosted Actions/publication. MP06 participants/guests preparation begins separately without changing this compiled candidate.


### MP17 complete native first repeat — failed historical fixture

The complete campaign with seed1792969038332 finished with one failing historical test (TestRecipeVersionsPopulatedUpgrade; web1147.980s). All258other main tests passed. This is a failed full regression, not acceptance. A focused diagnostic reproduced the failure(web15.374s) and recorded the actual blocked statement: Shopping5's new planning-receipt migration reached its household barrier before Recipes2. The test had advanced only Core, so its Recipes2-specific wait could never match.

The historical fixture now advances every independent schema preceding Recipes before starting the legacy writer. It retains the actual populated Recipe1 upgrade and ingredient-before-parent contention assertion. Early migration errors are observed directly, the real blocked statement is reported, and the migration goroutine is drained before fixture cleanup. This change affects only the test. The310-file application/source manifest differs only in this test file; runtime code, embedded UI, migrations, Docker images and prior browser/recovery evidence remain unchanged. Two focused repetitions and the same-seed complete repeat are running; final race follows only after native passes.

MP06 preparation proceeds in a separate ignored source export under .local/participants-work. Its provisional Store/read/copy/schema paths compile and pure/store/web unit suites pass; no MP06 PostgreSQL or browser test has run. A source-review readiness finding was corrected to check exact FK/Unique columns plus index readiness. Root application code is unchanged by this preparation. Current checklist52verified/3inprogress/125open.


### MP14/MP17 complete corrected native — passed; race running

The same-seed complete native repeat passes all259 main tests with no failures (web1147.817s). Combined internal coverage is72.3% (7386/10216 statements;55% gate). The historical Recipe1→current upgrade now reaches the actual Recipes2 barrier and passes; its two focused repetitions passed earlier at web23.783s. The native compiled candidate predates only the later test-cleanup error-log addition required by errcheck; application Go/SQL/embedded assets are unchanged. Current tagged lint reports0issues, and scoped Linux race includes the final diagnostic bytes. This timing justifies the25-minute native test-package budget as headroom, not a performance claim.

The scoped Linux race campaign is running serially against55434, selecting all planning-purchase/demand/binding contracts and allocation helpers, both Merge cases, actual checkout replay/projections, repeat/repair, the three audit contracts and the corrected historical Recipe upgrade. Unmatched packages do not acquire whole-tree race acceptance. Checklist remains52verified/3inprogress/125open until that result is known. The separate MP06 export is excluded.


## MP14/MP17 — locally verified

The final scoped Linux race passes(web708.702s, Store1.204s): all37 matching Web planning-purchase/demand/ingredient contracts, all matching allocation/preview/decision pure helpers, both Merge cases, actual checkout replay/projections, migration repeat/repair, the three audit contracts and corrected populated Recipe upgrade. Unmatched packages report no tests; this is scoped race acceptance, not a whole-tree race result. Full same-seed native passes259 main tests, web1147.817s,72.3% internal coverage(7386/10216); final tagged lint is clean. The native compiled test predates only cleanup error logging, exercised at final-byte compilation scope in the race campaign; production Go/SQL/assets are identical.

Together with the710-check browser chain,42 purchase checks/ten clean axe scans/ten valid opened captures, independent correction ship verdict, separate system comparison, exact encrypted synthetic recovery,59 migration hashes,71 Node cases and the recorded security/dependency/image evidence, MP14 and MP17 meet their local feature acceptance. Authenticated restored-service replay, production RTO/RPO, live8100 upgrade, hosted Actions and publication remain unclaimed. The preceding failed native campaign and its barrier diagnostic remain recorded; they are not substituted for the successful repeat.

Checklist:54verified/1inprogress(MP06)/125open. MP06's separate ignored source export has not been applied or executed against PostgreSQL; it is excluded from this acceptance. The reviewed runtime and backup image identities remain d6696e7054b12d6099a34329af8ed69f9cd5bfd8dd24de4fbd147268e1e5f250 and24fd57e6c3a6eb2a031e9e4d7a98dd8c6af547878895f462024eb17ad9329f37. No push or live deployment occurred.
