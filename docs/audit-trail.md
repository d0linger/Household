# Audit trail

Household records committed changes to 45 registered tables in `core.audit_log`. PostgreSQL triggers capture field differences, including child rows changed by an action or a cascading delete. Semantic events explain operations such as sign-in, password recovery, module activation and terminal worker failures. Both forms share request attribution where they belong to the same operation.

The implementation is in [core migration 5](../internal/database/migrations/core/000005_audit.up.sql), [transaction helpers](../internal/audit/audit.go), [store mutations](../internal/store/store.go) and the [administrator viewer](../internal/web/audit.go). This history is additional application data, not an independent or cryptographically signed security journal.

## Registered coverage

The owner-managed `core.audit_policies` table defines keys, permitted values, labels and ignored fields. The following 26 domain tables and 19 core tables are registered:

| Schema | Count | Tables |
|---|---:|---|
| shopping | 3 | `lists`, `items`, `item_events` |
| fuel | 3 | `vehicles`, `odometer_series`, `fillups` |
| recipes | 5 | `recipes`, `ingredients`, `steps`, `tags`, `recipe_tags` |
| pantry | 4 | `products`, `locations`, `lots`, `stock_movements` |
| expenses | 3 | `categories`, `entries`, `entry_lines` |
| chores | 2 | `task_templates`, `task_occurrences` |
| meters | 3 | `meters`, `devices`, `readings` |
| documents | 1 | `documents` |
| mealplan | 2 | `plans`, `meals` |
| core | 19 | `households`, `users`, `memberships`, `user_preferences`, `module_states`, `sessions`, `totp_credentials`, `recovery_codes`, `resource_links`, `module_purge_requests`, `attachments`, `attachment_derivatives`, `capture_jobs`, `reminders`, `notifications`, `push_subscriptions`, `import_batches`, `import_rows`, `backup_runs` |

Trigger coverage applies to ordinary CRUD, bulk import, capture confirmation, cross-module action results, attachment lifecycle, account/settings changes, background changes and operator mutations. A successful transaction produces its complete set of row changes; an aborted transaction produces none. Idempotency replay returns the existing result without another set of changes. A failed authentication attempt can still have an explicit semantic failure event because it is not a successful domain mutation.

Some internal tables are deliberately excluded from row auditing:

| Tables | Reason and retained evidence |
|---|---|
| `core.audit_log`, `core.audit_policies` | Avoid recursion; policy belongs to the migration owner. Policy changes are deployment work, not an application setting. |
| All `schema_migrations`, `core.units`, `core.module_installations`, `fuel.fuel_types` | Migration/reference catalogs rather than household dialogs. Version/checksum evidence remains in deployment tooling. |
| `core.rate_limit_buckets`, `core.auth_challenges` | Frequent security bookkeeping and challenge payloads. Authentication outcomes are explicit security events; credentials are never copied. |
| `core.jobs`, `core.file_deletions` | Queue/lease bookkeeping. Terminal job failures are semantic events; actual capture, derivative and attachment changes are audited on their authoritative tables. |
| `core.outbox_events`, `core.event_deliveries`, `core.idempotency_records` | Delivery and replay receipts duplicate authoritative changes and can contain payloads. |
| `core.search_documents`, `core.alert_states` | Derived search/alert projections. Source records, generated notifications and reminders retain their own history. |

This is mutation history. It does not record every read, download, rejected request, rate-counter increment or filesystem access. Successful external commands and file operations have their existing operational checks; their database lifecycle changes are covered as listed above.

## Stored event and field differences

Each event has an identity sequence `id`, UUID `event_id`, `created_at`, household, nullable `actor_user_id`, `actor_kind`, database login role `db_role`, module/entity/resource identifiers, `entity_key`, `entity_label`, action/outcome, request UUID, `changes` and constrained semantic `details`.

Row actions are uppercase `CREATE`, `UPDATE`, `DELETE`. Semantic actions retain their meaningful names, such as `record_write`, `module_purged`, `login_failed` or `worker_lease_exhausted`; their `changes` object is empty. One operation can therefore have a semantic event and several row events.

`changes` maps a field name to this shape; the following is an illustrative recipe update, not production data:

```json
{
  "base_servings": {
    "before": 2.000000,
    "after": 4.000000,
    "before_present": true,
    "after_present": true,
    "redacted": false
  },
  "notes": {
    "before": "***REDACTED***",
    "after": "***REDACTED***",
    "before_present": true,
    "after_present": true,
    "redacted": true
  }
}
```

Presence is independent of value: a newly inserted nullable field has `before_present=false`, `after_present=true` and may have `after=null`. Deletion reverses presence. A real SQL NULL is different from an absent row field. The trigger compares actual values before masking, so a changed private field remains visible as a change without exposing either value.

