# Settings, account security and bilingual interface

User-approved scope, 2 November 2026: unify settings navigation, make logout visible, adopt applicable Treckrr/Parkrr security behavior including passkeys, repair administrator user creation, compact the audit log, place appearance controls in the top bar, add Ocean/Pink automatic appearance and provide German/English throughout Household.

Preserve the Ocean/Pink visual identity, existing household data, module boundaries, exact amounts, audit redaction and server authorization. Reference patterns are adapted to Household; unrelated reference containers/files are excluded. No Git push or remote publication is authorized by this task.

## Implementation checklist

### Security and user administration

- [x] Trace existing user creation and identify the ten-minute authentication-freshness failure.
- [x] Inventory existing authentication, MFA, sessions and reference safeguards.
- [x] Separate missing administrator permission from required recent authentication.
- [x] Add explicit reauthentication without extending absolute session lifetime.
- [x] Preserve user-form drafts while completing required reauthentication.
- [x] Add bounded, one-use WebAuthn ceremonies with required user verification.
- [x] Add passkey registration, login, reauthentication, naming and revocation.
- [x] Show actual TOTP status, recovery-code count and supported factor actions.
- [x] Add protected TOTP removal and recovery-code regeneration.
- [x] Show current/other sessions and revoke other sessions safely.
- [x] Preserve bootstrap password changes, disabled-account checks, rate limits and last-admin protection.
- [x] Add forward Core36 migration and reviewed Fuel readiness compatibility.
- [x] Verify authentication, authorization, challenge replay, concurrent updates and populated upgrade paths.

### Settings navigation and audit

- [x] Review current Treckrr/Parkrr navigation and audit patterns from public, pinned sources.
- [x] Share a real-route settings navigation across General, Modules, Security, Users and Audit.
- [x] Keep administration links role-aware and active state accessible.
- [x] Make logout directly visible in the shared header while retaining POST/CSRF and offline cleanup.
- [x] Separate device/personal settings from module activation and deletion controls.
- [x] Replace oversized audit cards with compact summaries and expandable changes.
- [x] Display household-scoped current usernames with truthful system/former-user fallbacks.
- [x] Keep exact before/after values, protected-field masking and legacy-history semantics.
- [x] Keep technical IDs secondary and preserve pagination/finance visibility boundaries.
- [x] Verify keyboard, mobile layout, filters, escaping and permission behavior.

### Appearance and language

- [x] Add top-bar appearance controls using only Household's Ocean and Pink families.
- [x] Support Light, Dark and System for each family; persist accurate selected mode.
- [x] Apply live OS scheme changes only when the selected family uses System.
- [x] Add a visible German/English language switch to authenticated and public UI.
- [x] Resolve explicit account/device locale without mutating shared module descriptors.
- [x] Add embedded catalogs and explicit translation boundaries for static application copy.
- [x] Translate shared pages, all nine modules, accessible names, dialogs and dynamic states.
- [x] Translate application validation/error messages without translating user records or API identifiers.
- [x] Localize display dates/numbers while preserving civil dates, exact decimals, EUR and machine formats.
- [x] Cover offline shells and local PWA resources; retain private-data cache exclusions.
- [x] Add catalog/placeholder/coverage checks and DE/EN behavior regressions.

### Acceptance and local delivery

- [x] Complete appropriate unit, integration, real-browser and security checks.
- [x] Inspect all six appearance selections and both languages at mobile/desktop widths.
- [x] Validate passkey flows with a browser virtual authenticator and negative authority cases.
- [x] Record actual results, remaining platform limitations and independent review.
- [x] Update PRODUCT.md, DESIGN.md, README.md and implementation/testing documentation.
- [x] Take and verify a pre-upgrade backup before the migration reaches the local development database.
- [x] Remove only task-owned test processes, databases and temporary credentials.
- [x] Rebuild/migrate/recreate the local Docker stack on 8100, preserving volumes and secrets.
- [x] Verify deployed health, assets, schema readiness and changed UI.

## Agreed behavior

The administrator error was caused by `Identity.Recent()` using login creation time and `createUser` returning the same forbidden response for an old login and a missing role. Recent-authentication protection now has an explicit confirmation workflow; a stale administrator login returns `reauth_required` while an unauthorized role remains forbidden. The user draft survives confirmation or cancellation.

The audit log already captures selected field changes. Compact presentation must keep the stronger Household allowlist, distinguish missing/null/protected values and preserve financial visibility checks. Current usernames are presentation lookups, not new historical snapshots; unresolved former users remain distinguishable from system actors.

| Family | Light | Dark | Automatic |
|---|---|---|---|
| Ocean | `light` | `dark` | `system` |
| Pink | `pink-light` | `pink-dark` | `pink-system` |

`pink-system` is a truthful new stored preference and requires a forward constraint update. The existing nullable locale column can store the validated German/English selection. User-entered titles, notes, imports, IDs, enum values and API field names remain unchanged by language selection.

Security/authentication and audit are independent implementation streams within the shared settings area. Localization is a cross-cutting infrastructure stream; final acceptance requires the full interface, not only a translated header. Runtime verification follows completed implementation, and only executed evidence is marked passed.

## Executed evidence

Final feature acceptance: 375 Go unit mains, 366 Node tests, 28 selected PostgreSQL test mains, 135 settings/security browser assertions, 24 clean Axe scans and 246 all-module localization browser assertions passed. All 34 legacy browser components passed in a documented sequential campaign. Earlier passing components were retained across scoped test-selector corrections. The final two components ran after a narrow Energy hour-boundary correction; the unchanged UI/authentication/storage sources are independently pinned in `tests/browser-source-qualification.json`. The shared security/language suites and full Go/static checks ran again on the final build. No unexecuted repeat or full integration-suite run is claimed.

The first database campaign exposed missing audit classification for encrypted passkey protocol state; Core36 now preserves all 163 business policies and rejects unknown tables. The mobile Shopping correction removes an inherited label margin while preserving the first 52px check target above navigation at 390×844; 30 assertions and three Axe scans include 320px keyboard clearance. The Energy correction ignores stale timezone bounds only when the resulting hour retains the verified offset. Year-end, DST, quarter-hour-zone and exact-quantity tests pass. The normal worker recovered all four previously blocked persisted series without altering their measurements, leases or retry schedule.

The final Finance component passed 297 assertions/16 Axe scans, Meters 118/14 and Documents 120/16. Documents ran in the same-source Docker runtime because the native Windows fixture lacked the complete PDF tools; actual extraction, previews, search, export and authority boundaries passed. Browser authenticator support was virtual; physical biometrics and cross-device synchronization were not tested. Independent visual review covered audit, security, narrow headers and nine English module captures. Failed attempts and prerequisite failures remain preserved.

The encrypted pre-upgrade backup was refreshed on 3 November and actually restored in an isolated verifier. All task-owned browser/server resources, PDF/PostgreSQL review containers, temporary image tag, synthetic uploads and credentials were removed. Source/binary verification passed; the existing development stack remained unchanged during cleanup. Final Docker delivery completed on 3 November at 06:28 UTC: the rebuilt app and recreated database are healthy, Core36/all module versions and 163 audit policies are ready, and existing volumes and secrets are preserved. The deployed app passed 24 public HTTP checks, exact comparison of 12 embedded assets and 16 language/appearance/system combinations. Evidence: `outputs/settings-security-localization/` (including `deployment/deployment.json` and `deployment/public-verification.json`), `outputs/settings-security-language-review/` and `outputs/i18n/browser/`.
