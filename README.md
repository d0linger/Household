# Household

**A self-hosted household app for everyday planning.** Shopping, vehicles, recipes, pantry, finances and optional modules share one mobile-first PWA. Go serves the interface and API from one binary; PostgreSQL holds the data. Docker Compose runs the app, database and one-shot migrations. The interface is available in German and English. Frontend scripts, styles, fonts and icons are served locally.

[![Production image](https://github.com/d0linger/Household/actions/workflows/release.yml/badge.svg)](https://github.com/d0linger/Household/actions/workflows/release.yml) [![Browser checks](https://github.com/d0linger/Household/actions/workflows/browser.yml/badge.svg)](https://github.com/d0linger/Household/actions/workflows/browser.yml) [MIT license](LICENSE)

> One installation serves one household. `dev` is the development branch; checked `dev` → `main` PRs feed the production image. Publishing an image does not deploy an installation. The [release procedure](docs/release-process.md) explains the gates and rollback.

## Features

| Area | What it covers |
| --- | --- |
| Today | Personal attention items, active cooking and household-day meals. |
| Shopping | Shared lists, quantities, ordering, reviewed imports and exports, barcode and repurchase suggestions. |
| Vehicles | Fuel and odometer records, maintenance, exact consumption and optional expense handoff. |
| Recipes | Versioned recipes, cooking sessions and timers, reviewed stock use and Shopping transfer. |
| Pantry | Products, locations, dated lots, stock movements, shortages and expiry review. |
| Finance | Exact money, private access, budgets, accounts, receipts, reviewed imports and immutable postings. |
| Meal Planning | Week and agenda views, portions, participants, stock demand and reviewed purchases. |
| Chores | Assignments, schedules, rotations, history and personal day plans. |
| Meters | Readings, physical replacements, tariffs, bills and optional Home Assistant/MQTT adapters. |
| Documents | Inbox, preserved originals, scans, local PDF/OCR review, deadlines and protected search. |

Shopping, Vehicles, Recipes, Pantry and Finance are enabled initially. Administrators can enable Meal Planning, Chores, Meters and Documents without deleting their data when paused. Optional integrations are disabled until configured and approved. The [180-item checklist](docs/taskliste.md) and [module acceptance records](docs/improvements-180.md) track the implemented scope and its verification.

The UI offers Ocean and Pink families, each with Light, Dark and System appearance. Members can order their own module navigation. The PWA caches public assets and an offline shell; private API responses are not cached. Notifications require account and device consent. Physical push delivery on Windows, Android and iOS still needs testing against a device-reachable HTTPS installation; see the [notification record](docs/notifications-implementation.md).

## Quick start

Requirements: Docker Engine with Compose v2 (rootful or [rootless](docs/installation.md#rootless-docker)). No source code, Go or scripts are needed. [`compose.yaml`](compose.yaml) is the only deployment file. Step-by-step guide in German: [docs/installation.md](docs/installation.md).

```sh
curl -fsSLO https://raw.githubusercontent.com/d0linger/Household/main/compose.yaml
(umask 077; set -C; docker run --rm ghcr.io/d0linger/household:production init > .env)
# set HOUSEHOLD_ORIGIN in .env; for a local trial use "init --development" instead
docker compose up -d
```

`umask 077` keeps `.env` readable only by your user, and `set -C` refuses to overwrite an existing `.env`, whose keys an installation needs; delete an empty `.env` left by a failed run before retrying. `household init` writes one `.env` with every setting and freshly generated password and key, including the Web Push key. It never prints them to a terminal. **Back up `.env` separately:** without it, encrypted data and backups cannot be read.

`docker compose up -d` then starts three services:
- `db`: PostgreSQL, without a published port.
- `app`: its `start` command creates the database roles as the PostgreSQL superuser, migrates as the owner and serves as the restricted app role on `127.0.0.1:8100`.
- `backup`: daily encrypted restic snapshots. It is also the owner-credential maintenance service, e.g. `docker compose run --rm --no-deps backup recover admin`.

Every container runs without root; `app` and `backup` also run with a read-only root filesystem (`db` keeps PostgreSQL's writable root). Sign in as `admin` with `HOUSEHOLD_ADMIN_PASSWORD` and change it at first sign-in.

For production, put an HTTPS reverse proxy in front, set the exact `HOUSEHOLD_ORIGIN` and `HOUSEHOLD_TRUSTED_PROXIES`, and verify an encrypted backup with `docker compose run --rm --no-deps backup restore-check SNAPSHOT_ID`, which restores into a temporary database of its own (`household_restore_auto_…`) on the same server and always drops it afterwards. See [deployment, upgrades and recovery](docs/operations.md). The app supports one instance per household and one application replica.

The published image is `ghcr.io/d0linger/household:production` (also `:latest`). It is built from the `ocr` target and includes Tesseract, `restic` and the PostgreSQL 18 client. Pin a deployment to a verified digest rather than a mutable tag. To run a local build instead, use `docker build --target ocr -t household:local .` and set `HOUSEHOLD_IMAGE=household:local` in `.env`. No workflow deploys to a server.

## Features that switch on by configuration

- **OCR:** Local Tesseract with German/English data. It is on whenever the image contains it, unless `HOUSEHOLD_OCR_ENABLED=false`. Existing PDF text is preferred; extracted values remain subject to review.
- **Web Push:** On when `HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY` is set (`init` generates it), unless `HOUSEHOLD_WEB_PUSH_ENABLED=false`. The contact defaults to the HTTPS origin. Device delivery needs a reachable HTTPS origin and explicit user consent; see [Web Push operations](docs/operations.md#device-notifications).
- **Documents inputs and meter adapters:** Fill in their lines in section 4 of `.env` (IMAP, Paperless, Home Assistant, MQTT), then approve them in the app. A scanner folder is enabled by uncommenting its line under `app.volumes` in `compose.yaml`. For a private meter CA, add `- ./meters-ca.pem:/config/meters-ca.pem:ro` under `app.volumes` and set `HOUSEHOLD_METERS_CA_FILE=/config/meters-ca.pem` in `.env`. All nine modules run inside the single `app` service; administrators enable optional modules in the app.

## Configuration

`.env` is the only configuration file. The principal settings are:

| Setting | Purpose |
| --- | --- |
| `HOUSEHOLD_ENV` | `production` or explicitly `development`; production requires HTTPS. |
| `HOUSEHOLD_ORIGIN` | Exact external browser origin; also used for passkeys and push. |
| `HOUSEHOLD_PORT` | Local published port, default `8100`. |
| `HOUSEHOLD_TRUSTED_PROXIES` | Exact proxy CIDRs; empty trusts none. |
| `HOUSEHOLD_ADMIN_USERNAME` / `_PASSWORD` | Bootstrap account; change the password at first sign-in. |
| `HOUSEHOLD_IMAGE` | Image to run, default `ghcr.io/d0linger/household:production`. |
| `HOUSEHOLD_OFF_ENABLED` | Optional product suggestions, off by default. |

Generated values (`POSTGRES_PASSWORD`, `HOUSEHOLD_OWNER_PASSWORD`, `HOUSEHOLD_APP_PASSWORD`, CSRF/rate/encryption keys, the VAPID key and `RESTIC_PASSWORD`) must not be changed casually. With `compose.yaml`, `app` and `backup` connect to `db:5432/household` with the role passwords from `.env`; do not set `HOUSEHOLD_DATABASE_URL` there. `HOUSEHOLD_DATABASE_URL` is for running `household serve` or maintenance commands outside this Compose file, and then without `HOUSEHOLD_DATABASE_PASSWORD` (an explicit setting wins over a file in `/run/secrets`, two explicit sources are refused). Every `HOUSEHOLD_<NAME>` secret also accepts `HOUSEHOLD_<NAME>_FILE` or a file `/run/secrets/<name>`, for operators who mount secrets as files. `RESTIC_PASSWORD` belongs to restic and does not use that folder: to mount it as a file, set `RESTIC_PASSWORD_FILE=/run/secrets/restic_password` on `backup`. Never commit `.env`, `.local/secrets`, uploads or backup repositories. See [operations](docs/operations.md) for key rotation, restore and full configuration.

Passkeys need the configured HTTPS hostname, or `localhost` in development. Numeric IP origins cannot enroll passkeys. Password, TOTP and recovery codes remain available. Ocean/Pink and DE/EN require no external assets or translation service.

## Security and data

Passwords use bcrypt; sessions are stored as hashes and can be revoked. PostgreSQL holds durable rate limits, audit history and module data. State-changing requests require CSRF protection; HTTP responses use a restrictive CSP and related headers. Financial and stock corrections append new records rather than silently changing posted history. Audit history records actors and before/after fields where permitted. [Security design](docs/security.md) and [private reporting](SECURITY.md) give details and limits.

The app image runs without root, with a read-only root filesystem and bounded writable paths. The database has a persistent named volume; uploads use another named volume. Backups must include both data and the required security keys. Schema changes use checksum-checked forward migrations. The runtime role can read but not write migration ledgers, and `/health/ready` serializes and caches its database inspection for five seconds. Member-triggered background work (uploads, document processing, exports, recurrences, notifications) has per-member or per-kind bounds; see [shared resources](docs/security.md#shared-resources-and-background-work). Read the [upgrade and rollback procedure](docs/operations.md#upgrades-and-rollback) before changing a live installation.

## Development and release

Go **1.27.4**, PostgreSQL **18** and Docker Compose are used. Node dependencies are only for browser and documentation tooling; production frontend assets are embedded in the Go binary. After changing embedded assets, rebuild the binary/image.

```powershell
./scripts/verify.ps1 -Integration
./scripts/test-race.ps1
npm ci --ignore-scripts
npm test
npm run test:workflow
```

Database and browser integration tests use a disposable PostgreSQL instance on `127.0.0.1:55434` and a review server on port `8101`; never point fixtures at real household data. Follow the [test guide](docs/testing.md) for those checks, browser evidence, security scanners and resource limits. The production workflow requires quality, browser, Shopping, Fuel recovery and container checks on the exact merge commit, then publishes the scanned image. See [CI and release](docs/release-process.md).

Start with [CONTRIBUTING.md](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md) before changing behavior. [PRODUCT.md](PRODUCT.md) defines scope and [DESIGN.md](DESIGN.md) defines the visual system. The [dependency policy and current audit](docs/dependencies.md) explains what “all dependencies” covers. The [implementation ledger](docs/implementation.md) records executed checks and unresolved limits; it is historical evidence, not installation instructions. Module-specific contracts and validation are under [`docs/`](docs/). The optional [design laboratory](design-preview/index.html) is a prototype, separate from the running app.

## License

MIT — see [LICENSE](LICENSE). Preserve third-party notices when redistributing images or bundled tools. Dependency versions and license hashes can be inspected through `cmd/dependency-check`.
