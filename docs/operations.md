# Operating Household

## Container layout and health

`compose.yaml` uses an internal database network, named PostgreSQL/uploads volumes, a non-root read-only app container, dropped capabilities, PID/memory limits, bounded JSON logs and a writable temporary filesystem. The database is not published. The app waits for database health and successful one-shot migrations. SIGTERM marks readiness unavailable, drains HTTP requests, cancels workers and closes the pool.

```sh
docker compose ps
docker compose logs --tail=100 app migrate
docker compose exec app household healthcheck
```

`/health/live` checks the process. `/health/ready` checks every expected schema version/dirty flag. These endpoints do not expose data or database configuration. `/metrics` is hidden unless its secret token is configured. Send that token in `X-Metrics-Token`; do not put it in URLs.

For production, use a valid HTTPS reverse proxy and the exact canonical origin/trusted proxy CIDR. A proxy on the host can reach the loopback port. A containerized proxy needs the intended frontend network connection; determine the actual source network before configuring trust. Never trust all addresses simply to make forwarded headers work.

## Backups

Enable the separate helper with `compose.backup.yaml`. It has database-owner credentials, no HTTP port, read-only uploads access, an encrypted repository volume, and a private work volume. It requires the app to be healthy. The runtime app cannot mark backup verification successful.

Maintenance accepts explicit `postgres://user:password@host:port/database` or `postgresql://` URIs. Percent-encode reserved characters. Allowed query parameters are `sslmode`, `sslrootcert`, `sslcert`, `sslkey`, and `connect_timeout`; repeated parameters and connection overrides such as `dbname`, `host` or `service` are rejected. Set `sslmode` explicitly: maintenance defaults to `require`, while pgx normally defaults to `prefer`. Generated Compose URLs specify `sslmode=disable` for the isolated Docker database network. Inherited `PG*` settings are cleared for operator tools so they cannot redirect the validated connection. The name `household_restore` is reserved for disposable verification and cannot be a backup source.

```sh
docker compose -f compose.yaml -f compose.backup.yaml up -d --build backup
docker compose -f compose.yaml -f compose.backup.yaml run --rm --no-deps backup backup
docker compose -f compose.yaml -f compose.backup.yaml logs --tail=50 backup
```

The first command creates the scheduler. The second creates an additional immediate snapshot and prints its nonsecret snapshot identifier. The database and ready attachment/preview metadata are captured behind the household exclusive write barrier. The helper includes a custom-format dump, schema/file-hash manifest and security-key bundle, then encrypts them with restic. This barrier can briefly delay writes; schedule large backups outside normal household use.

Daily backups run at 03:00 Europe/Vienna. Retention is 7 daily / 4 weekly / 12 monthly, scoped to the `household` tag. The repository is fully checked after a backup. Failed runs are recorded and logged; operators must monitor backup logs and the age of the last successful/verified run. Local snapshots do not protect against loss of the entire host: replicate the encrypted repository to separate storage using your chosen open-source storage/backup transport.

Preserve the restic password separately from the repository. Retain `.local/secrets` securely. Temporary unencrypted dump/key material lives only in the private work directory and is removed after the command; encrypt the host disk/backup work volume when the threat model includes physical disk access.

## Restore verification

The recovery profile creates a **separate disposable database** with no published port. `restore-check` refuses any database name except `household_restore`, refuses the source target, and requires a known completed snapshot.

```sh
docker compose -f compose.yaml -f compose.backup.yaml --profile recovery up -d --wait restore-db
docker compose -f compose.yaml -f compose.backup.yaml run --rm --no-deps backup restore-check SNAPSHOT_ID
docker compose -f compose.yaml -f compose.backup.yaml stop restore-db
```

This decrypts into a private temporary directory, restores PostgreSQL with fail-fast checks, verifies schema versions, counts ready files/previews and hashes every referenced restored file. Only then is the source backup marked verified. The disposable recovery database is overwritten on each verification; it must never be used as the live application database. Record duration and test periodically with representative data, including attachments.

## Disaster recovery

1. Preserve the failed installation and encrypted repository; do not overwrite the only surviving copy.
2. Create a separate replacement deployment with the same release image, PostgreSQL major version and a private maintenance window.
3. Restore the selected restic snapshot into a private recovery directory with the saved repository password.
4. Identify `manifest.json`, `database.dump`, `security-keys.json`, and the manifest's uploads path.
5. Restore into the replacement database as `household_owner` using PostgreSQL 18 `pg_restore --no-owner --exit-on-error` and restore the uploads directory with UID/GID 10001 access.
6. Restore the security-key bundle into the corresponding secret files. Generate/retain replacement database credentials appropriate to the replacement deployment; do not copy stale connection URLs blindly.
7. Run readiness/schema checks, verify referenced file hashes, test login/TOTP and one read/write workflow, and revoke pre-incident sessions if the incident involved compromise.
8. Switch the reverse proxy to the verified replacement, then retain the old deployment until recovery has been accepted.

