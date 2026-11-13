# Vehicles and Fuel module

Status: FU01–FU20 meets local module acceptance on28October2026. The current456-source freeze passes70native and87selected Linux race mains,20real browser workflows/1031checks, six actual local OCR sources, encrypted backup/restore/purge and the existing28-component browser chain/908checks. The439-source initial snapshot remains in `.local/fuel-module-work`; the project root contains the authoritative corrections. Follow the [module checklist](fuel-module-plan.md), [validation matrix](fuel-module-validation.md), and [180-item task list](taskliste.md). Earlier broad-suite failures/timeouts remain recorded; local module acceptance is not complete current repository coverage, hostedCI, Linux browser/recovery portability or a live8100 deployment.

## Workspaces and daily flows

The module opens at `/m/fuel/workspace/overview`. Its twelve workspaces are overview, quick capture, history, consumption reports, costs, maintenance, charging, trips, CSV import, documents, odometer management, and personal defaults. Existing vehicle, series and fill-up URLs remain available; legacy import/capture confirmation directs users to the new explicit review workflows.

1. Create a vehicle and a real odometer series. Explicitly select an existing applicable series where a historical assignment is ambiguous. Archive/unarchive a vehicle without deleting its records.
2. Select current personal vehicle/series/station defaults. Enter at least two of quantity, unit price and total. Review the derived value and rounding difference before acceptance.
3. Record charging separately, including explicit wall/battery/unknown measurement basis. A missing amount is unknown; zero means free. Wall energy is not declared driving consumption.
4. Record manual trips with purpose, dates and increasing readings in their actual series. Overlapping documented distances do not become an invented complete vehicle distance.
5. Record other vehicle costs. Linking an expense is explicit and deduplicated. A cost correction preserves the original; a linked expense requires explicit atomic reversal and replacement approval. Missing or inaccessible historical targets never cause silent recreation.
6. Set maintenance dates or kilometer thresholds. Display due/unknown status against an unambiguous measured series. Complete a cycle once and explicitly choose the next targets. Create a task only through the reviewed opt-in contract; its cycle identity survives loss of a disposable Core link.
7. Link current authorized documents to the vehicle. Inaccessible document titles and task metadata are suppressed.
8. Review CSV rows or local OCR/photo proposals. Confirming an odometer photo creates an observation, not an invented fill-up, charge or trip.

## Exact values and known limits

### Device reminders (Fuel12 development increment)

Vehicle editing provides an optional responsible household member for the existing service, inspection and insurance dates. Maintenance plans use their own assignee; an unassigned plan does not inherit the vehicle's responsible person. Creating a linked chore remains a separate reviewed action with separately configured chore notifications. Personal vehicle defaults and record creators never become notification recipients implicitly.

Calendar reminders occur seven civil days before and on the due date, at09:00 in the household timezone. They expire24 hours after that stage. The assignment, source edit and module activation must precede the stage; an edit after an elapsed stage conservatively suppresses that stage instead of replaying it. Both personal Fuel notification permission and permission on each selected device must already exist. No registration, service title, mileage or other vehicle content is included in the encrypted generic push payload.

Mileage reminders use the same exact, documented four-stream reading as the maintenance workspace, with an additional maximum age of30 civil days. A fresh initial series reading is eligible; future, inconsistent or ambiguous readings are not. The first already-exceeded target is a silent baseline. A later observed below-target→at-or-above-target transition creates one episode using the last below-target observation as its consent boundary. Configuration/assignment changes and vehicle edits reset the observation basis. A short transition entirely between scans can be missed; there is no forecast or guaranteed alarm. Completion, archive, reassignment, incompatible series, stale readings and withdrawn consent invalidate pending delivery. Already accepted provider messages cannot be recalled reliably.

The existing worker advances at most50 sources per durable scan page and retains its cursor across restarts. Fuel12 stores observation state separately from business facts and preserves existing assignments without inventing historical consent. This increment is undergoing automated module acceptance; physical-device testing and the live Docker upgrade remain separate gates in [the notification checklist](notifications-device-plan.md).

To configure this increment after deployment:

1. In vehicle editing, explicitly select the person responsible for vehicle dates and confirm the reviewed change. For maintenance, select the plan's own assignee separately.
2. That person enables Vehicles under Notifications and explicitly allows Vehicles on each intended connected device. A successful test message verifies only its own test category; it does not subscribe other categories.
3. Configure reminders before their upcoming stage. For mileage, allow a valid below-target observation before a later threshold crossing; an already exceeded target is not replayed after opt-in.
4. Check the application's current vehicle or maintenance workspace when opening a generic alert. Device delivery is best effort and old provider-accepted messages may still appear after source changes.

