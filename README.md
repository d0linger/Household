# Household

All **180/180 approved improvements are implemented and locally verified**, including the Documents expansion (DO01–DO20). See the [checked task list](docs/taskliste.md), [120-step Documents plan](docs/documents-module-plan.md), [behavior and ownership contract](docs/documents-module-contract.md) and [executed evidence](docs/documents-module-validation.md). Local verification does not imply a live deployment or publication.

Documents adds a local inbox/review/library workflow, combined personal views, typed metadata, confirmed deadlines, authorized links/task handoff, original file versions, ordered scans, rules with preview, durable processing, PostgreSQL full text, duplicate review and portable ZIP archives. Originals are preserved byte-for-byte; existing legacy image attachments retain only their previously stored sanitized bytes. Use the module workspace at `/m/documents/workspace/inbox` after enabling Documents. Reader/editor/admin household roles apply; there is no separate private-document ACL. Linked Finance data keeps its current Finance rights.

The runtime now includes local Poppler utilities and Liberation replacement fonts for existing PDF text, safe first-page previews (including PDFs without embedded fonts) and source-preserving page assembly. These are local renderer resources; no browser font is fetched externally. The optional `ocr` image adds local Tesseract German/English OCR. Limits and failure/cancellation behavior are documented in the Documents contract; unsupported/encrypted or oversized files require manual review. No external service is required.

Optional inputs remain disabled until both operator configuration and administrator approval:

| Input | Operator settings | Ownership |
|---|---|---|
| Scanner folder | `HOUSEHOLD_DOCUMENT_WATCH_DIR`, or optional `compose.documents-watch.yaml` with absolute `HOUSEHOLD_DOCUMENT_WATCH_HOST_DIR` | Read-only mount; producer writes/fsyncs/closes and atomically renames immutable files to `*.ready.pdf/jpg/jpeg/png`. No source move/delete. |
| IMAP | `HOUSEHOLD_DOCUMENT_IMAP_ADDRESS` (`host:993`), `HOUSEHOLD_DOCUMENT_IMAP_USER`, `HOUSEHOLD_DOCUMENT_IMAP_MAILBOX`, `HOUSEHOLD_DOCUMENT_IMAP_PASSWORD_FILE` | Verified implicit TLS, EXAMINE and BODY.PEEK; no Seen change, deletion or flag mutation. |
| Paperless | `HOUSEHOLD_DOCUMENT_PAPERLESS_URL` (HTTPS origin), `HOUSEHOLD_DOCUMENT_PAPERLESS_TAG` (explicit positive tag ID), `HOUSEHOLD_DOCUMENT_PAPERLESS_TOKEN_FILE` | One-way scoped original import into independent local copies; no remote writes, metadata overwrites or ACL synchronization. |

The optional `compose.documents.yaml` forwards the network-input settings. Mount credential files read-only at their configured container paths using the installation's secret overrides; these optional files do not create or expose credentials. The corresponding non-`_FILE` secret variables are supported for existing secret-management environments, but must never be committed. Use a dedicated read-only IMAP account and a scoped Paperless account appropriate for all Household readers. Revoking approval or disabling Documents fences subsequent import completion. Polling uses bounded batches, durable source/key/hash receipts, retry backoff and current jobs/generations. Source errors are intentionally redacted.

Portable exports contain originals, checksums, metadata, deadlines, scan provenance and currently authorized links, capped at 50 documents, 100 files and 64 MiB. They exclude internal storage keys and credentials. Archives contain sensitive household data and follow your existing backup encryption and retention procedure. Archiving keeps originals; permanent deletion uses the separate verified-backup-gated Owner purge.

Local verification covers all 797 registered Linux race test identities, 68.99% observed Go statement coverage, all 12 quality gates and 297 Node tests. The complete 34-component browser regression passes 1,676 checks and 213 clean axe scans; the actual nonempty encrypted restore/purge passes all 16 stages. Exact index review verifies 107 SQL pins and 102 byte-identical predecessor migrations. Code checkpoint: `a7fe5f1`. The automated reboot and failed attempts remain documented; only complete passing test trees and whole successful coverage profiles count. See the [executed validation ledger](docs/documents-module-validation.md).

A self-hosted, mobile-first household PWA. Go serves embedded pages and JSON APIs; PostgreSQL stores independent module data. Docker Compose runs the application, isolated database and one-shot migrations. No frontend CDN, remote font, external script, cloud account or paid application license is required.

