# Differential planning demand — MP14 development

Status: implemented development slice with focused verification; not completed MP14 acceptance. Attributable already-pending Shopping coverage depends on MP17 and is explicitly reported as unlinked. Checklist:52 verified/1 in progress/127 open.

## Source and identity

Select1–42 whole open/planned recipe meals from one non-template plan. Each frozen component retains independent portions and stable ingredient UUIDs. Aggregate mapped product/base-unit demand before subtracting stock once. Whole-meal selection is intentional: existing manual per-meal claims cannot safely be apportioned to a partial ingredient selection.

Core20 owns planning_ingredient_bindings; Pantry8 and Mealplan4 install narrow target/source guards and lifecycle hooks. Optional schemas retain no cross-module foreign keys. The source key is household, Mealplan generation, component and frozen ingredient UUID; plan/meal/source fingerprint are immutable context. Unmapping preserves the row and increments its version. Actual component deletion removes payload through a narrowly scoped privileged hook. Product deletion clears only that product's mappings without inverse parent locks, including while Mealplan is disabled. Actual source purge removes payload; Pantry purge clears targets and advances the Pantry generation.

Confirmed plan mappings survive Recipes purge and portion/title edits. Global catalogue mappings are only unconfirmed suggestions for the current Recipes generation. Plan copies have new component identities and start unassigned. Changing a product's base unit requires reconfirmation; disabled/stale targets are redacted.

## Exact coverage

One read-only REPEATABLE READ transaction and one shared transaction timestamp cover the entire preview. For selected meals S, usable stock per eligible lot is max(physical minus other meals' active current-generation unexpired claims,0). Own claims are already included and never added twice. Archived products/locations contribute to physical stock but not eligible stock. Undercoverage is summed by lot; surplus elsewhere cannot hide a deficit.

Unknown/incompatible amounts keep a group incomplete; they never become zero shortage. Exact rational quantities remain visible even when six-decimal posting is impossible. Positive bookable shortages must pass numeric(18,6) and optional whole-unit rules. Zero shortage remains valid without a positive booking amount.

Shopping coverage is currently unlinked. The displayed shortage is not an instruction to add that amount to an existing list. MP17 must compare stable desired net amount N with its own attributable current contribution Q; repeatedly subtracting Q from the replacement target would oscillate.

## API and interface

- GET /m/mealplan/demand/{plan}: embedded authenticated page, linked from the week and eligible meal actions.
- GET /api/v1/planning/{plan}/demand/preview?meal=UUID&meal=UUID: read-only selected whole-meal preview. The suffix avoids standard ServeMux ambiguity with /planning/source/{source}.
- POST /api/v1/planning/{plan}/meals/{meal}/components/{component}/ingredients/{ingredient}/binding: writer authorization, CSRF, request UUID, current module generations and reviewed plan/meal/component/binding/product versions.

Mapping changes are metadata only: no stock booking, reservation, Recipe edit or Shopping position. Each change receives explicit review. Uncertain replies freeze selection and retain the exact serialized request/key. Receipt replay follows current authorization/generation/source checks before accessing the old target, so deleting a replaced target does not invalidate an already-saved proof. Fresh writes lock product before binding. Retained cleared identity/version prevents recreating an old insert after ordinary receipts are pruned.

## Resource and acceptance boundaries

At most42 meals, ten components per meal,1MiB per component,2MiB per meal,16MiB total and500 ingredients. Read metadata/size bounds before snapshots; batch bindings, suggestions and stock. Mutations use READ COMMITTED and bounded NOWAIT secondary locks under the existing household/module barrier. API deadline20seconds; page authorization deadline10seconds.

Executed development checks and corrected fixtures appear in implementation.md. Focused native, unit/workflow and source checks do not imply complete native, Linux race, browser, encrypted restore or MP17 acceptance. Live8100 is unchanged.
