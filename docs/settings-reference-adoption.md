# Settings, security and audit reference adoption

Public reference sources were inspected read-only on 2026-11-02. GitHub's repository and commit APIs confirmed these current main-branch revisions:

- [Treckrr 86c70c3305ba60d0742a4b60dafc390c4da7a69b](https://github.com/d0linger/Treckrr/tree/86c70c3305ba60d0742a4b60dafc390c4da7a69b), committed 2026-11-02 09:03:27 UTC.
- [Parkrr d6368e8d0d7bb29505a98fa1235c3809c4179cc3](https://github.com/d0linger/Parkrr/tree/d6368e8d0d7bb29505a98fa1235c3809c4179cc3), committed 2026-11-02 08:52:19 UTC.

Both repositories use the MIT license: [Treckrr license](https://github.com/d0linger/Treckrr/blob/86c70c3305ba60d0742a4b60dafc390c4da7a69b/LICENSE), copyright 2026 Treckrr, and [Parkrr license](https://github.com/d0linger/Parkrr/blob/d6368e8d0d7bb29505a98fa1235c3809c4179cc3/LICENSE), copyright 2026 Parkrr contributors. Substantial copied source must retain the applicable notices. Household's audit presentation was implemented independently against its existing audit contract; no reference implementation or dependency was copied into that workstream.

No sibling checkout, container, database, local policy or secret was accessed. Reference policies do not authorize changing Household's workflow or deploying elsewhere.

## Decisions

| Capability | Verified reference pattern | Household adaptation |
|---|---|---|
| Settings navigation | Treckrr's profile groups account controls; its drawer groups administration. Parkrr's settings groups account controls and links to administration. | The shared horizontal Settings / Security / Users / Audit / Modules navigation is the user's requested Household adaptation. It is not claimed as an identical reference feature. Preserve server authorization and hide administrator destinations from other roles. |
| Logout | Both references expose a named logout action in navigation. | Expose logout in the shared shell, retaining POST, CSRF and Household's private-device cleanup. |
| Appearance | Both references expose a header theme control. | Keep Ocean/Pink palettes and existing device preference semantics; do not import their visual identity or external assets. |
| Language | The inspected reference source establishes German UI, not a complete DE/EN catalog. | Household's language switch and message catalogs are independent implementation work. User records and canonical API values remain unchanged. |
| Audit density | Parkrr renders actor, operation, record summary and time with field old/new values. | Use compact responsive rows and native expandable changes. Technical identifiers have a separate disclosure. |
| Audit attribution | Parkrr stores/display usernames with its events. | Resolve current names through a household-scoped account/membership join. Do not write identity snapshots or relabel old names as historical evidence. |
| TOTP | Both references offer TOTP/recovery management alongside account controls. | Preserve encrypted secrets, one-use recovery codes and authenticated security mutations. Show actual factor status. |
| Passkeys | Both use user-verifying discoverable WebAuthn credentials. Treckrr verifies password and an existing TOTP factor for enrollment. | Require a verified origin/RP, strong enrollment confirmation, bounded one-use ceremonies, request throttles, account/session rechecks and atomic credential updates. Keep password/TOTP recovery available. |
| Sessions | Both expose current-device information and revocation of other sessions. | Keep revocation scoped to the authenticated account and audit its actual effects. |

Source entry points:

- [Treckrr shared layout](https://github.com/d0linger/Treckrr/blob/86c70c3305ba60d0742a4b60dafc390c4da7a69b/internal/web/templates/layout.html) and [profile](https://github.com/d0linger/Treckrr/blob/86c70c3305ba60d0742a4b60dafc390c4da7a69b/internal/web/templates/profile.html).
- [Treckrr WebAuthn handlers](https://github.com/d0linger/Treckrr/blob/86c70c3305ba60d0742a4b60dafc390c4da7a69b/internal/server/webauthn.go) and [single-use ceremony store](https://github.com/d0linger/Treckrr/blob/86c70c3305ba60d0742a4b60dafc390c4da7a69b/internal/store/webauthn_ceremony.go).
- [Parkrr audit/settings rendering](https://github.com/d0linger/Parkrr/blob/d6368e8d0d7bb29505a98fa1235c3809c4179cc3/web/static/js/app.js), [passkey handlers](https://github.com/d0linger/Parkrr/blob/d6368e8d0d7bb29505a98fa1235c3809c4179cc3/internal/handlers/passkeys.go) and [WebAuthn service](https://github.com/d0linger/Parkrr/blob/d6368e8d0d7bb29505a98fa1235c3809c4179cc3/internal/auth/webauthn.go).

## Household audit boundary

The viewer remains administrator-only and household-scoped. The existing financial visibility function remains part of every query. Account names are resolved only through a membership in that household, including inactive memberships so disabling an account does not erase its attribution. Missing users are shown as former or unknown users. System, worker, operator, bootstrap and direct-database origins remain distinct. The special legacy recovery projection continues to treat the stored account ID as the target, not the operator.

The API adds optional `actor_username`, explicitly a current-name lookup; the immutable actor ID remains unchanged. Search parameter `q` accepts at most 100 Unicode characters and searches current usernames and already-stored record labels, case-insensitively. Percent and underscore are literal characters, not SQL wildcards. It does not search protected field contents or fetch source records to recreate private labels. Existing module, action, resource and request filters remain. The descending-ID cursor returns at most 50 entries, preserves filters and provides a link back to the newest matching page.

The viewer retains PostgreSQL numeric precision through `json.Decoder.UseNumber` and server formatting. Missing, NULL and protected values remain distinct. Field comparisons continue to use stored audit data, never reconstructed live record values. Only fixed metadata and generated value labels enter translation; usernames, record labels and user-entered before/after values bypass the message catalog. Technical IDs and versions remain inspectable in the expanded technical section.

Household's explicit safe-column policy remains authoritative. The reference's broader field-name-based redaction policy, username snapshot storage, audit retention durations, application-specific financial assumptions and passkey-only operating mode are not adopted by this viewer.

## Verification scope

Focused unit coverage covers actor presentation, generated versus user record labels, exact large amounts, protected values, absence/NULL/boolean distinctions, technical-field separation, escaped rendering and translation boundaries. Integration coverage adds household-scoped current-name resolution, inactive/missing/out-of-scope actors, literal search, renaming without history mutation and filtered cursor navigation.

Execution results belong in [the implementation ledger](implementation.md). The existence of tests and this reference review does not certify their execution, browser appearance, migrations, backup recovery or deployment.
