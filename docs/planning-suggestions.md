# Pantry-based and date-aware recipe suggestions

MP11/MP12 are locally verified after participant checkpoint886f09f. The reader, private signed API and embedded weekly-editor disclosure pass complete native regression295main tests/73.3% internal coverage(Web1234.590s),42Web contracts in scoped Linux race(Web603.489s), the778-check browser chain, current-image synthetic recovery and scoped independent correction/documentation review. See [the executed evidence and limits](planning-suggestions-verification.md). No new table or automatic inventory/planning mutation is introduced.

Initial preparation existed only in ignored `.local/suggestions-work`: pure exact product grouping/coverage, independent alternatives, separate calendar reasons and deterministic bounded-page ranking. Eight pure tests passed(Store1.555s), including repeated product subtraction, unknown versus zero, subprecision/large exact quantities, caller ownership,0-day versus unknown opening, invalid metadata and20 reproducible ranking permutations. That preparation had no database/API/PWA integration and did not change the then-running MP06 regression candidate.

The next preparation increment evaluated the complete bounded free-lot set, excluded passed use-by quantities from coverage without deleting physical data, capped each displayed date reason to the candidate's actual known requirement and ignored unused/unrelated lots. Its13-test campaign passed(Store1.282s), including a relevant60th lot beyond display samples, case-insensitive duplicate identity rejection,5001-lot overflow, unknown need/no urgency and the seven-calendar-day window across Austrian DST. It remained isolated from the then-running root regression until materialization after886f09f.

The public Store reader checks current session/role/auth-version/expiry, module generations, one15-second read-only RR transaction, bounded source metadata/ingredients and complete lot/claim reads. Source review closes the past-planned-day P2 and cursor-scope design concern. The final prepared pure campaign passes(Store1.084s/Web2.239s), followed by compile-only integration validation(Web2.501s). Six actual PostgreSQL contracts then pass: actual claims/independent alternatives/no read side effects,60-lot/use-by review, reader/session/module privacy, creation-anchor pagination and ingredient/cancellation bounds, and authenticated signed HTTP cursors with actual timezone/module changes. Source-only reviews did not execute these tests. Current browser and final acceptance evidence is recorded separately below as it becomes available.

## User contract

Offer independently evaluated recipe alternatives for an explicit positive exact portion amount and household calendar date. Show why each alternative is covered, short or incomplete, which explicit product mappings were used, and which relevant free lots have a use-by date, opening guidance or best-before date. Unknown ingredient amounts or incompatible units must never become zero demand or “fully available.” Opening guidance and best-before dates are not food-safety guarantees; passed use-by dates get their own review state and must not make a recipe a recommended way to use that lot.

Each alternative sees the same current free stock. Two alternatives individually covered by that stock are not thereby covered together. The page states this distinction and the observation time. Choosing a recipe opens the existing explicit immutable-source review; it does not save a meal, reserve stock, create shopping items or consume a lot. Existing demand/source review remains authoritative after selection.

## Read architecture

Use one context-bounded RepeatableRead/ReadOnly transaction with current session/household authorization, module states/generations, one ReadAt and household timezone. A disabled source/target cannot leak private names or stale optional payload. Keep independent optional schemas; cross-module data uses the existing Core mapping/reservation contracts and typed Store reader boundaries, with no cross-optional FK.

Reuse `RecipePantryComparison`/`comparisonMappings` for immutable versions and explicit catalogue→Core→Pantry bindings, `ingredientDemand` for exact rational scaling, the lot-wise stock/claim semantics of `planningDemandStock`, and `pantryOpeningHint`/`pantryPriority` for separately labelled date reasons. `comparisonStock` is physical stock and must not stand in for available stock. Existing FEFO-three and priority-fifty samples are display previews and cannot define complete coverage or urgency.

