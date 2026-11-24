# Operating Household

## Container layout and health

[`compose.yaml`](../compose.yaml) is the command-line deployment file; [installation.md](installation.md) is the step-by-step guide. Settings and passwords live in one `.env` created by `household init`. Every container runs without root (PostgreSQL as uid 70, Household as uid 10001); the Household containers are read-only with dropped capabilities, `no-new-privileges`, a size-limited `/tmp` and bounded JSON logs. Data lives in named PostgreSQL/uploads/backup volumes. The file sets no cgroup limits, so it also runs unchanged under rootless Docker. The database is not published. The app waits for database health; its `start` command then creates or updates the `household_owner`/`household_app` roles as the PostgreSQL superuser, migrates as the owner and `exec`s `household serve` as the app role without the superuser or owner password in its environment. SIGTERM marks readiness unavailable, drains HTTP requests, cancels workers and closes the pool.

The [Portainer stack](portainer.md) maps Portainer variables explicitly and puts the app on the existing HTTPS proxy network. Its backup worker starts independently of app health so it can resume an interrupted restore even when the app cannot open the live database.

```sh
docker compose ps
docker compose logs --tail=100 app
docker compose exec app household healthcheck
```

`/health/live` checks the process. `/health/ready` checks every expected schema version/dirty flag, that the runtime role cannot write any migration ledger, and the module contract inspectors. One inspection runs at a time; its result is cached for five seconds and bounded to two seconds, so unauthenticated probes cannot multiply database work. Shutdown makes readiness fail immediately. These endpoints do not expose data or database configuration. The `household healthcheck` command probes `/health/ready` on the port from `HOUSEHOLD_LISTEN` (default `:8080`); a wildcard bind is probed on `127.0.0.1`, a specific address on that address.

A recovered request panic is logged as `request panic` with its request ID, route, Go panic type and a call path of function names and file:line. The panic value and argument words are never logged, because they can hold request data. Meter-adapter failures are logged under `meter adapter processing failed` with a fixed `reason` (`deadline`, `lease_lost`, `lease_refused`, `database_unreachable`, `database_<SQLSTATE>`, `panic` or `unclassified`); feed problems themselves are recorded on the meter source. `/metrics` is hidden unless its secret token is configured. Send that token in `X-Metrics-Token`; do not put it in URLs. It exposes request counts and durations by route pattern, panics, rejected and rate-limited sign-ins, CSRF rejections, worker failures, the maintenance pause, database pool use and the backup state; [monitoring](monitoring/README.md) lists every series and example alert rules.

For production, use a valid HTTPS reverse proxy and the exact canonical origin/trusted proxy CIDR. A proxy on the host can reach the loopback port. A containerized proxy needs the intended frontend network connection; determine the actual source network before configuring trust. Never trust all addresses simply to make forwarded headers work.

## Backups

The `backup` service runs the same image with database-owner credentials, no HTTP port, the uploads volume, an encrypted local repository volume and a private work volume. It is the only process with repository credentials: `start` strips `RESTIC_PASSWORD*`, `HOUSEHOLD_BACKUP_OFFSITE_*`, `HOUSEHOLD_BACKUP_S3_*`, `HOUSEHOLD_S3_*` and `HOUSEHOLD_BACKUP_REST_*` before `serve` runs. Starting the scheduled service normally requires the app to be healthy; a one-off `run --rm --no-deps backup backup` can take the final maintenance snapshot while the app is stopped. The database must remain healthy. The runtime app cannot mark backup verification successful.

The `backup` service is also the owner-credential maintenance service: run owner-only commands (`backup`, `restore-check`, `restore`, `restore-rollback`, `backup-health`, `recover`, `purge`, `encryption-reencrypt`, `encryption-retirement-check`, `pantry-rebuild`) with `docker compose run --rm --no-deps backup COMMAND`. Changing `.env` takes effect only after the affected services are recreated (`docker compose up -d`).

Maintenance accepts explicit `postgres://user:password@host:port/database` or `postgresql://` URIs. Percent-encode reserved characters. Allowed query parameters are `sslmode`, `sslrootcert`, `sslcert`, `sslkey`, and `connect_timeout`; repeated parameters and connection overrides such as `dbname`, `host` or `service` are rejected. Set `sslmode` explicitly: maintenance defaults to `require`, while pgx normally defaults to `prefer`. URLs built from `HOUSEHOLD_DATABASE_PASSWORD` specify `sslmode=disable` for the isolated Docker database network. Inherited `PG*` settings are cleared for operator tools so they cannot redirect the validated connection. The names `household_restore`, `household_restore_auto_<unix>_<random>`, `household_restore_job_<12 hex>` and `household_pre_restore_<unix>_<8 hex>` are reserved for restore maintenance and can never be a backup source; similar names such as `household_restored` remain valid sources.

