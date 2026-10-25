# Household

A self-hosted, mobile-first household PWA. Go serves the embedded interface and JSON API; PostgreSQL stores normalized module data. Docker Compose runs the application, an isolated database, and a one-shot migration service. No frontend CDN, cloud account, paid license, remote font, or external script is required.

The default modules are **shopping, fuel, recipes, pantry, and expenses**. **Chores, meters, documents/warranties, and meal planning** can be enabled in settings. One installation holds one household with multiple administrators, editors, and readers. The interface is German, with EUR and Europe/Vienna defaults.

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

The approved expansion is tracked in [the 180-item checklist](docs/improvements-180.md), with separate implementation and executed-test evidence. The current development batch adds searchable reference choices (50 results per page, signed scope-bound cursors), list-specific quick entry with reviewed local quantity parsing, grouped shopping mode, version-aware conflict review and adaptive live refresh. Shopping reads use 100-row pages and whole-list progress; private responses remain uncached. The [catalog extension](docs/shopping-catalog.md) adds store-specific aisle order, explicit product packs and private previews, substitutes, household assignments, a 30-minute shopping activity claim and dated exact-price estimates. [List reuse](docs/shopping-reuse.md) adds household product favourites and explicit templates: favourites fill a reviewable draft; copying at most 500 items creates an independent working list with reset purchase state. Controlled consolidation now offers a server-calculated preview and an atomic, version-checked commit for compatible positions; attachments or links prevent removal until reviewed. The [pantry workspace](docs/pantry-workspace.md) now provides exact, filtered stock pages, separately loaded lots and historical day-end quantities. Transactional lot balances remain derived from the immutable journal. The [reviewed checkout](docs/shopping-checkout.md) adds explicit per-position storage decisions, actual quantities and prices, receipt comparison and a durable once-only completion. Its purchase snapshot, stock lots, journal, projections and list archive commit together. The [recipe workspace](docs/recipe-workspace.md) joins metadata, ingredients, steps and tags in one atomic editor with explicit device drafts, named conflict comparison, immutable complete versions and independent variants. The [cooking workspace](docs/cooking-session.md) adds private sessions tied to an immutable recipe version, exact portion scaling, persistent multiple timers and explicitly confirmed household cooking history. The [meal-planning workspace](docs/mealplan-workspace.md) adds a desktop week grid and mobile agenda, independent recipe components, reviewed source changes, move/swap controls and durable template copying. This ten-item development slice has passed local regression and scoped independent acceptance. The [pantry daily-workflow slice](docs/pantry-daily.md) adds exact packs, local barcode aliases, opening-date guidance and reviewed consumption/undo. Its schema, exact value rules, transactional consumption/undo, priority reads and mobile review screens passed local integration, scoped Linux race, restore, browser and independent acceptance;that checkpoint verified44 of the180 improvements. The [ingredient catalogue/comparison slice](docs/ingredient-pantry.md) adds explicit catalogue/alias selection, versioned Pantry-product bindings and a read-only exact portion/version stock comparison. It has passed complete native integration, scoped Linux race, encrypted restore, browser and independent UI/documentation acceptance;that checkpoint verified46 of180 improvements. Current development migrations are core18, shopping4, pantry6, recipes4 and mealplan2; readiness requires67 audit policies. The [intake/inventory batch workspace](docs/pantry-batches.md) provides private server drafts, exact pack intake, location-scoped selected-lot counts including zero balances, explicit stale-stock review and durable atomic receipts through dedicated APIs and the local PWA interface. PA04/PA05 passed complete local native regression, scoped Linux race, encrypted restore and the sixteen-component browser chain. Housekeeping prunes at most100 discarded/expired unposted drafts per job and retains posted sources. The verified RE12 checkpoint fd7297c uses core16/pantry4. The [reviewed cooking-stock posting](docs/cooking-stock.md) adds a separate completed-cooking review with explicit consume/skip decisions, aggregate lot checks, atomic journal posting, durable replay and per-lot reversal. It has passed complete native integration, scoped Linux race, encrypted restore,515-check browser regression and scoped independent database/UI/documentation acceptance; the RE12 checkpoint verified47 of180 improvements; accepted PA04/PA05 now bring the total to49. This expansion is still in progress and is not represented as 180 completed features or as deployed to the local production container.

| Area | Behavior |
|---|---|
| Shopping | Shared lists, quantities/units, groups, stable ordering, versioned checking, short undo, archives, CSV preview/import/export |
| Fuel | Vehicles, odometer series, partial/full/missing fill handling, exact costs, consumption segments, explicit expense creation |
| Recipes | Atomic editor, immutable versions/variants, cooking sessions/timers, explicit ingredient catalogue/aliases and product bindings, read-only version/portion stock comparison, reviewed atomic consumption from completed cooking, selected-ingredient shopping transfer |
| Pantry | Products, locations, dated lots, immutable movements, concurrent consumption protection, compensating corrections, transfers, low-stock and expiry reminders |
| Expenses | Exact integer cents, drafts/refunds, category allocations, deferred total validation, immutable postings, reversals, monthly reports |
| Extensions | Recurring assigned chores, physical meter history and consumption, document deadlines, meal plans linked to recipes |
| Shared services | Private image/PDF files, sanitized image previews, local capture review, authorized full-text/trigram search, notifications, durable jobs and outbox |
| PWA | Install manifest/icons, versioned public asset cache, neutral offline screen, opt-in actor-bound offline drafts and explicit replay review |
| Security | Bcrypt, hashed sessions, capped sliding expiry, HMAC CSRF, PostgreSQL rate limits, TOTP/recovery codes, recent-login checks, role management and field-level audit history |

