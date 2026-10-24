# Focused application security review — 2026-10-24

This review covers the existing Household Go/PostgreSQL/PWA application and its authenticated local HTTP boundary. The inspected baseline is the candidate following local QA commit `993604f`. The implemented audit-trail, authentication and usability changes subsequently passed the checks recorded in [the final pass report](audit-review.md). Baseline failures remain documented separately from post-fix verification. This is not a certification, an external penetration test, or a claim that every possible exploit has been excluded.

## Scope, method and evidence

- Inspected authentication, session and MFA services; HTTP middleware and handlers; authorization in storage mutations; attachment/capture/import flows; service-worker/offline behavior; database grants; and the corresponding regression tests.
- Traced attacker-controlled values to SQL, templates, filesystem access and subprocess arguments. The main application uses parameterized values and compiled descriptor identifiers, `html/template`, `os.Root`, and fixed executable arguments. No user-controlled server-side HTTP fetch was found in the inspected application flows.
- Executed a focused PostgreSQL/HTTP regression on the guarded disposable `household_test` database. Fixture setup validates the actual database and separate runtime/owner roles and claims the advisory fixture lock before resetting schemas. The production household was not seeded, reset or attacked.
- Initially used the earlier QA report as historical context, then executed fresh native integration, Linux race, scanner, browser, OCI and recovery checks after the implementation settled. See [audit-review.md](audit-review.md) and [implementation.md](implementation.md) for the current evidence.
- No sibling Treckrr/Parkrr files, databases or containers were changed. No remote service was tested, and no publication or push occurred in this focused review.

## Confirmed finding: capture content remained readable after source deletion

**ID:** SEC-01. **Priority:** P2. **Severity:** medium, limited to an already authenticated household member. **Category:** broken access control / residual data exposure.

The baseline `capturePage` handler selected `core.capture_jobs` using capture ID, household and expiry, then checked module state/generation. It did not check whether the associated attachment was still ready or whether its source resource still existed. Deleting a document through the application marks its attachment orphaned and schedules delayed file cleanup, but an existing capture URL could continue rendering the OCR source text and extracted values. The private download and capture-confirmation paths already rejected the deleted source; the review read path was inconsistent.

**Preconditions and impact:** the caller has a valid household session, knows a review capture ID, and the capture has not expired. Content from a deleted resource remains visible until capture expiry or delayed file cleanup. This is not unauthenticated access, cross-household exfiltration or a way to recreate the deleted resource.

The focused test creates synthetic source/attachment/capture records with a unique OCR sentinel, verifies the live capture is readable, then deletes the source through the real authenticated DELETE endpoint. It asserts a subsequent GET returns 404 and never includes the sentinel. Further cases exercise individual attachment/source boundaries.

| Baseline scenario | Observed result | Expected result |
|---|---|---|
| Source deleted through the application | 200; private sentinel visible | 404; sentinel absent |
| Attachment marked orphaned | 200; private sentinel visible | 404; sentinel absent |
| Attachment generation mismatched | 200; private sentinel visible | 404; sentinel absent |
| Attachment module mismatched | 200; private sentinel visible | 404; sentinel absent |
| Source missing independently of cleanup | 200; private sentinel visible | 404; sentinel absent |
| Capture generation mismatched | 404; sentinel absent | Already protected |
| Capture expired | 404; sentinel absent | Already protected |
| Module disabled | 404; sentinel absent | Already protected |

The baseline run reproduced **five failing privacy scenarios**; the three existing boundaries passed. The package run completed in 32.441 seconds. These are deliberate regression failures, not passing verification results.

**Implemented correction:** `internal/web/attachments.go` now joins the capture to a ready attachment with matching household, module and generation, joins the current enabled module generation, and verifies the live source resource before rendering any capture state or content. The source entity comes from the compiled module registry. The change preserves existing authentication, expiration and confirmation rules.

**Verification: passed.** The focused audit/capture PostgreSQL run passed in 76.846 seconds, including the five formerly failing privacy cases and the three pre-existing boundaries. The subsequent complete native integration suite also passed (web package 200.926 seconds, combined internal statement coverage 61.3%). A transient compilation failure during concurrent development was resolved before these runs; it is not treated as a test result.

Regression file: `internal/web/capture_privacy_integration_test.go`. Focused command:

```text
go test -tags=integration ./internal/web -run TestCapturePrivacy -count=1 -timeout=3m
```

The command requires the guarded test DSNs and `HOUSEHOLD_REQUIRE_INTEGRATION=1`, using the same isolated setup as `scripts/test-integration.ps1`. Do not point it at the live database.

## OWASP coverage and limits

