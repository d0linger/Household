# Operating Household

## Container layout and health

`compose.yaml` uses an internal database network, named PostgreSQL/uploads volumes, a non-root read-only app container, dropped capabilities, PID/memory limits, bounded JSON logs and a writable temporary filesystem. The database is not published. The app waits for database health and successful one-shot migrations. SIGTERM marks readiness unavailable, drains HTTP requests, cancels workers and closes the pool.

```sh
docker compose ps
docker compose logs --tail=100 app migrate
docker compose exec app household healthcheck
```

`/health/live` checks the process. `/health/ready` checks every expected schema version/dirty flag. These endpoints do not expose data or database configuration. The `household healthcheck` command probes `/health/ready` on the port from `HOUSEHOLD_LISTEN` (default `:8080`); a wildcard bind is probed on `127.0.0.1`, a specific address on that address.

A recovered request panic is logged as `request panic` with its request ID, route, Go panic type and a call path of function names and file:line. The panic value and argument words are never logged, because they can hold request data. Meter-adapter failures are logged under `meter adapter processing failed` with a fixed `reason` (`deadline`, `lease_lost`, `lease_refused`, `database_unreachable`, `database_<SQLSTATE>`, `panic` or `unclassified`); feed problems themselves are recorded on the meter source. `/metrics` is hidden unless its secret token is configured. Send that token in `X-Metrics-Token`; do not put it in URLs.

For production, use a valid HTTPS reverse proxy and the exact canonical origin/trusted proxy CIDR. A proxy on the host can reach the loopback port. A containerized proxy needs the intended frontend network connection; determine the actual source network before configuring trust. Never trust all addresses simply to make forwarded headers work.

## Backups

Enable the separate helper with `compose.backup.yaml`. It has database-owner credentials, no HTTP port, read-only uploads access, an encrypted repository volume, and a private work volume. Starting the scheduled service normally requires the app to be healthy; a one-off `run --rm --no-deps backup backup` can take the final maintenance snapshot while the app is stopped. The database must remain healthy. The runtime app cannot mark backup verification successful.

Every Compose example in this runbook must retain the installation's complete reviewed `-f` file set and environment. Base-only examples describe the default single-secret installation. Include configured Web Push/Documents/Meters overlays; keyring installations must use `compose.keyring.yaml`, and backup commands also need `compose.backup-keyring.yaml` after `compose.backup.yaml`. Dropping overlays can change secret parsing or disable configured services. See [encryption keys and rotation](#encryption-keys-and-rotation).

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

This decrypts into a private temporary directory, restores PostgreSQL with fail-fast checks, verifies schema versions, counts ready files/previews and hashes every referenced restored file. Core40 additionally decrypts every protected record using the snapshot's archived security bundle before marking the source backup verified; current live encryption keys cannot substitute for missing archived readers. Use the matching predecessor release to verify a pre-upgrade snapshot, then rehearse the candidate migration and decryption separately on the restored copy. The disposable recovery database is overwritten on each verification; it must never be used as the live application database. Record duration and test periodically with representative data, including attachments.

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

1. Read release notes and compare the migration manifest. Build or load and check the reviewed candidate before downtime; retain the exact old image, secret files and full Compose configuration.
2. For incompatible maintenance, stop the app (including its workers) and the scheduled backup service, leaving PostgreSQL healthy. Take a fresh one-off backup with the old release and `--no-deps`, then verify it by isolated restore with that same release.
3. For the current Core37→40 upgrade, migrate only that restored copy with the candidate first. Require current readiness, preserved records/files and successful protected-record verification with the archived keys. The old binary cannot certify a Core40 database, and the new binary cannot certify an unupgraded Core37 snapshot.
4. Keep the live app stopped from the final snapshot through live migration. If it resumes or writes intervene, repeat the final backup/verification. Run the reviewed candidate's owner migration service using the complete overlay set; preserve PostgreSQL/uploads volumes and secrets.
5. Start the new app, confirm readiness and embedded assets, then test login, module state and representative records/files. Ensure the local image tag used by ordinary Compose commands resolves to the accepted candidate; retain the predecessor image under a separate rollback tag.
6. Resume the scheduler only with the compatible image and secret configuration. Monitor errors/backup age and record actual backup, migration and acceptance results.

Migrations are embedded, serialized by a global advisory lock, and versioned independently per schema. A dirty or unexpected migration state stops readiness. Never blindly force a dirty version clean. Diagnose the failed transaction and restore a verified copy or deliver a forward fix. Published migration files must remain unchanged; add a new migration.

