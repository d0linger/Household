# Stock reservations and planned differential meal demand

Status: PA12/MP18 locally verified at Core19/pantry7/mealplan3, at the scopes in stock-reservations-verification.md. MP14 is in development as described in planning-demand.md; MP17 remains open. Current checklist:52 verified/1 in progress/127 open; the reservation checkpoint had52 verified/128 open.

## Delivery order

First complete PA12/MP18 together: explicit lot reservations and every release boundary. Then implement MP14/MP17 against those contracts: differential demand and reviewed updates of attributable shopping positions. Outbox notifications cannot substitute for transactional quantity correctness.

## Core contracts

Optional module schemas remain independent. Core owns generation-bound identities and terminal history; no cross-module foreign key is introduced.

| Logical record | Identity and required information |
|---|---|
| Confirmed planning requirement | Household, Mealplan generation, plan/meal/component/ingredient identities, frozen source fingerprint, relevant portion/date/status revision, explicit Pantry product/unit mapping and Pantry generation. Names and dates are descriptive, never unique identities. |
| Lot reservation revision | Requirement identity, Pantry generation/product/lot, exact amount/base unit, expected lot/product/movement basis, version, fixed UTC expiry, active/released/fulfilled state and terminal reason. Release history is retained. |
| Shopping contribution revision | Requirement identity, Shopping generation/list/item, desired and last-applied exact quantity/unit, expected item version, active/purchased/stored/missing/manual-review disposition. Begin with one attributable open item per source; visual grouping does not imply destructive merging. |
| Operation proof | Actor/request key and hash with immutable result or minimal effect identity, retained independently of expiring general idempotency responses. An old request cannot reactivate a released revision or recreate effects after an actual module purge. |

These are logical responsibilities across the full four-item roadmap. The first PA12/MP18 slice deliberately supports explicit per-meal lot claims; inferred ingredient requirements and Shopping contributions remain MP14/MP17 work. Core audit policies mask human plan content. Runtime privileges, narrowly scoped guards and readiness are explicit. New migrations preserve historical files and follow the household maintenance barrier.

## Quantities and stock scope

1. Scale each independent component snapshot using its own confirmed portions. Aggregate duplicate known demand by product/unit before subtracting stock.
2. Distinguish all physical stock, eligible stock under the named selection filter, active reservations, undercoverage and free eligible stock. PA11's physical sum includes archived locations; planning selection normally excludes them. Do not silently reuse either total as the other.
3. Allocate explicit eligible positive lots. A FEFO sample explains ordering but never supplies the full aggregate. Keep MHD, use-by and sourced opening hints distinct; ordering is not a guarantee that food is usable.
4. For selected sources S and lot l, usable quantity for S is `max(physical_l - active_reservations_of_other_sources_l, 0)`. Own reservations already belong to that share. Never subtract them twice or add them back without a physical cap.
5. Show unknown quantities, unassigned products, incompatible units and inexact six-decimal booking amounts as incomplete. Partial actions require explicit inclusion/skip decisions; unknown is not zero shortage.
6. If real consumption, waste or inventory lowers physical stock beneath reservations, preserve the real journal and show undercoverage. Do not move a reservation to another lot automatically or prevent honest inventory counts to preserve a plan.
7. Expired UTC leases stop counting immediately; a bounded housekeeping job later records their terminal state. Extending or recreating a reservation requires a current explicit review.

## Snapshot and lifecycle rules

Plan-owned mappings survive Recipe catalogue/source deletion. Global ingredient bindings may suggest an initial product, but do not silently retarget an already confirmed plan. Copies receive new requirement identities and no reservation or shopping history.

Portion/source/component/date/slot/status changes and deletion release affected reservations atomically, including generic CRUD paths. Define a demand revision so a harmless title/note edit need not release quantities. Move/swap must update all affected requirements once. Deactivating Mealplan or Pantry releases active reservations under the existing exclusive household barrier; reactivation never recreates them automatically. Purge retains minimal Core tombstones while removing sensitive payload and active references. Pantry-only use remains possible.

## Mutation and concurrency order

