# Local Shopping barcodes — SH13

This increment is integrated in development source. Its remaining current-source acceptance gates are recorded below. It has not upgraded live8100.

## Intended workflow

Open **Barcode prüfen** from an active Shopping list. Enter a code, use a keyboard-style scanner, or explicitly start the camera. Local lookup does not create records. An unknown code can be assigned to an existing product after reviewing its pack. Enter a quantity yourself and explicitly add an unchecked item. No purchase, stock or expense is created by scanning.

Codes retain leading zeros and case. Trimmed1–64 visible ASCII characters support internal household codes; there is no automatic UPC/EAN merge or external lookup. Each household code identifies one Shopping product pack, with at most100 codes per product. Existing mappings block changing that pack. Reassignment requires explicit removal and a new assignment.

## Camera and privacy

Camera access is requested only after a click, in a secure context with a supported native BarcodeDetector. Manual input remains usable when access is denied or unsupported. A hit fills only the code draft. Closing, cancelling, leaving the page or reaching the one-minute lease stops the stream; late permission/playback/detection results cannot restart it.

Only the dedicated authenticated barcode document permits camera access and local video streams. Other documents deny camera. Codes/images are not sent to an external service, and private API responses are uncached. The alias audit records actor and product/version while redacting the code.

## Database and authorization

Shopping8 adds one table, its constraints/indexes, strict readiness checks and one audit policy. Same-household product references, uniqueness, capacity and mapped-pack integrity are checked in PostgreSQL. Alias inserts and pack changes require read-committed transactions; stale repeatable-read snapshots are refused.

Typed lookup and mutations validate the current session, membership, module state/generation and open list. Writes also validate versions and explicitly reviewed input, preserve idempotency and acquire bounded row/advisory locks. App UPDATE privilege exists only on the immutable ID column to permit row locking; the database trigger rejects every actual UPDATE, including a no-op. Exact column ACLs and the trigger remain readiness requirements.

On Core26, the existing complete audit contract gains exactly its one new policy. Core27–31 refuse this upgrade. Core32 and later must expose the unchanged approved private support interface and exact catalogue shape; a version number alone does not authorize the path. The latest producer-aware audit definition is preserved. Owner purge includes the alias table through the existing schema inventory.

## Executed evidence and remaining gates

The latest383-source standalone candidate passes four initial and twelve remaining PostgreSQL contracts. These cover actual mapping/add/replay, least privilege, current authority, stale snapshots, capacity, pack identity, rollback, concurrency, purge, populated upgrade and readiness drift. Nine Node camera/identity/quantity cases pass; Gosec reports146 files,22980 lines and zero findings.

Four actual424-source combined tests pass56.299s after a strict PostgreSQL18 catalogue correction: fresh Core32-before-Shopping8 workflow, populated Core26/Shopping8 upgrade, genuine private-marker ACL refusal, and NOT NULL inheritance drift refusal/restoration. Failed intermediate candidates remain recorded in `docs/implementation.md`.

The existing22-contract Shopping compatibility campaign has20 passes and two old fixtures expecting77 instead of78 audit policies. The corrected five policy-census fixtures have three immediate passes; two historical Core22/23 setup fixtures then pass after capping only their pre-upgrade Shopping setup at7. Their final normal upgrade assertions remain intact. Production code and all69 migration files are unchanged by these fixture corrections.

The final standalone383-source browser candidate passes ten recorded groups with zero external requests, page errors or Axe violations. Mobile, desktop and dark captures are inspected. A stable save-button selector fixes the actual blocked product search found in the first browser run. Exact source322bb07078b0f093f1bb3dddd67e0fb55f2136db3967d756b307ef397321ea5a is now materialized in Root, preserving all68 old SQL files. The complete26-component browser suite passes against the exact non-root runtime image using actual shared-session handoff. Seven focused Linux race contracts pass(two Store pure and five actual PostgreSQL/Web contracts), Web91.521s, with all383 source hashes unchanged and no race report. This is scoped acceptance, not a full-current native/race or hardware-camera campaign.

Standard Root verification passes95Node cases, non-database Go tests/vet/formatting, module verification,69migration checksums and task-list/workflow checks. Actionlint passes. Gosec reports146files/22980lines/0findings on identical production Go sources. Current govulncheck finds0reachable or imported-package vulnerabilities; GO-2026-5932 concerns the unused unmaintained openpgp package in required x/crypto, not the application's bcrypt usage. No new dependency is added. Independent source/integration/CI-origin review closes without remaining material findings. The review container is stopped before the next isolated database campaign; live8100 is unchanged.

Browser camera evidence uses deliberate permission denial and a canvas stream with a synthetic detector. It does not claim physical-device testing or native recognition accuracy.
