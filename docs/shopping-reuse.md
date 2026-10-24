# Shopping reuse and controlled consolidation

This is the next ordinary extension of the existing shopping surface. It addresses SH06 and SH07; implementation and evidence remain separate in improvements-180.md. Existing DESIGN.md controls layout, labels, focus and colour.

## Reuse contract

- Household product favourites are an explicit editable flag. A favourite selection fills the quick-entry draft with the named variant and one pack; the user can change it before adding. No selection silently writes an item.
- A list can be marked as a template. Template lists have a dedicated filter and remain distinguishable from working lists. Normal cross-module shopping targets exclude templates.
- Copying a list requires a new name and creates a separate working list with new item identities. Quantities, product identities, groups and notes are copied. Check history, unavailability/substitution decisions and assignments are reset. The store can be retained as context.
- Copying is bounded to 500 items. An oversized source fails explicitly without a partial list. The source version and every copied row are locked during the operation. Child locks use NOWAIT because ordinary item edits can already own a child before checking the parent; contention returns a retryable conflict instead of creating opposite lock waits. New item search projections are written in the same transaction.
- Repeating the same operation key returns the original new list; a new explicit operation key can create another independent list. Editing a template later never edits an earlier copy.

## Consolidation contract

- The user explicitly chooses two open items from the same working list. The surviving item is named before confirmation. Candidate differences are shown; no name-only automatic merge occurs.
- Both items must have the same explicit original product, replacement and availability. Quantities must exist and refer to compatible content dimensions; pack counts are converted using the immutable pack identity. Free-text items and conflicting assignments or notes require manual review instead of silent loss.
- The merged quantity uses exact rational conversion and the surviving unit. Results that cannot be represented at six decimal places, exceed the database bound or become non-positive fail unchanged.
- Both reviewed versions are checked under deterministic row locks. The survivor is updated and the other item removed atomically. Audit retains the before/after changes, search projections follow the result, and any attached file or resource link blocks removal until separately reviewed.
- The list must remain open and non-template. Retry with the same idempotency key cannot double the amount. A concurrent check, quantity edit or moved item returns a conflict.

## Acceptance

Use real PostgreSQL tests for independent copy identities, reset state, exact boundary quantities, stale source/target versions, same-key replay, 501-row rejection, foreign-list rejection, mismatched variants and preserved audit/search. Browser tests cover favourite draft selection, template copy, explicit merge review, keyboard focus and mobile layout. No item is marked verified from this specification alone.