Use BeginMutation/current session/module generations, then the plan and selected meals/components in deterministic order, existing lot/product stock locks, Core contract rows and finally an optional Shopping destination. Secondary contested locks fail promptly. Read current projections and other reservations in new statements after the relevant locks. Two plans reserving one lot contend on the same lot used by consumption and inventory.

Housekeeping locks only bounded expired Core rows with SKIP LOCKED and expected version/expiry predicates; it must not establish an inverse plan/lot lock chain. A renewal sees a terminal/version conflict and asks for a new review. Retain the current resource limits:42 meals/week,10 components/meal,500 reviewed ingredient lines,16MiB source copies and existing bounded HTTP input. Explicitly cap allocation fan-out before implementation; never solve it with unbounded locks or JSON.

## Repeatable Shopping behavior

Compute the desired net purchase N from demand and eligible physical coverage first. Compare it with the existing attributable open quantity Q: proposed delta is N-Q, and additional demand is max(N-Q,0). Do not first subtract the contribution being updated; that creates an oscillating smaller target on every refresh.

Only an unchanged, open, attributable item may receive a reviewed update. Manual edits, checked purchases, deletion, attachments, merges or a changed destination generation require an explicit conflict decision. Unlinked equal names are not assumed to be the same product. Cancellation releases reservations; it does not erase purchased history.

Checkout with actual Pantry intake marks the corresponding contribution stored in the same transaction. Otherwise physical stock plus a still-counted shopping contribution would count the same goods twice. Checkout without intake leaves coverage explicitly unresolved or user-confirmed; a checked item is not automatically Pantry stock.


## Implemented first slice: explicit per-meal claims

- Core owns two tables: meal_reservations combines the minimal durable operation identity with a lease's terminal lifecycle, and meal_reservation_allocations stores detailed product/lot/base-unit amounts. Runtime cannot delete either table's records or modify allocation quantities. Purge removes allocation payload using the maintenance role and retains terminal minimal proofs. No optional-module foreign key is added.
- Each deliberate review creates a fresh immutable lease ID. A source-serialized monotone revision identifies the newest lease even after clock rollback; state version guards release/replacement. One active recorded root per meal/generation is enforced independently of the expiry worker. Existing expired roots are explicitly replaced rather than reopened.
- Reserve accepts at most100 distinct lots, each with exact positive amount, current product/lot/location versions, physical stock, movement count and other-meal claim basis. Whole-unit products cannot receive fractional claims. Locks use ordered sources/lots and fresh READ COMMITTED reads; replacing a lease excludes its own prior share once and caps availability by physical stock.
- Expiry is explicit in the reviewed request: in the future, at most31 days ahead and no later than the planned household calendar day's end. The page proposes the earlier bound and names the household timezone. Read-side state and quantity aggregates use one statement timestamp. Effective expiry needs no worker; housekeeping terminalizes at most100 locked roots with version/expiry rechecks and no subsequent source/lot lock.
- Source AFTER-hooks invalidate relevant meal/component changes and plan-template transitions, including generic CRUD. Title/notes, component effort and ordering remain irrelevant. Recipe purge preserves frozen plan-owned components and these manual claims. Mealplan/Pantry disable or generation change releases active roots under the household barrier; direct SQL uses NOWAIT to avoid an inverse wait.
- Reserve keys/hashes survive general idempotency pruning. Replay returns the original immutable identity plus explicitly current lease state, never another effect. Release addresses one exact lease ID and is naturally repeatable, so retry cannot release a successor. Changed source/stock/claims require a new review. A late allocation failure rolls back the old lease's release and every new record.
- The planning action opens a bounded, code-led extension of the existing workspace. Named lot review separates physical, own, other, total reserved, free and undercovered amounts. Editors assemble an explicit draft, confirm every line/expiry/replacement, recover uncertainty with frozen requests and release an identified lease. Reader pages expose no mutation controls. This is manual reservation, not inferred Recipe demand or a Shopping update.

Current execution: all12 reservation contracts, repeat/repair, five historical upgrades and both affected projection/repair cases pass together (web188.649s). Complete native backend regression passes at70.4% (6172/8765; web864.116s), at the compiled-candidate scope recorded in implementation.md. The corrected18-component browser chain passes625 checks, including41 reservation checks/10 clean axe scans/10 captures. Independent F01 follow-up is resolved/ship at its screenshot/source/report scope; separate design documentation preserves the existing system. Scoped Linux race passes at web322.439s for the named reservation/helper/projection/upgrade/repair/configuration cases; packages with no matches do not receive whole-tree acceptance. PA12/MP18 are locally verified. Earlier failed/superseded fixture, audit-assertion and pre-readiness browser attempts remain in the implementation ledger.