Numeric values remain PostgreSQL numbers. Go uses `json.Decoder.UseNumber` in the audit viewer and shared record decoder, preserving decimal text and integer minor units without a `float64` intermediate. The HTML viewer formats those values on the server. The JSON API still emits JSON numbers: external clients must use a decimal-aware parser where exact values matter; ordinary JavaScript `Number` parsing can lose precision.

`entity_key` preserves composite identifiers, including recipe/tag links, user preferences, module states and import rows. `resource_id` is a convenient UUID where one can be derived; it does not replace the complete key. Labels come only from permitted policy fields, otherwise a table name and shortened opaque ID are used.

## Privacy boundary

Values are stored only for explicitly permitted fields, plus the common `id`, `household_id`, `created_by` and `version` fields. Other changed fields are masked, including unknown columns added later. Password hashes, tokens, encrypted secrets, recovery-code hashes, usernames/email/display names, raw OCR, extracted/confirmed capture payloads, imported raw values, document titles, private free text, file storage keys/hashes and repository details are not copied into the new audit values.

Safe strings are capped at 256 characters; labels at 160. These bounds also apply to direct database writes. Safe policy fields must be scalar: a permitted object/array causes the mutation to fail rather than duplicating an unbounded payload. Semantic metadata is limited to 4 KiB and currently accepts only `mfa_reset` as a boolean and `target_user_id` / `requested_by` as UUIDs; other keys are rejected.

`created_at` and `updated_at` are ignored in row diffs. Session `last_seen_at` / `idle_expires_at` and the TOTP replay counter are also ignored to avoid recording routine authentication activity. An UPDATE whose remaining values do not change produces no row event. A version increment is itself recorded, even if the displayed business fields are unchanged.

Selected household record names/titles are intentionally allowed so administrators can identify ordinary shopping, recipe, task and meal changes. The policy cannot detect a password, personal identifier or other private text entered into such an allowed name/title. Application administrators and backup operators can access retained permitted values. The mechanism does not promise automatic detection of all personal data.

## Attribution and atomicity

HTTP middleware generates a fresh UUID for each request and returns it in `X-Request-ID`; a supplied request header does not choose the audit identity. Authentication supplies the trusted application actor. The context helpers do not perform authorization: existing role, session, household and module-generation checks remain required.

For example, the store write path establishes context before its existing transaction boundary:

```go
ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
tx, err := s.BeginMutation(ctx, id, m.ID, in.Generation)
if err != nil {
    return nil, err
}
defer rollback(tx)
```

`BeginMutation` calls `audit.Begin` before any mutation, then performs its existing locks and authorization checks. `audit.Bind` writes transaction-local PostgreSQL settings with `set_config(..., true)`. The same `pgx.Tx` performs the changes, audit inserts, projections and receipt writes. Rollback removes all of them; local settings disappear when the transaction ends and cannot leak into the next pooled request. `audit.Exec` is the single-statement transaction wrapper. `audit.Event` invokes the constrained database event function rather than inserting arbitrary JSON into the log.

`WithSystem` clears inherited user/household attribution. Worker ticks use `actor_kind=worker` and a new request UUID; terminal unsupported/exhausted/failed jobs have failure outcomes. Backup, restore verification and purge use `operator`. Purge records the administrator who requested it as `details.requested_by`; the executing operator is not misrepresented as that user. `WithHousehold` scopes operator/worker work without inventing a human actor. Bootstrap is identified separately. A direct database write without application context is attributed as `database` for runtime credentials or `operator` for owner credentials, with no claimed application user.

These settings provide application attribution, not authentication of a database client. A compromised runtime database credential could set custom session settings or call the allowed semantic function. It still cannot directly insert/update/delete audit rows or change trigger policy using its normal privileges. Independent tamper evidence would require a separately controlled destination and is not implemented.

## Purge, permissions and retention

Ordinary `AFTER INSERT OR UPDATE OR DELETE` triggers audit row changes. Because PostgreSQL TRUNCATE bypasses row-delete triggers, each registered table also has a `BEFORE TRUNCATE` trigger. For owner maintenance it streams existing rows into policy-filtered DELETE events before truncation, within the same transaction. A failed purge rolls back its audit rows too. Large purges now perform audit work proportional to the rows removed and can increase storage and execution time.

The runtime role cannot directly insert, update, delete or truncate `core.audit_log`, use its identity sequence, edit/read the policy table, install/disable triggers or execute the private writer functions. It can read history for the application and execute only the constrained semantic writer and readiness function. Web authorization restricts access further to administrators. This is not database row-level security for a leaked runtime credential.

