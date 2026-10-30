# Mealplan completion: MP08, MP13, MP15, MP19

Status: MP08/13/15/19 are jointly verified locally; Mealplan is20/20 and the authoritative task list is101verified/0inprogress/79open. The complete scope was published before implementation on30October2026, from402ee6c. Actual source-qualified tests, browser/recovery, private raw cleanup and independent joint acceptance are recorded in `docs/mealplan-module-validation.md`. Exact indexed scan passes and the85-file main checkpoint is `acfe08fb533222693044d2cf2c4d27ea3dc0d607`; final documentation-only packaging follows separately. No push or live deployment.

## Full change summary

| Improvement | Delivered behavior | Acceptance boundary |
|---|---|---|
| MP08 planned leftovers | Review a recipe component's production and expected surplus, allocate exact portions to a later leftover meal, inspect reciprocal links, reconcile changes and copy independent weeks. | A forecast never creates physical stock, reservations, cooking records or purchasing coverage. |
| MP13 weekly budget | Versioned EUR target, exact ingredient-use estimates from frozen plan snapshots, optional reviewed manual meal estimates, unknown/stale prices and comparable alternatives. | Known subtotal is not a complete total; missing prices never count as zero. |
| MP15 calendar | Private authenticated ICS download and personally revocable seven-day feed capabilities, stable events, revisions and minimal cancellations. | No calendar bearer secret, notes, people, ingredient, cost or instruction disclosure in logs, caches or default events. |
| MP19 autoplan | Deterministic weighted preview for explicitly selected empty slots, whole-week stock simulation, explained alternatives and one reviewed atomic apply. | No AI dependency, background scheduling, hidden replacement, consumption or reservation. |

The existing editor, frozen components, participants, demand, reservations, shopping synchronization, templates and generic CRUD remain part of joint regression. Readers inspect estimates, leftovers and authenticated downloads; writers change planning data. Calendar capability creation is an explicit personal sharing action available to authorized readers too, without giving them planning write permission. Revoke is issuer-only (administrators may revoke capabilities for the household).

## Pinned product and data contracts

### Planned surplus

A selected source component's `servings` is its total planned production. Expected surplus is a positive subset, not an additional multiplier. The review displays production, expected surplus and the implied amount for the source meal. A source uses manual meal portions; participants remain visible but do not silently enlarge production. Example: six produced, four expected surplus, two portions for the source meal. Independent component quantities stay explicit.

One normalized source record belongs to one source component. One allocation belongs to one leftover destination meal; positive allocated portions cannot exceed either the target portions or the source's remaining expected surplus. Partial coverage says exactly how many target portions are covered. Source and target share household and plan; the source is a recipe meal, the destination is a leftovers meal. Date then slot order is breakfast, lunch, dinner, snack; no clock time is invented. Reject self/earlier targets and cancelled sources. Planned leftovers never enter recipe ingredient demand.

Store reviewed source meal/component versions and destination version. Quantity, component, source recipe, date, slot, kind or relevant status changes require explicit re-review; stale links are visibly unresolved and do not count as currently promised coverage. Stale allocations still consume declared capacity until explicitly removed or reconciled. All write entry points share these rules. Deleting a source clears its capacity and allocations; the destination remains an unresolved ordinary leftover meal. Copying remaps source/component/destination identities only when all are copied; a link crossing the selected copy window produces a named review conflict requiring explicit unlink/review before copying. No physical leftover posting occurs automatically, including after marking cooked.

### Budget and rules

Weekly settings are keyed by household, plan and exact seven-day start. They have a version, nullable integer EUR target, bounded manual estimates and a closed validated rules document. Manual estimates are per meal and bind to its reviewed version; changes make them stale. Copying a week may copy its explicit target/rules and remapped manual estimates with fresh versions; no feed credentials are copied.

