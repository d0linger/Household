# Planned stock reservations and differential meal demand

Status: read-only pattern/design work for PA12, MP18, MP14 and MP17. These four checklist items remain open. This document implements no reservation, shopping contribution or lifecycle behavior. PA11 acceptance is a separate workstream.

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

These are logical responsibilities; the implementation will choose the smallest auditable table set after a focused source review. Core audit policies mask human plan content. Runtime privileges, narrowly scoped guards and readiness are explicit. New migrations preserve historical files and follow the household maintenance barrier.

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

## Planned implementation sequence and acceptance

- [ ] Confirm bounded requirement/lease/contribution states, mappings and allocation limits.
- [ ] Add Core schema/privileges/audit/readiness and immutable operation proofs.
- [ ] Add pure exact demand/allocation/undercoverage and local-calendar-to-UTC expiry tests.
- [ ] Implement read-only snapshot demand and explicit mapping selection without live Recipe dependence.
- [ ] Implement reviewed atomic reserve/release with shared lot locks and current movement basis.
- [ ] Integrate source changes, generic CRUD, move/swap and deletion release paths.
- [ ] Integrate disable/reactivate/purge generation and tombstone behavior.
- [ ] Add immediate expiry filtering and bounded race-safe terminal housekeeping.
- [ ] Expose physical/reserved/free/undercovered values in Pantry and planning review.
- [ ] Verify two-plan contention, concurrent consumption/counting and late-write rollback.
- [ ] Verify actual source/target purges, no revival after receipt pruning and independent Recipe purge.
- [ ] Complete PA12/MP18 browser/accessibility, migration/restore, race/security and independent finish evidence.
- [ ] Add MP14 differential coverage using selected-own versus other reservations once.
- [ ] Add MP17 durable source contributions and explicit create/update/remove preview.
- [ ] Integrate Shopping manual edits/check/delete/merge and checkout storage dispositions.
- [ ] Verify unchanged repeated refresh, changed source portions, equal-name ambiguity and checked-item protection.
- [ ] Verify checkout cannot double-count stored goods as both physical and pending coverage.
- [ ] Complete final regression and independent review; only then update the four fixed checklist IDs.

The independent database pattern scan was source-only. No operation in this plan has been executed or accepted merely because a similar existing pattern exists.