Automatic destructive down migrations are intentionally unavailable. When an older binary is incompatible with the new schema, rollback means restoring the verified pre-upgrade database/uploads/security keys into a replacement deployment, or using a compatible forward repair. Reusing an old binary against a newer schema fails readiness rather than guessing compatibility.

## Encryption keys and rotation

Phase F/Core40 passed automated acceptance and the verified local development upgrade on 10 November 2026. Physical-device delivery remains explicitly open. N49 has passed actual mixed-format and fully rotated encrypted backup/restore, including rejection of a missing retained escrow key; this does not certify a future installation's backup or physical-device delivery. See the [current evidence](notifications-implementation.md#current-checkpoint--10-november-2026). The default single-secret configuration remains supported. Core40 labels existing encrypted records as legacy format1 without changing their ciphertext, counters, expirations, consent or semantic history. New protected writes use format2, authenticating the key identifier, purpose and immutable record/account identity; push records also bind the household. TOTP, passkey credentials, encrypted passkey challenges and push connections share the bounded reader ring. CSRF, rate-limit, VAPID and restic keys remain independent.

The optional ring is a private UTF-8 JSON document, at most32KiB, with integer `version: 1`, one `active` identifier, optional explicit `legacy` identifier, and one to eight `keys` entries containing `id` and `secret`. Identifiers are64 lowercase hexadecimal characters: SHA-256 of the exact UTF-8 prefix `household-encryption-v1`, one NUL byte, and the exact configured secret string. They must match the supplied material. Unknown/duplicate fields, duplicate keys, invalid identifiers and trailing JSON are rejected. The `legacy` key is necessary while format1 records remain; omission disables legacy reads and does not infer the active key. Preserve the historical secret as loaded by the existing configuration; do not base64-decode it or otherwise change its bytes when constructing a ring. Existing `_FILE` loading removes surrounding whitespace; embedded keyring `secret` strings preserve their bytes.

Use `household encryption-keygen --out NEW_PRIVATE_FILE` to generate a new one-key ring from32 cryptographically random bytes. It needs no database/configuration, creates a new private file exclusively and never prints key material. Existing paths are refused. **That newly generated ring alone is not a replacement for an established installation:** privately merge its entry with all required old read keys, set `legacy` correctly, and choose the active key deliberately. Protect host-file ACLs, especially on Windows. Never paste ring contents into issues, terminal logs, source control or public manifests.

To opt in, set the nonsecret host path `HOUSEHOLD_ENCRYPTION_KEYRING_PATH` in the private deployment environment. It must identify an already prepared ring. Docker Compose2.24.4 or newer is required. The overlays remove both legacy encryption environment names, set `HOUSEHOLD_ENCRYPTION_KEYRING_FILE`, and replace only the secret whose container target is `/run/secrets/encryption_key`. That historical filename now contains the ring; the new environment name selects its parser. Other secret mounts survive. This uses Docker's [documented secret-target merge and reset behavior](https://docs.docker.com/reference/compose-file/merge/).

```sh
# Inspect names/paths and merged configuration; do not print private file contents.
docker compose -f compose.yaml -f compose.keyring.yaml config --quiet

# Use the same file set for application/migration commands after release approval.
docker compose -f compose.yaml -f compose.keyring.yaml up -d --build

# A backup-enabled installation must include both keyring overlays.
docker compose -f compose.yaml -f compose.keyring.yaml -f compose.backup.yaml -f compose.backup-keyring.yaml config --quiet
docker compose -f compose.yaml -f compose.keyring.yaml -f compose.backup.yaml -f compose.backup-keyring.yaml up -d --build backup
```

Append any existing optional Web Push/Documents/Meters overlays as needed. Keep `compose.backup-keyring.yaml` after `compose.backup.yaml`; otherwise the latter can reintroduce the legacy encryption environment. Do not run a keyring app with a legacy-only backup helper. All app, migration and backup processes must receive the same reviewed ring version. Secret files are read at startup: changing a host file is not evidence that a running process has loaded it; recreate the affected services deliberately. These examples do not grant remote deployment permission.

Rotate in this order:

1. Preserve the current release image, secret files and an encrypted backup verified with its matching release. Core40 is a schema boundary: an older binary must not be started against the upgraded database.
2. Prepare a ring retaining the old active/legacy keys and the proposed new key. First distribute this reader set to every app, worker, migration and backup process while keeping the old active identifier. Stop any old release process that cannot understand format2. For a single-instance Compose installation, use a maintenance window and recreate all affected services together.
3. Verify configuration/readiness and a restore rehearsal with the new release. Then switch `active` to the proposed key and recreate all affected processes with the same ring. Old keys remain readers; new writes use the active key.
4. Run owner-only bounded re-encryption batches. Runtime credentials cannot run this operation. Each invocation commits at most the selected number of rows; interrupted or failed batches are rolled back and can be retried. Batches serialize through a dedicated advisory lock and compare the original ciphertext/format/key before replacement. They retain protected semantic state and produce content-free audit records.

   ```sh
   docker compose -f compose.yaml -f compose.keyring.yaml run --rm --no-deps migrate encryption-reencrypt --batch 50
   ```

5. Repeat successful batches until `changed` is zero. A failure is not completion; diagnose missing keys or corrupt records instead of discarding them or relabeling ciphertext. The command returns counts and key identifiers, never plaintext.
6. Create and verify a fresh encrypted backup using the full backup/keyring overlay set. Backup captures protected-key inventory and the database dump through the same exported database snapshot. Its private version2 `security-keys.json` contains actual retained encryption keys plus CSRF/rate secrets inside the encrypted restic bundle. Public evidence contains identifiers only. Restore verification must use that snapshot's own keys to decrypt every restored protected record before marking it verified; current live keys are not a substitute. Preserve compatibility with older unversioned three-secret backup bundles.
7. Inspect the old identifier with the owner-only command below. Require zero live references, a non-active key, successful full protected-record verification and a freshly verified backup. The report deliberately keeps `retirement_approved` false: it cannot prove historical/off-site backup or rollback requirements.

   ```sh
   docker compose -f compose.yaml -f compose.keyring.yaml run --rm --no-deps migrate encryption-retirement-check OLD_64_HEX_KEY_ID
   ```

8. Retain old key material and its explicit legacy mapping for every backup, replica, offline consumer and rollback horizon that still needs it. Only after reviewing those horizons may the operator remove a read key from the live ring and recreate all affected services. Never delete the only recoverable key copy. Retiring an encryption reader does not rotate VAPID: Web Push signing-key changes require the separate device reconnection procedure.

For disaster recovery, restore `security-keys.json` into private files alongside the matching database/uploads/image. A version2 bundle's `encryption_keys` object is the complete ring; its CSRF/rate values remain separate secrets. Keep the restic repository password separately. Rolling back Core40 requires the verified predecessor database/uploads/key snapshot and matching old image together; removing the overlay or downgrading the binary alone is insufficient.

The automatic security bundle does **not** contain the optional VAPID private signing key: the backup helper has no VAPID secret mount. Preserve `.local/secrets/web_push_private_key` separately in encrypted operator-controlled storage to retain device connections after disaster recovery. Without that key, generate a new VAPID identity and reconnect devices deliberately. Passing protected-record restore verification proves stored subscription decryptability, not preservation of its signing key or physical push delivery.

## Device notifications

