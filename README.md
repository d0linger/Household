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

| Area | Behavior |
|---|---|
| Shopping | Shared lists, quantities/units, groups, stable ordering, versioned checking, short undo, archives, CSV preview/import/export |
| Fuel | Vehicles, odometer series, partial/full/missing fill handling, exact costs, consumption segments, explicit expense creation |
| Recipes | Ingredients, ordered instructions, timers, tags, portion scaling, compatible unit conversion, selected-ingredient shopping transfer |
| Pantry | Products, locations, dated lots, immutable movements, concurrent consumption protection, compensating corrections, transfers, low-stock and expiry reminders |
| Expenses | Exact integer cents, drafts/refunds, category allocations, deferred total validation, immutable postings, reversals, monthly reports |
| Extensions | Recurring assigned chores, physical meter history and consumption, document deadlines, meal plans linked to recipes |
| Shared services | Private image/PDF files, sanitized image previews, local capture review, authorized full-text/trigram search, notifications, durable jobs and outbox |
| PWA | Install manifest/icons, versioned public asset cache, neutral offline screen, opt-in actor-bound offline drafts and explicit replay review |
| Security | Bcrypt, hashed sessions, capped sliding expiry, HMAC CSRF, PostgreSQL rate limits, TOTP/recovery codes, recent-login checks, role management and append-only audit |

Changes use optimistic versions and idempotency keys. Financial postings and stock movements are corrected through new entries. Pausing a module retains its data and returns 404 for its routes, files, search, and exports. Purge is a separate operator action requiring a recent admin request and verified backup.

Cross-module actions bind both source and destination data generations. Purge requires a backup created after the current pause; retention reconciles expired snapshot references before they can be used for deletion approval. Migration checksums and append-only migration history are checked in CI.

CSV import is limited to 500 rows and commits all rows together after confirmation. Exports are limited to 10,000 records, with formula neutralization for text fields. Imports create records; they do not silently update existing rows. Reference fields use UUIDs. Use the backup for a complete installation export.

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
```

The integration script starts a separate disposable PostgreSQL container on `127.0.0.1:55434`, validates both database roles and the actual database, and resets only compiled schemas in `household_test` before/after each fixture. `HOUSEHOLD_REQUIRE_INTEGRATION=1` prevents missing URLs silently skipping CI tests. Browser checks prepare their own data through the real API on port 8101. Run database and browser suites sequentially:

```powershell
./scripts/run-review-server.ps1
# In another terminal:
node ./scripts/browser-check.cjs
```

Use the bundled Playwright runtime in this workstation, or install the pinned development dependencies in [package.json](package.json). The review server resets only the test database's `bootstrap` account. Never point it at a live household.

CI defines **GOLint, GOSec, GOVul, GODep**, race/integration tests, browser checks, workflow linting, immutable action-pin checks, and Gitleaks secret scanning. The exported Household OCI candidate is checked for blob integrity, non-root runtime configuration, populated SPDX SBOM and matching provenance subjects. These checks verify attached evidence, not a trusted signature. It does not publish automatically. See [the test guide](docs/testing.md) and [verification evidence](docs/implementation.md); workflow files alone are not evidence of a GitHub run.

`npm test` exercises the quality-checking tools, including malformed coverage/OCI/workflow inputs. Set `HOUSEHOLD_LOAD_CHECK=1` when running the browser suite to include a bounded 1,000-row concurrent workload; results are written to `outputs/qa/load-report.json`. This local regression budget is separate from production capacity acceptance.

The [2026-10-24 QA review](docs/qa-review.md) records the fixes and measured results: 96 browser checks, passing Windows integration and Linux race suites, 57.2% combined internal-package statement coverage and a fresh encrypted restore with file verification. CI enforces a 55% combined coverage floor; unit-only coverage uses a separate 14% floor. Broader release acceptance remains visible in the checklist.

On Windows, `./scripts/test-recovery.ps1` rehearses an encrypted backup and restore with synthetic domain data, an original file and a preview in the disposable recovery service. It verifies exact values and file hashes, then cleans its test source and temporary repository volumes. It never uses the live database as the fixture source or restore target.

## Source map

```text
cmd/household       serve, migrate, healthcheck, recover, backup, restore-check, purge
cmd/dependency-check  runtime dependency/license inventory
cmd/test-fixture    synthetic review account, requires testtools build tag
internal/auth      credentials, sessions, TOTP, durable limits
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
