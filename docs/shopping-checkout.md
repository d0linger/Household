# Reviewed shopping checkout

This contract covers SH11, SH12 and SH20. Implementation is present; final acceptance is being recorded separately in the 180-item checklist. DESIGN.md and the existing list workspace remain authoritative.

## Review and durable completion

1. An open working list offers an explicit checkout review. Read every candidate with its version and reviewed purchased/substitution state; never infer product identity from names.
2. For each purchased position, select a pantry product and location, inspect the conversion into its base unit, and enter the actual amount and lot dates. Provide an explicit “do not store” decision for non-stock goods. Unchecked and unavailable items require an explicit disposition before archiving the list.
3. Display the original plan and actual purchase side by side. Preserve the original positions and exact price-estimate provenance. Unknown prices remain unknown. Actual receipt totals and any difference are explicit; a discrepancy is not silently allocated to goods.
4. Link only a ready, authorized receipt already uploaded to this list. Uploading an original file is a separate existing operation; the reviewed checkout transaction binds its metadata. No OCR value is trusted without review.
5. Confirmation uses the existing session, module-generation, version, audit and idempotency barriers. Lock order must be reviewed against normal item edits and stock movements. A durable unique completion identity supplements the expiring generic request receipt, preventing duplicate stock after later retries with a new request key.
6. One transaction stores the reviewed checkout snapshot, lots, stock movements, core resource links and list completion. All destination search/reminder projections are included. Roll back every destination if any selected item, product, location, generation, receipt or quantity has changed or is invalid.
7. Completed checkout snapshots are immutable. Inventory corrections remain compensating stock movements; the history links to the lot's subsequent corrections without rewriting the original purchase. Reopening a list cannot repeat its checkout; copy the list for a new purchase.

## Implemented contract

- `GET /m/shopping/lists/{id}/checkout` opens the authenticated review. `GET` and `POST /api/v1/shopping/lists/{id}/checkout` read and confirm it. Read access follows the enabled shopping module; confirmation requires a writable, current session and CSRF token.
- Review covers every position, up to 500; a larger list is rejected with an explicit split instruction. The server fingerprints the list, every item and its chosen product/price provenance. A changed source requires a new review. Refresh names affected positions and old/new values while retaining actual purchase input. An emptied list closes both stale editor and confirmation controls.
- Purchased positions require either `store` or `no_stock`; other positions require `skip`. Actual amounts use physical units or individual pieces, not catalog pack counts. Both browser and server calculate base quantities exactly and reject rounding, overflow, incompatible dimensions and fractional integer-only stock. Dates follow the household's local calendar; a completed purchase cannot be in the future.
- Blank prices remain unknown; an explicit zero is free. Totals use exact integers. The full known plan, unknown counts, actual prices and any unallocated receipt difference are saved. A difference is never silently spread over items. Confirmation accepts a currently ready attachment owned by this list and shopping generation, or an explicit no-receipt choice. Upload is an earlier independent operation.
- Shopping4 adds `shopping.checkouts`: UUID, household/list identity, unique `(household_id,list_id)`, SHA-256 request hash, bounded item count, bounded JSON purchase snapshot, actor and timestamp. Runtime privileges are SELECT/INSERT only, with the existing immutable-row trigger. Core10 adds its audit policy; 50 policies are required by readiness. The API offers no generic checkout write/delete route.
- The durable request hash is independent of the transport idempotency key and does not expire. Identical retries return the original completion, including after expiry of generic receipts or a destination purge. A different body cannot reuse an already completed list. Shopping generation/session authorization is still checked before a replay.
- One transaction holds the household/module barriers and the list, acquires child locks with NOWAIT, then locks explicit pantry product/location choices and verifies their versions. Pantry is required only when at least one position is stored. All lots, movements, balances, search/reminder projections, core links, snapshot and list archive commit or roll back together. Target lock conflicts return a review conflict.
- Cross-module stock identities use `core.resource_links`; the purchase snapshot deliberately retains descriptive historical stock information in shopping even if pantry is later purged. No cross-module foreign key prevents optional-module purge. Current lot links are shown only while the destination module and generation still match. Shopping purge removes its completion with its list through the existing owner-only maintenance path.
- Finance posting is not part of this checkout. Optional financial linkage remains a separate explicit future action. Private drafts remain in page memory; this workflow does not silently queue an offline purchase confirmation.

## Module boundaries

Shopping owns checkout snapshots. Pantry owns products, lots and movements. Cross-module identity and provenance use the existing core resource-link and target-generation contracts; no foreign key ties one optional module's schema to another. Optional financial linkage must respect whether the finance module is enabled and must not implicitly post an expense. Final adapter and UI details are to be specified against the existing contracts before coding.

## Interface and verification

Use the current disclosure/form/record-row vocabulary. Mobile review presents one position at a time with an explicit progress indicator and preserved form values; desktop may show a compact summary with expandable decisions. Final confirmation lists stock destinations, skipped goods, receipt and planned/actual differences. A stale preview returns named conflicts and preserves the draft, without blind automatic retries.

Test exact unit conversion, substitutions, integer-only products, zero/negative/overflow inputs, unknown prices, stale child versions, module disable/purge generations, inaccessible receipts, rollback after a later invalid line, two simultaneous confirmations, new-key repeat after completion, audit/search/reminder projections and restored completion receipts. Browser checks must exercise a real multi-position checkout, error recovery, keyboard focus, mobile/dark layouts and accessibility.
