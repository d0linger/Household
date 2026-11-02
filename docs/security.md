# Security model and account recovery

## Accounts and sessions

Passwords use bcrypt cost 12, at least 14 Unicode characters and at most 72 bytes. Login uses a dummy bcrypt comparison for unknown accounts. Expensive hashing is bounded and credential changes have persistent per-user limits. Network/account limits are HMAC-subject PostgreSQL buckets and survive application restarts. Login admits 100 attempts per client address per 15 minutes, 20 per account from one client network (IPv4 /24, IPv6 /56) per hour, and 500 per account across all networks per hour. Exhausting one network's budget therefore cannot lock the account holder out from another network, while the account-wide cap bounds distributed guessing.

Session tokens contain 256 random bits. Only SHA-256 hashes are stored. Idle expiry slides up to 24 hours on interactive authenticated page access, capped by 30-day absolute expiry; API polling does not keep sessions alive. Cookies are HttpOnly, SameSite=Lax and Secure in production, with the `__Host-` prefix. Explicit local HTTP development uses a separate cookie name.

Login rechecks the verified password under a user lock and verifies TOTP/recovery consumption in the same transaction before minting a session. Credential resets cannot leave an old-password login holding a new valid authorization version. Mutations take the household barrier and lock the current session/user/membership before writing, so queued requests recheck revocation and role changes.

TOTP secrets are encrypted with AES-GCM using an independent deployment key. Accepted counters cannot be replayed. Recovery codes are random, hashed, single-use, and displayed once. Enrollment requires a session created within ten minutes and transactional session validation. User administration protects the final active administrator and revokes affected sessions after role/status changes.

| Action | Reader | Editor | Admin |
|---|---|---|---|
| Read active modules/files/search | Yes | Yes | Yes |
| Write household records | No | Yes | Yes |
| Own password/TOTP/sessions/preferences | Yes | Yes | Yes |
| Toggle modules/manage users/request purge | No | No | Yes, with recent login for sensitive actions |
| Execute purge/recovery/backup | No HTTP endpoint | No HTTP endpoint | Separate operator credentials and CLI |

## Browser and request boundaries

Every HTML response has a new CSP nonce. Scripts are local and nonce-authorized; styles/fonts/images/connect/worker/manifest are local. Frames and plugins are forbidden. HSTS, nosniff, frame denial, a restrictive permissions policy, and same-origin referrers are set. Production rejects an incorrect Host, untrusted HTTPS forwarding, cross-origin unsafe requests and cross-site Fetch Metadata.

Unsafe authenticated requests require a session-bound HMAC CSRF token. Login has a separately bound pre-authentication cookie/token. JSON decoders reject unknown fields and trailing documents; bodies and multipart files are bounded. Errors expose a stable user message/request ID and keep internal details in operator logs.

## Files and extraction

JPEG/PNG inputs are decoded with pixel/concurrency limits and re-encoded without metadata. PDFs are downloadable attachments, with sandbox CSP. SVG and executable uploads are rejected. Opaque UUID storage keys and `os.Root` prevent file traversal. Access checks household membership, active module/generation and resource existence. Previews use the same authorization.

OCR runs a fixed local Tesseract executable with argument separation, bounded output and a 20-second timeout. Source text is untrusted data, escaped for display, and cannot supply executable instructions. Capture confirmation validates every field and commits once; a changed replay conflicts. No external inference or automatic record creation occurs.

## Database and audit

The runtime role has scoped DML privileges; it cannot migrate, truncate, alter schema, insert/update/delete audit history directly, update/delete stock movements, or mark backups verified. Migration/maintenance uses a separate non-superuser owner role. SQL values are parameters; dynamic identifiers come only from compiled descriptors/catalog allowlists. Financial triggers and the append-only rejection trigger run as invoker. Core5 audit capture functions use narrowly granted `SECURITY DEFINER`, a pinned `pg_catalog` search path and qualified objects; the owner-only installer and policy table cannot be invoked or changed by the runtime.

Audit retains actor/resource identifiers, server-generated request IDs, actions, outcomes and field changes. Explicitly permitted domain names, dates, quantities and amounts remain visible; credentials, document titles, OCR/import content and private free text are masked before insertion. User text in permitted names can still contain personal information. Unknown fields default to masked values. Runtime history is append-only; the database owner remains trusted and can alter it. No automatic audit deletion is enabled. Establish an operator retention policy before collecting long-lived history; module purge retains its audit trail and encrypted backups. See [audit-trail.md](audit-trail.md). External security reporting contacts are not invented; until a repository/contact is configured, report sensitive findings directly to your installation operator.

## Operator account recovery

Stop the app, set a fresh password in `.local/secrets/bootstrap_password`, and run the owner-credential migration service as a one-off:

```sh
docker compose stop app
docker compose run --rm --no-deps migrate recover admin
docker compose up -d app
```

This forces a password change and revokes all sessions. Existing MFA remains required. If the authenticator and recovery codes are both lost, explicitly add `--reset-mfa` to the recovery command. That action removes the factor/codes and records an audit event. Recovery refuses runtime-role credentials.

Keep secrets outside Git and back up the restic repository password independently. Preserve the stored-secret encryption key. Restoring an encrypted snapshot provides its security-key bundle; perform any key migration as controlled maintenance, never by simply replacing the encryption key file.

## Verification interpretation

Static analysis and dependency scans are gates, not a proof of absence of vulnerabilities. `govulncheck` on Go 1.27.3 reports zero reachable/package vulnerabilities. Its module-level advisory for the unmaintained `golang.org/x/crypto/openpgp` package is not imported by Household; bcrypt is the used package. Scanner exceptions are narrow inline explanations for trusted operator paths, fixed executables, bounded conversion, or explicit HTTP development.