**Progress: 180 of 180 approved improvements are locally verified.** All nine modules have 20 verified improvements each: Shopping, Vehicles/Fuel, Recipes, Pantry, Meal Planning, Finance, Chores, Meters and Documents. The [checked task list](docs/taskliste.md) and [canonical acceptance ledger](docs/improvements-180.md) record actual status. Regenerate the task list with `node scripts/update-tasklist.cjs` after status changes.

Finance is locally accepted at code checkpoint `0102380`. It adds seven workspace areas under `/m/expenses/workspace/`: exact original/base money, account cash events, personal filters, budgets and savings goals, draft recurrences, reviewed bank CSV, receipt/OCR review, reconciliation and selected financial access across indirect consumers. Forward migrations use Core 29–31, Expenses 5, Fuel 8, Pantry 14 and Mealplan 9; all 85 previous SQL files retain their raw bytes. The ordinary image reports unavailable OCR honestly; the OCR image provides local Poppler/Tesseract.

Finance evidence includes a complete 717-main Linux-race campaign plus two separately executed, source-qualified cashflow race mains (no single 719-main run claimed), 70.86% observed-statement coverage, all 12 final quality gates, 307 ordinary Go mains, 297 Node tests, the complete 31-component browser chain with 1,273 checks and 167 clean Axe scans, and 15 actual encrypted restore/purge stages. The exact 892-blob indexed secret scan passes with zero findings. Final test source `a6e871cc` preserves every production byte of built/browser-tested source `515c3433`. [Criteria mapping](docs/finance-acceptance-map.md), [contracts and plan](docs/finance-module-plan.md), and [executed evidence, preserved failures and limits](docs/finance-module-validation.md) provide details. The live instance on port 8100 is unchanged; nothing is pushed or published.

Accepted module records: [Shopping](docs/shopping-module-validation.md), [Vehicles/Fuel](docs/fuel-module-validation.md), [Recipes](docs/recipes-module-validation.md), [Pantry](docs/pantry-module-validation.md), [Meal Planning](docs/mealplan-module-validation.md), [Finance](docs/finance-module-validation.md), [Chores](docs/chores-module-validation.md), [Meters](docs/meters-module-validation.md), [Documents](docs/documents-module-validation.md).

The current Chores source adds a focused workspace at `/m/chores/workspace/today`, immutable schedule/checklist/completion history, local-calendar rules, suitable rotation and absences, personal day proposals, leased local digests and revocable timed ICS feeds. Forward sources are Chores 2, Core 32, Fuel 9, Pantry 15 and Mealplan 10; the 92 released SQL files remain unchanged. These sources are locally accepted at code checkpoint `74e049f` after complete source-bound tests, actual encrypted recovery and exact-index review. [Runtime/API contract](docs/chores-module-contract.md), [whole-module checklist](docs/chores-module-plan.md), [criteria-to-test map](docs/chores-acceptance-map.md), and [executed checks and remaining validation gates](docs/chores-module-validation.md) distinguish implementation from actual verification. Prepared Pantry transfers and licensed provenance exports follow the compiled schema boundary, retaining future/dirty refusal after compatibility upgrades. Readiness preserves every schema/catalog check and its two-second HTTP deadline, inspecting independent contracts in at most three concurrent database lanes to avoid serial startup delays. No new service or required environment variable is introduced; the existing Docker application and worker run the module.

The default modules are **shopping, fuel, recipes, pantry, and expenses**. **Chores, meters, documents/warranties, and meal planning** can be enabled in settings. One installation holds one household with administrators, editors and readers. The interface is German, with EUR and Europe/Vienna defaults.
## Run locally

Docker Engine with Compose v2 or newer is required. Docker Desktop is optional; its own licensing is separate from this MIT-licensed application. Native Linux Docker Engine is sufficient.

On Windows PowerShell:

```powershell
./scripts/initialize.ps1 -Development
docker compose up -d --build
```

On Linux/macOS:

```sh
sh scripts/initialize.sh --development
docker compose up -d --build
```

Open **http://localhost:8100**. Sign in as `admin` with the value in `.local/secrets/bootstrap_password`, then set your own password. The initial password must be replaced before household data can be accessed. Password changes revoke sessions and require fresh login. Initialization refuses to overwrite existing secrets.

The local instance binds only to `127.0.0.1:8100`. PostgreSQL has no published port. Do not put development mode on a public interface.

## Production installation

