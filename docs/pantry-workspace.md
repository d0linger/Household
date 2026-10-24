# Pantry stock workspace

This ordinary extension preserves DESIGN.md and the existing pantry journal. Its first batch addresses PA01, PA16, PA17 and PA20 before shopping checkout writes inventory. Acceptance is recorded separately in improvements-180.md; this document alone does not implement or verify a feature.

## Stock truth and projection

- Immutable stock movements remain the authority. A module-local balance projection stores the exact current quantity per lot; the application role can read it but cannot directly change it.
- A narrowly scoped trigger updates the projection in the same transaction as an accepted movement. Insert rollback also rolls back the projection. Existing movements are backfilled during migration, and a reconciliation query compares projection with journal, including missing and unexpected lots.
- Product totals are summed across all eligible lots, independently of the displayed product page. Quantities are returned as decimal strings. Rows with no movements have zero stock; no float conversion or positive-stock-only filter changes historical accounting.
- Module disable/purge and administrative restore retain their existing core barriers. Readiness checks the trigger contract; pantry-check compares all journal/projection rows and owner-only pantry-rebuild takes the household barrier before the journal lock. Derived balances are not a second editable journal or a second authoritative audit stream.

## Product overview and navigation

- The normal pantry entry shows product, current amount and unit, locations and the nearest meaningful lot date. Expand a product to inspect its lots with remaining amount and labelled best-before/use-by/opened dates.
- Product-name filtering and stable cursor pagination must work past 100 products. Search cursors bind household, user, module generation and search. Lot pages are separately bounded; counts/totals refer to the complete selection rather than only the current page.
- Keep the established record rows, labelled controls, mobile navigation, dark theme and keyboard behavior. Product/lot details remain reachable. Loading and failures preserve the search and explain how to retry.

## Historical view

- A date selector reconstructs stock from journal entries whose recorded booking timestamps fall before the end of the selected household-local day. Report the timezone and effective cutoff. Future timestamps and ambiguous implicit browser timezones must not move the boundary silently.
- Corrections are new movements at their recorded booking time. PostgreSQL transaction timestamps describe recording time, not an independently tracked commit timestamp. The historical view shows the original stock before a later correction, and identifies the number and latest booking timestamp of later corrections separately; it never rewrites history to imply a correction was known earlier.
- Historical product/lot labels may use current descriptive metadata; quantities derive from the journal. State this limitation plainly rather than claiming versioned historical labels.

## Verification

Use real PostgreSQL tests for populated migration, zero-stock lots, simultaneous movements, transaction rollback, reversal, projection permission denial, reconciliation and exact totals beyond one page. Test a historical cutoff before/after a correction and Europe/Vienna day boundaries. Browser checks cover page/filter navigation, lot expansion, loading failure, keyboard focus, mobile/dark layout and axe. Existing inventory and restore tests must continue to pass before the projection is accepted.