Do not run a production destructive restore against the source database as a routine test. The automated verification path exists specifically to avoid that.

## Upgrades and rollback

1. Read release notes and compare the migration manifest.
2. Take and verify a fresh backup.
3. Stop the app/scheduler during incompatible maintenance.
4. Build or load the reviewed image and run its migration service.
5. Start the new app, confirm readiness, then test login, module state and representative records/files.
6. Resume the scheduler and monitor errors/backup age.

Migrations are embedded, serialized by a global advisory lock, and versioned independently per schema. A dirty or unexpected migration state stops readiness. Never blindly force a dirty version clean. Diagnose the failed transaction and restore a verified copy or deliver a forward fix. Published migration files must remain unchanged; add a new migration.

Automatic destructive down migrations are intentionally unavailable. When an older binary is incompatible with the new schema, rollback means restoring the verified pre-upgrade database/uploads/security keys into a replacement deployment, or using a compatible forward repair. Reusing an old binary against a newer schema fails readiness rather than guessing compatibility.

## Module pause and purge

Pausing is reversible and keeps records. The admin settings form changes a versioned module state; disabled module pages, API, search, reports, files and exports return 404. Queued work pauses and old generations cannot replay.

Purge is deliberate maintenance:

1. Pause the module.
2. Create/verify a recent backup.
3. Sign in freshly and request deletion in settings by entering the module ID and verified backup ID/snapshot.
4. Obtain that request's UUID from the owner database/operator tooling. Requests expire after 30 minutes.
5. Execute the exact request with the separate owner-credential service:

```sh
docker compose run --rm --no-deps migrate purge REQUEST_UUID
```

The executor independently checks requester status/admin role, request expiry, disabled state, matching generation and a complete backup created after the current pause, created within seven days and verified within seven days. Retention marks snapshots ineligible before pruning and reconciles remaining repository IDs; a failed retention run leaves those records ineligible until a successful backup reconciles them. Backup/verification operations serialize with a repository advisory lock. It truncates only compiled module tables, clears associated search/jobs/imports/links/notifications and source/destination action receipts, advances the generation, and audits completion. Lookup tables/migration histories remain. Attachment blobs are tombstoned for durable cleanup after metadata commit. Audit identifiers and encrypted historical backup copies follow their respective retention policy; purge does not erase already retained snapshots.

## File and job maintenance

Images/PDFs use opaque private filenames. Upload errors remove incomplete files; a crash can leave an unreferenced blob, which housekeeping removes after 24 hours. Deleted resources mark their files orphaned and queue cleanup after seven days. Confirmed module purge queues its file cleanup immediately. Cleanup and preview jobs use bounded batches and lease/generation checks; no network-facing path accepts arbitrary filesystem paths or executables.

Jobs retry with exponential delay and a maximum attempt count. Unsupported/failed work is recorded; OCR failure returns the capture to manual review. Inspect `core.jobs` through owner tooling for operational failures. Do not edit domain history to "fix" a queue failure.

Final expired OCR leases also return eligible captures to manual review. Retryable failures keep captures pending for the next attempt; disabling OCR returns them to manual entry. Generation, live-file and confirmed-capture checks prevent stale jobs from reviving invalid work. OCR/operator output caps apply to streaming copies as well as ordinary writes.

## Local quality workflow

`scripts/test-integration.ps1` starts `household-tests-db-1` on port 55434. The review server uses port 8101 and only `household_test`. Their fixture credentials are explicitly synthetic and must never be reused for a live installation. Normal production Compose does not include these ports/services.

`scripts/test-recovery.ps1` is a Windows/Docker rehearsal using only the disposable restore-db service. It refuses to overwrite an existing household_fixture database, migrates a fresh fixture source, seeds a synthetic quantity plus two private binary objects, backs up to a separate encrypted repository, restores household_restore, and checks domain values and both file hashes. It removes its source database and test repository/work volumes; outputs/recovery-review/report.json retains the result. Its deliberately unusable fixture account is not an authentication demonstration. Stop the recovery service after verification when it is no longer needed.

The application is license-free open source. Docker Desktop, hosted GitHub runner quotas, optional off-site storage and the host itself can have separate terms/costs. Native Linux Docker Engine and a self-hosted runner require no proprietary application license.