Prices are current explicitly mapped Shopping observations, with module generations, quantity/unit, date and provenance. Observations more than90calendar days old are shown as stale prior estimates and excluded from the known current subtotal. Quantities come from Mealplan-owned immutable component snapshots and selected production portions. Read in one bounded repeatable snapshot, reuse exact rational conversion/cost rules, and round the aggregate once. Optional module unavailability is explicit; source-generation mismatches cannot revive old identifiers. Source preparation is costed once; only a currently reviewed leftover link adds no second preparation cost and explains whether its source was costed in this week. Unlinked or stale leftover intentions remain unknown. Ingredient-use cost is distinct from checkout expenditure and existing stock is not free food. An incomplete estimate cannot be labelled within budget.

Autoplan uses at most20 explicitly selected recipe candidates, at most28 selected empty date/slot pairs,42 total meals,500 aggregate candidate ingredient lines and16MiB total snapshots. These are visible limits, not a global optimum claim. Each slot has reviewed positive portions. Closed integer weights0–100 cover free-stock coverage, valid date urgency, effort, known cost and repetition. Unknown values are explained rather than rewarded as zero. Invalid/passed use-by dates receive no urgency benefit. Stable IDs break score ties.

The simulator subtracts existing active demand from a private availability pool while accounting for the plan's existing reservations exactly once, then decrements the same simulated pool after each selected proposal. Alternatives are evaluated against the slot's correct remaining pool. Preview never writes stock. Users can replace a proposal with another reviewed candidate or leave a slot blank. Existing, cooked and linked meals are preserved. A bounded five-minute signed review binds actor/session/authority, plan/settings versions, input, module generations, immutable source versions and stock/price dependency fingerprint. Apply rechecks those dependencies, holds the established mutation locks, saves every selected meal/component in one transaction and stores one idempotent receipt. A matching committed replay returns the original result after current Mealplan authority verification; stale uncommitted previews require renewed review.

### Calendar

Initial feeds are explicitly tied to one plan and one seven-day window (maximum42 current meals), valid for90days unless revoked earlier. They are not rolling subscriptions. Create a new scoped feed or rotate by create-and-revoke. Secret32randombytes are shown once; only SHA-256 is stored. Feed authority binds issuer user/membership, captured auth version and role, Mealplan generation, plan and window. Every read checks current active user/membership, password-change state, auth version/role, module state/generation, expiry and revocation before any response/conditional handling. A feed does not require the original browser session to remain alive; logout does not falsely promise revocation, whereas password reset/account or membership changes invalidate it.

Events are all-day `DATE` values with exclusive next-day end, a stable household/generation/meal UID, current sequence, UTC modification stamp and status. Summary contains title and slot; cancelled meals and minimal deletion/move tombstones emit cancellations without retaining deleted human content. A move outside the scoped week leaves a cancellation for the old date; a current event in the same scope supersedes its old tombstone. Bound cancellation history and fail clearly rather than truncate. No notes, participants, ingredients, costs, attachments or instructions are exported. Use CRLF, TEXT escaping and UTF-8-safe75-octet folding. Responses are private/no-store, no service-worker persistence or browser local-storage token copies; raw paths/queries/secrets are excluded from logs/errors. Download and feed UI explain the limited sharing scope.

## Architecture and schema specification

Five new tables remain inside Mealplan; no foreign keys into optional Recipes/Pantry/Shopping:

| Table | Owned fields and constraints |
|---|---|
| `mealplan.leftover_sources` | UUID/household/plan/source meal/source component, unique household+component, exact positive expected portions, reviewed source meal/component versions, version/actor/timestamps; composite same-household own-table foreign keys and quantity guards. |
| `mealplan.leftover_allocations` | UUID/household/plan/source/target meal, unique household+target meal, positive exact portions, reviewed target version, own version/actor/timestamps; same-household own-table foreign keys and source-capacity validation under source lock. |
| `mealplan.weekly_settings` | UUID/household/plan/week_start, unique household+plan+week, nullable nonnegative EUR minor budget, bounded validated manual-estimate/rule JSON, version/actor/timestamps. |
| `mealplan.calendar_feeds` | Household and plan identifiers, issuing membership, Mealplan generation, SHA-256 capability digest, captured issuer authority, expiry and revocation; bounded active feeds per issuer. Secret is never a column or audit value. |
| `mealplan.calendar_tombstones` | Household/plan/meal UUID/original date, generation, sequence/deletion timestamp; unique household+generation+meal+date; no title or other deleted content. |

