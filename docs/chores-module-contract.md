# Chores workspace contract

Status: locally accepted after complete implementation, joint executed validation and local code checkpoint `74e049f`. AllCH01–CH20criteria are verified; source-bound evidence and explicit limits are in [the validation ledger](chores-module-validation.md). All92predecessor SQL files remain byte-identical.

## Application boundary

The standard-library HTTP workspace is `/m/chores/workspace/{pane}`: `today`, `areas`, `series`, `plan`, `history`, `settings`. Focused work uses `/work/{occurrence}`; a series uses `/series/{template}`, with `new` for creation. Assets are embedded and local. Private pages, payloads, capability URLs and mutations are never stored in localStorage, IndexedDB, service-worker private caches or an offline command queue. A transport failure retains the exact command and idempotency key in page memory for explicit retry.

Every business mutation requires a current authenticated editor or administrator, matching household, current session/auth version, enabled module, expected data generation and expected resource version. Readers can inspect the household and mutate only their own preferences/calendar capabilities. Personal authorization is not business write authority. HMAC-bound CSRF applies to all authenticated POST routes. Capability GET routes require no session and expose only their bounded calendar projection.

The store uses parameterized PostgreSQL queries. Read snapshots are repeatable-read/read-only with an eight-second operation budget; ordinary Chores writes use twenty seconds. Lock order is household lifecycle, current authority, module generation, household Chores advisory lock, then resources. The existing global deadlines are unchanged. Actual completion, mandatory checklist, prerequisite evidence, successor, audit and projections commit together.

## Closed requests and reads

Commands contain explicit `generation` and `version`. JSON is bounded to 64 KiB and rejects unknown, duplicate, case-ambiguous, null nonnullable and trailing fields. Writes require a UUID `Idempotency-Key`. The server checks current authority before returning any receipt; an old key does not restore revoked access. The Core response receipt contains at most 128 KiB. Unique occurrence identities and completion facts remain after receipt retention expires.

| Endpoint below `/api/v1/chores` | Contract |
|---|---|
| GET `/tasks` | Scope `mine`, `household` or `unassigned`; dates, area, template, state, literal title search and stable due/id cursor. Default includes overdue work through the household's current civil day. Paused future and archived tasks require explicit filters. |
| GET `/areas`, `/series`, `/absences` | Bounded management pages, search and explicit archive/history inclusion. Area ordering uses configured position/id; series and absence identities use UUID pages. |
| GET `/series/{id}` | Current profile, immutable version, ordered checklist/roster and prerequisites. Old unresolved rule facts remain marked for review. |
| POST `/series/preview` | Normalized rule, real UTC/local projections, effective boundary, cancellation count and explicitly retained open occurrence. No side effects. |
| POST `/series` | Complete normalized decision; existing changes carry both profile `version` and `base_version`. New immutable version replaces future planning, preserving captured history. |
| POST `/series/{id}/control` | `pause`, `resume`, `archive`, `activate`; optional `pause_until` on pause. Distinct from single-occurrence decisions. |
| GET `/occurrences/{id}` | Frozen instructions/material/checklist, prerequisites, actor facts, original deadline, current deadline, last actual completion, already planned next occurrence, and closed Core-origin labels. |
| POST `/occurrences/{id}/preview` | Side-effect-free preview for `snooze`, `edit_once`, `skip`. Original/current/new deadlines and non-completion meaning are explicit. |
| POST `/occurrences/{id}` | `claim`, `release`, `check`, `complete`, `snooze`, `edit_once`, `skip`. Unsupported extra fields are rejected. A claim lasts four hours and is not a completion. |
| POST `/areas[/{id}]` | Create/edit/reorder/archive without deleting historical area names. |
| POST `/absences[/{id}]` | Explicit member, inclusive dates, optional suitable substitute; overlap rejected. Removal requires the existing id/version and `delete`. |
| GET/POST `/preferences` | Only the current user's budget, difficulty, notification/quiet/escalation choices and timezone. Version zero creates; subsequent writes require current version. |
| GET `/plan` | Deterministic priority/due/budget proposal, selected minutes, remaining minutes, unknown estimates and rejection explanations. No assignment or write. |
| GET `/history` | At most 366 civil dates, actual person/area filters, descending completion time/id cursor, known actual minutes and separately counted unknown facts. |
| GET `/calendar.ics` | Authenticated bounded calendar download. |
| GET/POST `/calendar-feeds` | Own metadata list and explicit capability creation. |
| POST `/calendar-feeds/{id}/revoke` | Own current-version revocation. No capability resurrection. |

List pages contain at most 100 rows. Task/history/area cursors are bound to the household, session, generation and selected filter. No cursor grants resource authority. Other UUID pages still apply the current household and full authorization on every request. Task/detail readers reject an oversized result instead of claiming a partial total. A day proposal supports at most 500 candidate tasks and asks for a narrower operational scope on overflow.

## Scheduling, history and rule changes

Rules are closed forms: once, 1–366 calendar-day intervals, selected ISO weekdays with week intervals, and original monthly day with month intervals. Season months apply to scheduled rules. Completion rules are day intervals without season filters. A month's shorter length clamps its occurrence, preserving the original day for later months.

