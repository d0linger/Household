# Household 0.1.0 — local release candidate

Date: 2026-10-24. Publication and remote pushing are not part of this delivery.

The first candidate implements the approved modular household PWA with an embedded German interface, Go HTTP server, isolated PostgreSQL schemas and Docker Compose. Shopping, fuel, recipes, pantry and expenses start enabled. Administrators can enable chores, meters, documents and meal planning without adding another service.

Authentication includes forced bootstrap password replacement, role checks, bcrypt, hashed sliding sessions, durable rate limits, CSRF, TOTP and one-use recovery codes. Domain writes use optimistic versions, idempotency receipts and source/destination module generations. Posted financial and stock history uses compensating corrections.

The app includes authorized search, private sanitized images/PDFs, local capture review, previews, reminders, bounded background jobs, CSV preview/import/export, exact quantities and reports. The optional OCR image bundles German/English Tesseract. Offline private data is limited to explicitly enabled drafts; users inspect named changes before replay. Private pages and API responses are not service-worker cached.

Encrypted restic backups include database, upload integrity manifest and security keys. Retention reconciles snapshot eligibility, purge requires a verified backup created after the current module pause, and the separate owner role runs maintenance. A disposable recovery rehearsal checks a synthetic quantity and original/preview file hashes.

The complete plan is in [master-plan.md](master-plan.md). Current executed checks, remaining work and reviewer dispositions are in [implementation.md](implementation.md); that live ledger is authoritative over any earlier design-prototype checkmark.

## QA follow-up

The follow-up fixes origin/input validation, bounded command output, maintenance connection/restore safeguards, exhausted OCR jobs, orphan scanning beyond 10,000 files and shopping keyboard focus after reload. Independent test fixtures, required PostgreSQL checks, race/fuzz coverage, workflow/secret validation and actual OCI/SBOM inspection now guard these behaviors. Windows and Linux integration runs, 96 browser checks and a fresh encrypted recovery rehearsal passed. See [the review report](qa-review.md) for measured coverage, workload observations and remaining acceptance limits.

## Security, audit and usability follow-up

Core migration5 adds centralized field-level history for 45 tables, a household-scoped administrator viewer, explicit authentication/worker events, transaction-local identity and owner-controlled safe-field policies. Existing events remain identifiable as legacy records. Runtime writes cannot alter audit history; this does not protect against the database owner. Module purge now retains per-row audit evidence, including truncation.

The pass closes stale capture visibility after source deletion, preserves exact related amounts and selected references outside the first page, and rejects oversized reports explicitly. Dialog names, validation-error focus and failed relationship loading/retry improve keyboard and assistive-technology use. Native integration and Linux race checks passed with 61.3% combined coverage; three browser components passed 149 checks and eight axe scans. The current Docker image and encrypted recovery rehearsal are verified locally. See [the final pass report](audit-review.md), [the security review](security-review-2026-10-24.md), [audit architecture](audit-trail.md) and [current evidence](implementation.md).

## Deployment limits

- One household and one app replica per installation.
- Production needs an exact HTTPS origin, correctly scoped trusted proxy and operator-managed external backup storage.
- The live local instance starts empty. Browser screenshots and recovery fixtures are synthetic and isolated.
- Default capture is manual; local OCR is an optional Docker target. Cloud AI, Web Push and passkeys are deferred.
- There is no configured Git remote, hosted CI execution, public deployment or published image. Workflow definitions and local candidate images are included.
- Docker Desktop and external hosting/storage can have separate terms; Linux Docker Engine and the application use an open-source stack.

Read [operations.md](operations.md) before installation or recovery, and [security.md](security.md) for threat boundaries and scanner scope.
