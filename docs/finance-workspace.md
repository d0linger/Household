# Atomic expense editing and financial visibility

EX02 transaction integrity comes first, followed by the complete EX19 selected-access contract. The verified Expenses3/reader foundation is saved as b196d22 after PA15. The atomic shared writer and permanent minimal Core24 operation proof are now materialized from separately tested preparation. Root compatibility passes 36 main contracts (Web284.843s) plus two existing populated census upgrades (Web22.471s); the 350-file source manifest remains unchanged. This does not yet expose a new HTTP editor or selected financial access. EX01/EX03/EX06/EX09/EX10 remain separate later acceptance items.

The first EX02 increment retains the existing explicit household-wide sharing. It does not need a fictitious access-policy revision or new Core visibility tables. Remove AccessVersion from the shared editor DTO; introduce real policy revisions only when the later Core contract exists. The complete selected-access inventory below remains mandatory before any restricted mode is offered.

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

### Additional access-path inventory

The source-only follow-up inventory also covers indirect metadata, background work and historical copies. These are prerequisites for selected financial access; they are not findings against today's explicitly household-shared application. Bootstrap currently exposes identity/module states, not financial totals; keep future badges and aggregates filtered before counting. The general attachment quota error must not reveal private file counts or sizes.

Before financial reminders are offered, replace target-path authority with a typed generation-bound resource/policy reference. Check permitted recipients both at delivery and on every notification read, including titles, links and counts. Revocation must hide delivered notices. Source deletion must not leave an identifying dead link. Minimal outbox/job payloads stay internal; any future status/error/activity UI must check source access before exposing IDs or counts.

OCR and thumbnail results inherit current attachment access. Check source liveness and attachment binding at result commit, and current source/result access before confirmed capture replay. Moving an attachment to a created expense requires conservative policy inheritance and explicit approval of any widened audience. Do not include OCR text or private filenames in diagnostics.

Resource links do not transfer permissions. Check the concrete destination before returning its ID, label, filename or preview. Existing shopping checkout snapshots and fuel sources are independent shared copies; making a copied expense selected does not retroactively hide those facts. An explicit product decision is needed to change those sources.

Core audit rows can expose financial attachment sizes/source IDs, capture result IDs, links and import counts. Filtering only the Expenses module is insufficient. Bind sensitive Core history to an explicit policy before pagination, and retain a denied historical policy identity through source deletion/purge. Missing policy must never mean household-shared. Separate administrative security metadata from financial content.

Purge removes detailed response payloads while retaining only the minimum deduplication identity. Permanent proofs, result counts and historical snapshots still require current visibility before display. Operator backups intentionally contain all data and remain outside application grants.

The smallest safe first increment excludes the new financial editor and financial generic mutations from the existing opt-in IndexedDB queue. That queue stores complete form bodies and displays names/amounts/counts. A later private draft contract needs explicit local storage consent and renewed authorization before display/transmission. Server revocation cannot reliably remove data already downloaded to a device. The existing service worker itself caches only assets and the offline shell.

Required indirect cases include revocation after notification delivery, during OCR and after confirmed capture; a visible source linked to a selected expense; revocation between mutation and replay; deletion/purge with retained audit; and policy/account changes with a local draft present. Reuse the existing capture privacy sentinel/lifecycle test patterns.

The prior Expenses2 child guard locked the parent without incrementing its version. Expenses3 now increments its parent content revision after every App category-line insert/update/delete; complete child identities/versions are checked in addition to the parent version. The bundle proves a complete read set, including deliberate removals, before replacement. A truncated or stale client cannot silently drop unseen lines. Preserve existing posted/oversized legacy records and offer a bounded read/recovery state rather than truncating them.

Lock order is household/module → current access policy/context → entry → sorted bounded children → categories/memberships. Incumbent generic child writes acquire a child before its parent; use bounded NOWAIT contention and compatible guards rather than allowing opposite lock orders to wait indefinitely. New selections require active resources; retained inactive payer/category references remain visibly distinguishable and receive an explicit posting decision.

Save/post changes, category lines, parent revision, attribution, search/audit projections and durable operation evidence commit together. Add a permanent minimal request-key/hash/actor/generation/operation proof with no private form payload. Generic30-day idempotency alone is insufficient for repeat-safe financial creation/posting. Replays first check current authorization; a later saved version, deleted resource or lost access is not an old private response to disclose. Purge and retained minimal evidence follow the existing explicit lifecycle contract.

