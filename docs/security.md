# Security model and account recovery

## Accounts and sessions

Passwords use bcrypt cost 12, at least 14 Unicode characters and at most 72 bytes. Login uses a dummy bcrypt comparison for unknown accounts. Expensive hashing is bounded and credential changes have persistent per-user limits. Network/account limits are HMAC-subject PostgreSQL buckets and survive application restarts. Login admits 100 attempts per client address per 15 minutes, 20 per account from one client network (IPv4 /24, IPv6 /56) per hour, and 500 per account across all networks per hour. Exhausting one network's budget therefore cannot lock the account holder out from another network, while the account-wide cap bounds distributed guessing. A further budget of 300 attempts per 15 minutes applies to each client network prefix (IPv4 /24, IPv6 /64), so rotating addresses inside one prefix cannot multiply pre-authentication bcrypt work. Second factors have their own limit: after 10 wrong TOTP or recovery codes for an account within 15 minutes, login and re-authentication refuse the factor before checking it until the window passes.

Session tokens contain 256 random bits. Only SHA-256 hashes are stored. Idle expiry slides up to 24 hours on interactive authenticated page access, capped by 30-day absolute expiry; API polling does not keep sessions alive. Cookies are HttpOnly, SameSite=Lax and Secure in production, with the `__Host-` prefix. Explicit local HTTP development uses a separate cookie name.

Login rechecks the verified password under a user lock and verifies TOTP/recovery consumption in the same transaction before minting a session. Credential resets cannot leave an old-password login holding a new valid authorization version. Mutations take the household barrier and lock the current session/user/membership before writing, so queued requests recheck revocation and role changes.

TOTP secrets are encrypted with AES-GCM using an independent deployment key. Accepted counters cannot be replayed. Recovery codes are random, hashed, single-use, and displayed once. Enrollment requires a session created within ten minutes and transactional session validation. User administration protects the final active administrator and revokes affected sessions after role/status changes.

