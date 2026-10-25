# Pantry daily workflows

Next approved expansion slice: PA02/PA03/PA06/PA07/PA08/PA09/PA18. This is a working contract, not implementation or acceptance evidence. It extends the existing pantry workspace in Household's established Operate design. The planning slice must finish its independent acceptance before this slice is checkpointed.

## Purpose and screens

Keep exact stock and the immutable movement journal as the authority. Product rows retain their full-selection totals and separately loaded lots. Add a labelled consumption-priority view, an explainable lot suggestion and a reviewed quick-consume form directly beside the selected lot. Product detail gains packaging and barcode management. Lot detail gains optional, sourced guidance for use after opening. All controls remain keyboard-accessible on mobile and desktop; no camera permission is needed for manual barcode entry or a hardware scanner.

The consumption review shows product, named location/lot, amount/unit, current stock and resulting stock. It offers an exact own amount, half of the reviewed stock, or a configured number of packs. A pack is an explicit exact quantity in a compatible unit; it never means the entire current lot. Half/pack results that violate integer quantities or the supported decimal precision require correction rather than rounding. Review survives validation failures. An unknown network outcome retains the exact body and request key and freezes further writes until explicitly recovered.

## Stock concurrency and undo

Use the household/session/module barrier, operation-key serialization and lot-first locking. A journal insertion does not currently advance `lots.version`: compare the reviewed metadata version, projection movement count and exact remaining stock after the lot lock. Treat a missing projection as zero stock/count, never as an implicit positive balance. A concurrent booking returns the named current balance and preserves the intended quantity; continuing requires a new explicit review. Do not blindly rebase a half-stock or pack operation.

Consumption and its durable operation identity, audit/projections and outbox effects must commit together. A matching operation key must recover the original movement after a lost response and receipt expiry; a changed payload under that key conflicts. Bounded snapshots/metadata cannot contain raw credentials or unbounded human content.

Undo appends exactly one compensating movement. Reuse the immutable journal's `reverses_movement_id` uniqueness, but validate original household, lot and the exact opposite amount as well. Existing generic reversal and transfer paths must remain compatible with this contract. No UPDATE/DELETE of journal history is introduced. A repeated undo returns or identifies the existing compensation; it must not create stock twice.

## Packs and barcode identity

Packs belong to pantry products within the same household. Store label, positive exact amount/unit, active state and optimistic version; convert only through supported dimension-compatible exact unit rules. Product base units cannot change after stock history exists. A changed pack definition must not reinterpret an earlier stock movement or a pending consumption review.

Allow multiple exact barcode strings on a product and optionally its pack. Preserve leading zeroes. The household-wide identity must resolve to one product/pack; conflicting assignments produce a named review error. Enforce bounded input and describe supported GTIN/custom-code validation explicitly. Manual scanner input uses local lookup; no external catalogue lookup is enabled implicitly.

Existing `products.barcode` values must be preserved during upgrade, including duplicates or malformed legacy entries. Backfill only unambiguous supported identities; expose remaining legacy conflicts for explicit correction. Every existing generic write/import/search path must either maintain the new identity contract or reject the unsupported edit clearly. Optional shopping/recipe integrations continue through core contracts, never a foreign key into another optional schema.

## Dates, opening guidance and FEFO

Preserve separate best-before, use-by and opened dates. An optional after-opening duration must carry its source (user/manufacturer plus a bounded source note). Unknown remains unknown. A derived opened-date hint must be visibly labelled as a hint and must not silently replace a recorded use-by date. Date arithmetic uses household-local calendar days, including DST boundaries. No automatic consumption or disposal follows a date.

Consumption priority and FEFO suggestions show the effective date, its type and the reason for the ordering. They consider positive current stock only, keep unknown dates distinct and use stable tie-breakers. Cap/paginate candidates without presenting an incomplete selection as a whole-stock total. A suggestion is a proposal: the user can select another authorized lot and review exact remaining quantities. Archived or incompatible products/locations and later stock changes must be handled explicitly. Reservations and recipe-product bindings are still separate open expansion work; do not claim those deductions here.

## Acceptance

Use unit tests for exact pack/half arithmetic, unsupported dimensions, integer/precision boundaries, barcode normalization/checks and date ordering. Real PostgreSQL tests must cover populated upgrades with conflicting legacy barcodes, concurrent stock operations, stale projection counts, rollback, once-only retry/undo after receipt pruning, cross-household denial, generic-path compatibility, privileges and audit redaction. Browser checks must cover practical consume/undo, a named concurrent balance change, uncertain-response recovery, pack/barcode edits, explanatory date ordering and mobile keyboard/error states. Preserve populated backup/restore and run the affected unit, integration, race, security and accessibility checks before marking any item verified.

## Direction contract

THESIS: Put a named lot and its exact stock immediately beside a reviewable consumption decision. Date priority explains the proposed order; it never substitutes a hidden stock mutation for the user's choice.

OWN-WORLD: Extend Household's established Operate surface, system sans, green accent, quiet separators, labelled native inputs and 44px controls. Preserve DESIGN.md and existing light/dark tokens. This is an ordinary extension, with no new visual world or generated imagery.

STORY: Find an earlier-dated lot or look up a local barcode, inspect source/date/stock, select an exact amount, review before/after, then book. A conflict retains the selection. A lost response freezes further writes while the same operation is recovered.

FIRST VIEWPORT: Area navigation, page title and status precede the selected product/lot, exact balance and opening source. The amount form and its explicit review follow in the same bounded column. Priority uses readable separated rows; smaller screens stack actions and inputs.

FORM: Extend the incumbent pantry task flow with a lot-specific review and a separately filtered priority list. No new composition lottery or seed is introduced for this extension. Existing screenshots and DESIGN.md are the visual authority.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Implemented workflow boundaries

- Pack definitions: at most 50 per product; compatible exact base quantity with six decimal places. Barcode aliases: at most 100 per product; 1–64 printable ASCII characters, surrounding whitespace trimmed, case and leading zeroes preserved. These are local identifiers; no GTIN check digit certification is claimed.
- The priority list returns at most 50 lots per page with a signed cursor tied to household, member, generation, filters, timezone and current calendar day. Positive active stock only; unknown dates follow known dates. Product-specific filtering is the manual FEFO suggestion.
- Consumption reviews load one bounded consistent statement: lot, packs, barcodes and the latest 20 operation summaries. Full journal history remains separately reachable. The server locks parents first, then reads the current projection in a new READ COMMITTED statement before comparing metadata versions, stock and movement count.
- Uncertain-response recovery retains the exact request/key in the current page and warns before navigation. It is not a persistent offline booking queue. Stored operation receipts are durable; their immediate-after stock is explicitly historical.
- Opening durations may be zero, remain null when unknown, and must name their source. They do not replace use-by dates. Existing generic administration handles pack/barcode edits; local lookup does not request a camera or external catalogue.