Use only new forward migrations. Core contract creation precedes Expenses backfill/guards; initial empty installation and populated Expenses2 upgrade must both work. Existing immutable migration hashes stay unchanged. Runtime roles receive only exact necessary grants; fixed search paths, exact trigger/type/constraint checks and audit-policy/readiness census accompany schema changes. Fuel-to-expense, capture/import and all generic child paths must participate in the new invariants before availability.

## Implementation sequence

### EX02 delivery checklist

- [x] Exact shared EUR/cent normalization and complete child-set reconciliation.
- [x] Expenses3 migration barrier, parent revision, posted guards and NOWAIT compatibility.
- [x] Exact readiness including individual privileges, trigger restrictions and ordered index direction.
- [x] Current-authorized bounded RR bundle reader, legacy bounds and inactive references.
- [x] Ten new root PostgreSQL contracts and six existing compatibility contracts actually pass.
- [x] Atomic shared writer and permanent minimal proof, including replay, late rollback and real purge contracts.
- [ ] Current-authorized private HTTP endpoints and mobile editor; explicit allocation rest and posting review.
- [ ] Financial offline-queue exclusion, browser/axe/keyboard and independent interface review.
- [ ] Fresh final native/scoped-race/security/image/recovery acceptance before EX02 is verified.

The word private for HTTP denotes authenticated, non-cacheable transport; selected financial visibility remains a later EX19 feature.

### Ordered implementation work

1. Preserve the accepted MP11/MP12 and PA15 candidate while preparing the financial increment separately.
2. Finalize pure EUR/header/line normalization and complete child-set reconciliation; test signed differences, bounds and no usable partial result after error.
3. Add forward Expenses3 integrity with the household migration barrier, Invoker guards, parent version increments after every App child write and fail-fast opposite lock order in both Go and SQL.
4. Add exact local readiness, including conditional/column-restricted trigger rejection, each individual privilege and the composite parent FK; preserve74audit policies/48domain tables.
5. Actually test populated Expenses2 upgrade, legacy101-line preservation, old-writer drain, posted guards, draft cascade and actor-attributed parent/child audit before materializing only named files.
6. Implement a bounded current-authorized RR bundle reader with complete child/version evidence and a named oversized legacy recovery state.
7. Implement normalized parent-first atomic shared save/post with complete baseline and explicit removed-line checks.
8. Preserve compatible generic writes, signed discounts, fuel/capture/import creation and actual purge under the same guards.
9. Add permanent minimal operation proofs and current-authorized replay after generic response pruning; never return obsolete form payload as current data.
10. Test stale child edits, post-versus-edit, opposite locks, simultaneous versions and active membership changes.
11. Force late deferred/projection failures; compare raw entry/children/revision/audit/search/proof state for complete rollback and key reuse.
12. Build the shared local mobile editor with labelled EUR inputs, category rows, exact remaining amount and explicit draft/save/post review; exclude financial writes from generic device queue.
13. Test incomplete/negative/mixed allocations, long labels, inactive selections, conflicts and unknown-outcome same-body/key recovery in the browser.
14. Execute independent interface/documentation review, security/workflow checks, current images/recovery, complete native and meaningful scoped race; accept EX02 only from actual evidence.
15. Finalize responsible-member administration/recovery behavior and the full financial access-path inventory.
16. Add generation-bound Core policy/grant/subject contracts with no optional-schema FK; backfill legacy shared access explicitly.
17. Implement current access checks and real access revisions for shared editor/read/replay/policy-management operations.
18. Apply visibility before pagination/count/aggregation across every generic/search/reference/related/export/report/file/capture/import/audit/replay path, including indirect history and notifications.
19. Exercise two actual users across all visibility paths, including revoked access before replay, delivered notifications, running OCR, hidden aggregates/files and retained audit after purge.
20. Verify upgrade/recovery, current images, complete regression, scoped race, browser/security and independent review before exposing selected access or accepting EX19.
21. Then build EX01 summaries and reliable EX03 correction/refund workflows; accounts and per-person balances remain separate acceptance items.
## Preparation evidence

Nine pure tests pass in the ignored preparation copy(package expenseedit0.843s,95.2% observed package statement coverage). They cover incomplete draft versus explicit post, signed discount/refund behavior, canonical EUR units, no caller mutation, aggregate ranges,100-row bounds, duplicate/stale identities, explicit shared legacy responsibility and bounded canonical selected grants. This is not financial feature, SQL, privacy, browser or runtime acceptance. The source-pattern reviewer only inspected existing code and reported the parent-version and access-path constraints; that reviewer ran no tests.