Forward migrations preserve all81 released SQL bytes. Core28 extends audit policies95→100 and exact readiness; Mealplan8 creates tables and narrowly scoped secure lifecycle guards. Fuel7 advances its exact Core27 authorization/readiness fence to28 without weakening unknown-version denial. Pantry13 updates only its captured Core-audit function fingerprint while proving the accepted predecessor. Recipes5 requires no compatibility migration. The runner stages Core26/legacy histories explicitly capped at Recipes4/Fuel5/Pantry12/Mealplan7, then Core27/Recipes5/Fuel6, then Mealplan8/Core28/Fuel7/Pantry13, and only then installs final audit triggers. Restore/ready/grants/security-definer ownership/search_path and predecessor upgrade tests cover this complete order. New own-table fields use existing audit redaction policy; secrets/human content are omitted.

HTTP contracts live below `/api/v1/planning/{plan}`: `leftovers`, `budget`, `autoplan/preview`, `autoplan/apply`, `calendar.ics`, `calendar-feeds`, and `calendar-feeds/{feed}/revoke`. Public read-only calendar capability retrieval is `/calendar/mealplan/{token}.ics`. Strict JSON, CSRF, bounded requests/timeouts, current authority, module generation, named conflicts and idempotency apply. Source readers and transaction-local planning saves are reused through Core contracts; no component snapshot is replaced from a changed live recipe silently.

Implementation owners: migrations/leftovers lane; budget/autoplan lane; complete UI lane; Root owns common command/read helpers, HTTP/calendar, shared integration coordination and evidence. Owners may prepare tests during implementation but execute none until the entire module source gate closes. Existing shared files have one assigned writer at a time.

## Visual and interaction specification

Preserve DESIGN tokens, local assets, native dialogs,44px controls and16px inputs. Desktop keeps seven day columns;390px mobile keeps the chronological agenda.

```text
Wochenplanung                  [Woche] [Kalender] [Woche vorschlagen]
Budget: 42,18 € bekannt · 3 Preise fehlen     [Details / Ziel bearbeiten]
Montag · Abendessen
  Gemüsepfanne · Produktion6 Portionen
  Geplante Reste:4 · davon2 für Dienstag    [Reste prüfen]
Dienstag · Mittagessen
  Gemüsepfanne vom Montag ·2 von2 geplant
  Prognose, kein Vorratsbestand             [Quelle ansehen]
```

Budget details expand into known/stale/unknown ingredient rows and day/meal totals. Source review displays production, expected surplus, already allocated and free forecast portions together. Autoplan has criteria, preview and explicit apply stages with per-slot reasons/alternatives; no empty preview is a success state. Calendar panel offers authenticated download, scope/expiry preview, one-time token display/copy fallback and revocation. Dirty meal editors and proposal drafts survive refresh/conflict; navigation never silently discards them. Readers, offline/disabled modules, missing data, expired proofs, uncertain writes, narrow screens, keyboard Escape/focus return and reduced motion receive deliberate states. No secret survives panel close/navigation in persistent client storage.

## Chronological checklist

### Phase1 — scope before implementation

- [x] 001 Read product/design/implementation and current module status.
- [x] 002 Verify Recipes acceptance and preserve unrelated Finance draft.
- [x] 003 Inspect all four remaining Mealplan criteria.
- [x] 004 Survey frozen components, versions, participants and existing bounds.
- [x] 005 Survey source, demand, reservation and purchase contracts.
- [x] 006 Survey generic/workspace mutation and copy paths.
- [x] 007 Inspect current price and exact-conversion behavior.
- [x] 008 Inspect suggestion coverage and whole-week simulation gap.
- [x] 009 Inspect current authorization, lifecycle, logging and cache boundaries.
- [x] 010 Identify Core/Fuel/Pantry migration compatibility dependencies.
- [x] 011 Pin surplus, budget, feed, autoplan and resource contracts above.
- [x] 012 Publish complete module plan before changing behavior.

### Phase2 — whole module implementation; no test execution

