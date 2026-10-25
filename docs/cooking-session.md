# Cooking session

Implementation contract for RE02, RE03, RE04 and RE10 under the approved 180 improvements. Source implementation is present; final operational acceptance is tracked separately in improvements-180.md and implementation.md. Preserve the Household design; surface mode: Operate.

## User contract

Open a reviewed recipe version in cooking mode. The session captures that immutable source, selected servings and ordered instructions. Changes to the recipe during cooking must not silently replace the session. Show one current step, readable instructions, progress and a keyboard/touch ingredient checklist; previous/next steps remain explicit. Optional screen Wake Lock is requested only through a user control and gracefully reports denial, unsupported browsers and release on visibility changes.

Changing session servings scales known ingredient amounts using exact rational arithmetic, without editing the recipe. Unknown quantities remain unknown and named hints remain visible. Rounding, when a fraction cannot fit the display precision, is marked as an approximation; the exact source and selected servings remain stored. No unverified density conversion belongs in this slice.

Several named kitchen timers may run concurrently. Store their duration, state and absolute deadline in the authenticated user's household-scoped session, rather than relying on JavaScript intervals. Navigation/reload restores them, including an elapsed timer while the page was closed. Pause/resume and adjustments use optimistic versions and explicit confirmation. Do not promise an alarm while a browser is suspended or closed; in-page alerts work when the app can run, and optional push delivery remains a separate feature.

Finishing records actual servings, the household-local preparation date, an optional rating and notes. The result is independent of recipe editing and visible as cooking history. Finishing must be idempotent and must not automatically consume pantry stock; reviewed stock consumption remains RE12. An abandoned session can be explicitly closed without inventing a completed cooking event.

## Persistence and security

Keep sessions, ingredient progress, timer state and immutable completed cooking records inside the recipes schema. Bind every write to the household, authenticated user, module generation and current session version. Readers may view recipe history but may not create or mutate sessions. Decide whether sessions are private or explicitly shared; private per-user working sessions are the default. History is household-readable after the user explicitly finishes.

Use bounded open-session and timer counts, stable history pagination, audit policies that redact notes, exact quantities and reliable deletion/purge semantics. Retain selected source-version provenance even if the current recipe is later deleted. Module disable/purge must stop further actions and prevent replay from an old session generation. Avoid mandatory external services or foreground-only assumptions in persisted timing logic.

Boundaries: at most 10 open sessions per user and 20 timers per session, durations from one second to 24 hours. PostgreSQL time is authoritative; API responses include server time so the browser can display countdowns without assuming the device clock is correct. A timer deadline is UTC and independent of daylight-saving transitions. Session mutations are short, versioned transactions; expired timers are computed from their deadlines rather than a mandatory background job. Once a cooking result is committed, further completion attempts return that result and cannot duplicate history.

Recipes without preparation steps remain valid authoring records. Starting the step-focused mode should clearly direct the user to add a step, while preserving the recipe. Optional ratings have an explicit unknown state. The cooking date uses the household timezone; quantities and ratings cannot be inferred from an abandoned session. A new session reads a selected immutable version, while later history access remains possible through that version even after deletion of the live original.

## Required acceptance

Unit tests: exact portion scaling including fractional output, timer transitions and deadline calculations with an injected clock. PostgreSQL: user/generation isolation, concurrent actions, repeated finish, immutable history, source edits/deletion and rollback. Browser: real cooking flow, keyboard progress, multiple timers across navigation/reload, elapsed/pause/resume states, denied Wake Lock, review before finish, mobile/dark layout and accessibility. Recovery: retain source revision, active timer deadlines and completed history without duplicate completion.

## Implementation decisions from the database review

Private sessions and timers use dedicated methods, never generic module entities, reference search or search projections. Every session read includes household and user predicates; completed records are household-readable. BeginMutation's household/session/module barriers precede request deduplication and the session row lock. Creating sessions additionally serializes the per-user count; timer counts are bounded under the session lock. Every timer or progress mutation compares and advances the session version, so concurrent tabs cannot silently overwrite each other.

Use a same-module composite source-version FK, without a live-recipe FK. Only the initial start locks and verifies the current live parent. Progress IDs and step positions are checked against the immutable snapshot. Source, owner and session identity never change. Terminal sessions reject further timer/progress operations. Finishing or abandoning terminates remaining timers in the same transaction.

Read PostgreSQL clock_timestamp after acquiring locks. Paused timers preserve milliseconds; elapsed timers cannot be resumed as positive time. The maximum of twenty applies to stored timer rows, including elapsed ones, until explicitly removed. Exact portion scaling uses big.Rat; outputs may exceed input decimal bounds, and a rounded tiny positive amount must be displayed as less than the precision threshold instead of an apparent zero.

Completion has a permanent unique session key. After authorization and generation validation, matching replay returns the original result even when the caller carries an older session version; different completion details conflict. Restrictive user FKs match the existing deactivate-user lifecycle. Core12 adds three redacted policies (54 total; 35 domain tables); recipes3 contains the three tables. Module purge discovers all schema tables together. Tests must cover archived/deleted source reads, concurrent count limits and replay after request-receipt deletion as well as restore.