1. Run the initializer without the development switch.
2. Set `HOUSEHOLD_ORIGIN` in `.env` to the exact HTTPS origin.
3. Place an HTTPS reverse proxy in front of the loopback HTTP port. Forward `Host`, `X-Forwarded-Proto`, and `X-Forwarded-For` correctly.
4. Set `HOUSEHOLD_TRUSTED_PROXIES` to the exact proxy network CIDR. Inspect your Docker network rather than copying a broad example network.
5. Start the containers and complete the bootstrap password change.
6. Enable encrypted backups, preserve the repository password separately, and run a restore verification.

Production is the default and rejects HTTP origins. Unknown environment names are rejected. The deployment supports one app replica; use the PostgreSQL connection pool rather than adding app replicas. See [operations](docs/operations.md) for deployment, recovery, upgrades, and maintenance.

## What is included

[Local repurchase suggestions](docs/shopping-repurchase.md) are implemented in the development source. Completed Shopping purchases provide explained product/pack intervals; users review the quantity and explicitly add an unchecked item. Shopping7 adds bounded history lookups and readiness checks. This increment has its own executed native, browser and security evidence; the running8100 deployment remains unchanged.

[Local barcode capture](docs/shopping-barcode.md) is locally verified in development source. Shopping8 maps household codes to reviewed product packs; manual entry and an explicitly started supported camera fill a draft. Only a reviewed quantity and explicit confirmation add an open Shopping item. Camera permission is confined to its dedicated document. Native, compatibility, complete26-component browser and seven focused Linux race contracts pass. The running8100 deployment is unchanged.

[Waste review and analysis](docs/pantry-waste.md) is locally verified in development source. Pantry11 adds classified, explicitly reviewed lot waste and compensating movements with durable operation proofs. Reports separate exact unit totals and unknown valuations. Fifteen new PostgreSQL contracts, twenty existing stock/cooking/checkout contracts, six combined Barcode contracts, the full27-component browser suite and eleven focused Linux race contracts pass on their recorded source freezes. Live8100 is unchanged.

[Vehicle overview](docs/fuel-overview.md) remains available at `/m/fuel/overview`. Fuel2 adds optional manually recorded dates; the overview explains counter-series ambiguity and exact current/historical consumption without exposing monetary or note fields. Its recorded native contracts, current28-component browser regression and87-case selected Linux race campaign pass under the complete Fuel module acceptance. This reader supports the current Core26/manual-only contract; later financial visibility remains a separate module rollout. Live8100 is unchanged.

Meal Planning includes [participants/guests](docs/planning-participants.md), [stock/date suggestions](docs/planning-suggestions.md), exact portions, independent recipe components and reviewed Shopping contributions. Accepted historical checkpoints and their original test results remain in the module validation documents.

The approved scope and current acceptance state are maintained in [the180-item ledger](docs/improvements-180.md) and [checked task list](docs/taskliste.md). Each module has a separate behavior contract, implementation plan and actual validation evidence.

| Area | Behavior |
|---|---|
| Shopping | Shared lists, quantities/units, groups, stable ordering, versioned checking, short undo, archives, CSV preview/import/export |
| Fuel | Vehicles, odometer series, partial/full/missing fill handling, exact costs, consumption segments, explicit expense creation |
| Recipes | Atomic editor, immutable versions/variants, cooking sessions/timers, explicit ingredient catalogue/aliases and product bindings, read-only version/portion stock comparison, reviewed atomic consumption from completed cooking, selected-ingredient shopping transfer |
| Pantry | Products, locations, dated lots, immutable movements, concurrent consumption protection, compensating corrections, transfers, low-stock and expiry reminders |
| Expenses | Exact integer cents, drafts/refunds, category allocations, deferred total validation, immutable postings, reversals, monthly reports |
| Meal Planning | Week/agenda, participants, exact portion/component changes, reviewed demand/Shopping, leftover forecasts, budget proposals and revocable calendar |
| Chores | Schedule/checklist/completion history, rotations/absences, personal plans, bounded digests and revocable timed calendar |
| Meters | Typed registers, physical replacements, exact consumption/tariffs, reviewed imports/photos/bills, optional HA/MQTT, aggregates and retention |
| Documents | Inbox/review, immutable originals/versions, scans, confirmed deadlines, local PDF/OCR, rules, protected search, duplicates and scoped optional inputs; joint validation pending |
| Shared services | Private image/PDF files, sanitized image previews, local capture review, authorized full-text/trigram search, notifications, durable jobs and outbox |
| PWA | Install manifest/icons, versioned public asset cache, neutral offline screen, opt-in actor-bound offline drafts and explicit replay review |
| Security | Bcrypt, hashed sessions, capped sliding expiry, HMAC CSRF, PostgreSQL rate limits, TOTP/recovery codes, recent-login checks, role management and field-level audit history |