Exact-index-export encrypted synthetic recovery passes: backup7.76s/restore20.72s, restored exact0.250001 claim, revision/version1, request identity/hash, frozen source basis, readiness, all earlier domain values and two file hashes. No restored authenticated reservation replay or production RTO/RPO is claimed.

## PA12/MP18 acceptance and next sequence

- [x] Bound explicit leases to100 named lots with current source/stock review.
- [x] Add two Core tables, secure guards, runtime privileges, audit and readiness.
- [x] Verify exact physical/own/other/free/undercovered arithmetic and input bounds.
- [x] Implement reviewed atomic reserve/replacement and exact-lease release.
- [x] Integrate source edits, generic CRUD, move/swap and deletion release paths.
- [x] Integrate disable/reactivate/purge generation and minimal durable proofs.
- [x] Filter expiry immediately and terminalize at most100 locked roots per job.
- [x] Expose Pantry and planning quantities without mixing historical/current data.
- [x] Verify contention, real consumption, late rollback and same-balance journal conflicts.
- [x] Verify receipt pruning, actual purges and independent Recipe purge.
- [x] Complete browser/accessibility, upgrade/restore, scoped race/security and independent finish evidence.
- [ ] Add MP14 plan-owned ingredient mappings and whole-meal differential coverage.
- [ ] Add MP17 durable source contributions and explicit create/update/remove review.
- [ ] Integrate Shopping manual edits/check/delete/merge and checkout dispositions.
- [ ] Verify stable repeat refresh, changed portions, equal-name ambiguity and checked-item protection.
- [ ] Verify stored goods cannot count as both physical and pending shopping coverage.
- [ ] Complete separate MP14/MP17 regression and review before advancing those IDs.

The independent database pattern scan was source-only. No operation in this plan has been executed or accepted merely because a similar existing pattern exists.

## Next-slice source inventory (MP14/MP17 research only)

The existing planning workspace owns frozen component snapshots and separate component portions (`mealplan_read.go`, `mealplan_values.go`). Its current client-side ingredient preview scales each component but neither persists ingredient/product mappings nor computes differential stock coverage. The Recipe comparison's `ingredientDemand` and `IngredientAmount` provide exact rational conversion/display helpers; its stock sum is physical stock in active locations, not reservation-aware planning availability. Reuse those arithmetic primitives, not that stock total or a live Recipe lookup, for MP14.

MP14 must aggregate repeated mapped products before assigning coverage. For example, two component lines requiring0.6kg each against1kg available produce0.2kg shortage, not two independently covered lines. With0.6kg physical stock,0.4kg own claim and0.3kg other-meal claim, selected-meal usable stock is0.3kg; adding the own claim to free stock without a physical cap is incorrect after real consumption. Lot-level undercoverage must remain visible even where another lot has surplus. Unmapped or inexact demand remains unresolved rather than silently becoming zero.

The first differential preview must select whole meals. Existing manual claims belong to a meal rather than one ingredient/component, so their own share cannot safely be attributed to a component-only selection. Stable mapping identity is household, Mealplan generation, component ID and frozen ingredient ID; a catalogue ID or recipe ID cannot distinguish independent components. Current Recipe bindings may suggest a mapping, but a confirmed plan-owned mapping must remain readable after actual Recipe purge without joining its catalogue.

MP17 must distinguish the desired net purchase from its previous attributable contribution. Demand2kg with0.5kg eligible coverage gives a stable target1.5kg. An unchanged open contribution1.5kg therefore needs no update; subtracting that contribution before computing its replacement target causes oscillation. Persist plan/component/ingredient identities, frozen mapping/source basis and destination generation before enabling updates. A manually edited, checked, merged, deleted or actually stored contribution requires its own explicit lifecycle decision. These examples are next-slice acceptance requirements, not implemented or executed MP14/MP17 evidence.