- [x] 013 Mark only MP08/13/15/19 in progress and regenerate checklist.
- [x] 014 Assign disjoint parallel lane ownership and shared interfaces.
- [x] 015 Add shared current-authority read transaction helper.
- [x] 016 Add shared idempotent versioned command transaction helper.
- [x] 017 Define strict request/result structures and bounded validation.
- [x] 018 Add five Mealplan tables with composite ownership constraints.
- [x] 019 Add own-table indexes and minimal role grants.
- [x] 020 Add source/target forecast guards and deterministic lock order.
- [x] 021 Add source review records and exact capacity calculations.
- [x] 022 Add allocation create/update/delete commands.
- [x] 023 Add reciprocal source/destination read projections.
- [x] 024 Preserve partial target coverage and unresolved states.
- [x] 025 Reconcile source component and quantity changes.
- [x] 026 Reconcile meal date/slot/kind/status changes.
- [x] 027 Cover generic mutations and deletions with the same invariants.
- [x] 028 Copy and remap in-range source/component/target identities.
- [x] 029 Reject crossing copy-window links with a named reconciliation conflict.
- [x] 030 Keep every forecast out of stock and purchasing authorities.
- [x] 031 Add weekly budget/rule settings and optimistic versions.
- [x] 032 Add reviewed version-bound manual meal estimates.
- [x] 033 Batch frozen ingredient quantities and authorized price metadata.
- [x] 034 Reuse exact conversions and round aggregate cost once.
- [x] 035 Expose known totals, missing/stale reasons and price provenance.
- [x] 036 Count source preparation once for planned leftovers.
- [x] 037 Show comparable alternative prices and uncertainty.
- [x] 038 Copy week target/rules and remap reviewed manual estimates.
- [x] 039 Build bounded deterministic candidate and slot inputs.
- [x] 040 Build one private available-stock pool for the selected week.
- [x] 041 Account existing demand/reservations once before proposals.
- [x] 042 Decrement shared simulated availability per selected proposal.
- [x] 043 Score closed weighted rules with explicit unknowns.
- [x] 044 Exclude unsafe date bonuses and stabilize tie ordering.
- [x] 045 Expose reasons, limits and per-slot replacements/blank choices.
- [x] 046 Bind signed review to actor/session and all relevant dependencies.
- [x] 047 Revalidate preview dependencies before atomic apply.
- [x] 048 Save all selected meals in one transaction and one receipt.
- [x] 049 Preserve existing meals and prohibit simulation side effects.
- [x] 050 Add personal bounded calendar feed lifecycle.
- [x] 051 Generate one-time random tokens and store hashes only.
- [x] 052 Recheck issuer/module/generation/expiry authority on every feed read.
- [x] 053 Add title-only all-day calendar projection.
- [x] 054 Add stable UIDs, monotone sequences and UTC modification stamps.
- [x] 055 Add minimal deletion/move cancellation tombstones.
- [x] 056 Implement TEXT escaping, CRLF and byte-safe folding.
- [x] 057 Add authenticated downloads and public scoped feed handlers.
- [x] 058 Add no-store/referrer/logging/cache/token privacy boundaries.
- [x] 059 Add complete leftovers editor, capacity review and reciprocal links.
- [x] 060 Add weekly budget summary/details/target/manual estimate forms.
- [x] 061 Add calendar scope, download, one-time display and revocation panel.
- [x] 062 Add autoplan criteria/preview/replacement/apply panel.
- [x] 063 Integrate panels with existing editor/week events and drafts.
- [x] 064 Add keyboard/focus/mobile/loading/error/offline/Reader states.
- [x] 065 Integrate all local embedded assets and strict server routes.
- [x] 066 Add Core28 audit100 policies and exact readiness.
- [x] 067 Add Fuel7 and Pantry13 predecessor-proven compatibility migrations.
- [x] 068 Stage migration barriers and preserve all81 predecessor SQL bytes.
- [x] 069 Extend disable/purge/cleanup/restore ownership behavior.
- [x] 070 Prepare meaningful Go unit/integration and migration test sources.
- [x] 071 Prepare UI/Node/browser test sources and the populated recovery fixture contract.
- [x] 072 Prepare security/race/whole-module regression selections.
- [x] 073 Update README/API/operator documentation for actual source behavior.
- [x] 074 Independently review source integration and resolve every lane finding.
- [x] 075 Freeze complete module source and record hash manifest.
- [x] 076 Close whole-source gate before executing module tests.