Administrators open **Settings → Protokoll** to inspect changes, before/after fields, actor, module, operation, resource and request. Transaction-bound constrained triggers, strict audit-policy readiness and schema-local ownership preserve attribution. Migration evolution and actual catalog-drift tests are recorded in the module validation documents.

Changes use optimistic versions and idempotency keys. Financial postings and stock movements are corrected through new entries. Pausing a module retains its data and returns 404 for its routes, files, search, and exports. Purge is a separate operator action requiring a recent admin request and verified backup.

Cross-module actions bind both source and destination data generations. Purge requires a backup created after the current pause; retention reconciles expired snapshot references before they can be used for deletion approval. Migration checksums and append-only migration history are checked in CI. Each physical migration connection uses the canonical pg_catalog search path, independently of operator connection settings, so strict schema snapshots compare consistently after upgrade and restore. Migration connection and initialization budgets are bounded; ordinary runtime connections retain their configured settings.

CSV import is limited to 500 rows and commits all rows together after confirmation. Exports are limited to 10,000 records, with formula neutralization for text fields. Imports create records; they do not silently update existing rows. Reference fields use UUIDs. Use the backup for a complete installation export.

Generic source-record reports remain bounded and return explicit limit messages rather than partial results. The Meters workspace instead uses bounded time windows and aggregates beyond10,000raw readings. Reference choices use searchable50-row pages with native label/current-choice support. Module-specific contracts define their own limits.

The [target-stock replenishment workspace](docs/pantry-replenishment.md) explains exact stock, pack rounding, surplus and reviewed Shopping transfer. [Reservations](docs/stock-reservations.md) distinguish physical, reserved, eligible-free and undercovered stock.

The locally accepted [meal-demand workspace](docs/planning-demand.md) and [purchasing workspace](docs/planning-shopping.md) preserve frozen ingredient mappings, exact coverage and attributable Shopping contributions. Mapping alone changes no stock. The full browser registry currently has34components; its final17share one cleaned-up private test session without changing production login limits.

## Local OCR

The default image supports manual capture review and local PDF text/preview. For optional local Tesseract processing of scanned Documents and JPEG/PNG receipts:

```sh
docker compose -f compose.yaml -f compose.ocr.yaml up -d --build
```

The OCR image bundles German/English language data. Existing PDF text is preferred; scanned pages can use bounded local OCR. Extracted text remains reviewable and never silently confirms metadata, deadlines or financial records. Web Push is separately optional and off by default; cloud AI and passkeys are outside this release.

## Encrypted backups

```sh
docker compose -f compose.yaml -f compose.backup.yaml up -d --build backup
```

The helper creates an initial encrypted restic snapshot, then runs daily at 03:00 Europe/Vienna. It retains 7 daily, 4 weekly, and 12 monthly snapshots. Database dumps and uploads are taken behind the household write barrier, with a schema manifest and encrypted security-key bundle. Full repository data checks follow each backup. The repository and work directories use separate named volumes.