Trigger writer functions use `SECURITY DEFINER`, a fixed `pg_catalog` search path, qualified object names and owner-controlled policy. The installer requires the separate `household_owner` role. That owner can change schema/policy and, as with the pre-existing append-only rule, can modify history. Database owners, superusers and backup administrators remain trusted operators; the log is not tamper-proof against them.

There is no newly introduced automatic audit deletion policy. Module purge retains audit history, including the filtered deletion records, even after current module data is gone. Database backups contain audit history; restoring a backup restores that history. Operators must account for retained values and backup retention when deciding a household privacy/retention policy. Removing a current record or attachment is not removal of all historical audit values or backup copies.

## Migration and existing history

The migration runner holds its existing advisory lock, migrates core first and then the independent module schemas. Core migration 5 creates the event columns, policy and functions. Only after all module tables exist does `core.install_audit_triggers()` install both triggers on every registered table. Repeating migration also reinstalls missing/disabled registrations.

Readiness checks expected clean schema versions, then `core.audit_ready()`. It requires the current 45 policies, enabled triggers using the expected functions, and no unclassified persistent table in the ten compiled schemas. The explicit exclusions listed above are recognized by name. A future table therefore requires an audit policy or a reviewed exclusion before readiness succeeds.

Migration preserves prior semantic rows. They receive `actor_kind=legacy`, empty `changes`/keys/labels and no invented before/after history. Existing `details` are not rewritten or retroactively sanitized. Earlier operator-password-recovery events used the affected account in the actor column and omitted household/module/entity: the viewer projects only that known legacy shape into the singleton household, marks its origin as legacy, treats the old user ID as the target resource, and clears the displayed actor. Original rows remain unchanged. Other history without a household remains available to the database operator rather than entering a household-scoped web result.

## Administrator view and API

Administrators open `/settings/audit`; `/api/v1/core/audit` returns the same household-scoped data as JSON. Reader/editor roles are refused. Filters are `module`, `action`, `resource` UUID, `request` UUID and a positive integer `before` cursor. For example:

```text
/api/v1/core/audit?module=shopping&action=UPDATE
```

Results are ordered by descending event ID. Queries fetch 51 rows, return at most 50, and expose `next_before` when another page exists. Continue with that value as `before`; this avoids OFFSET drift as new events arrive. This order follows allocated IDs, not a promise of global transaction commit order. The viewer labels timestamps in UTC and distinguishes missing, NULL and protected values. Values are escaped through Go templates; no document contents or credentials are fetched to reconstruct labels. Normal private-response/no-store headers apply.

## Extending the contract

1. Add the domain/core table through its normal migration; keep its ownership boundary intact.
2. Add or update the owner-managed policy in a new migration: real primary-key columns, a narrow safe-value list and optional safe label. Decide explicitly whether generated timestamps/counters should be ignored.
3. Review the common permitted fields and every new safe scalar for privacy and length. Unlisted changed fields remain masked; do not allow arbitrary settings/payload JSON.
4. Update the expected policy count in readiness when adding a registration, or document and add a justified internal-table exclusion.
5. Route every new mutation through the existing audited transaction boundary, supplying authenticated actor/request context before the write. Set a system/operator source for independent work. Do not let clients submit actor identity.
6. Add a semantic action to the database allowlist only if it explains a meaningful operation; keep sensitive input out of `details`.
7. Test fresh/repeated migration, populated upgrade, triggers, exact values, presence/redaction, failed transactions, replay, cascades/bulk actions and actor isolation. Add viewer/permission tests for any new surface.
8. Update the migration checksum manifest, this coverage list, README and implementation ledger with executed evidence.

## Executed focused evidence

On 2026-10-24, `TestAuditTrailContract` passed nine subtests covering all 26 domain registrations, unknown-table readiness rejection, create/update/delete, no-op suppression, NULL presence, rollback/replay, exact values, private-note masking, composite keys, cascading deletes, runtime privilege denials, pooled concurrent actor separation and operator TRUNCATE history. `TestAuditPopulatedUpgrade` built the actual prior core-4/module schemas using the migration runner, populated domain data and legacy history, upgraded twice, and verified preservation, new auditing and revoked direct-insert privileges. That focused run passed in 15.944 seconds.

The earlier combined audit/capture-privacy run passed in 76.846 seconds. Tagged lint for store/worker/maintenance/web completed with zero findings. `TestAuditWorkerFailures` also exists for terminal worker attribution; its database execution belongs to the subsequent full-suite evidence. These focused observations do not certify later edits or replace the current [implementation ledger](implementation.md) and final full-suite results.
