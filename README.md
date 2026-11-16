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

Requirements: Docker Engine with Compose v2. Docker Desktop is optional. The following creates local development secrets, starts an isolated PostgreSQL container and binds Household to `127.0.0.1:8100`.

**Windows PowerShell**

```powershell
./scripts/initialize.ps1 -Development
docker compose up -d --build
```

**Linux/macOS**

```sh
sh scripts/initialize.sh --development
docker compose up -d --build
```

Open [http://localhost:8100](http://localhost:8100). Sign in as `admin` using `.local/secrets/bootstrap_password`; change that password at first sign-in. The initializer refuses to overwrite existing secrets. PostgreSQL has no published port. Development mode belongs on a private machine, not a public interface.

For production, initialize without the development switch (`./scripts/initialize.ps1` on Windows or `sh scripts/initialize.sh` on Linux/macOS). Edit `.env` before starting: set the exact HTTPS `HOUSEHOLD_ORIGIN` and `HOUSEHOLD_TRUSTED_PROXIES` to the reverse proxy's exact CIDR. Then run `docker compose up -d --build`, complete the bootstrap password change, and verify an encrypted backup by restoring it to the isolated recovery service. See [deployment, upgrades and recovery](docs/operations.md). The app supports one instance per household and one application replica.

The published image is `ghcr.io/d0linger/household:production` (also `:latest`). Pin a deployment to a verified digest rather than a mutable tag. This repository's Compose file builds `household:local`; switching a live installation to a published digest is an operator deployment step. No workflow deploys to a server.

## Optional services

- **Local OCR:** `docker compose -f compose.yaml -f compose.ocr.yaml up -d --build` adds Tesseract with German/English language data. Existing PDF text is preferred; extracted values remain subject to review.
- **Encrypted backups:** `docker compose -f compose.yaml -f compose.backup.yaml up -d --build backup` starts scheduled restic snapshots. Keep the repository password separately and rehearse [restore verification](docs/operations.md#restore-verification). A local backup volume does not protect against host loss.
- **Web Push, Documents inputs and meter adapters:** use only the relevant [Compose overlays](docs/operations.md) and configure their secrets and approvals. They are off by default. Each installation supplies its own origin; no public URL is compiled into the app.

## Configuration

Copy `.env.example` through the initializer. The principal settings are:

| Setting | Purpose |
| --- | --- |
| `HOUSEHOLD_ENV` | `production` or explicitly `development`; production requires HTTPS. |
| `HOUSEHOLD_ORIGIN` | Exact external browser origin; also used for passkeys and push. |
| `HOUSEHOLD_PORT` | Local published port, default `8100`. |
| `HOUSEHOLD_TRUSTED_PROXIES` | Exact proxy CIDRs; empty trusts none. |
| `HOUSEHOLD_ADMIN_USERNAME` | Bootstrap account name, default `admin`. |
| `HOUSEHOLD_OFF_ENABLED` | Optional product suggestions, off by default. |

Database URLs, CSRF/encryption/rate keys and the bootstrap password are mounted as secret files by Compose. Never commit `.env`, `.local/secrets`, uploads or backup repositories. See [operations](docs/operations.md) for key rotation, restore and full configuration; [`.env.example`](.env.example) lists optional adapters.

Passkeys need the configured HTTPS hostname, or `localhost` in development. Numeric IP origins cannot enroll passkeys. Password, TOTP and recovery codes remain available. Ocean/Pink and DE/EN require no external assets or translation service.

## Security and data

Passwords use bcrypt; sessions are stored as hashes and can be revoked. PostgreSQL holds durable rate limits, audit history and module data. State-changing requests require CSRF protection; HTTP responses use a restrictive CSP and related headers. Financial and stock corrections append new records rather than silently changing posted history. Audit history records actors and before/after fields where permitted. [Security design](docs/security.md) and [private reporting](SECURITY.md) give details and limits.

The app image runs without root, with a read-only root filesystem and bounded writable paths. The database has a persistent named volume; uploads use another named volume. Backups must include both data and the required security keys. Schema changes use checksum-checked forward migrations. Read the [upgrade and rollback procedure](docs/operations.md#upgrades-and-rollback) before changing a live installation.

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