The subsequent shared preparation has14pure contracts passing(package0.901s,95.5% observed package coverage), plus compile-only integration and affected tagged lint0issues. Complete baseline/current identities and versions detect unseen/removed/edited children independently of parent-version history; removals are explicit, ordered and bounded. Initial preparation compile failed for a missing test-only fixture; the first lint found a capitalized sentinel error. Both are corrected in the successful repeat.

The final preparation campaign actually passes ten PostgreSQL contracts in63.878s: five integrity/upgrade contracts and five reader contracts. This includes populated Expenses2 and101-line legacy preservation, old-writer drain at the household barrier, direct App child version propagation and actor audit, posted immutability, signed discounts, draft cascade, NOWAIT contention, current authorization, household calendar, complete row/text bounds and one consistent RR snapshot. Readiness rejects each missing CRUD privilege, disabled/conditional/column-restricted trigger, wrong index key order and mixed index direction. The new Btree follows(household_id,entry_id,position,id); readiness checks its exact ascending definition, valid/ready/live status and absence of predicates/expressions.

An earlier five-reader campaign and its diagnostic repeat failed because the non-superuser owner cannot inspect another role's pg_stat_activity query details. The controlled snapshot observer now uses the App role; its narrow repeat and the final ten-contract campaign pass without weakening the consistency assertion or database privileges. Those failed logs remain evidence. Source-only independent review closes the index/readiness and shared text-limit findings; it performed no database tests.

Only13named files were copied after hash-checking the two existing root targets against their preparation baseline. The manifest adds two new hashes while preserving all61accepted hashes. Root now expects Expenses3/63migration files, with74audit policies/48optional-domain tables unchanged. Root compatibility passes six existing application/audit/migration/capture contracts(Web91.379s) and all ten new financial contracts(Web61.927s). Pure root tests pass0.747s/95.5%; standard verification passes17.2%(1946/11299),78Node cases,63migration hashes, module verification and workflow policy; affected tagged lint0issues. The old PA15 full-suite/race/browser/image evidence does not certify these new financial changes. Latest pure preparation passes14contracts in0.732s with95.5% package coverage; affected tagged lint reports0issues. EX02/EX19 remain in progress, checklist58verified/2inprogress/120open.

Foundation Gosec passes with explicit cmd/internal scope:133files/20623lines/0findings. The earlier unrestricted traversal included the ignored tool cache and was stopped; it is not counted as a passing scan. No new dependency is required.

## Atomic writer increment — focused backend regression passed

Ten prepared writer contracts pass in 56.755s; a separate populated Core23 upgrade passes in 12.454s. Canonical read-set ownership has a pure test; affected tagged preparation lint reports zero issues. Three initial preparation faults were corrected: a compile-time fixture call signature, SQL CASE parentheses, and an unsupported semantic audit action. The writer now uses the existing record_write/post event vocabulary. The first upgrade assertion attempted the owner-only audit-policy census through App and failed; the corrected owner observer passes without granting App more privileges. Failed logs are retained.

SaveExpense locks current household/session/Expenses scope before reading a durable key. It normalizes explicit EUR, canonicalizes copies of baseline/removals and UUID keys, retains final line ordering, and compares a complete current child set. Parent-first locking selects at most 101 candidates in index order, rejects oversized legacy sets, then locks only the accepted IDs in UUID order with NOWAIT. Retained inactive references may remain in a draft; new inactive selections and posting with inactive references require correction. Header, lines, targeted deleted-line cleanup, search/outbox, actor audit and permanent proof commit together. Deferred proof evidence rejects a proof without the same transaction's matching parent version/state/actor effect.

The minimal Core proof stores no form, amount, label or rich response. Its actor/key uniqueness spans action and generation. Replays authorize current scope before returning acceptance identity plus separately labelled current existence/version/state; they never return an old form. They work after later saves, source deletion and generic response pruning. Real isolated Expenses purge preserves the proof, changes generation and prevents recreation with the old key. A forced deferred failure leaves entry, lines, versions, search, outbox, audit and proof unchanged, and permits key reuse.

Only ten named files were copied into root after checking existing targets against the saved foundation. Core24 adds one forward migration: 64 total hashes, all 63 prior hashes preserved, 75 audit policies and still 48 optional-domain tables. Three existing census assertions now expect the new classified Core table. A fresh source-manifest-bound root campaign covers all financial and affected existing contracts. HTTP/UI, financial queue handling, full native/scoped race and current images/recovery remain required before EX02 acceptance.
