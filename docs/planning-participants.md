# Participants, guests and explicit portion modes

Status: MP06 in progress, materialized in the application after local checkpoint ecc8391. Core23/Mealplan7, Store/read/copy and PWA integration are implemented. Eight native PostgreSQL contracts pass together (web55.802s final repeat); an additional activity-lock contract passes(web10.515s). The final740-check browser chain, current-image encrypted recovery, scoped independent impact-correction verdict and separate design comparison pass. Complete native regression is running; scoped Linux race remains pending. The [verification ledger](planning-participants-verification.md) and [180-item checklist](improvements-180.md) separate implementation from acceptance.

## User contract

A meal can retain a bounded selection of household members and an anonymous guest count. Each selected member and guest contributes one suggested portion. Users explicitly choose either participant-derived portions or a positive exact manual amount. Manual override preserves the roster and guest count, so returning to derived portions is deliberate and understandable.

Existing meals stay manual. Existing recipe components keep their independent effective amounts. Each component can explicitly follow the meal's effective portions or retain its own positive exact amount; existing recipe snapshots and source identities do not change. Participant changes never directly create Shopping records or consume physical stock.

Initial limits are100 unique household memberships and100 guests per meal. Refuse excess rather than truncating it. Derived zero is invalid; manual portions may be fractional and use the existing twelve-integer/six-fractional-digit decimal boundary. An unknown quantity is not zero. No age, dietary, guest-name or implied nutritional profile is introduced.

## Model and invariants

The forward migrations are Mealplan7 and Core23. The prior59 migration files remain unchanged; the development manifest contains61 hashes.

| Owner | Record | Contract |
|---|---|---|
| Mealplan meal | `servings_mode` | `manual` or `participants`; existing rows default to manual. |
| Mealplan meal | `guest_count` | Integer0–100; existing rows default to0. |
| Mealplan meal | Existing `servings` | Effective positive exact decimal, consumed by existing readers. Derived mode equals saved roster count plus guest count. |
| Mealplan roster | Meal-owned participant rows | Household, local meal and Core membership identity; unique meal/membership pair. Composite household foreign keys, local meal cascade, no cross-optional-schema foreign key. |
| Mealplan component | `servings_mode` | `independent` or `meal`; existing rows remain independent. |
| Mealplan component | Existing `servings` | Effective positive exact decimal. Linked mode must equal its parent meal's effective portions. |

Deferred final-state checks enforce roster bounds, derived equality and linked-component equality. Roster writes take the parent meal/plan locking path and advance versions. Avoid recursive synchronization triggers: the existing component guard already updates its meal. The dedicated Store transaction writes intended effective quantities; guards validate the committed result.

Core23 registers the additional participant audit policy, bringing the verified schema census to74 policies and48 optional-domain tables. Readiness checks exact FK/Unique columns, valid ready indexes, individual runtime grants, trigger identity/type/deferral/search path and CHECK expressions/columns. Aggregate portion modes and guest counts are safe audit fields; membership identities and free person names are redacted.

## Membership and compatibility

Newly selected members must belong to the current household and be active, with the user and membership checked under shared row locks. A participant already saved in a meal remains recorded after account deactivation. Deactivation must not silently change planned quantities. Historical inactive names are visibly marked; no new selection list claims they are currently active.

The existing [membership reference search](../internal/web/references.go) already supports active choices and individual historical references. Preserve its50-row searched pages and selected labels; a roster boundary must not become an accidental first-page boundary.

An optional nested participation input must distinguish omitted fields from explicitly clearing the roster. Legacy clients retain existing participant data and component-link modes. A legacy quantity change incompatible with an existing linked/derived mode fails with a useful workspace message, rather than silently switching modes or ignoring the user's amount. Normalize membership UUID case and sort identities before the canonical mutation hash; reject duplicates, including mixed-case duplicates.

Active-member validation belongs to the service, alongside the household-bound database foreign key, following the existing Shopping assignment pattern. A blanket database rule requiring every inserted participant to be active would incorrectly reject a deliberate copy of a source meal with a now-inactive member. Do not introduce a freely supplied bypass flag.

