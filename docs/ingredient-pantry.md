# Ingredient identity and pantry comparison

RE07 and RE11 are locally verified, bringing the checklist to46 verified improvements and134 open. Pantry daily workflows were checkpointed locally as `be3529c`; this slice's executed acceptance is in implementation.md and ingredient-pantry-verification.md. Reservations, cooking consumption and differential shopping transfer remain separate open work.

## Explicit identity

Add household-owned `recipes.ingredient_catalog` and `recipes.ingredient_aliases`. Preserve display spelling; an authoritative database key applies Unicode NFC, whitespace collapse and lowercase for local search and uniqueness. Do not remove diacritics, punctuation or quantity text. Bound names/aliases to 200 characters and aliases to 50 per catalogue entry. Alias parents are immutable. A matching name may suggest a catalogue entry; it never selects a pantry product or changes a recipe automatically. A canonical name matching another entry's alias produces explicit alternatives, not an implicit winner.

Ingredients gain a nullable same-household catalogue FK and a server-captured catalogue label. Existing ingredients remain unassigned. Generic/workspace/import/capture edits create new complete recipe versions. Immutable snapshots retain their selected catalogue UUID and label; alias edits never reinterpret them. A catalogue entry used by a live ingredient cannot be deleted until that reference is explicitly cleared. Historical unavailable identities remain unavailable.

## Core product-binding contract

Add `core.ingredient_product_bindings`, without FKs to optional schemas: household, stable binding UUID, source catalogue UUID/Recipes generation, nullable target pantry-product UUID/generation, optimistic version, actor and timestamps. One binding exists per household/source generation/catalogue. Clearing a target creates a versioned tombstone, preventing old-version reuse after clear/recreate.

Assignment validates membership/session, current generations and same-household live source/active target. Lock source catalogue, selected target product, then binding in that order, returning contention as a reviewable conflict. Check module/generation authorization before idempotent replay. Product/version changes invalidate an uncommitted selection. Unbinding can remove a stale association without disclosing disabled Pantry target identities, labels or stock.

Generic deletion and module purge clean this contract within their existing authorized transaction. Target deletion tombstones its binding; source deletion/purge removes its contract. Read comparisons independently verify generations and live endpoints instead of trusting stored UUIDs.

## Read-only comparison

Select a current or historical recipe version and positive exact servings. Read source, current bindings and stock in a bounded repeatable-read snapshot. Explain that recipe content is the selected version while assignment and stock are current.

Scale rationally, convert compatible dimensions, aggregate ingredients sharing one pantry product, then subtract stock once. Keep unknown quantities, missing catalogue, absent/stale/disabled mapping, archived products and incompatible dimensions as distinct reasons. None means zero shortage. Include complete physical stock sums from active locations, separately from a small explicitly bounded FEFO sample. Reservations are not implemented in this slice. Rounded displays never become stock bookings; calculations retain exact rationals.

## Direction contract

THESIS: Show ingredient identity and missing information before representing a recipe as cookable. Keep recipe source, current product binding and shared stock distinct.

OWN-WORLD: Extend Household's Operate layout, local system fonts, green actions, labelled native controls and light/dark tokens. Preserve DESIGN.md.

STORY: Maintain deliberate aliases, select catalogue identities in the recipe, explicitly choose pantry products, then inspect portion-specific demand and explained gaps.

FIRST VIEWPORT: Catalogue screens keep name and alias management together. Comparison begins with source/portions and refresh, followed by product demand and separately explained unknown ingredients. Mobile stacks details without horizontal scrolling.

FORM: Ordinary extension of the incumbent editor and pantry rows; no replacement world, imagery or new composition seed. Search failures preserve the recipe draft.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Required acceptance

- Populated upgrade preserves old recipes/versions with no invented mappings.
- Unicode/whitespace, duplicate keys, 50-alias boundary, household/parent constraints, stale generations and archived endpoints have regressions.
- All ingredient write paths capture complete versions; browser/device draft round trips preserve catalogue selections.
- Binding changes use expected identity/version, atomic audit and idempotency; delete/disable/purge/restore behavior is tested.
- Duplicate ingredients share stock once; g/kg and ml/l convert exactly; incompatible/unknown/sub-micro demand remains truthful.
- Browser checks cover alias search, explicit assignment/removal, preserved drafts/errors, historical-source/current-stock copy, keyboard/mobile/accessibility.
- Update README/schema/test/evidence docs; execute appropriate native, Linux race, security, restore, complete browser and independent checks before either item is verified.