Typed `total_minor` is a string containing integer cents. Quantity, unit price, distance, energy and displayed record versions use exact strings; calculations use decimal/rational arithmetic. Chart coordinates alone may be quantized. Unknown values remain null and are explained.

Fuel type fixes the quantity dimension: diesel/petrol/LPG use liters, CNG/hydrogen use kilograms, electric charging uses the exact Core unit `kWh`. No density conversion or automatic foreign exchange occurs. Existing legacy fill-ups retain EUR because Core1 constrained household currency to EUR and the old Fuel form explicitly recorded euro amounts. New currency codes are explicit and totals remain separate. Expense creation supports the household currency only.

Consumption requires actual full-tank interval boundaries. Partial fills within an interval contribute; the opening full tank provides the anchor rather than the interval's cost. Missing fills, changed fuel, overlapping series, tied ordering, backward readings and incomplete intervals remain structured unknowns. Large histories use versioned, invalidated segments advanced in bounded worker chunks. A rebuilding or stale projection never becomes a partial complete total. Detail pages and complete grouped totals have separate bounded contracts.

Reports declare requested dates, actual interval coverage, units, currencies, cost scope and denominator. Cost per documented kilometer is not a claim that every real vehicle journey was recorded. Trip export does not claim tax certification.

## Import and local assisted capture

CSV input is UTF-8, at most 2 MiB and 1000 data rows. Native Fuelio support follows its documented Log structure. Drivvo/Spritmonitor require explicit column, date, decimal, currency and unit mappings where no stable native schema is established. Logical row numbers are separate from physical source-line provenance. Confirm at most 100 explicit row decisions per operation; fingerprints bind the actual file/row source. No service login or network integration is required.

An absent missing-fill flag does not prove continuity: the user must declare whether the exported history is complete. Native source flags are retained. Invalid, skipped, duplicate and already processed rows have explicit outcomes.

Files use the existing authorized vehicle upload path and 10 MiB limit. Images are already sanitized and bounded to 20 megapixels. The local worker checks storage key, size and SHA-256 before invoking Tesseract outside a database transaction. PDF proposals cover the first page only, rasterized locally with Poppler at a bounded size. OCR text is limited to 64 KiB; extraction time and process output are bounded. Suggestions retain evidence and ambiguities; caller-supplied OCR text is not trusted as a source.

The existing optional OCR Docker target additionally includes `poppler-utils`. Enable the OCR deployment only when local extraction is wanted. A disabled or failed recognizer reports that state and creates no business fact. Actual OCR/container/browser execution remains part of the pending joint acceptance phase.

## Reviewed commands and uncertain outcomes

The UI obtains a current source stamp, previews one closed typed command, and accepts a ten-minute actor/session-bound review envelope under one stable idempotency key. Execution checks current session, membership, role, module generation, dependencies, source versions and current warnings again. Signatures do not replace authorization.

Fuel-owned durable operation fences and independently captured DML effects survive ordinary response-cache pruning and record deletion. An unfinished operation cannot commit. Responses are historical accepted receipts, not claims that the old record is still current. Current Core target visibility is checked before linked historical results are returned. Writer authority is required for execution, replay, status recovery and explicit abandonment.

After response loss, retain the same key and reviewed payload. The browser persists only a minimal actor/household-scoped status locator; it does not persist tokens, receipts, CSV, OCR text or form values. A missing status is unknown. Explicit abandonment locks that key: an already accepted operation returns its receipt; otherwise a durable terminal fence prevents later processing. Only that confirmed outcome permits starting another operation. No automatic new key or source rebasing occurs.

## Architecture and operation

Candidate forward migrations are Fuel3 facts/staging, Fuel4 durable operations/guards and Fuel5 audit/readiness. The 71 earlier SQL files remain immutable. Facts, derived segments, personal settings, staging and operation proofs have separate responsibilities. Fuel source revisions and unit reference data are not App-writable. Costs and completion events are append-only to the App role. Owner maintenance retains minimal operation fences while a real module purge changes the generation; deactivation preserves records.

Cross-module business reads/writes go through `core_fuel_module.go`. Other Fuel code queries its own schema and shared Core authority/reference contracts. New facts use exact-resource search/outbox/attachment cleanup instead of scanning every module record. A future financial-derived Fuel implementation must integrate inherited per-record visibility across all routes; the current candidate deliberately requires the manual/shared Core26 contract.

New API prefix: `/api/v1/fuel/module`, with typed workspace/detail/export reads, preview/execute, status and explicit abandonment. All assets are embedded and locally delivered. The standard non-root, read-only Docker deployment and isolated PostgreSQL remain the hosting model.

## Acceptance

No passing result for this complete candidate is claimed here. Phase3 must execute the [full matrix](fuel-module-validation.md), fix module errors, repeat affected checks, and update actual source/evidence and checkboxes before the next module begins.