## Transaction and existing integrations

Extend the existing [planning mutation](../internal/store/mealplan_mutation.go), [save](../internal/store/mealplan_save.go), [bounded read](../internal/store/mealplan_read.go) and [week-copy](../internal/store/mealplan_move.go) paths. Keep current session/generation authorization, plan/meal/component versions, idempotency and parameterized queries. Lock parents before children, with ordered NOWAIT secondary locks; reject overflow rather than skipping a contested member.

Save the roster, guests, meal mode/effective amount and every linked component's effective amount atomically. A later failing guard must roll back versions, roster, components, audit, projections and request evidence. Read the bounded roster in the existing bundled meal query, including a sentinel; do not query each member separately.

Physical demand reads component amounts. Merely changing a meal's display portions would leave its procurement wrong. Linked amounts therefore flow into the existing MP14 comparison. Actual portion changes already invalidate PA12 leases and require a fresh MP17 source decision; verify these real paths. Preserve component IDs and frozen snapshots so explicit ingredient/product mappings survive. A roster change at equal total has no automatic inventory effect.

A week copy preserves roster, anonymous guests, modes and effective quantities, including deliberately copied inactive participants. It creates new identities with status open and carries no reservation, Shopping contribution, checkout history or prior receipt. Moves and swaps preserve the meal-owned roster. Meal deletion and actual module purge remove local participant payload; independent Core users and memberships remain intact.

## Interface

Extend the established planning editor. A labelled participant section offers searched household-member selection, a guest count and an explicit portion-mode control. Show the calculation, for example “2 Haushaltsmitglieder +1Gast =3Portionen,” next to the editable manual override. Distinguish saved inactive members without dropping them.

Each existing component exposes “Use meal portions” versus “Own portions,” with the effective amount visible. Changing participants updates linked draft quantities; independent components remain untouched. Before saving, show which component amounts change and that existing reservations/procurement require fresh review. Keep keyboard focus, server validation, conflict drafts, unknown-outcome retry and mobile44px controls consistent with the existing workspace. Reuse local styles and scripts; no new dependency or visual identity.

## Acceptance sequence

1. Unit-test derived/manual counts, exact fractions, identity canonicalization, duplicate/zero/overflow rejection and independent/linked component amounts.
2. Exercise a populated upgrade from the current Mealplan6/Core22 candidate; prove old quantities and identities are unchanged.
3. Test final-state database guards, household foreign keys, each required runtime grant, audit attribution/redaction and readiness detecting damaged guards.
4. Test actual saves for active, foreign, missing and preserved inactive members; omission versus explicit clearing; stale versions; same-key replay and late rollback.
5. Test a real week copy with inactive participants, new identities/status and no copied claims or Shopping effects; test moves/deletion and actual module purge.
6. Prove linked portions change real MP14 demand, release a real PA12 reservation, preserve product mappings and require an explicit MP17 source decision.
7. Exercise concurrent roster/component edits and membership deactivation through their actual locking paths; no partial result or silent participant loss.
8. Connect the PWA, then check desktop/mobile, light/dark, keyboard/focus, long/inactive labels, reference pagination, reader access, validation/conflicts and exact retry.
9. Run complete native regression, scoped Linux race, security/workflow checks, encrypted restore and fresh independent UI/documentation review. Update README, the ledger and checklist only with executed evidence.

MP06 is locally verified. Native focused campaigns cover nine contracts, including actual reservation release and MP17 source review, inactive-member copy/purge,100-member replacement, late rollback, authorization/privacy, contested user/membership activity and a populated Core22/Mealplan6 upgrade. Final browser30checks/seven clean axe scans and the740-check complete chain pass, as do current-image synthetic recovery and scoped independent correction/documentation review. Complete native regression passes272main tests(web1219.400s),72.7% internal coverage(7528/10353). Scoped Linux race passes(web624.225s, Store1.038s) under the recorded participant/weekly/reservation/demand/audit/migration/portion selection. The standalone Windows race attempt without CGO is not acceptance. See [the verification ledger](planning-participants-verification.md) for failed historical-fixture evidence, corrected repeat and operational limits.