Passkeys validate the configured origin and hostname; their private keys remain on authenticators. Household encrypts stored credential metadata and transient passkey challenges. Core40 writes purpose/account/record-bound AES-GCM envelopes, with household binding for push subscriptions, while retaining explicitly configured legacy readers. A missing read key is an operational failure, not permission to discard encrypted records or disable valid connections. The [key rotation and recovery runbook](operations.md#encryption-keys-and-rotation) defines reader retention, snapshot-bound escrow and the incompatible-schema rollback boundary.

| Action | Reader | Editor | Admin |
|---|---|---|---|
| Read active modules/files/search | Yes | Yes | Yes |
| Write household records | No | Yes | Yes |
| Own password/TOTP/sessions/preferences | Yes | Yes | Yes |
| Toggle modules/manage users/request purge | No | No | Yes, with recent login for sensitive actions |
| Request backup, restore test or live restore; change the backup schedule | No | No | Yes; a live restore needs a recent login and the snapshot-bound confirmation |
| Execute purge/recovery/backup/restore | No HTTP endpoint | No HTTP endpoint | Only the owner `backup` service or operator CLI |

Administrators can require a second factor (TOTP or passkey) for administrators and optionally editors under **Settings → Security** (`core.security_policies`). The acting administrator needs a factor and a recent sign-in. Affected accounts see a notice until the grace period (immediately, 7, 14 or 30 days from switching the requirement on) ends; afterwards the server allows them only their security settings, sign-out, password change, language and preferences, answering everything else with a redirect or `403` code `mfa_required`. The policy is cached for at most 30 seconds and a failed policy read fails the request instead of relaxing it. `household recover USER --reset-mfa` remains the operator's way out.

Every session records a keyed SHA-256 hash of a coarse "browser · system" label in `core.known_devices` (keyed with the rate secret and the account; no user agent, IP or name is stored; entries expire after 400 days without use). A sign-in from a label the account has not used before creates the personal security event `sign_in_new_device` (inbox and, with consent, Web Push) once the account's device history is older than one day, so existing devices are learned after an upgrade. Device tracking runs in a savepoint and never blocks a sign-in. Rotating the rate secret restarts the learning.

Backup work follows the purge pattern. The web role may insert an unclaimed request into `core.backup_jobs` (column grants plus the `backup_jobs_guard` trigger refuse claimed state, results or a missing requester) and cancel it while unclaimed; it may change only the schedule columns of `core.backup_settings`. The owner `backup` service claims each request and repeats every check with its own privileges: active administrator, unexpired request, full snapshot ID, and for a restore the typed confirmation bound to that snapshot plus a session re-authenticated within ten minutes. During a live restore the web role can only confirm its pause through the `SECURITY DEFINER` function `core.acknowledge_backup_drain`. Repository passwords and offsite credentials never reach the web process: `start` removes them from its environment, restic receives only the variables of the repository one command uses, the repository URL may not contain credentials and is stored only as that nonsecret label, and tool output is reduced to fixed reason codes. The backup service mounts the uploads volume writable only because a live restore must reconcile files; the replaced database and moved uploads are kept until an administrator discards them.

## Browser and request boundaries

Every HTML response has a new CSP nonce. Scripts are local and nonce-authorized; styles/fonts/images/connect/worker/manifest are local. Frames and plugins are forbidden. HSTS, nosniff, frame denial, a restrictive permissions policy, and same-origin referrers are set. Production rejects an incorrect Host, untrusted HTTPS forwarding, cross-origin unsafe requests and cross-site Fetch Metadata.

The only unsafe route without these origin checks is `POST /share`, the share-target fallback: it reads no body, changes nothing and only redirects to `/share`. Shared files stay in the device cache until the member saves them through the normal CSRF-protected upload routes. Static assets are gzip-compressed; HTML and API responses are not, so secrets such as CSRF tokens are never compressed together with attacker-influenced text (BREACH).

Outbound requests from the web process are opt-in and carry no household data: `HOUSEHOLD_UPDATE_CHECK` sends one anonymous GitHub API request a day, and `HOUSEHOLD_PASSWORD_BREACH_CHECK` sends the first five hexadecimal characters of a new password's SHA-1 hash to the Have I Been Pwned range API with padding enabled. Both use TLS 1.2 or newer, bounded timeouts and no redirects. The breach check fails open, because it is a hint, not an authentication factor.

Unsafe authenticated requests require a session-bound HMAC CSRF token. Login has a separately bound pre-authentication cookie/token. JSON decoders reject unknown fields and trailing documents; bodies and multipart files are bounded. Errors expose a stable user message/request ID and keep internal details in operator logs. User-correctable account refusals (password rules, re-authentication for factor changes, factor already active or not yet set up) are HTTP 422 with their message, not generic failures. Panic logs contain the panic type and a value-free call path only.

## Files and extraction

JPEG/PNG inputs are decoded with pixel/concurrency limits and re-encoded without metadata. PDFs are downloadable attachments, with sandbox CSP. SVG and executable uploads are rejected. Opaque UUID storage keys and `os.Root` prevent file traversal. Access checks household membership, active module/generation and resource existence. Previews use the same authorization.

OCR runs a fixed local Tesseract executable with argument separation, bounded output and a 20-second timeout. Source text is untrusted data, escaped for display, and cannot supply executable instructions. Capture confirmation validates every field and commits once; a changed replay conflicts. No external inference or automatic record creation occurs.

## Shared resources and background work

One household shares a single application process, database pool and serial background worker, so member-triggered work is bounded where it enters. Calendar feeds are rate limited per client before they touch the module gate, never wait for that gate and run with a five-second database budget. Attachment downloads read and authorize outside any transaction before taking their row lock, so they never hold one pool connection while waiting for another. CSV exports have a 16 MiB budget for every module and at most two concurrent exports. Each uploader may have at most 20 pending preview/OCR jobs; document processing refuses a duplicate extraction of the same version and caps 10 outstanding requests per member. The upload quota counts every stored byte, including orphaned and purged attachments that still occupy storage.

The worker claims at most ten jobs per tick, at most five of one kind, within a 25-second budget, and a failing job no longer ends the tick. Jobs whose source attachment disappeared retire instead of retrying. Automatic finance recurrences run one bounded batch per scheduled job and never re-queue themselves; automatic consent is refused while the first missing occurrence is more than 31 days old, so a far-past anchor must be caught up manually. The recurrence scheduler skips consents already scheduled today or holding a live job and rotates its bounded selection. Superseded shopping change events (same actor, lists and kind) are acknowledged in one statement before staging. Meter and energy input instants must lie within the last 30 years and inside a margin of the 1900–2200 range the projection worker accepts.

## Database and audit

The runtime role has scoped DML privileges; it cannot migrate, truncate, alter schema, insert/update/delete audit history directly, update/delete stock movements, or mark backups verified. Migration/maintenance uses a separate non-superuser owner role. SQL values are parameters; dynamic identifiers come only from compiled descriptors/catalog allowlists. Financial triggers and the append-only rejection trigger run as invoker. Core5 audit capture functions use narrowly granted `SECURITY DEFINER`, a pinned `pg_catalog` search path and qualified objects; the owner-only installer and policy table cannot be invoked or changed by the runtime.

Audit retains actor/resource identifiers, server-generated request IDs, actions, outcomes and field changes. Explicitly permitted domain names, dates, quantities and amounts remain visible; credentials, document titles, OCR/import content and private free text are masked before insertion. User text in permitted names can still contain personal information. Unknown fields default to masked values. Audit rows of Fuel integration bindings, Meters bills and expense-editor operations that name an Expenses entry follow that entry's Finance visibility, so administrators excluded from a selected entry do not learn its identity from those rows. Runtime history is append-only; the database owner remains trusted and can alter it. No automatic audit deletion is enabled. Establish an operator retention policy before collecting long-lived history; module purge retains its audit trail and encrypted backups. See [audit-trail.md](audit-trail.md). External security reporting contacts are not invented; until a repository/contact is configured, report sensitive findings directly to your installation operator.

## Personal security events and notification privacy

Real password changes, TOTP enable/disable, recovery-code regeneration/use, passkey addition/removal, session revocation, administrative account changes and device authorization create a personal inbox event atomically with the change. Only the affected account receives it, even when an administrator performed the change. Anonymous login failures, no-op updates, passkey renaming, routine device refresh and ordinary logout do not produce these messages. The closed event reference contains no passkey label, address, token or private free text.

External Security notifications require explicit account and device consent, current registering-session authority and quiet-hours checks. Their delivery freshness is one hour; enabling consent or registering a later device never replays an earlier event. Dismissing the source prevents a future authorized send. Password recovery or account changes can revoke every current session: the personal inbox event remains available after a legitimate later login, while revoked push sessions stay rejected. This channel is not an independent account-recovery mechanism or a guaranteed emergency alert.

Push uses generic localized lock-screen text and encrypted transport payloads; document titles, filenames, financial amounts and credential contents are excluded. Delivery still involves the browser vendor's gateway and network metadata; self-hosting Household does not make Web Push an entirely local transport. Provider acceptance, a fulfilled display request and the user's confirmation are distinct observations, and already accepted messages cannot reliably be recalled. Keep subscription endpoints and encrypted registration material out of public diagnostics.

Bounded reconciliation removes terminal delivery intents after 30 days and inbox notices after 90 days; cleanup can lag those thresholds. There is no separate attempt-history table or configurable notification-retention setting. This cleanup does not remove audit records or retained encrypted backups. Configure operational retention for those separately. Follow [device notifications](operations.md#device-notifications) for setup, origin changes and troubleshooting; changing `HOUSEHOLD_ORIGIN` alone does not revoke existing subscriptions. Physical device acceptance remains open.

## Operator account recovery

Set a fresh password in `HOUSEHOLD_ADMIN_PASSWORD` in `.env`, stop the app and run the recovery through the owner-credential `backup` service:

```sh
docker compose stop app
docker compose run --rm --no-deps backup recover admin
docker compose up -d app
```

This forces a password change and revokes all sessions. Existing MFA remains required. If the authenticator and recovery codes are both lost, explicitly add `--reset-mfa` to the recovery command. That action removes the factor/codes and records an audit event. Recovery refuses runtime-role credentials.

Keep `.env` outside Git and back it up independently; it holds the restic repository password. Preserve every required encryption reader, including the explicit legacy key while legacy records remain. Restoring an encrypted snapshot provides its security-key bundle; perform any key migration as controlled maintenance, never by simply replacing the encryption key in `.env`. The automatic backup escrow does not include the VAPID private key; preserving `.env` does.

## Verification interpretation

Static analysis and dependency scans are gates, not a proof of absence of vulnerabilities. `govulncheck` on Go 1.27.3 reports zero reachable/package vulnerabilities. Its module-level advisory for the unmaintained `golang.org/x/crypto/openpgp` package is not imported by Household; bcrypt is the used package. Scanner exceptions are narrow inline explanations for trusted operator paths, fixed executables, bounded conversion, or explicit HTTP development.