### Phase3 — tests and module-wide correction, only after Phase2

- [x] 077 Execute all relevant Go unit tests and retain terminal results.
- [x] 078 Execute complete Node/UI contract tests and syntax checks.
- [x] 079 Verify pinned migrations, immutable predecessors and static SQL contracts.
- [x] 080 Execute fresh PostgreSQL migration/readiness/grants/audit checks.
- [x] 081 Execute populated predecessor upgrades through all staged barriers.
- [x] 082 Execute leftover exact-capacity and concurrent allocation cases.
- [x] 083 Execute partial coverage, chronology, stale review and deletion cases.
- [x] 084 Execute generic/workspace/move/swap/copy invariants together. Existing module14/full30Planning38 plus FinalE's nine linked move/swap cases pass, including valid/stale review and rejected chronology with atomic no-effects.
- [x] 085 Prove forecast writes produce zero stock/cooking/purchase side effects.
- [x] 086 Execute frozen-snapshot cost/conversion/rounding cases.
- [x] 087 Execute unknown/stale/disabled/purged pricing and manual estimates. Existing unit/module14/browser87 cases plus actual Pricing22runeca8155e prove price deletion, replacement, disable and genuine encrypted-restore/Shopping-purge unknown states without changing the frozen plan or actual3kg stock; independent saved-file review passed.
- [x] 088 Execute source-once and cross-week leftover costing. Actual browser87 proves200c once, later-week0c, stale target unknown and explicit re-review.
- [x] 089 Execute deterministic scoring, unknowns and tie cases.
- [x] 090 Prove multi-meal simulation cannot spend the same stock twice.
- [x] 091 Execute existing-demand/reservation accounting and replacement cases. FinalE's two real reviewed-demand/own-reservation cases pass with the original physical/receipt assertions; existing preview replacement workflows remain covered by module14/browser87.
- [x] 092 Execute stale proof/source/settings/stock/price/role/session denials. Existing expiry/generation/stock cases and FinalE's seven real signed HTTP dependency/current-authority withdrawal cases pass; no permission guard is relaxed.
- [x] 093 Execute atomic apply failure, replay and concurrent writer cases.
- [x] 094 Execute ICS escape/fold/date/DST/UID/sequence/cancellation cases.
- [x] 095 Execute feed issuer/expiry/role/password/module/generation/revoke denials. Native calendar cases plus actual browser87/recovery17; synthetic authority mutations remain explicitly labelled.
- [x] 096 Prove feed privacy in HTTP logs/errors/headers/service worker/storage. Actual no-store downloads, public revoke, log checks, active SW/cache and IDB keys/values/local/session storage inspection passed.
- [x] 097 Execute full native regression with exact test identities and coverage. Independent FinalE replay verifies651ordinary passes as545qualified historical+106fresh, plus corrected targeted3/21and controlled Push3/7with0fail/skip. Ordinary72.0722501% atomic coverage is unchanged; no all654fresh/single-source or supplemental-coverage-merge claim.
- [x] 098 Correct actual failures and repeat affected checks with honest lineage. All three failed native campaign reports remain failed; corrected genuine predecessor and explicit ingredient-review fixtures pass their actual bounded follow-ups, followed by sole-test transfer with all606preimages preserved.
- [x] 099 Execute scoped Linux race on authorization and concurrent module paths. FinalE executes the corrected three mains and controlled Push under actual Linux race instrumentation; qualified ordinary shard artifacts retain their distinct historical source identities.
- [x] 100 Execute vet, linter, Gosec, vulnerability and dependency checks. A5F11-gate scanner/dependency evidence retains its original scope; current607taggedVet/Lint0issues/gofmt also passed, with no new scanner run inferred.
- [x] 101 Build optimized non-root runtime/backup images and validate Compose. Actual209runtime/OCR/backup UID10001/source pins; Compose configuration-only checks, no production startup claim.
- [x] 102 Execute real390px/desktop browser workflows for all four features. Final staged209run280f passes87checks and eight actual views.
- [x] 103 Execute Reader/keyboard/conflict/dirty-draft/uncertain-write workflows. Actual standalone87/shared88 plus full30Planning38 prove Reader boundaries, keyboard focus, conflict/draft preservation and exact same-key recovery.
- [x] 104 Execute real browser feed download/revoke and private-cache checks. Actual Reader sharing, issuer-only revoke, private download and active-SW67-cache-entry/four-IDB privacy checks passed.
- [x] 105 Execute clean Axe scans and visually inspect generated screenshots. Eight clean scans/eight independently viewed hash-verified PNGs; initial visual failures remain preserved separately.
- [x] 106 Execute complete existing browser regression and fix real regressions. Actual fresh run771087c4 passes30components/1,066checks/151clean Axe scans/163saved PNGs; the preserved legacy-selector failure is corrected only in its two semantic test selectors.
- [x] 107 Execute populated encrypted backup/restore with new module data. Actual recoverye6b4 passes17stages, populated nine-table scope/three keys/audit100; synthetic operator and empty-upload scope disclosed.
- [x] 108 Execute current-owner purge/generation/old-intent/independence checks. App denial, Owner purge, generation1→2, old-intent409 and independent module rows passed.
- [x] 109 Confirm exact owned test-resource cleanup and preserve failed reports. Actual raw-only cleanup removes five private roots/16credential files after114byte-exact public raw copies; six frozen source archives intentionally remain. Final independent114-hash/source/summary checks and actual post-census pass; original failed campaigns and both refused retention attempts remain preserved. Historical E replay precedes the sole-test transfer/private removal and is not claimed rerunnable afterward.
- [x] 110 Independently reconcile source/test/browser/security/recovery evidence. Actual FILE-only joint report `outputs/mealplan-module-validation/joint-acceptance-c37d3fcfaf694066a6412871e8c12927.json` SHA`bca63809a449582ae2abd13ffbd61ed85221027babe7ca05b7cc86f2064a2bf1` passes functional acceptance with no material blocker and retains every disclosed lineage/runtime limit.