The local backup volume protects against application errors; copy the encrypted repository to separate storage for host-loss protection. Keep `.local/secrets/restic_password` independently. A backup is marked verified only after a successful isolated database restore and file-hash verification. See [the recovery runbook](docs/operations.md#restore-verification).

For a Shopping8 restore, the maintenance owner preserves the migration-pinned barcode CHECK representation after PostgreSQL's dump/reparse roundtrip. Only the two reviewed equivalent representations are accepted. The bounded transaction locks the restored table, confirms its exact catalog shape, and commits only if the existing strict barcode readiness check succeeds; unrelated drift rolls back. Application-role permissions and migration checksums are unchanged.

## Configuration

| Variable | Purpose / default |
|---|---|
| `HOUSEHOLD_ENV` | `production` or explicitly `development` |
| `HOUSEHOLD_ORIGIN` | Canonical scheme/host/port; HTTPS required in production |
| `HOUSEHOLD_PORT` | Compose loopback port, 8100 |
| `HOUSEHOLD_TRUSTED_PROXIES` | Comma-separated exact proxy CIDRs; empty trusts none |
| `HOUSEHOLD_DATABASE_URL_FILE` | Runtime connection secret; owner URL belongs only to migration/maintenance |
| `HOUSEHOLD_CSRF_SECRET_FILE` | Independent random CSRF key |
| `HOUSEHOLD_ENCRYPTION_KEY_FILE` | Independent key protecting stored TOTP secrets |
| `HOUSEHOLD_RATE_SECRET_FILE` | Independent key for private rate-limit subjects |
| `HOUSEHOLD_ADMIN_PASSWORD_FILE` | Bootstrap/recovery password; no default password |
| `HOUSEHOLD_DB_MAX_CONNECTIONS` | 2–100, default 10 |
| `HOUSEHOLD_UPLOAD_DIR` | Private file directory, `/data/uploads` in Docker |
| `HOUSEHOLD_OCR_ENABLED` | Off unless the OCR image/profile is selected |
| `HOUSEHOLD_OFF_ENABLED` | Optional server-side product suggestions, default `false` |
| `HOUSEHOLD_OFF_USER_AGENT` | Explicit application/version and operator contact; missing means not configured |
| `HOUSEHOLD_OFF_STAGING` | Fixed OFF staging service, development only; default `false` |
| `HOUSEHOLD_METRICS_TOKEN_FILE` | Optional secret for `/metrics` via `X-Metrics-Token` |

Secret settings accept either their plain environment form or `_FILE`, never both. Keep the encryption key with recoverable backups; replacing it without re-encrypting existing secrets breaks TOTP recovery.

The origin is normalized to browser form (lowercase host, no trailing root slash or default port). Paths, queries, fragments and invalid ports are rejected; international DNS names use ASCII/punycode. Maintenance database URLs must explicitly specify user, host, port and database. Set `sslmode` explicitly so PostgreSQL tools and application connections use the intended TLS policy; see [maintenance connection rules](docs/operations.md#backups).

The in-progress OFF adapter uses only the fixed production or explicit development staging APIv3 service; it accepts no custom upstream URL. The browser contacts Household only. Local Pantry aliases win, and an external request needs explicit consent and a suitable GTIN. Product name, brand and pack text remain reviewed suggestions with ODbL/DbCL attribution; the complete retrieved proposal and selected-field markers remain as historical provenance after adoption. There are no external images, uploads, automatic stock entries or shelf-life claims. Global persistent request budget, cache limits and current authority rechecks are implemented and under joint module testing, not yet operationally accepted.

## Develop and verify

The Docker compiler uses two Go execution threads and a 2 GiB soft memory limit to keep source builds predictable on smaller self-hosted machines. These build-stage settings do not enter the runtime image; runtime limits remain configured through Compose.

Native integration packages have a bounded 60-minute Go test budget and a 75-minute CI job budget. These orchestration limits accommodate the complete populated database suite; individual SQL and application-operation deadlines retain their existing limits.

The Finance browser component has a bounded ten-minute orchestration budget for its complete multi-user workflow; other components retain five minutes. This changes only the test starter. Individual UI waits and application deadlines stay unchanged. Credential-free phase timings locate slow stages, and mobile screenshots show their actual task content in a 390px viewport.


Go **1.27.3** or newer is required. The Docker builders are pinned to an immutable Go image digest. Production assets are embedded with `embed.FS` and need no Node build step.

```powershell
./scripts/verify.ps1 -Integration
# Linux container race detector, run sequentially after integration:
./scripts/test-race.ps1
# Optional focused regression; the default above still enforces full coverage:
./scripts/test-race.ps1 -Run 'TestShopping|TestReference'
```

The integration script starts a separate disposable PostgreSQL container on `127.0.0.1:55434`, validates both database roles and the actual database, and resets only compiled schemas in `household_test` before/after each fixture. `HOUSEHOLD_REQUIRE_INTEGRATION=1` prevents missing URLs silently skipping CI tests. Browser checks prepare their own data through the real API on port 8101. Run database and browser suites sequentially:

Each ordinary fixture runs all migrations once against fresh schemas and performs real authentication. `TestIntegrationMigrationRepeatAndAuditRepair` separately verifies repeatability, readiness and privileged audit-trigger repair; historical populated-upgrade regressions remain separate. Phase timings and the bounded local comparison are documented in [test-fixture-performance.md](docs/test-fixture-performance.md). No schema cache is used.

Migration checksums pin exact SQL bytes. Git attributes disable newline conversion within `internal/database/migrations/`; preserve this rule so Windows staging and Linux checkouts keep the verified migration bytes unchanged.

```powershell
./scripts/run-review-server.ps1
# In another terminal:
npm run test:browser
```

Use the bundled Playwright runtime in this workstation, or install the pinned development dependencies in [package.json](package.json). The review server resets only the test database's `bootstrap` account. Never point it at a live household.

Browser orchestration can set the development-only `HOUSEHOLD_BROWSER_PRIVATE_ROOT` to an existing OS-protected directory inside this project's `.local`. The suite creates and removes its private handoff child there; outside paths, non-directories and escaping junctions are rejected. Without this setting its existing `.local` behavior is retained. The isolated Fuel regression wrapper supplies its ACL-restricted per-run directory; authentication state is never a public test artifact.

Fuel has a separate [OCR/recovery verification entry point](docs/fuel-module-browser.md), `scripts/run-fuel-module-browser.ps1`, and dedicated `fuel-ocr-recovery.yml` workflow. They require real current native test evidence and source-labelled immutable images, six actual local OCR sources, an encrypted backup, restore verification and a generation-incrementing purge. These are kept outside the shared browser chain. The installed Windows entry passes the complete actual lifecycle; Linux browser/recovery and hosted workflow execution remain unverified. Forced or uncertain Chromium cleanup denies success and retains private recovery state.

CI defines **GOLint, GOSec, GOVul, GODep**, race/integration tests, browser checks, workflow linting, immutable action-pin checks, and Gitleaks secret scanning. Gosec scans all application packages through `./cmd/... ./internal/...`, keeping ignored historical working copies outside its scope. The exported Household OCI candidate is checked for blob integrity, non-root runtime configuration, populated SPDX SBOM and matching provenance subjects. These checks verify attached evidence, not a trusted signature. It does not publish automatically. See [the test guide](docs/testing.md) and [verification evidence](docs/implementation.md); workflow files alone are not evidence of a GitHub run.

The Go integration/race workflow now discovers the actual registered tests and assigns them deterministically to six jobs, each with an independent PostgreSQL service. `scripts/run-go-test-shard.cjs` requires every assigned test to finish successfully and binds raw results/atomic coverage to the exact source, compiler and inventory. `scripts/merge-go-test-shards.cjs` requires all six matching artifacts and identical coverage block definitions before producing statement-presence coverage (`mode: set`), enforcing the existing55% floor and rendering HTML. Raw execution counts remain in the original profiles. Local contract checks cover missing/failed/skipped tests and mismatched artifacts; an actual six-job GitHub run is not claimed.

`npm test` exercises the quality-checking tools, including malformed coverage/OCI/workflow inputs. Set `HOUSEHOLD_LOAD_CHECK=1` when running the browser suite to include a bounded 1,000-row concurrent workload; results are written to `outputs/qa/load-report.json`. This local regression budget is separate from production capacity acceptance.

The latest [security, audit and usability review](docs/audit-review.md) records passing Windows integration and Linux race suites, 61.3% combined internal-package coverage, 149 checks across three browser suites, eight clean axe scans, and a fresh encrypted restore with file verification. Unit-only coverage is 15.2%; CI enforces separate 55% combined and 14% unit floors. The [earlier QA review](docs/qa-review.md) remains historical evidence. Broader release acceptance remains visible in the checklist.

On Windows, `./scripts/test-recovery.ps1` rehearses an encrypted backup and restore with synthetic domain data, an original file and a preview in the disposable recovery service. It verifies exact values and file hashes, then cleans its test source and temporary repository volumes. It never uses the live database as the fixture source or restore target.

## Source map

```text
cmd/household       serve, migrate, healthcheck, recover, backup, restore-check, purge
cmd/dependency-check  runtime dependency/license inventory
cmd/test-fixture    synthetic review account, requires testtools build tag
internal/auth      credentials, sessions, TOTP, durable limits
internal/audit     transaction attribution and constrained audit events
internal/document  typed document fields and bounded local PDF/OCR/scan tools
internal/documentinput  optional read-only watch/IMAP/Paperless adapters
internal/config    validated deployment configuration
internal/database  pool, embedded migrations, schema readiness
internal/module    compiled descriptor/input contracts
internal/modules   nine independent module descriptors
internal/recipeimport  bounded portable recipe formats, inert HTML parsing and pinned HTTPS fetch
internal/store     transactional domain rules and integrations
internal/web       HTTP, embedded templates/assets, private uploads
internal/worker    leased jobs, OCR, previews, reminders, cleanup
internal/maintenance  operator-only backup, verification and purge
deploy/            database initialization and backup image
docs/              architecture, schema, plan, operating runbooks and evidence
scripts/           initialization and repeatable checks
```

Read [architecture](docs/architecture.md), [schema](docs/database.md), [security](docs/security.md), and the [220-step master plan](docs/master-plan.md). `AGENTS.md` requires README updates when structure, configuration, or architecture changes.

The earlier [interactive phase overview](outputs/design-review/phase-overview.html) and [50 interactive design examples](outputs/design-review/household-50-examples.html) are review artifacts in this workspace. They are ignored by Git and not part of the production binary. Their review checkmarks are not release verification.

The final [design review](docs/design-review.md) records the independent finding scores and the limits of the browser/accessibility evidence. Offline draft review uses German labels and named relationships; reports retain vehicle/device identity and localized interval dates.

## License

MIT. Runtime Go dependencies use permissive MIT, BSD, and Apache-2.0 licenses; `cmd/dependency-check` records exact versions and license-file hashes. System fonts and project-drawn SVG icons avoid remote/font-license dependencies. PostgreSQL, Alpine, restic, and optional Tesseract remain under their respective open-source licenses. Preserve upstream notices when redistributing images.

The disposable database in "compose.test.yaml" uses a 1 GiB tmpfs with bounded WAL and frequent checkpoints for repeated migration tests. It is isolated on 127.0.0.1:55434; these test settings do not modify the live database. See the [test guide](docs/testing.md).

CI splits the actual registered integration/race inventory into six isolated jobs. Local complete validation can use smaller closed packets with the same strict discovery, event ledger, source hashes and unchanged test deadlines. Interrupted/failed attempts remain failed; only complete passing main trees and complete successful coverage profiles are qualified. See [implementation evidence](docs/implementation.md).

### Shopping SH15/SH17: local module acceptance

The full sequence is in [docs/shopping-module-plan.md](docs/shopping-module-plan.md); closed API/device contracts are in [docs/shopping-extension-api.md](docs/shopping-extension-api.md). Implementation and source review preceded module testing. The [validation record](docs/shopping-module-validation.md) retains actual passes, failed runs, corrections and evidence limits. SH15/SH17 were locally verified at checkpoint `3c5d710`; the live checklist has since advanced to the Recipes module.

SH15 adds personal list subscriptions and grouped in-app notices. SH17 adds explicitly saved complete offline read lists with limits of 10 lists / 1,000 items / 2 MiB / 7 days. Consent explicitly mentions saved item notes and display labels. The neutral `/offline/shopping` shell contains no household data, and private HTML/API responses stay outside service-worker caches. Read copies have no mutation controls.

Web Push is optional and disabled by default. Base Compose requires no new key. After building the updated image, operators can run `household push-keygen --private-out /keys/web_push_private_key --public-out /keys/web_push_public_key` with an explicitly mounted writable output directory. This command needs no database, prints no keys and refuses existing output files. Both Unix output files use mode 0600; Windows also depends on the chosen directory's ACLs. Keep the private key at `.local/secrets/web_push_private_key` for Compose, back it up securely, and never commit it. The public file is not secret and may be shared deliberately.

For optional delivery set `HOUSEHOLD_WEB_PUSH_ENABLED=true` and `HOUSEHOLD_WEB_PUSH_VAPID_CONTACT=mailto:operator@example.org`, then use `docker compose -f compose.yaml -f compose.webpush.yaml up -d --build`. An HTTPS contact URI on port 443 is also supported. The overlay adds the extra secret only to the app; migrations need no push key. The server derives the public VAPID key. Rotation requires deliberate device reconnection. Personal encrypted connections remain withdrawable while Shopping is paused. Browser permission is requested only after a deliberate user action.

Delivery uses reviewed Google/Mozilla/Apple/Microsoft production gateways with fixed host, HTTPS, DNS and time limits. The lock-screen text is always “Änderungen im Einkauf. Household öffnen.” and the click target is the local inbox. Browser permission, server registration and push-gateway acceptance are separate states; an accepted HTTP response does not prove physical device display. In-app notices work independently of Web Push. No new SQL migrations are introduced; all 75 accepted migrations remain unchanged.

The ordinary integration suite covers personal registration, device privacy and worker progression. Controlled external-request tests additionally use `integration,testtools`; `node scripts/run-shopping-push-testtools.cjs` is a separate race-enabled Linux CI campaign with exact source-derived required mains and refusal of empty/skipped/unfinished runs. The six existing integration-only coverage shards remain separate. The local `scripts/test-integration.ps1` includes the controlled test tag without requiring a production transport override. Local native, controlled protocol, scoped Linux race, browser and encrypted recovery campaigns passed with the exact source boundaries recorded in the validation record. Hosted CI, vendor device display and a permanent backup schedule were not exercised locally.

### Recipes RE05/06/09/13/14/15/17/18/19/20: locally accepted

The [Recipes module plan](docs/recipes-module-plan.md) defines the ten improvements and their completed joint acceptance gate. Source adds `/m/recipes/find`, `/m/recipes/import-review`, `/m/recipes/recipes/{id}/insights` and the neutral `/offline/recipes` shell. Private `/api/v1/recipes/` routes provide filtered discovery, purpose-bound import previews and explicit per-row confirmation, a portable Household JSON v1 download, sourced recipe insights, and consent-based read snapshots. Household JSON, selected Schema.org, Mealie, Tandoor and Paprika profiles are bounded in `internal/recipeimport`; URL import permits only public HTTPS:443 with a pinned resolved IP. Source URLs stored in recipes omit query parameters. Personal filter views and selected offline read copies reside only on the device and are cleared on logout or opt-out. Fresh and populated migrations stage Core26 with Fuel5 and accepted histories, followed by Core27, Recipes5 and Fuel6. The [validation ledger](docs/recipes-module-validation.md) records actual local acceptance and source boundaries. Local checkpoint0227996 contains the implementation; live8100 remains unchanged.

### Mealplan MP08/13/15/19: locally accepted

The [complete Mealplan plan](docs/mealplan-module-plan.md) fixes the four remaining features before source changes: forecast-only leftover allocations, exact frozen-snapshot weekly costs, personal revocable seven-day calendar links and deterministic reviewed whole-week autoplan. Five new Mealplan tables require forward Core28/Mealplan8/Fuel7/Pantry13 migrations with all81 released SQL bytes preserved. Implementation precedes joint module tests; this scope publication is not runtime acceptance. Current checklist: **97 verified / 4 in progress / 79 open**.

### Meters ME01–ME20: locally accepted

The [complete 120-step plan](docs/meters-module-plan.md), [module contract](docs/meters-module-contract.md) and [acceptance map](docs/meters-acceptance-map.md) define the whole prepared module. The workspace at `/m/meters/workspace/overview` provides mobile reading/photo capture, physical replacements, independent typed registers, exact consumption/coverage, tariff and actual-payment comparison, original-bill review, reading plans, CSV previews and optional telemetry mapping. Private data remains on the server; all interface assets are local and embedded. All20criteria are locally accepted:776source-qualified complete Linux-race main trees/70.51%coverage, final12quality gates/Node297, actual browser/Axe/recovery/purge, current non-root images/OCI and exact-index review pass. Code checkpoint`e25b88b`; overall **160 verified / 20 Documents open**. Original failed attempts and exact source boundaries remain in the [validation ledger](docs/meters-module-validation.md).

Forward migrations use Meters2/Core33/Fuel10/Pantry16/Mealplan11 after the exact accepted Chores predecessors. All 97 released SQL files remain unchanged; 102 raw migration pins cover the prepared schema. A shared civil-time package preserves the accepted Chores fold/gap behavior. The existing worker runs bounded interval/aggregate jobs and optional telemetry feeds with current generation/lease fences. Raw telemetry retention is explicitly configured by an administrator and disabled by default; manual readings, original revisions and device-change evidence remain retained.

Application and PostgreSQL continue to run in the same isolated Docker Compose services. HA/MQTT are optional and off by default. Add only the required `compose.meters-ha.yaml`, `compose.meters-mqtt.yaml` and optional `compose.meters-ca.yaml` overlays to the normal Compose invocation. Set the trusted HA/broker origin, MQTT account and operator-owned `HOUSEHOLD_METERS_HA_TOKEN_PATH` / `HOUSEHOLD_METERS_MQTT_PASSWORD_PATH` files. The overlays mount them as `_FILE` secrets. Optional `HOUSEHOLD_METERS_CA_PATH` supplies a private CA. TLS verification is mandatory unless the operator explicitly permits trusted plaintext with `HOUSEHOLD_METERS_ALLOW_PLAINTEXT=true`. Then approve the exact sensor/topic, physical device, register type and unit in the Meters settings. No endpoint credential is sent to the browser. See the [operator configuration and protocol bounds](docs/meters-module-contract.md#optional-adapters-and-operator-configuration).

Meters is locally accepted at codee25b88b after complete source-qualified race, browser/visual, encrypted recovery, security and exact-index checks. The [validation record](docs/meters-module-validation.md) preserves original failures/timeouts and precise source qualifications. Local acceptance does not deploy to8100.

The previous generic Documents screens remain read-only entry points with links to the managed workspace. Existing pending capture links retain authorized read access, while archived/missing sources deny that text. Their old confirmation endpoint cannot reparent an immutable original; review and new processing use the Documents workspace.
