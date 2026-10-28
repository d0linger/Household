# Pantry waste review — PA14

This increment is locally verified in development source alongside SH13. Current Root394 contains70migrations; live8100 remains unchanged.

## Reviewed behavior

The dedicated waste workspace records an explicitly reviewed lot quantity and reason. It posts the actual stock movement and projection atomically, records the actor, and supports an explicit compensating movement. Historical unclassified waste remains unknown; it is never rewritten with invented reasons or costs.

Reports use household civil dates, keep units separate, and explain gross waste, compensations and net movement. A reversal belongs to its actual event date, so a selected period can have a negative net amount. Missing trustworthy valuation produces an unknown cost, not zero. Exact decimal/rational arithmetic avoids floating-point changes.

Current authentication, membership, module generation and review identity precede every mutation or replay. Durable journal operation proofs survive the bounded response cache. A lost or corrupt response does not create a second movement or fabricate a successful response. Internal proof fields are omitted from generic API projections. Existing generic waste requests can replay an exact retained legacy response; a fresh cache miss is directed to the reviewed waste workspace. Other stock reasons and the existing legacy reverse action retain their behavior.

The report is bounded to366civil days,10000candidate movement rows and signed50-row pages. Changed source scope invalidates paging. Budget overflow refuses the result instead of returning incomplete totals.

## Checklist and executed evidence

- [x] Independent source review closed for386native files and19owned paths.
- [x] All68previous SQL files preserved; only forward Pantry11 is proposed.
- [x] Six pure Go main tests, four Node groups, integration compilation, JavaScript syntax and tagged lint passed.
- [x] Four actual PostgreSQL smoke contracts passed28.884s with source hashes unchanged: workflow/replay/undo/private projection, strict HTTP/CSRF/views, genuine populated Pantry10 upgrade, and strict readiness drift.
- [x] Eleven remaining actual PostgreSQL contracts passed80.461s with386hashes unchanged, including durable proof after cache loss/corruption, legacy generic replay/refusal, exact date/unit totals, paging/budgets, current authority and concurrency/rollback boundaries.
- [x] Actual Gosec scan of148files/23552lines reports0findings and produces its JSON report.
- [x] Twenty existing journal, stock, batch, cooking and checkout compatibility contracts passed162.176s; all386 source hashes remain unchanged.
- [x] Explicit composition with accepted SH13 preserves its global camera denial and dedicated allowance. All six actual combined migration, waste and barcode contracts pass41.069s on394sources; all69priorSQL files are unchanged. Independent review closes all three exact merges and the shared-session browser runner.
- [x] Final current-source27-component browser suite passes. PA14 records31checks, four clean Axe scans, no page errors or external requests, and actual shared-session handoff. Mobile/dark/desktop layouts are inspected; the stale validation alert is cleared after successful explicit review and visually confirmed.
- [x] Eleven focused Linux race main contracts pass(six Store pure and five actual Web/PostgreSQL), Web71.831s. All394source hashes remain unchanged, no race report appears, and the private race environment is removed. Per-package seeds are retained; no single shared seed is claimed.
- [x] Root installation and README development availability recorded;99Node cases and27browser components are wired into local verification and CI.
- [x] Current Root verification passes99Node cases, non-database Go/vet/formatting, module checks,70migration checksums and task-list/workflow checks. Observed unit-only coverage is16.4%; actual current-source Gosec reports150files/23936lines/0findings.
- [x] Final PA14 checkbox verified; fixed180 totals advance to62verified/2inprogress/116open.

The original isolated native manifest is `5b9496f188dc2a5be1e9bcff474e1c7552b1cc9cffca9372c35f3449e1c0b1ac`. Current394-source UI manifest is `5bddba118d7066033cacdef65bd7620e3e891397b9143ddb3d3c965721440ec7`; its only production difference from the combined native freeze is the reviewed alert cleanup in one JavaScript asset. Go and all70SQL files are unchanged. Final runtime image is `sha256:2b6a3c5856709ac0f18476f13830cf26bda7502cf0e5eac5458717b749ded56c`, user10001:10001/read-only/actual App role; its readiness and compiled health command pass. No current deployment, whole-current native/race or encrypted-recovery acceptance is inferred.