An operator `backup` holds the live-restore lock for its whole run (waiting up to five minutes for the service's current work), so it can never end between a live restore's state copy and its database swap. `restore-check latest` chooses the snapshot under the same repository lock it verifies under.

```sh
docker compose up -d backup
docker compose run --rm --no-deps backup backup            # local snapshot
docker compose run --rm --no-deps backup backup --offsite  # local snapshot plus offsite copy
docker compose logs --tail=50 backup
```

The database and ready attachment/preview metadata are captured behind the household exclusive write barrier. The helper includes a custom-format dump, schema/file-hash manifest and security-key bundle, then encrypts them with restic. This barrier can briefly delay writes; schedule large backups outside normal household use. restic only ever receives the variables of the repository a command uses, never the service's whole environment.

### Schedule, retention and requests in the app

Administrators manage backups under **Settings → Backup** (`/settings/backup`). The page shows the health of the local and offsite targets (derived from each target's own schedule plus a grace of a tenth of the interval, 15 minutes to 2 hours), the last restore test, every restore point and the history of requested and scheduled work. It stores the schedule in `core.backup_settings`: start time and interval (default daily at 03:00 in the household zone), local retention (default 7 daily / 4 weekly / 12 monthly, `household` tag), offsite interval and retention, and the automatic restore test (default weekly, local).

The history puts running and queued jobs first. Their state, current stage and start time refresh while the page remains open. The signed S3 bucket probe has a 10-second budget once claimed. The older Restic repository opens with a separate 90-second budget. Full backup integrity checks and trial restores can take longer, especially on slow storage or with large uploads.

For Garage and compatible S3 services, opening the repository URL directly in a browser sends an unsigned request. An anonymous `AccessDenied` response is expected. Use **Test offsite connection** for the signed bucket check. If the signed check returns access denied, verify the backup service's configured access key and secret, the selected bucket's read and write grants, the S3 region and that the proxy preserves the signed host and path. The repository encryption password is separate from the S3 credentials. [Garage's restic setup](https://github.com/deuxfleurs-org/garage/blob/main-v2/doc/book/connect/backup.md)

Buttons on the page record work in `core.backup_jobs`: back up now (local, or local plus offsite), test the offsite connection, verify a restore point, restore a restore point and discard a kept previous state. The web role may only insert unclaimed requests and cancel them before they are claimed; a trigger refuses every other change. The backup service polls every 30 seconds, claims one request with `FOR UPDATE SKIP LOCKED` and validates it again with owner privileges: the requester must still be an active administrator, a restore must name a full snapshot ID and carry the typed confirmation `WIEDERHERSTELLEN <first 8 characters>`, and a restore or a discard of the kept state must come from a session re-authenticated within ten minutes. Unclaimed requests expire after 30 minutes; an expired request is never claimed. The service holds its restore lock for a whole tick, from claiming to finishing the work, and bounds one tick to six hours, so a stalled tool cannot hold the repository forever and an operator `restore` never starts in between. Scheduled runs and restore tests appear in the same history. Every request, cancellation and schedule change is recorded as a semantic audit event; backup work itself is operational state, like `core.jobs`.

The service writes its heartbeat, release and the nonsecret repository labels into `core.backup_settings`. The page warns when the service has not reported for three minutes. The worker notifies administrators in the app (inbox, not Web Push) when the last local backup or offsite copy failed, the backup is overdue, no backup was restore-tested within the configured interval (or 30 days when the test is off), the service stopped reporting, or a live restore completed or failed. Each incident has its own deduplication key, so dismissing an old warning never hides a new one. The service's container healthcheck runs `household backup-health`, which fails when a target's last attempt failed or its schedule is overdue. The last attempt is the newest finished backup run or backup job of the target, so a job that failed before it created a run (for example an unreachable repository) or after it (a failed retention prune) counts as failed, and a failed offsite-only copy does not fail the local target. Only history of the configured repository counts: started jobs record the labels of the repositories they used, so after replacing a repository its target starts fresh from its activation instead of inheriting the old repository's successes or attempts. A target that never succeeded is shown as "never" until the first scheduled slot after the household's creation (offsite: after its settings were last saved) plus grace has passed, and then as overdue. A failed scheduled attempt waits for the next slot instead of retrying every tick. The heartbeat is refreshed every minute, also while a long backup or restore check runs.

After a local backup restic fully checks the local repository. Failed runs are recorded with a public reason code (for example `wrong_password`, `repository_unreachable`, `access_denied`, `disk_full`); tool output is never stored or shown because it can name locations or provider details.

### Offsite repository

#### Portable backup files

The default for new backups is one dated `household-*.tar.age` file. The
worker captures a consistent PostgreSQL dump, ready attachments and previews,
the file-checksum and schema manifest, and the recovery key bundle. The local
file lives under `/data/backups/portable`. For S3, the file is an object directly
under the configured prefix, such as `s3-dp/household/household-…tar.age`.
A mounted offsite destination stores its files in its `portable` directory.
Files share the same dated name at both locations. Household authenticates the
whole file with age, using `RESTIC_PASSWORD` locally and
`HOUSEHOLD_BACKUP_OFFSITE_PASSWORD` externally.

Use the updated Compose or Portainer template. The app mounts `backups`
read-only and shares the `backup_exchange` volume with the backup service.
Exchange holds encrypted uploads and prepared downloads. Keep the existing
stack name and named data volumes. A proxy that caps request bodies or
timeouts must allow the upload on `/api/v1/core/backup/upload`. The Household
upload limit is 32 GiB and 30 minutes.

1. Under **Settings → Backup**, download a local file directly. For an
   external file, choose **Verify and prepare download**, wait for the
   trial restore, then choose **Download**. Prepared copies expire after
   six hours.
2. Upload only backup files from installations you trust: restoring a dump
   runs its SQL, and a confirmed live restoration keeps the objects it creates.
   Choose **Upload backup file**, select the encrypted archive, and submit.
   The worker authenticates it with the configured local or external backup
   password and checks it in a disposable database. The live keyring must be
   able to read the data before the worker registers a verified local point.
3. At that verified point, choose **Restore**. Review the date, confirm
   your sign-in and enter the displayed confirmation phrase. The usual
   safety backup and live restore procedure apply.

Keep the passwords used by any retained files. Portable files carry their own
password encryption and a Restic repository key change affects only Restic
snapshots. Existing Restic data stays readable by its original passwords and
is exempt from the portable-file retention job. The two formats can coexist
at the same S3 prefix. A restic REST target uses Restic for new writes. An
operator can also select the older writer with `HOUSEHOLD_BACKUP_FORMAT=restic`.

#### S3 settings and legacy Restic repositories

Set the offsite destination in `.env` and recreate the services:

```sh
HOUSEHOLD_S3_ENDPOINT=s3.example.org
HOUSEHOLD_S3_BUCKET=s3-dp
HOUSEHOLD_S3_PREFIX=household
HOUSEHOLD_S3_REGION=us-east-1
HOUSEHOLD_S3_ACCESS_KEY=…
HOUSEHOLD_S3_SECRET_KEY=…
HOUSEHOLD_BACKUP_OFFSITE_PASSWORD=<own long password, kept separately>
```

`HOUSEHOLD_S3_PREFIX` defaults to `household`. Set a distinct relative prefix for each installation sharing a bucket. Use the S3 API host and its configured region. TLS is enabled by default. A private test network can use `HOUSEHOLD_S3_USE_SSL=false`.

Supported are `s3:http(s)://host/bucket[/prefix]` (AWS, Backblaze B2, Wasabi, Hetzner, MinIO and other S3-compatible storage), `rest:http(s)://host/path` with optional `HOUSEHOLD_BACKUP_REST_USERNAME`/`_PASSWORD`, and an absolute mounted path such as a NAS share added under `backup.volumes`. The repository URL rejects credentials, queries and fragments. Household uses it as the public label in the app and `core.backup_runs`. The credentials and repository password also accept `_FILE` or a file in `/run/secrets`. Use a separate repository password of at least 16 characters and keep a copy of it and `.env` off the server.

In Restic mode, the first copy creates the offsite repository with the local chunker parameters (`restic init --copy-chunker-params`), so later copies deduplicate. Each run copies the new local snapshot (`restic copy`), checks the repository structure plus one rotating sixth of its data (`restic check --read-data-subset=n/6`) instead of downloading everything daily, records an offsite run with the source snapshot's size and applies the offsite retention. When the offsite schedule is due between local runs, the newest local snapshot is copied; if it is already offsite, no new run is recorded (the target does not look freshly backed up) and only the rotating data check runs. A copy whose earlier run failed after `restic copy` (in its check) is recorded with its own snapshot time once the check passes, and runs left `retention_pending` by an interrupted prune are reconciled. A repository is initialized only when restic confirms it does not exist (exit code 10 or its "does not exist" diagnostic); access, credential and connection errors are reported as such and never lead to an `init`. The S3 connection test checks the bucket with a signed read-only request. The copy opens or initializes the repository and verifies its contents. You may keep the existing composite repository setting and its associated credentials. Choose a single configuration style per deployment. The restic cache lives on the work volume (`/work/restic-cache`, or below `HOUSEHOLD_BACKUP_WORK`).

Offsite storage can have separate terms and costs. Egress for restore tests from offsite counts against them; the default restore test reads the local repository.

Preserve `.env` (it holds the restic passwords, encryption key and Web Push key) separately from the repositories. Temporary unencrypted dump/key material lives only in the private work directory and is removed after the command; encrypt the host disk/backup work volume when the threat model includes physical disk access.

### Changing a repository password

restic keeps several keys per repository. To replace a password, add a key with the new one, switch `.env`, verify a backup and only then remove the old key:

```sh
docker compose run --rm --no-deps --entrypoint restic backup key add       # prompts for the new password
docker compose run --rm --no-deps --entrypoint restic backup key list
# set the new RESTIC_PASSWORD in .env, docker compose up -d, take and verify a backup
docker compose run --rm --no-deps --entrypoint restic backup key remove OLD_KEY_ID
```

For the offsite repository, run the same commands with `-e RESTIC_REPOSITORY=… -e RESTIC_PASSWORD=…` set to the offsite values (and its transport credentials). Keep old passwords until every snapshot you may need has been verified with the new key.

### Operator alerts outside the app

The app's own inbox cannot report a stopped server. The backup service can additionally send failed backups, offsite copies, restore tests and restores, and completed restores, to an external channel: an ntfy topic (`HOUSEHOLD_ALERT_NTFY_URL`, optional `HOUSEHOLD_ALERT_NTFY_TOKEN`), a webhook receiving a small JSON document (`HOUSEHOLD_ALERT_WEBHOOK_URL`) or e-mail (`HOUSEHOLD_ALERT_SMTP_ADDRESS` as `host:587` with STARTTLS or `host:465` with TLS, `_USER`, `_PASSWORD`, `_FROM`, comma-separated `_TO`). `HOUSEHOLD_ALERT_NAME` names the installation in messages. URLs must be https without credentials; secrets accept `_FILE` or `/run/secrets`. `start` strips these settings from the web process. Messages carry fixed texts, the target and a public reason code, never household data. Mails are 7-bit safe (quoted-printable body, RFC 2047 header words folded at 76 characters), so relays without 8BITMIME or SMTPUTF8 accept them; channels deliver concurrently. Delivery failures are logged and never block the work. Combine this with external uptime monitoring: a host that is down sends nothing.

## Restore verification

`restore-check` creates a **temporary database of its own** (`household_restore_auto_<time>_<random>`) on the PostgreSQL server of the source connection with the superuser password from `.env`, prepares the Household roles in it, verifies the snapshot and always drops that database again, also after a failure. Concurrent checks therefore never share or drop each other's database. A later check drops leftovers of interrupted runs that are older than six hours. With an explicit `HOUSEHOLD_RESTORE_DATABASE_URL` (or without a superuser password) it uses that separate `household_restore` database unchanged instead. A configured or derived target must be named exactly `household_restore`; `household_restore_auto_…` names are reserved for automatic checks, because their leftover cleanup may drop them. `restore-check` refuses any other target name, refuses a source with one of the reserved names, and requires a known completed snapshot of the chosen repository.

```sh
docker compose run --rm --no-deps backup restore-check SNAPSHOT_ID
docker compose run --rm --no-deps backup restore-check latest
docker compose run --rm --no-deps backup restore-check --offsite latest
```

This decrypts into a private temporary directory, restores PostgreSQL with fail-fast checks, verifies schema versions, counts ready files/previews and hashes every referenced restored file. Core40 additionally decrypts every protected record using the snapshot's archived security bundle before marking the source backup verified; current live encryption keys cannot substitute for missing archived readers. Use the matching predecessor release to verify a pre-upgrade snapshot, then rehearse the candidate migration and decryption separately on the restored copy. The disposable recovery database is overwritten on each verification; it must never be used as the live application database. The backup service runs this check automatically at the configured interval for the newest restore point of the chosen target whose schema matches the running release; administrators can also request it for any compatible restore point in the app.

## Live restore

Administrators can restore the running installation from a local or offsite restore point under **Settings → Backup**, after confirming their sign-in and typing the confirmation phrase; operators can run the same procedure with

```sh
docker compose run --rm --no-deps backup restore [--offsite] FULL_SNAPSHOT_ID --confirm "WIEDERHERSTELLEN XXXXXXXX"
```

Only restore points whose schema manifest equals the running release are offered; older ones need their own release (see disaster recovery). The backup service performs the restore in phases recorded on the job and in a journal file on the work volume:

1. **Prepare** while the app keeps running: take a safety backup of the current state (without applying retention, which could otherwise forget the restore point being restored when both are from the same day), restore the snapshot into a new database `household_restore_job_<id>` on the same server, run every restore-check verification, require that the current key ring in `.env` can read every protected record, carry over backup history, schedule and work (so no restore point or the safety backup disappears from the app), end every session and authentication challenge, revoke calendar feeds that are no longer valid in the live data and drop push connections the live data no longer has, then run readiness. Any failure stops here; the live database is untouched.
2. **Pause:** the job waits for the app. The app answers every request except liveness, assets and `/maintenance/status` with a self-refreshing 503 page (JSON for API clients). Requests and worker ticks register as admitted work before they check the pause, so once the pause is visible no new work starts; the app confirms through `core.acknowledge_backup_drain` only while no admitted work is running, also when it confirms again on a later check. A running app always holds one presence session on the database; only a database without any app session for 15 consecutive seconds counts as a stopped app that needs no confirmation. Without either within two minutes the restore fails and the app resumes. After the pause, the operational state (backup jobs and history, schedule, feeds, push connections) is copied into the stage again, so a job cancelled or a feed revoked during preparation stays that way.
3. **Switch:** the stage marks its own restore as switching, so an app that connects to it (or starts) stays paused until the restore is finished. Connections to the live database are disabled, it is renamed to `household_pre_restore_<unix>_<random>` and the stage takes its name. Upload files the restored data does not reference, or references with other content, are moved to `/work/pre_restore_<…>/` and the restored files are copied in, all checked against the manifest hashes.
4. **Finish:** the job is marked complete in the restored database with an audit event; the app resumes and everyone signs in again.

If the service stops during a restore, its next start completes or undoes the switch from the journal before it opens its own database pool, so it also starts when the interruption left no database under the live name: before the second rename the original database name and connections are restored; after it the upload reconciliation and record are finished; once the outcome is recorded only cleanup remains: the job marked complete in the same transaction as the `restore_completed` event is the durable marker, so uploads are never reconciled again after the app may have resumed, and a journal write failure after that commit never reports the restore as failed. Directory sync failures of the journal stop the restore before the switch. The replaced database and moved uploads are kept until an administrator discards them on the backup page (a fresh sign-in is required); the discard clears the kept-state reference only for databases it actually dropped. To switch back instead, stop the app and run

```sh
docker compose stop app
docker compose run --rm --no-deps backup restore-rollback
docker compose up -d
```

The rollback writes its own journal before the first rename; an interrupted rollback is completed by the next service start or by running `restore-rollback` again. One `restore-rollback` call either completes such an interrupted operation or starts one new rollback, never both, so a retry cannot roll back a second, earlier restore. Afterwards the backup page offers to discard the rolled-back restored state; the discard also clears references to databases that no longer exist and removes leftover moved-upload folders whose database is gone, reporting a folder it could not remove so the next discard retries it.

While paused, the worker's feed loops (cooking timers, Home Assistant and MQTT meter feeds) count each database write as admitted work and skip writes the pause refuses; a web process that starts during a restore stays paused until it has read the restore state successfully.

The live restore needs the PostgreSQL superuser password in the backup service (as for `restore-check`), identical database-level settings for the live and stage database, enough free space on the work volume (about twice the snapshot size), and writable uploads for the backup service.

## Disaster recovery

When the host is lost, restore into a new installation:

1. Preserve the failed installation and every repository; do not overwrite the only surviving copy.
2. Create a separate replacement deployment with the same release image, PostgreSQL major version and the saved `.env` (restic passwords and keys). For an offsite-only recovery, point `RESTIC_REPOSITORY` at a local volume and the offsite settings at the offsite repository.
3. Start `db` and `app` once so `start` creates the roles and schema, then stop `app`.
4. Run `docker compose run --rm --no-deps backup restore [--offsite] FULL_SNAPSHOT_ID --confirm "WIEDERHERSTELLEN …"`. The command reads the snapshot directly from the repository, so it works although the new database does not know it; list snapshots with `docker compose run --rm --no-deps --entrypoint restic backup snapshots` (for offsite, with the offsite variables). The restore refuses a snapshot whose protected records the `.env` key ring cannot read: restore the snapshot's `security-keys.json` into `.env` first if the original `.env` is lost (restic restore into a private directory to read it).
5. Run readiness/schema checks, test login/TOTP and one read/write workflow, and revoke pre-incident sessions if the incident involved compromise.
6. Switch the reverse proxy to the verified replacement, then retain the old deployment until recovery has been accepted.

Do not run a production destructive restore against the source database as a routine test. The automated verification path exists specifically to avoid that.

## Upgrades and rollback

Installations from before the single `compose.yaml` (several Compose files with `.local/secrets`) move their existing secret values into `.env`; follow [the migration steps](installation.md#umstieg-von-einer-älteren-installation-mit-localsecrets).


1. Read release notes and compare the migration manifest. Build or load and check the reviewed candidate before downtime; retain the exact old image, `.env` and `compose.yaml`.
2. For incompatible maintenance, stop the app (including its workers) and the scheduled backup service, leaving PostgreSQL healthy. Take a fresh one-off backup with the old release and `--no-deps`, then verify it by isolated restore with that same release.
3. For the current Core37→42 upgrade (Core41 only replaces Finance functions and re-pins readiness; Core42 adds the backup settings and job tables, four nullable backup-run columns and re-pins the audit, Pantry and Fuel contracts; neither rewrites data), migrate only that restored copy with the candidate first. Require current readiness, preserved records/files and successful protected-record verification with the archived keys. The old binary cannot certify a Core42 database, and the new binary cannot certify an unupgraded snapshot. Snapshots taken before Core42 stay listed in the app but can only be restored with their own release. Core42 also changes `compose.yaml`: the `backup` service gets the writable uploads volume and its `backup-health` healthcheck; take the new file before `docker compose up -d`.
4. Keep the live app stopped from the final snapshot through live migration. If it resumes or writes intervene, repeat the final backup/verification. Run `docker compose run --rm --no-deps app setup` with the reviewed candidate image; preserve PostgreSQL/uploads volumes and `.env`.
5. Start the new app, confirm readiness and embedded assets, then test login, module state and representative records/files. Ensure the local image tag used by ordinary Compose commands resolves to the accepted candidate; retain the predecessor image under a separate rollback tag.
6. Resume the scheduler only with the compatible image and secret configuration. Monitor errors/backup age and record actual backup, migration and acceptance results.

Migrations are embedded, serialized by a global advisory lock, and versioned independently per schema. After the final stage the owner revokes every runtime write privilege on all `schema_migrations` ledgers (SELECT remains for readiness). A dirty or unexpected migration state, or a writable ledger, stops readiness. Owner purge requires the exact current Pantry ledger version and the owner-owned scrub function. Never blindly force a dirty version clean. Diagnose the failed transaction and restore a verified copy or deliver a forward fix. Published migration files must remain unchanged; add a new migration.

Automatic destructive down migrations are intentionally unavailable. When an older binary is incompatible with the new schema, rollback means restoring the verified pre-upgrade database/uploads/security keys into a replacement deployment, or using a compatible forward repair. Reusing an old binary against a newer schema fails readiness rather than guessing compatibility.

## Encryption keys and rotation

Phase F/Core40 passed automated acceptance and the verified local development upgrade on 10 November 2026. Physical-device delivery remains explicitly open. N49 has passed actual mixed-format and fully rotated encrypted backup/restore, including rejection of a missing retained escrow key; this does not certify a future installation's backup or physical-device delivery. See the [current evidence](notifications-implementation.md#current-checkpoint--10-november-2026). The default single-secret configuration remains supported. Core40 labels existing encrypted records as legacy format1 without changing their ciphertext, counters, expirations, consent or semantic history. New protected writes use format2, authenticating the key identifier, purpose and immutable record/account identity; push records also bind the household. TOTP, passkey credentials, encrypted passkey challenges and push connections share the bounded reader ring. CSRF, rate-limit, VAPID and restic keys remain independent.

The optional ring is a private UTF-8 JSON document, at most32KiB, with integer `version: 1`, one `active` identifier, optional explicit `legacy` identifier, and one to eight `keys` entries containing `id` and `secret`. Identifiers are64 lowercase hexadecimal characters: SHA-256 of the exact UTF-8 prefix `household-encryption-v1`, one NUL byte, and the exact configured secret string. They must match the supplied material. Unknown/duplicate fields, duplicate keys, invalid identifiers and trailing JSON are rejected. The `legacy` key is necessary while format1 records remain; omission disables legacy reads and does not infer the active key. Preserve the historical secret as loaded by the existing configuration; do not base64-decode it or otherwise change its bytes when constructing a ring. Existing `_FILE` loading removes surrounding whitespace; embedded keyring `secret` strings preserve their bytes.

Use `household encryption-keygen --out NEW_PRIVATE_FILE` to generate a new one-key ring from32 cryptographically random bytes. It needs no database/configuration, creates a new private file exclusively and never prints key material. Existing paths are refused. **That newly generated ring alone is not a replacement for an established installation:** privately merge its entry with all required old read keys, set `legacy` correctly, and choose the active key deliberately. Protect host-file ACLs, especially on Windows. Never paste ring contents into issues, terminal logs, source control or public manifests.

To opt in, put the prepared ring into `.env` as one line, `HOUSEHOLD_ENCRYPTION_KEYRING='{"version":1,...}'` (single quotes keep the JSON literal), and remove or comment out `HOUSEHOLD_ENCRYPTION_KEY`; the app refuses both at once. Every service reads the same `.env`, so app, setup and backup always receive the same ring. Settings are read at startup: recreate the services (`docker compose up -d`) after changing the ring, and check the result with `docker compose config --quiet`. These steps do not grant remote deployment permission.

Rotate in this order:

1. Preserve the current release image, `.env` and an encrypted backup verified with its matching release. Core40 is a schema boundary: an older binary must not be started against the upgraded database.
2. Prepare a ring retaining the old active/legacy keys and the proposed new key. First distribute this reader set to every app, worker, migration and backup process while keeping the old active identifier. Stop any old release process that cannot understand format2. For a single-instance Compose installation, use a maintenance window and recreate all affected services together.
3. Verify configuration/readiness and a restore rehearsal with the new release. Then switch `active` to the proposed key and recreate all affected processes with the same ring. Old keys remain readers; new writes use the active key.
4. Run owner-only bounded re-encryption batches. Runtime credentials cannot run this operation. Each invocation commits at most the selected number of rows; interrupted or failed batches are rolled back and can be retried. Batches serialize through a dedicated advisory lock and compare the original ciphertext/format/key before replacement. They retain protected semantic state and produce content-free audit records.

   ```sh
   docker compose run --rm --no-deps backup encryption-reencrypt --batch 50
   ```

5. Repeat successful batches until `changed` is zero. A failure is not completion; diagnose missing keys or corrupt records instead of discarding them or relabeling ciphertext. The command returns counts and key identifiers, never plaintext.
6. Create and verify a fresh encrypted backup. Backup captures protected-key inventory and the database dump through the same exported database snapshot. Its private version2 `security-keys.json` contains actual retained encryption keys plus CSRF/rate secrets inside the encrypted restic bundle. Public evidence contains identifiers only. Restore verification must use that snapshot's own keys to decrypt every restored protected record before marking it verified; current live keys are not a substitute. Preserve compatibility with older unversioned three-secret backup bundles.
7. Inspect the old identifier with the owner-only command below. Require zero live references, a non-active key, successful full protected-record verification and a freshly verified backup. The report deliberately keeps `retirement_approved` false: it cannot prove historical/off-site backup or rollback requirements.

   ```sh
   docker compose run --rm --no-deps backup encryption-retirement-check OLD_64_HEX_KEY_ID
   ```

8. Retain old key material and its explicit legacy mapping for every backup, replica, offline consumer and rollback horizon that still needs it. Only after reviewing those horizons may the operator remove a read key from the live ring and recreate all affected services. Never delete the only recoverable key copy. Retiring an encryption reader does not rotate VAPID: Web Push signing-key changes require the separate device reconnection procedure.

For disaster recovery, restore `security-keys.json` into `.env` alongside the matching database/uploads/image. A version2 bundle's `encryption_keys` object is the complete ring; its CSRF/rate values remain separate secrets. Keep the restic repository password separately. Rolling back Core40 requires the verified predecessor database/uploads/key snapshot and matching old image together; removing the ring from `.env` or downgrading the binary alone is insufficient.

The automatic security bundle does **not** contain the VAPID private signing key. Preserve `.env`, which holds `HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY`, separately in encrypted operator-controlled storage to retain device connections after disaster recovery. Without that key, generate a new VAPID identity and reconnect devices deliberately. Passing protected-record restore verification proves stored subscription decryptability, not preservation of its signing key or physical push delivery.

## Device notifications

Web Push is on whenever `.env` holds `HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY` (`household init` generates one); `HOUSEHOLD_WEB_PUSH_ENABLED=false` turns it off, and `HOUSEHOLD_WEB_PUSH_VAPID_CONTACT` defaults to the HTTPS origin. Keep a reachable HTTPS `HOUSEHOLD_ORIGIN` for real devices. The domain belongs to installation configuration; it is not compiled into the application.

1. Sign in on the intended device and open **Notifications** in the settings tray. Connect it through the explicit permission action; browser/OS permission alone does not enable server delivery.
2. Enable each wanted account category and its separate device category. Review quiet hours/timezone and the module's own responsibility, source-confirmation or scheduling controls. Consent is personal; enabling or reconnecting does not replay past events.
3. Send the explicit test notification. Distinguish provider acceptance, a service-worker display request and personal confirmation. Check browser/OS permission, the current signed-in session, device pause, account/device consent, quiet hours, source eligibility and expiry when delivery is missing. After a session revocation, sign in again and reconnect deliberately; do not bypass revocation to restore delivery.
4. Monitor bounded worker failures and readiness without logging subscription endpoints or decrypted payloads. A server outage cannot reliably report itself through its own Push channel; monitor backups and host availability independently. Push is not a guaranteed alarm for cooking or safety-critical use.

For a domain move, first disable sending (`HOUSEHOLD_WEB_PUSH_ENABLED=false` in `.env`, then `docker compose up -d`). While the old origin remains reachable, have users remove their old device connections in Notifications. Then change the origin/proxy, recreate the affected app and deliberately reconnect at the new origin; review permissions and account/device categories before re-enabling sending. Preserve the VAPID identity unless intentionally rotating it. If old connections cannot be removed, keep sending disabled until their authority is safely revoked; do not assume an origin change did it. Registration envelopes bind account, household, session, role, authorization version and VAPID identity, but do not store the registration origin. Previously provider-accepted messages cannot reliably be recalled. A new hostname can also require new passkey enrollment; retain a working password/MFA recovery path.

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
docker compose run --rm --no-deps backup purge REQUEST_UUID
```

The executor independently checks requester status/admin role, request expiry, disabled state, matching generation and a complete backup created after the current pause, created within seven days and verified within seven days. Retention marks snapshots ineligible before pruning and reconciles remaining repository IDs; a failed retention run leaves those records ineligible until a successful backup reconciles them. Backup/verification operations serialize with a repository advisory lock. It truncates only compiled module tables, clears associated search/jobs/imports/links/notifications and source/destination action receipts, advances the generation, and audits completion. Lookup tables/migration histories remain. Attachment blobs are tombstoned for durable cleanup after metadata commit. Audit identifiers and encrypted historical backup copies follow their respective retention policy; purge does not erase already retained snapshots.

## File and job maintenance

Core5 records field-level history before module truncation. Purging source data does not erase audit history; administrator history can retain permitted names and values. No automatic audit retention deletion is configured. The owner must define any separate retention or archival policy, include encrypted historical backups in that policy, and preserve restore evidence. See [audit-trail.md](audit-trail.md) for the runtime/owner trust boundary.

Images/PDFs use opaque private filenames. Upload errors remove incomplete files; a crash can leave an unreferenced blob, which housekeeping removes after 24 hours. Deleted resources mark their files orphaned and queue cleanup after seven days. Confirmed module purge queues its file cleanup immediately. Cleanup and preview jobs use bounded batches and lease/generation checks; no network-facing path accepts arbitrary filesystem paths or executables.

Jobs retry with exponential delay and a maximum attempt count. Unsupported/failed work is recorded; OCR failure returns the capture to manual review. Each 30-second worker cycle runs its steps (reminders, alerts, notifications, files, document inputs, module schedulers, jobs) independently: a failing or panicking step is logged once per cycle under `background work failed` with its step name, and the remaining steps still run. A panicking job is recovered, released into backoff and logged with its kind and ID. Background panics, like request panics, are reported by type and call path only. An unreachable database ends the cycle with a single `database unavailable` error. Inspect `core.jobs` through owner tooling for operational failures. Do not edit domain history to "fix" a queue failure.

Final expired OCR leases also return eligible captures to manual review. Retryable failures keep captures pending for the next attempt; disabling OCR returns them to manual entry. Generation, live-file and confirmed-capture checks prevent stale jobs from reviving invalid work. OCR/operator output caps apply to streaming copies as well as ordinary writes.

## Exports, help and sharing

- **Household export.** Administrators with a recent sign-in download `GET /api/v1/core/export.zip` from the backup page: one CSV per entity of every enabled module, including archived and template shopping lists and the items of archived lists, plus `manifest.json` with each file's SHA-256 and a list of entities left out because they exceed the CSV limit or are not visible to the requester (for example restricted finance data). Files and documents are not included; use the documents export and backups for those. The archive is built in memory up to 96 MiB and shares the two export slots with the other exports. Every download is recorded as the audit event `household_exported`; the export is refused if that record cannot be written.
- **Audit export.** `GET /api/v1/core/audit/export.jsonl` exports the filtered audit history that the viewer shows, newest first, at most 20,000 entries. Each line carries `chain = sha256(previous + "\n" + entry)`, starting from `sha256("" + "\n" + "household-audit-export-1")`, and a final manifest line names the count, the final chain value and whether the export was truncated; a truncated export also names `next_before`, and the same request with `before=<next_before>` continues with the older entries. Store the manifest separately to detect later changes to the file. Each download is recorded as `audit_exported` after the chain is final, so it appears in the next export, not in its own.
- **Browser errors.** Pages report uncaught script errors to `POST /api/v1/core/client-errors` (signed in, CSRF, 30 per member per 15 minutes). The report carries only an allowlisted error class (such as `TypeError`), the script file name, line and column; error messages are refused, because they can quote user input, and the page URL or query string is never recorded.
- **Compression.** Static assets are served gzip-compressed with `Vary: Accept-Encoding` when the browser accepts it. HTML and API responses stay uncompressed, because many carry CSRF tokens (BREACH).
- **Help.** `/help` explains Household's recurring terms in German and English.
- **Share target.** The installed app accepts photos and PDFs from the system share sheet. The service worker parks them in the device cache `household-share-v1`, and `/share` turns each into a new document through the normal, CSRF-protected document routes. Nothing reaches the server before the member saves. A parked file belongs to the first account that opens the share page on that device; other accounts never see it. Its create payload and request keys are fixed on the first save attempt, saving and discarding are serialized across tabs, and once the document exists only the upload is retried, so one shared file never becomes two documents. A share that arrives before the service worker is active is answered by `POST /share` with a redirect only; that route reads no body and is exempt from the origin check. Signing out clears the parked files with the other private device data.
- **Update check.** With `HOUSEHOLD_UPDATE_CHECK=true`, the app asks the GitHub API at most once a day for the latest `main` commit and shows on `/settings/system` whether the running revision differs. It sends no household data, only an anonymous request; the result stays in memory. Images without a build revision show no comparison. The check compares revisions, not release numbers: Household publishes no GitHub releases, and the `production` image is built from `main`. An image built from another branch (for example a local `dev` build) therefore reports a newer version whenever `main` differs; leave the check off there.
- **Password breach check.** With `HOUSEHOLD_PASSWORD_BREACH_CHECK=true`, new passwords (password change and user creation) are checked against the Have I Been Pwned range API: only the first five characters of the SHA-1 hash leave the server, with response padding. An unreachable service accepts the password and logs a warning. The bootstrap password from `.env` and the operator command `recover` are deliberately not checked, so account recovery never depends on an outside service.

## Local quality workflow

`scripts/test-integration.ps1` starts `household-tests-db-1` on port 55434. The review server uses port 8101 and only `household_test`. Their credentials come from `.local/secrets` (`scripts/initialize.ps1`, i.e. `household init --files`), are separate from `.env` and must never be reused for a live installation. Normal production Compose does not include these ports/services.

`scripts/test-recovery.ps1` is a Windows/Docker rehearsal. Each run uses its own in-memory PostgreSQL container `household-recovery-test-db-<run>`, its own generated database passwords (never those from `.env`), its own private directory `.local/recovery-test/<run>` and its own volumes, on the network of the running Compose project (`-ProjectName` selects a non-default project; `-BackupImage` the image under test). It migrates a fresh fixture source, seeds a synthetic quantity plus two private binary objects, backs up to a separate encrypted repository, restores household_restore, and checks domain values and both file hashes. It then removes everything it created; if a removal fails, the run fails. Only a fully clean run writes `outputs/recovery-review/report-<run>.json`, which names the run and the tested image. Its deliberately unusable fixture account is not an authentication demonstration.

The application is license-free open source. Docker Desktop, hosted GitHub runner quotas, optional off-site storage and the host itself can have separate terms/costs. Native Linux Docker Engine and a self-hosted runner require no proprietary application license.

## Pantry balance reconciliation

Stock movements remain authoritative. The runtime role can only read lot_balances. Run the operator CLI command `household pantry-check` to compare every lot balance, movement count and latest booking timestamp against the journal; a mismatch returns a failing exit code. After investigating the cause and taking a backup, `household pantry-rebuild` with the separate owner database role rebuilds only the derived balances. It takes the same outer household barrier as writers/purge before locking the journal; active household writes finish first. Runtime credentials cannot rebuild. Both commands are bounded to five minutes and log only mismatch counts. Health readiness checks the projection trigger and runtime privileges, not an expensive full journal scan. Restore verification also performs full reconciliation. Upgrades lock the same household barrier and journal during backfill and trigger installation; do not run independent operator writes concurrently with maintenance.