IANA timezone, local date/time and the versioned policy `civil-v1/forward-gap/early-fold` are persisted. A fold chooses the earlier actual instant; a gap forwards by its wall-clock gap, including a whole-day gap. Calendar-day calculation never uses a fixed 24-hour increment. Original UTC deadlines survive upgrades and snoozes. Legacy PostgreSQL behavior remains labeled `legacy-postgresql` until explicitly reviewed.

Scheduled rules use a rolling horizon of today plus 90 future civil days. Generation processes at most 64 tasks/512 task-and-checklist cells per batch. Equal-UTC gap collisions advance together; a cursor never skips the second logical occurrence. Empty seasonal windows still advance progress. Backlog is explicit. A completion rule has at most one managed open successor, using the actual completion date; a skip is separately identified and advances from the skipped scheduled date without creating a completion fact. A pause ends at its explicitly chosen automatic boundary or actual manual resume. Missed paused occurrences are marked skipped; a completion interval restarts from that pause boundary, clearly as a skip/resume anchor rather than an actual completion.

At a switch to completion scheduling, at most the earliest existing open occurrence remains unchanged; other future open occurrences are cancelled. Multiple already due occurrences must be explicitly resolved first. Captured instructions, source version, original deadline, checklist and terminal completions cannot be rewritten. An actual completion stores its real actor, nullable actual minutes (zero remains known zero), captured checklist and actual prerequisite completion identities.

Rotation uses ordered active suitable editors, inclusive absence dates and explicitly selected eligible substitutes. No suitable candidate leaves the task unassigned with an explanation. New absence/preferences never silently reassign accepted existing work. An inactive assignee can be explicitly replaced or an eligible editor can claim the unassigned work.

Initial bounds: 500 active series, 200 active areas, 50 checklist items/version, 32 eligible-person entries/version, 16 prerequisites/version and a finite dependency graph. Existing oversized legacy state is retained and requires narrowing/review. A cycle is rejected under the same serialized household lock, including concurrent attempts.

## Notifications and calendar capabilities

The existing durable worker owns the `chores_workspace` job kind. Scheduling is bounded. A worker verifies the real lease, module state and generation before locking work and again against the actual wall clock before commit. Lease loss rolls back all side effects. Source `created_by` remains provenance; worker audit/outbox actor is system/NULL, never a fabricated user action.

Absent preferences preserve existing individual reminders. Explicit modes are `off`, `legacy`, `digest`; newly managed tasks use digests rather than individual legacy reminders. Quiet intervals can cross midnight. A digest scheduled inside quiet hours moves to the interval's end, using the same civil DST policy. Deliveries deduplicate by household/generation/recipient/local-day/stage/source. There is no required external delivery. Escalation requires source opt-in and current explicit recipient acceptance, current user/membership, recipient quiet-time and mode checks. Notifications expose counts, not private instructions.

An ICS event exposes title, UTC start/end, stable household/generation/occurrence UID, monotonic sequence and cancellation/completion metadata. It omits instructions, materials and member names. Windows include 0–31 days back and 1–90 days ahead, with a 2,000-event ceiling. Snoozes retain the same UID, including events whose original date remains in the window. Paused future, skipped and archived occurrences are cancelled projections. Physical deletion belongs to the separate maintenance lifecycle.

Feeds have 256-bit random bearer capabilities; only SHA-256 digests are persisted. Session/auth version, exact membership/role, module generation, expiry and revocation are checked at every fetch. A maximum of 20 active feeds/person expires after 90 days. A URL is shown once and is absent from stored response receipts. Losing the first response requires explicit revoke/recreate; receipts cannot recover the secret. Request logging records the literal route pattern rather than the capability segment. Responses use `private, no-store` and `no-referrer`.

## Migration, maintenance and evidence

Forward stages are Chores 2, Core 32, Fuel 9, Pantry 15 and Mealplan 10. Chores 1 remains the early Core-26 migration barrier. Chores 2 requires the genuinely ready Core-31 predecessor and captures its precise function/owner/ACL, audit policy/trigger and table metadata. Core 32 admits exactly fourteen new Chores tables and audit policies, preserving all predecessor metadata. It changes only the reviewed Finance audit-policy count/catalog entry; Fuel/Pantry/Mealplan migrations rebind only their exact accepted dependencies. The temporary unclassified-table interval is proven against that captured predecessor instead of requiring an impossible ready audit midway through DDL. Final readiness requires all 135 policies and every module's closed catalog. Application readiness still validates every compiled schema version first, then runs the same independent read-only contract inspectors in at most three concurrent lanes. All results must pass; caller cancellation cancels pending work and releases acquired connections. No inspector, drift check or two-second HTTP deadline is removed or relaxed.

Disable preserves data and stops new actions, jobs and capability reads. Owner-only maintenance inventories all sixteen schema-local business tables and clears Core jobs, notifications, receipts, links and hashes under the existing verified-backup requirement. Purge advances generation, making old commands and capabilities unusable after re-enable. No optional module owns a foreign key into another optional module.

Test fixtures are isolated and synthetic. Controlled native backup rows test maintenance authorization only; they are not evidence of a real backup. Release acceptance separately requires actual encrypted backup/restore/purge, whole regression, security and image evidence. Source formatting, registration and checksum generation are never reported as executed functional checks. Live port 8100, sibling projects and publication remain outside this change.