### Phase4 — acceptance and immediate continuation

- [x] 111 Record actual passes, original failures, corrections and limits. The validation ledger records qualified651+fresh3/21+Push3/7, actual browser/recovery/pricing, exact current-source lineage and final raw-retention outcomes without rewriting failed originals.
- [x] 112 Jointly mark four features verified only after module acceptance. Independent functional joint acceptance, exact indexed scan and actual main checkpoint pass; MP08/13/15/19are jointly verified, Mealplan20/20.
- [x] 113 Regenerate checked task list to101verified/0inprogress/79open. Root updates the authoritative improvements list and regenerated task view only after the actual85-file main checkpoint.
- [x] 114 Perform exact staged-blob secret scan and immutable SQL proof. Approved repeat scans809indexed blobs with0findings/index unchanged;85SQL files and all81released predecessor bytes are exact. The preserved initial schema-prose false positive is corrected only by precise terminology, with no scanner-rule exclusion.
- [x] 115 Save local module checkpoint without push or live deployment. Actual main checkpoint `acfe08fb533222693044d2cf2c4d27ea3dc0d607` contains85paths/tree5cc7aaa1; recordSHA`4d8d29198e0aad28755e1367a572c515756e2c51ca20af401b555cb0ba612507`. Separate final documentation-only packaging follows; no second checkpoint is inferred.
- [ ] 116 Continue Finance, then Chores, Meters and Documents until180complete.

## Standards and evidence policy

Calendar encoding/date rules follow [RFC5545](https://www.rfc-editor.org/rfc/rfc5545.html). Token/log redaction follows the [OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html). Runtime evidence will be recorded separately; source, mocks, fixture-only results, historical unchanged-source passes, hosted CI, vendor calendar behavior and production deployment must never be conflated.