Web Push is optional. Use the [README setup](../README.md#optional-web-push) to generate private VAPID material and configure `HOUSEHOLD_WEB_PUSH_ENABLED`, `HOUSEHOLD_WEB_PUSH_VAPID_CONTACT` and `compose.webpush.yaml`, retaining every existing overlay. Keep a reachable HTTPS `HOUSEHOLD_ORIGIN` for real devices. The domain belongs to installation configuration; it is not compiled into the application.

1. Sign in on the intended device and open **Notifications** in the settings tray. Connect it through the explicit permission action; browser/OS permission alone does not enable server delivery.
2. Enable each wanted account category and its separate device category. Review quiet hours/timezone and the module's own responsibility, source-confirmation or scheduling controls. Consent is personal; enabling or reconnecting does not replay past events.
3. Send the explicit test notification. Distinguish provider acceptance, a service-worker display request and personal confirmation. Check browser/OS permission, the current signed-in session, device pause, account/device consent, quiet hours, source eligibility and expiry when delivery is missing. After a session revocation, sign in again and reconnect deliberately; do not bypass revocation to restore delivery.
4. Monitor bounded worker failures and readiness without logging subscription endpoints or decrypted payloads. A server outage cannot reliably report itself through its own Push channel; monitor backups and host availability independently. Push is not a guaranteed alarm for cooking or safety-critical use.

For a domain move, first disable sending (`HOUSEHOLD_WEB_PUSH_ENABLED=false` and recreate the app with the same full configuration). While the old origin remains reachable, have users remove their old device connections in Notifications. Then change the origin/proxy, recreate the affected app and deliberately reconnect at the new origin; review permissions and account/device categories before re-enabling sending. Preserve the VAPID identity unless intentionally rotating it. If old connections cannot be removed, keep sending disabled until their authority is safely revoked; do not assume an origin change did it. Registration envelopes bind account, household, session, role, authorization version and VAPID identity, but do not store the registration origin. Previously provider-accepted messages cannot reliably be recalled. A new hostname can also require new passkey enrollment; retain a working password/MFA recovery path.

Delivery reconciliation removes terminal intents after 30 days and in-app notices after 90 days in batches of at most 128 rows per cleanup query; this is asynchronous worker cleanup, not an exact-time erasure promise. No separate 14-day attempt-history table or operator retention setting exists. Audit and encrypted historical backups follow their separate policies. See [notification privacy](security.md#personal-security-events-and-notification-privacy). Physical Windows/Android/iOS acceptance remains open until the installation has reachable HTTPS; local automated acceptance does not certify device display.

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

Core5 records field-level history before module truncation. Purging source data does not erase audit history; administrator history can retain permitted names and values. No automatic audit retention deletion is configured. The owner must define any separate retention or archival policy, include encrypted historical backups in that policy, and preserve restore evidence. See [audit-trail.md](audit-trail.md) for the runtime/owner trust boundary.

Images/PDFs use opaque private filenames. Upload errors remove incomplete files; a crash can leave an unreferenced blob, which housekeeping removes after 24 hours. Deleted resources mark their files orphaned and queue cleanup after seven days. Confirmed module purge queues its file cleanup immediately. Cleanup and preview jobs use bounded batches and lease/generation checks; no network-facing path accepts arbitrary filesystem paths or executables.

Jobs retry with exponential delay and a maximum attempt count. Unsupported/failed work is recorded; OCR failure returns the capture to manual review. Each 30-second worker cycle runs its steps (reminders, alerts, notifications, files, document inputs, module schedulers, jobs) independently: a failing or panicking step is logged once per cycle under `background work failed` with its step name, and the remaining steps still run. A panicking job is recovered, released into backoff and logged with its kind and ID. Background panics, like request panics, are reported by type and call path only. An unreachable database ends the cycle with a single `database unavailable` error. Inspect `core.jobs` through owner tooling for operational failures. Do not edit domain history to "fix" a queue failure.

Final expired OCR leases also return eligible captures to manual review. Retryable failures keep captures pending for the next attempt; disabling OCR returns them to manual entry. Generation, live-file and confirmed-capture checks prevent stale jobs from reviving invalid work. OCR/operator output caps apply to streaming copies as well as ordinary writes.

## Local quality workflow

`scripts/test-integration.ps1` starts `household-tests-db-1` on port 55434. The review server uses port 8101 and only `household_test`. Their fixture credentials are explicitly synthetic and must never be reused for a live installation. Normal production Compose does not include these ports/services.

`scripts/test-recovery.ps1` is a Windows/Docker rehearsal using only the disposable restore-db service. It refuses to overwrite an existing household_fixture database, migrates a fresh fixture source, seeds a synthetic quantity plus two private binary objects, backs up to a separate encrypted repository, restores household_restore, and checks domain values and both file hashes. It removes its source database and test repository/work volumes; outputs/recovery-review/report.json retains the result. Its deliberately unusable fixture account is not an authentication demonstration. Stop the recovery service after verification when it is no longer needed.

The application is license-free open source. Docker Desktop, hosted GitHub runner quotas, optional off-site storage and the host itself can have separate terms/costs. Native Linux Docker Engine and a self-hosted runner require no proprietary application license.

## Pantry balance reconciliation

Stock movements remain authoritative. The runtime role can only read lot_balances. Run the operator CLI command `household pantry-check` to compare every lot balance, movement count and latest booking timestamp against the journal; a mismatch returns a failing exit code. After investigating the cause and taking a backup, `household pantry-rebuild` with the separate owner database role rebuilds only the derived balances. It takes the same outer household barrier as writers/purge before locking the journal; active household writes finish first. Runtime credentials cannot rebuild. Both commands are bounded to five minutes and log only mismatch counts. Health readiness checks the projection trigger and runtime privileges, not an expensive full journal scan. Restore verification also performs full reconciliation. Upgrades lock the same household barrier and journal during backfill and trigger installation; do not run independent operator writes concurrently with maintenance.