For new alternatives all current active PA12 claims are foreign. Free quantity is the sum of `max(physical lot quantity − current active claims,0)` over eligible lots/products/locations, evaluated at ReadAt with current generations. Avoid the nullable-array reuse trap in `NOT(meal_id=ANY($3::uuid[]))`: pass an explicit non-null empty selection or aggregate all claims directly. Expired/released claims do not count; a future meal date must not prematurely release a currently active claim. Open Shopping items are not physical stock.

Group repeated ingredient mappings by recipe/product before subtracting stock once. Never add kg, litres and pieces into one shortage magnitude. Evaluate each recipe independently, retaining exact fractions and non-postable subprecision values. Date priorities use only positive free quantities of products that recipe actually needs. Evaluate all relevant bounded lots; reject resource overflow instead of silently using the first displayed sample.

Every bounded lot participates in coverage and ranking. Response presentation is separately limited to50date reasons per candidate, with review/excluded evidence first and an explicit total count. This prevents a20-recipe page from building hundreds of thousands of DOM rows without changing coverage or claiming the displayed reasons are complete.

## Bounds and ranking

Start with a stable searched page of20 non-archived recipe identities plus sentinel and their latest immutable source versions. Check snapshot sizes/counts before loading ingredient fields; cap an evaluation at500 ingredients and16MiB. Sources eligible for the current planning handoff must also satisfy its existing1MiB component limit. Oversized/incomplete candidates have named reasons rather than silently truncated ingredients. Bound lot/dependency reads with sentinels and explicit errors.

Rank only the evaluated page, labelled as such; do not describe it as a global best-of-all-recipes list. Signed cursors bind actor, household, source/target generations, filter, portions and date basis. Stable tie-breakers use source identity. Prefer fully known/covered alternatives, then known shortages, then incomplete alternatives. Within those groups explain relevant upcoming date/opening reasons; do not manufacture a cross-unit quantity score. Preserve a distinct passed-use-by review state.

## Interface direction

Extend the established green Operate workspace. Use visible portion/date/search controls, a clear explanation of page scope and independent alternatives, and rows containing immutable source title/revision, coverage state, exact missing product amounts and separate date reasons. Native details reveal mapped ingredients and lot evidence without loading every source snapshot into the first viewport. A single “Rezeptstand prüfen” action enters the existing explicit planning workflow. Empty, disabled, stale, unknown, error and reader states remain usable at390px; no new remote assets, fonts or dependencies.

## Implementation and acceptance sequence

1. Confirm existing source/mapping/claim/date reader boundaries against current source; source-pattern review completed, with no tests or edits by that reviewer.
2. Define typed bounded request, candidate, coverage, product and date-reason models.
3. Implement pure exact per-recipe grouping and coverage classification.
4. Implement pure stable explanatory ranking and calendar-date reasons.
5. Unit-test repeated product ingredients, mixed dimensions, unknown amounts, large exact quantities and subprecision.
6. Unit-test independent alternatives, deterministic ties, opening zero-day guidance and passed-use-by handling.
7. Implement current authorization/module/generation and stable candidate-page reads.
8. Load bounded immutable ingredient metadata and explicit mappings in one read transaction.
9. Aggregate complete eligible lot balances and current claims; test non-null empty-selection semantics explicitly.
10. Derive relevant positive-free-lot date reasons without display-sample truncation.
11. Add a private uncached read-only API with bounded query input and scope-bound cursor.
12. Test actual stock/reservation states and generations, duplicate mapped ingredients and independent alternatives.
13. Test date boundaries/DST, archived locations/products, incomplete mappings/units, excessive sources/lots and source/module purge.
14. Test reader authorization and disabled-module privacy; verify reads write no meal, claim, shopping, journal or audit mutation.
15. Connect the local embedded PWA and explicit existing source-preview handoff.
16. Execute browser desktop/mobile light/dark, keyboard, empty/error/unknown, exact quantities and stale selection checks.
17. Complete bounded independent UI/documentation review, current-image recovery and source/security/workflow checks.
18. Run complete native regression and meaningful scoped Linux race, then update README, implementation ledger and the two fixed checklist rows only with executed evidence.