The category names below follow the [official OWASP Top 10:2025](https://top10.owasp.org/2025/), checked on the review date. The findings and coverage judgments are this project's source-review assessment, not an OWASP endorsement or conformance score.

| Category | Inspected protection / result | Remaining scope or limitation |
|---|---|---|
| A01 Broken Access Control | Household-scoped queries, server roles, transaction-time session/membership checks, protected admin operations, download/capture boundaries. SEC-01 corrected; focused and full native regressions passed. | Source review does not exhaust every possible concurrent read schedule. |
| A02 Security Misconfiguration | Host/origin checks, trusted-proxy boundaries, production HTTPS requirement, nonce CSP, secure cookies, local assets and separate database roles. | Public reverse-proxy/TLS and deployed headers have not been tested against an external origin. |
| A03 Software Supply Chain Failures | Fresh module/license, npm, Go vulnerability, actionlint, workflow policy and exact OCI/SBOM/provenance verification passed. | No hosted Actions execution or signed-release trust claim. One module-only Go advisory affects no imported package or reachable symbol. |
| A04 Cryptographic Failures | bcrypt cost 12; random session tokens stored as SHA-256; HMAC CSRF/rate subjects; AES-GCM TOTP secret storage; one-use hashed recovery codes. | Independent generated deployment secrets and secure backup of encryption keys remain operator responsibilities. |
| A05 Injection | Parameterized SQL values and compiled identifiers; escaped server templates; DOM text APIs; scoped file paths; fixed OCR executable/arguments. No injection exploit confirmed. | No exhaustive decoder fuzzing or third-party PDF viewer testing in this pass. |
| A06 Insecure Design | Explicit review before OCR/import writes, idempotency, generation fences, role separation and separate maintenance operations. | Off-host recovery, representative resource budgets and populated upgrade/recovery acceptance remain open in the ledger. |
| A07 Authentication Failures | Session lifetimes/revocation, forced password change, dummy bcrypt, bounded hashing, durable limits, recent-session requirements, replay-resistant MFA and final-admin protection. | No exhaustive credential-stuffing campaign; fixed account limits can also affect legitimate access during targeted abuse. |
| A08 Software or Data Integrity Failures | Explicit mutation transactions, immutable financial records, same-request replay checks, core5 runtime audit INSERT/UPDATE/DELETE/TRUNCATE denial. Populated upgrade and exact-value regressions passed. | Database-owner/application-credential compromise remains outside the evidence-authenticity boundary. |
| A09 Security Logging and Alerting Failures | Core5 adds field history across45 tables plus authentication and terminal-worker events. Redaction, actor isolation and administrator read restrictions passed native integration. | Deliberate operational exclusions and trusted owner documented; no external alert delivery configured. |
| A10 Mishandling of Exceptional Conditions | Request/DB timeouts, input and image limits, bounded OCR output, worker lease handling, transaction rollback and generic user errors. | Accidental operator-secret disclosure through malformed connection errors and operational recovery/error paths merit continued hardening. |

No authentication bypass, cross-household IDOR, SQL injection, stored/reflected XSS, SSRF or command execution was confirmed in the inspected flows. This statement is bounded by the scope above and must not be presented as proof that the application is vulnerability-free.

## Audit data policy: deny by default

Audit data should be selected **before database persistence**, not merely hidden by the viewer. Do not serialize arbitrary `OLD`/`NEW` rows, input objects, request bodies or job payloads. A future table or field must remain excluded until an explicit policy and test permit it.

Allow the event envelope: server timestamp, enumerated action/outcome, actor/resource UUIDs, household/module/entity, generated event/request identifiers, and schema/policy version. A changed-field list must itself be derived from known schema fields, never from attacker-supplied arbitrary keys. Record technical credentials changes with fixed markers such as `credential_changed=true` or `mfa_reset=true`; never retain their values or fingerprints.

| Data class | Fields/examples that must not appear as plaintext audit values |
|---|---|
| Credentials and authentication material | Passwords, password hashes, raw session/challenge/CSRF/metrics tokens, token hashes, OTP codes, recovery codes/hashes, TOTP secret/URI/ciphertext, encryption keys and key-bearing configuration. Ciphertext and hashes are also excluded even though they are not plaintext. |
| Deployment secrets | Connection strings and database credentials; restic credentials; keys used for signing, encryption, rate limiting or CSRF protection; raw environment dumps; authorization and cookie headers. |
| Files and extraction | Uploaded bytes, original filenames, storage keys, content hashes, OCR `source_text`, `extracted_values`, `confirmed_values`, and document/receipt payloads. |
| Import, queue and integration content | `normalized_values`, arbitrary job/outbox payloads, raw validation errors that echo input, request bodies/query strings, push subscriptions and external endpoints with embedded secrets. |
| Direct identifiers | Username, display name, email, vehicle registration, meter serial number, location note, issuer, payee, barcode and future bank/account/payment identifiers. Household-scoped UUIDs are still personal data and require restricted access. |
| Private free text | Document titles/categories, descriptions, notes, recipe instructions/source notes, task descriptions, shopping groups, station text and notification titles/paths. Ordinary domain names/titles have the explicit exception described below. |

Controlled enums, booleans, version/generation counters and selected exact amounts/quantities may support useful history, but each must be explicitly allowed per entity. Dates, assignee relationships and financial values can reveal household activity; retaining them is a product/privacy choice, not automatically safe because the SQL type is numeric or UUID. Use field-level change markers when values are unnecessary.

**Explicit product decision for ordinary labels:** the new audit policy retains allowlisted names/titles for shopping lists/items, vehicles, recipes/ingredients/tags, pantry products/locations, expense categories, chore templates, meters and meal plans/meals. These fields support readable before/after history and record labels; document titles and user/contact identifiers remain excluded. Allowed string changes are bounded to 256 characters and record labels to 160. A user can place personal or sensitive content in any ordinary name/title, so this is not a guarantee that the audit is free of personal data. Administrators can still see retained labels after the source record is deleted. Audit access, backup access and future retention decisions must account for that deliberate tradeoff.

Required negative tests should seed distinctive sentinels in excluded fields and assert they are absent from stored audit rows, API responses, rendered HTML and relevant logs. Verify denied roles cannot query history, unknown filter keys cannot affect SQL, pagination is bounded, and escaping remains effective. Also test unauthorized, rolled-back and replayed mutations: do not attribute a failed write as a committed change or duplicate successful history on an idempotent retry.

## Independent review of the new audit implementation

A subsequent independent read-only pass inspected migration `000005_audit.up.sql`, `internal/audit`, authentication attribution and the new web viewer/API. It found no confirmed secret-value bypass through unknown columns, pooled actor leakage, HTTP identity spoofing or cross-household audit read in the reviewed paths. Transaction-local identity settings, server-generated request IDs, explicit owner-managed field policies, constrained semantic-event details, fixed security-definer search paths and administrator/household-scoped reads were consistent in source. The parent subsequently executed the full native suite and fresh scanners; its actual results are recorded in the implementation ledger.

**Upgrade compatibility finding (P2, corrected and tested):** the previous `RecoverPassword` implementation inserted `operator_password_recovery` with null household/module/entity/resource fields and placed the recovered account ID in `actor_user_id`. The new viewer initially omitted those events. The implemented projection covers only that exact legacy shape and the current singleton household. It preserves original rows and empty legacy diffs, presents the old ID as the recovered account, clears the projected actor, and leaves unrelated unscoped rows excluded. `TestAuditViewerBoundaries` exercises the original insert shape, projected module/resource/request filters, unrelated-event exclusion and denied reader/editor access; the full native run passed.

## Hardening opportunities, not demonstrated HTTP exploits

- **Duplicate-content retention:** housekeeping deletes expired unconfirmed captures and old preview imports, but confirmed captures/imports retain copied content. Define a bounded retention/minimization policy without breaking necessary replay receipts. Audit history should not become another indefinite document store.
- **Malformed database configuration logs:** `database.Open` wraps pgx parsing errors and the CLI logs returned errors. The pinned pgx implementation explicitly documents its password redaction as best effort for ambiguous malformed inputs. Return a safe error category at the configuration boundary and test synthetic secret sentinels rather than trusting downstream string redaction.
- **Audit trust boundary:** append-only runtime grants protect updates/deletes, not evidence authenticity after the database/application credentials are compromised. A trigger-based provenance design can improve ordinary application coverage, but owner access and backups remain privileged boundaries; do not label the result tamper-proof or cryptographically signed without implementing and verifying those properties.
- **Operational acceptance:** hosted CI, external HTTPS, off-host backups, host-loss recovery and complete assistive-technology testing remain separate gates. Prior local test passes do not complete them.

## Review completion record

| Item | Status at focused handoff |
|---|---|
| Source review and privacy inventory | Complete for the stated scope |
| SEC-01 baseline reproduction | Executed: five failures reproduced, three pre-existing boundaries passed |
| SEC-01 source correction and regression tests | Implemented and verified against isolated PostgreSQL/HTTP |
| Post-fix focused/combined test | Focused76.846s and full native suite passed; current further evidence is in implementation.md |
| Audit-trail/authentication implementation | Independent source review plus fresh native integration, lint and security checks passed |
| Production deployment/public penetration test | Not performed by this review |