Administrators open **Settings → Protokoll** to inspect changes, compare before/after fields, and filter by module, operation, resource or request. Core migration 5 installs transaction-bound audit triggers on 45 authoritative tables, including all 26 domain tables. Secret and private free-text fields are masked before storage; ordinary domain names/titles and exact numeric values are explicitly allowed. The runtime cannot insert or edit audit rows directly. [Audit architecture and privacy boundaries](docs/audit-trail.md) describe exclusions, owner privileges, legacy events and retention.

Changes use optimistic versions and idempotency keys. Financial postings and stock movements are corrected through new entries. Pausing a module retains its data and returns 404 for its routes, files, search, and exports. Purge is a separate operator action requiring a recent admin request and verified backup.

Cross-module actions bind both source and destination data generations. Purge requires a backup created after the current pause; retention reconciles expired snapshot references before they can be used for deletion approval. Migration checksums and append-only migration history are checked in CI.

CSV import is limited to 500 rows and commits all rows together after confirmation. Exports are limited to 10,000 records, with formula neutralization for text fields. Imports create records; they do not silently update existing rows. Reference fields use UUIDs. Use the backup for a complete installation export.

Pantry reports support up to 100 products; fuel and meter reports up to 10,000 source records. Larger reports return an explicit limit message instead of partial results. Reference choices use searchable 50-row pages and retain named selected values beyond the current page.

The [target-stock replenishment slice](docs/pantry-replenishment.md) is locally verified: nullable targets preserve existing minimums, exact stock review explains whole-pack rounding and surplus, and explicit Shopping confirmation rejects changed stock, pack or destination generations. Manual base-unit purchases remain available beside automatic review. The checklist now has50 verified improvements; [acceptance evidence](docs/pantry-replenishment-verification.md) records native, scoped race, restore and584 browser checks. [Stock reservations](docs/stock-reservations.md) are the next planned slice.

## Local OCR

The small default image supports manual capture review. For local Tesseract processing of JPEG/PNG receipts:

```sh
docker compose -f compose.yaml -f compose.ocr.yaml up -d --build
```

The OCR image bundles German/English language data. It proposes conservative fields, retains the source text for review, and never creates a record automatically. PDF attachments remain downloadable and can be reviewed manually. Cloud AI, Web Push, and passkeys are outside this release; their unused integration tables do not imply those features are active.

## Encrypted backups

```sh
docker compose -f compose.yaml -f compose.backup.yaml up -d --build backup
```

The helper creates an initial encrypted restic snapshot, then runs daily at 03:00 Europe/Vienna. It retains 7 daily, 4 weekly, and 12 monthly snapshots. Database dumps and uploads are taken behind the household write barrier, with a schema manifest and encrypted security-key bundle. Full repository data checks follow each backup. The repository and work directories use separate named volumes.

The local backup volume protects against application errors; copy the encrypted repository to separate storage for host-loss protection. Keep `.local/secrets/restic_password` independently. A backup is marked verified only after a successful isolated database restore and file-hash verification. See [the recovery runbook](docs/operations.md#restore-verification).

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
| `HOUSEHOLD_METRICS_TOKEN_FILE` | Optional secret for `/metrics` via `X-Metrics-Token` |

Secret settings accept either their plain environment form or `_FILE`, never both. Keep the encryption key with recoverable backups; replacing it without re-encrypting existing secrets breaks TOTP recovery.

The origin is normalized to browser form (lowercase host, no trailing root slash or default port). Paths, queries, fragments and invalid ports are rejected; international DNS names use ASCII/punycode. Maintenance database URLs must explicitly specify user, host, port and database. Set `sslmode` explicitly so PostgreSQL tools and application connections use the intended TLS policy; see [maintenance connection rules](docs/operations.md#backups).

## Develop and verify

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

```powershell
./scripts/run-review-server.ps1
# In another terminal:
npm run test:browser
```

Use the bundled Playwright runtime in this workstation, or install the pinned development dependencies in [package.json](package.json). The review server resets only the test database's `bootstrap` account. Never point it at a live household.

CI defines **GOLint, GOSec, GOVul, GODep**, race/integration tests, browser checks, workflow linting, immutable action-pin checks, and Gitleaks secret scanning. The exported Household OCI candidate is checked for blob integrity, non-root runtime configuration, populated SPDX SBOM and matching provenance subjects. These checks verify attached evidence, not a trusted signature. It does not publish automatically. See [the test guide](docs/testing.md) and [verification evidence](docs/implementation.md); workflow files alone are not evidence of a GitHub run.

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
internal/config    validated deployment configuration
internal/database  pool, embedded migrations, schema readiness
internal/module    compiled descriptor/input contracts
internal/modules   nine independent module descriptors
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
