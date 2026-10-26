# Atomic expense editing and financial visibility

Next preparation: EX02 and EX19. The running MP11/MP12 candidate remains frozen. Only pure normalization exists in ignored `.local/finance-work`; no financial schema, HTTP permission, editor or operational acceptance is added to the application yet. EX01/EX03/EX06/EX09/EX10 remain separate later acceptance items.

## Existing truth

Expenses currently comprise shared household categories, draft/posted entries and category lines. An expense is positive, a refund negative, and a full reversal is a separate linked opposite entry. Posted facts are immutable and deferred guards require exact category totals. Integer cents are bounded per entry/line to absolute1e12. The existing household-wide authorization is intentional; restricted financial visibility is not an available capability.

The existing generic money boundary interprets JSON integer values as cents and strings as EUR. The new editor uses an explicit EUR-string input and normalized integer cents; it must never feed normalized centstrings back through the EUR parser. Saved unknown payer and historical missing accounts/person splits remain unknown. No account balance, ownership, exchange rate or allocation is invented during upgrade.

## First increment

Edit header plus at most100ordered category lines in one versioned transaction. A draft may retain a visible signed rest difference. Posting requires an explicitly reviewed, nonempty allocation whose exact sum equals the entry total. Signed discount lines remain possible; refund direction is explicit. Posting/reversal/correction are separate actions and never silently rewrite a posted entry. Broader EX03 acceptance requires reliable linked correction/refund evidence; the currently unused replaces_entry_id is insufficient.

The pure preparation checks canonical UUIDs/versions, duplicate row identities, explicit positive expense/negative refund amounts, exact cents, field bounds, caller ownership,100-row limits and exact aggregate/difference strings. It also normalizes structurally bounded visibility grants. These helpers do not establish database existence, activity, household identity, ownership or authorization.

## Visibility contract before private data

Legacy records stay explicitly household-shared. Do not infer a private owner from paid_by or created_by. New records can have an explicit responsible membership. Changing a legacy shared record to selected access first requires an authorized, audited assignment of responsibility. Selected grants distinguish view/edit, retain responsible edit access and allow at most100members including responsibility. Global household role still limits writing. Current session, active same-household memberships, grant-management authority and at least one active permitted editor must be validated under locks; pure UUID normalization is not that check.

Use a Core-owned, generation-bound visibility contract rather than foreign keys between optional schemas. A policy identifies household/module/generation/resource, household versus selected mode, explicit responsible membership and monotonic access revision. Grants reference Core memberships. Subject bindings let entry lines inherit entry policies; future account policies intersect with record policies. Missing/ambiguous required policy means denied access. Categories remain an explicitly shared catalogue in this increment. Server/database operators retain their infrastructure authority; the contract controls application access.

Apply the same predicate before LIMIT, COUNT, sorting and aggregation in lists/details, generic writes/deletes/actions, search, references/related links, reports/CSV, attachments/uploads/downloads/previews/OCR/capture, import preview/confirmation, audit and idempotent replay. Current resource/grant access must be checked before returning a stored response. Historical sensitive audit/proof payload must not bypass current visibility or become public when its resource/policy is missing. A separate access-change security event can retain administrative metadata without exposing financial values. No aggregate or UI may advertise private data before all these paths are covered.

Integrations keep explicit source and destination rights. Restricting a copied expense does not retroactively conceal facts that were already shared in a fuel entry or source document. The review must make that distinction understandable; restricted source details must not leak through a related target, title, attachment or replay.

## Concurrency, persistence and legacy boundaries

The current child guard locks the parent but does not increment its version. Introduce a parent content revision covering every category-line insert/update/delete, or compare complete child identities/versions in addition to parent version. The bundle must prove a complete read set, including deliberate removals, before replacement. A truncated or stale client cannot silently drop unseen lines. Preserve existing posted/oversized legacy records and offer a bounded read/recovery state rather than truncating them.

Lock order is household/module → current access policy/context → entry → sorted bounded children → categories/memberships. Incumbent generic child writes acquire a child before its parent; use bounded NOWAIT contention and compatible guards rather than allowing opposite lock orders to wait indefinitely. New selections require active resources; retained inactive payer/category references remain visibly distinguishable and receive an explicit posting decision.

Save/post changes, category lines, parent revision, attribution, search/audit projections and durable operation evidence commit together. Add a permanent minimal request-key/hash/actor/generation/operation proof with no private form payload. Generic30-day idempotency alone is insufficient for repeat-safe financial creation/posting. Replays first check current authorization; a later saved version, deleted resource or lost access is not an old private response to disclose. Purge and retained minimal evidence follow the existing explicit lifecycle contract.

Use only new forward migrations. Core contract creation precedes Expenses backfill/guards; initial empty installation and populated Expenses2 upgrade must both work. Existing immutable migration hashes stay unchanged. Runtime roles receive only exact necessary grants; fixed search paths, exact trigger/type/constraint checks and audit-policy/readiness census accompany schema changes. Fuel-to-expense, capture/import and all generic child paths must participate in the new invariants before availability.

## Implementation sequence

1. Complete MP11/MP12 native/race acceptance without changing its application source.
2. Finalize pure EUR/header/line and structural visibility normalization; test exact signed differences, limits and no partial usable result after invalid input.
3. Specify the full financial access-path inventory and explicit responsible-member administration/recovery behavior.
4. Add generation-bound Core policy/grant/subject contracts with no optional-schema FK.
5. Add forward Expenses migration for complete parent/child revision integrity and bounded new edits without rewriting posted legacy facts.
6. Backfill shared legacy access explicitly; test initial and populated upgrades, repeat, readiness tampering and grants.
7. Implement central current access checks for reads/writes and policy-management operations.
8. Apply visibility to every generic/search/reference/related/export/report/file/capture/import/audit/replay path before offering selected access.
9. Implement a bounded current-authorized RR bundle reader with complete child/version evidence.
10. Implement normalized, parent-first atomic save/post with current access revision and explicit removed-line checks.
11. Preserve compatible generic writes, fuel/capture/import creation and actual purge under the same guards.
12. Add permanent minimal operation proofs and current-authorized replay after generic response pruning.
13. Test stale child edits, post-versus-edit, opposite locks, simultaneous versions and active membership changes.
14. Force late deferred/projection failures; compare raw entry/children/revision/audit/search/proof state for complete rollback and key reuse.
15. Exercise two actual users across every visibility path, including revoked access before replay and hidden aggregates/attachments/audit.
16. Build the local mobile editor: labelled EUR inputs, category rows, exact remaining amount, draft/posted explanation and explicit save/post review.
17. Test incomplete/negative/mixed allocations, long labels, inactive selections, conflicts and unknown-outcome same-body/key recovery in the browser.
18. Execute independent interface/documentation review, security/workflow checks, current images/recovery, complete native and meaningful scoped Linux race.
19. Update README/checklist only from executed evidence; then build EX01 summaries and reliable EX03 correction/refund workflows on this foundation.

## Preparation evidence

Nine pure tests pass in the ignored preparation copy(package expenseedit0.843s,95.2% observed package statement coverage). They cover incomplete draft versus explicit post, signed discount/refund behavior, canonical EUR units, no caller mutation, aggregate ranges,100-row bounds, duplicate/stale identities, explicit shared legacy responsibility and bounded canonical selected grants. This is not financial feature, SQL, privacy, browser or runtime acceptance. The source-pattern reviewer only inspected existing code and reported the parent-version and access-path constraints; that reviewer ran no tests.
