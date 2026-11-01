# Finance authority and provenance ABI

Implementation contract for the approved complete Finance module, based on Core28/Expenses4. Read together with `finance-module-contract.md` and `finance-schema-contract.md`. This document fixes interfaces before the authority lane changes behavior. It is not test or feature acceptance.

## Ownership and transaction boundary

Root owns common Finance HTTP/types/preview/apply transactions, the existing expense editor/bundle integration and `database.go`. The authority lane owns new Core29/30/31 migrations, financial access/lineage/artifact/audit helpers, existing indirect integration paths and corresponding test sources. Budget owns Expenses5 and ledger helpers. No lane starts tests, builds, database/browser/container operations before the whole Finance source gate.

Helpers accept the caller's `pgx.Tx`; none commits, begins a nested pool transaction or publishes an external effect. The caller establishes the household/current identity/module transaction before a mutation. Foreground reads use one bounded repeatable-read snapshot; mutations use read committed plus the locks below. All helpers propagate context. Existing 10-second SQL and 20-second mutation limits remain unchanged.

`financialScope` retains its existing fields and gains verified user, membership, session, auth-version and module-state-version facts. Both `readFinancialScope` and `readSurfaceScope` construct these from the database in the same transaction, never from a client-supplied scope. The latter retains its ability to search other modules while Expenses is disabled.

## Public in-package Go boundary

The following names and argument order are shared between the authority, Root and ledger lanes:

| Helper | Result and contract |
|---|---|
| `financeResourceAccess(ctx, tx, id, scope, kind, rid, intent)` | `(string, error)`: current complete resource authority stamp; no private payload. |
| `financeProspectiveAccess(ctx, tx, id, scope, refs, intent)` | `(string, error)`: authorize and stamp the complete existing reference union before create/rebind. Empty refs with `view` returns the verified actor/module context stamp. |
| `prepareFinanceOrigin(ctx, tx, id, scope, kind, rid, origin, sources)` | `(string, error)`: reserve an operation and capture authoritative original requirements before the first business/audit write; returns the origin-operation UUID. |
| `completeFinanceOrigin(ctx, tx, id, scope, operationID)` | `error`: prove/complete the same transaction's origin and audit bindings before the caller commits. |
| `ensureFinanceResource(ctx, tx, id, scope, kind, rid)` | `error`: verify that the installed domain hooks created the resource's correct subject/ownership and origin. It is not a late substitute for origin preparation. |
| `withFinanceOrigin(ctx, origin, sources)` | `context.Context`: Root/producer-owned immutable contextual hint; copied source references, never an HTTP-authoritative policy set. |
| `financeOriginFor(ctx, ownSources)` | `(string, []FinanceResourceRef)`: merge and deduplicate the trusted outer sources with the primary writer's accounts/sources; preserve over-bound input for explicit rejection, never truncate it. |
| `financeAccessCommand(ctx, tx, id, scope, command)` | `(Record, error)`: strict change/assign/recover command under current target management or narrowly scoped recent-auth recovery authority. |
| `financeAccessProjection(ctx, tx, id, scope, kind, rid)` | `(Record, error)`: authorized row/detail `access` metadata described below. |
| `financeAutomationScope(ctx, tx, jobID, leaseToken, templateID)` | `(auth.Identity, financialScope, error)`: genuine current system authority; returned Identity has no invented SessionID. |
| `setFinanceAutomation(ctx, tx, id, scope, templateID, enabled)` | `error`: explicit generation/source/account/role-bound consent; responsibility must already be assigned. |
| `financeIngestStatus(ctx, tx, id, scope, sourceKind, fingerprint)` | `(bool,error)`: false if unclaimed; true only for a currently authorized accepted target; claimed hidden/missing/retired targets return neutral not-found, never their IDs. |
| `financeClaimIngest(ctx, tx, id, scope, sourceKind, fingerprint, targetKind, targetID)` | `error`: reserve the durable exact source claim under a prepared origin before the business write; deferred completion proves the actual matching target. |
| `financeResourcePredicate(scope, kind, intent, alias, first)` | `(string,[]any,error)`: compiled alias/parameterized SQL predicate, applied before paging/ranking/aggregation; no candidate filtering in Go. |
| `financeOriginProjection(ctx, tx, id, scope, kind, rid)` | `(Record,error)`: closed origin/status flags with no inaccessible source labels/IDs; ordinary edits preserve the original provenance root. |
| `financeAutomationInfo(ctx, tx, id, scope, templateID)` | `(Record,error)`: only `automatic` and a neutral `reason`; current responsibility/auth/template/account/policy/source revision is rechecked. |
| `financeAccessInfoTx(ctx, tx, id, scope, kind, rid)` | `(Record,error)`: metadata-only recent-administrator recovery authority, with no domain detail query. |

`ctx` is `context.Context`, `tx` is `pgx.Tx`, `id` is `auth.Identity`, `scope` is `financialScope`; string arguments above are Go strings. Reference slices have type `[]FinanceResourceRef`.

`FinanceResourceRef` has exported fields `Kind string`, `ID string`, `RowNumber int64`, and `AuditID int64`. Ordinary domain references use only Kind/ID. `import_row` uses the batch UUID plus a positive RowNumber; `audit` uses its actual positive BIGINT AuditID and no disguised UUID. These metadata fields do not make source classifications or policy lists authoritative. Invalid/unused combinations are rejected. The server derives every subject, policy, account and original requirement from actual stored typed facts.

Intents are the closed strings `view`, `edit`, `manage`, and `preference`. Edit/manage require the current global writing role as well as resource rights. Preference is permitted only for the current user's `saved_filters`; it cannot authorize a financial-domain write or private bank-profile write. Existing `financialPredicate`, `lockFinancialPolicy` and replay wrappers remain usable by existing callers while delegating to the complete contract.

`financeLedgerRead(ctx, tx, id, scope, query)` and `financeLedgerCommand(ctx, tx, id, scope, command)` continue to return `(Record, error)` and use these helpers inside Root's transaction.

## Exact resource mapping

All 21 new tables have a uniform `id` UUID; the schema's identity aliases are CHECK-constrained, not additional identities. Kind strings below are compiled table-plural names. Existing `entries` and `entry_lines` remain their authoritative original IDs. Categories remain an explicitly shared catalogue.

| New kind | Identity / effective primary authority | Additional required facts |
|---|---|---|
| accounts | id; own account policy/subject | Current generation; archive affects new selection, not permitted historical reads. |
| entry_finance | id=entry_id; existing entry | Current payment account and retained original account/source anchors. |
| entry_original_lines | id=line_id; existing line and entry | Parent original-money context; no second allocation identity. |
| cash_events | id; entry policy when entry_id exists, otherwise own cash policy | Its nullable account_id (including an income draft before movements), every movement account, reversal/original event and immutable origin. |
| account_movements | id; event_id cash/entry primary | Its account AND all other accounts required by the same event. One transfer leg cannot expose half a transfer. |
| entry_relations | id; entry_id | original_entry_id and any exact correction reversal; complete original unions. |
| entry_split_sets | id; entry_id | Current account/original requirements; payer is not an access grant. |
| entry_person_shares | id; split_set_id and matching entry_id | Complete entry/account/original requirements; beneficiary is not an access grant. |
| settlement_details | id=event_id; settlement cash primary | Both payment accounts and the exact reviewed debt-source union. |
| saved_filters | id; explicit Core creator ownership | Current referenced account/person/category criteria; unavailable criteria make the view invalid, never broader. Reader preference writes are allowed only here. |
| category_budgets | id; own policy/subject | Every account selected in its closed scope and original dependencies. Consumption remains a filtered aggregate. |
| savings_goals | id; own policy/subject | Optional linked account and original dependencies. |
| goal_contributions | id; goal_id primary | Any actual event/movement and reversed contribution's full requirements. |
| recurring_templates | id; own policy/subject | Current account, approved template sources and explicit responsibility/consent. |
| recurring_occurrences | id; template_id primary | Exact generated entry/event when present, source revision and retained occurrence proof. |
| categorization_rules | id; own policy/subject | Optional account and current category selection. A hidden rule cannot expose its payee condition. |
| bank_profiles | id; explicit Core creator ownership | Required current account. Global writing role is still required to edit. |
| reconciliations | id; own policy/subject | Account plus complete reviewed posting/original scope; no partial-bank-balance substitution. |
| reconciliation_items | id; reconciliation_id primary | Exact movement/event/account source versions and requirements. |
| receipt_reviews | id; own review policy/subject | Optional entry and every capture/attachment/original scope. An unattached review is still a protected financial fact. |
| receipt_review_lines | id; review_id primary | Parent review scope and any linked entry line's complete requirements. |

Children are not 21 independent sets of grants. Policy inheritance is derived by the closed mapping. Domain parent/account changes append the old/new original union; they cannot replace a prior protected dependency. Core policies/subjects keep same-household FKs and stable retired identities; optional schemas never FK into one another.

## Stamps, locks and denied outcomes

An empty-reference context stamp contains only verified actor/session/membership/role/auth-version, Expenses generation/state-version, timezone and base currency. It excludes wall-clock/read_at, random newly created policy IDs and unrelated hidden policies. It must stay stable between an unchanged preview/apply. For an existing resource, add only its complete currently authorized closure, current policy/grant revisions and required scope heads. The stamp is a comparison fact, never a bearer grant; every use reauthorizes the actual database state. Preview HMAC/review expiry remains Root's separate responsibility.

Stable lock order is household/current auth/module, required policies in UUID order SHARE NOWAIT, accounts in UUID order, domain parents/children in canonical order, then Core artifact/import/job rows. Manage-access mutations lock affected policies for update and prevent removal of the last active responsible editor. Existing opposite-order generic writers retain bounded NOWAIT conflict handling. After locks, a fresh RC statement rechecks the exact complete intersection. Missing, unresolved, retired or old-generation requirements deny; there is no admin or missing-binding shared fallback.

Current-authorized content/access-version conflict returns conflict without changing facts. Lost authority returns neutral forbidden/not-found before IDs, amounts, counts, filenames, stale response payload or accepted-operation details. Same-key replay must not fall through to a fresh create after missing payload, missing target, denied source, pruning or purge. An already shared Fuel/Shopping source is not retroactively made private by its result.

## Access command and UI fields

`FinanceCommand.resource` is `access`; its ID equals payload target_id. For change/assign, Version is the actual current domain-row version. For recover, Version is0: an excluded administrator does not read a private domain version. AccessVersion is the current closure stamp or the metadata-only recovery stamp. Exact closed payloads:

- change: `{target_kind,target_id,policy_version,access_mode,responsible_membership_id,grants:[{membership_id,permission}]}`; permission is view/edit, mode household/selected. Current responsible manager may explicitly reassign while preserving an active responsible edit grant.
- assign: `{target_kind,target_id,policy_version,responsible_membership_id}`; explicit self-assignment only for an unassigned household policy with current edit authority. It does not take another member's selected policy.
- recover: `{target_kind,target_id,policy_version,new_responsible_membership_id,reason}`; current recent-auth administrator, mandatory bounded reason and explicitly selected active writable successor. The operation records minimal administrative security metadata and grants no administrator content access.

An authorized row/detail has `access` with `access_mode`, `policy_version`, `access_version`, nullable `responsible_membership_id`, `grants`, `can_view`, `can_edit`, `can_manage`, `can_assign`, `can_recover`. Grants are empty unless the viewer may manage them. No role bypass is inferred from these presentation fields. Intents return the same authority stamp for an unchanged closure; intent changes permission checking, not the stamp's identity.

An excluded administrator obtains only neutral recovery metadata through `Store.FinanceAccessInfo(ctx,id,kind,rid)` (Root's proposed GET `/api/v1/expenses/finance/access/{kind}/{id}`). It does not call a content reader. No amount, title, filename, private count, old form or resource detail is returned. Its opaque stamp authorizes only the reviewed recovery action, never content retrieval. Normal detail projection and recovery metadata remain distinct.

`financeAccessInfoTx(ctx,tx,id,scope,kind,rid)` provides the same metadata inside Root's transaction. A recovery receipt uses the accepted policy revision as saved_version and `metadata_only=true`; matching status/replay reauthorizes recent administrator recovery metadata rather than forcing content access. Other access actions retain the normal currently authorized resource boundary.

## Before-first-audit origin contract

Preparation accepts closed origin kinds: `manual`, `bank`, `recurrence`, `correction`, `refund`, `reversal`, `fuel`, `capture`, `import`, `preference`, and `access`. A Fuel/Shopping shared source is a typed source-reference classification, not an invented financial policy. The source resolver uses actual typed database identities. For an existing object, known prior origins are retained; an unknown existing origin cannot be relabelled manual or healed by a new visible target. Fresh manual foreground input is explicit user input, not an inference from missing markers.

Each primary domain writer owns its own prepare/complete pair. Root supplies trusted outer context for bank import rows, recurring templates, corrections and other derived writes; nested entry/cash/reversal writers add their own exact account/source references. The default manual hint is valid only with an actual verified foreground identity. A worker requires its separately validated real system authority and cannot invent a browser session.

Origin operations form a bounded LIFO stack of at most16 roots inside the same transaction. Prepare captures the active validated pending parent, records a distinct typed target/root, and selects the child. Complete proves that child and restores its pending parent. Roots and their evidence are never overwritten by the selector. A same-transaction pending source is accepted only when bound to the validated own/ancestor operation, which permits an entry's cash child while the entry operation is still pending. Sidecar/parent-touch hooks resolve the exact typed target's pending ancestor rather than attributing the update blindly to the active child. Deferred commit checks require every operation complete. These origin-operation IDs are separate from the one final user `finance_operations` receipt.

The prepare helper records a bounded current-transaction reservation, verified actor (or the separately validated system authority), target identity and complete source/account union. A transaction-local selector only points to that reservation; a custom GUC alone grants no authority. New subject/ownership hooks run under the reservation before their business row audit. The installed trigger name orders after existing `financial_subject` and before `household_audit_change`. Deletions retain the actual OLD image and original identities. Core policy/subject events created before a new subject is complete receive typed same-transaction pending evidence; deferred completion must bind their exact original audit IDs and captured requirements, never a later transaction's artifact head.

Completion rejects missing/extra/substituted sources, unfinished reservations, wrong household/generation/actor/XID/time, missing origin heads, lost retained proof, and mismatched OLD/final images. The same-transaction audit/projection/operation effects commit together or all roll back. Internal producer/union functions are not directly executable by App. Narrow public factories validate current identity and use `search_path=pg_catalog`. All new security metadata exceptions are individually enumerated; the final business audit count stays exactly121.

Domain dependencies combine the previous set with the complete newly reserved sources. They exclude only redundant anchors governed by the exact own policy, which is checked separately along with typed parent/child bindings. All independent original policies remain required. Draft deletion therefore completes through the ordinary source check and exact OLD/effect proofs; it has no completion exception. An independently retired source, including one deleted by a nested operation in the same transaction, still prevents completion. The private union factory is not executable by the App role; retired rows and audits gain no read access.

Archive mutations also prepare/complete. Physical saved-filter deletion retains its exact OLD/private-owner evidence, retires the private identity and completes only with actual typed deletion evidence. Missing domain rows without that evidence are not successful deletes. The minimal receipt may record accepted old-version+1 while current status is neutral404; the retained identity and request key cannot create a replacement. Personal discard does not reveal denied account labels or an old financial response.

Shared Fuel/Shopping source descriptors are typed proved nonfinancial origins, not fabricated manual zero and not invented financial policies. Current link/create/mutation/replay checks source liveness and target rights. A retained Expense from a proven shared source remains readable after Fuel disable/purge under its own policy/accounts, with an unavailable-source badge, unless a real original protected financial subject was also required. Corrections/copies of an existing Expense permanently retain all original Expense/account requirements.

Historical classification uses bounded complete typed proof/audit chains only. Ambiguous records become unresolved. This does not permanently disable valid new selected records. Operator diagnostics and preserved backup are separate from application grants. Real maintenance distinguishes actual owner login/authorized SET ROLE from App inside owner-definer or RI frames.

## Permanent operation proof ABI

Root reads/inserts `core.finance_operations` using these exact columns:

| Column | Type / meaning |
|---|---|
| id | uuid primary key; minimal accepted operation identity |
| household_id | uuid, Core household |
| created_by | uuid, actual foreground user |
| membership_id | uuid, same-household membership |
| request_key | uuid, canonical Idempotency-Key |
| purpose | text, fixed canonical purpose `expense-finance-v1` |
| operation | text, closed dispatcher resource/action namespace |
| request_hash | bytea, exactly32bytes; canonical command hash without review token |
| expenses_generation | bigint greater than zero |
| resource_type | closed typed effective result kind |
| resource_id | uuid, actual result identity |
| result_version | bigint greater than zero, accepted content version |
| created_at | server-attributed timestamptz |

Unique `(household_id,created_by,request_key)` spans all new purposes. A shared DB guard serializes and rejects collisions with existing `core.expense_edit_operations` actor/key proofs as well; legacy canonical request bytes are not rewritten. No form, amount, label, account number or rich response is persisted here. Current-authorized status exposes acceptance identity plus separately labelled current state only. Proofs survive response pruning and Expenses purge; old generation/key cannot create another effect.

Core also retains minimal origin, private-preference ownership, ingest and system-consent evidence. Import preview payload remains the existing bounded Core import batch/row model. Personal preferences do not create account ownership. Bank previews require creator plus account plus all original/result requirements before labels, row counts or replay. Actual BIGINT audit keys and positive import-row keys remain typed.

Import rows inherit the prepared batch root and retain their actual batch/positive-row identity. They are valid source references, never standalone UUID origin roots; the artifact factory accepts attachment, derivative, capture or import batch only.

Core30 adds to existing `core.import_batches`: nullable historical `created_by uuid`, `version bigint` default1, `expires_at timestamptz`, `source_hash bytea`32, `profile_id uuid`, `profile_version bigint`, `account_id uuid`, `account_version bigint`, and bounded closed `review_data jsonb` for the approved profile snapshot. Optional-module IDs have no cross-schema FK. Existing import_rows carry typed preview payload/row fingerprint/errors. New batches require real creator and current account/profile identity; historical missing creator is unresolved. `import_batch` is an explicit artifact result kind for the final permanent operation proof and current-authorized status, not a fabricated Expenses row. Audit captures version/source hash and safe identity, never CSV payload.

`core.finance_ingest_claims` keeps only household, closed source kind (`bank_transaction`, `bank_file_row`, `recurrence`),32-byte source fingerprint, target kind/UUID/generation and creating origin identity. Unique household/source-kind/fingerprint spans rich-payload pruning and purge. No optional-domain FK or old result payload is stored. The server computes the canonical fingerprint; an HTTP fingerprint does not grant source authority. Failed target creation rolls back its pending claim. Missing accepted payload or a denied old target never converts an old claim into a new operation.

Automation consent is explicit and requires an already assigned active responsible writer. A real job lease selects an enabled template/current generation and verifies its consent, responsible membership/global role/auth version and complete account/source requirements under locks. The Core authority binds these actual facts to the current transaction; custom values alone are insufficient. Helpers accept an empty SessionID only under that typed verified job/lease binding. Audit remains the actual system worker, not a forged user session. Generation creates bounded reviewable drafts only; completion must finish all nested origin operations before Root's preview `SET CONSTRAINTS ALL IMMEDIATE` and rollback or an actual commit.

### Neutral permanent key seal

`core.finance_key_tombstones` retains only household/user/key, declared Expenses generation and creation time. `core.finance_seal_key(hh,user,membership,session,auth_version,generation,key)` verifies the actual current user/membership/session, including a Reader, without reading a financial target. A positive older generation up to the current module generation is accepted even while the module is disabled. The response is always neutral and never distinguishes accepted from unaccepted writes. Accepted operation proofs remain available under their separate current-authority status gate.

The seal and both legacy/new permanent operation INSERT guards serialize on the same household/actor/key advisory lock. If the seal wins before any accepted proof, a delayed receipt insertion fails and rolls back the complete late business transaction. If an accepted proof wins, the seal does not delete it or reveal its existence. The browser clears a withdrawn financial body immediately, retains at most the neutral locator, and may discard that locator only after the explicit seal response; it never automatically invents a replacement key.

### Existing background files

`finance_artifact_job_authorities` is a separate private metadata binding for actual thumbnail/capture job IDs, live leases, source attachment, source actor/current auth version and current transaction. No browser session is invented. Workers recheck source classification/rights before opening input and again before publishing derivatives/OCR; completion finishes the exact artifact reservation before releasing the job lease. Pure nonfinancial files retain their independent module boundary. Permanently denied/expired file cleanup retires the old private identity without relabelling it nonfinancial.

## Existing-path integration and release gate

An explicit initial household policy may have null responsibility and an empty grant list: the chosen shared mode supplies household-role rights. Initial selected mode requires explicit current creator and responsible edit grants. New advanced create validates and copies these fields without mutating the signed command. Existing EX02 nil-policy hashes remain unchanged. Owned saved-filter replacement/deletion uses its separate private-preference boundary; revoked criteria never broaden a loaded view, while explicit replacement can remove obsolete references. Old audit scopes retain their original requirements.

Parent/sidecar effects resolve actual typed ancestors, including entry→cash→movement, within16 levels. The exact pending ancestor target wins over a child touch. Completion aggregates all current entry/cash child source sets without rewriting the business row; whole-record derivations retain actual original line/movement subjects. Missing/substituted bindings and nonmatching row hashes deny access. New financial row-image hashes use UTC. The independent Fuel proof resolver reproduces Fuel's actual database reset-timezone convention and requires its real completed or same-transaction reserved effect. Protected Finance attachments cannot feed independently shared Fuel OCR facts; ordinary explicitly nonfinancial Fuel files remain supported.

Deleting an expense line reserves the actual stored entry as the operation root, after current authorization and the expected line version are checked. Retirement of that line and the resulting parent revision belong to the same operation. Source unions retain both previous and newly reserved independent requirements. Only redundant anchors governed by the exact own policy are excluded from source revalidation; that own policy is checked separately. Retiring an independent source, including through a nested operation in the same transaction, still prevents completion and rolls the transaction back.

Known historical operator-password-recovery audit events have a narrowly defined metadata-only projection after current administrator/session validation. It accepts only the installation's exact legacy actor/event shape, with no module/resource identity and empty entity key, changes and details. Unknown or data-bearing unscoped rows are denied. Ordinary financial audit events continue through their complete original requirements.

Generic financial writes/actions retain minimal permanent proofs under distinct legacy operation names using their unchanged Mutation hashes. Matching retries after rich-response pruning expose labelled accepted/current-version metadata only after current authority; cross-purpose collisions, old generations, retirement and sealed keys cannot create another effect. Actual Fuel uses its existing durable module-operation and binding proof. Generic Finance reminders/notices have no approved typed recipient/source producer and are excluded before delivery and reading; no notice feature is invented.

Retention uses the existing private artifact-job authority table's disjoint `retention` variant: actual housekeeping job/lease/current XID, no user/session/financial writer authority. `core.finance_prune_artifacts(job,lease)` removes at most100 expired captures and100 expired bank previews per call, plus100 old flat Finance previews, retiring exact OLD artifact metadata and preserving operation/ingest/key fences. Module purge retires the full original Expenses generation across Core artifacts and consent while preserving independent domains.

The authority lane integrates generic writes/actions/deletes; current Fuel module expense creation/correction/status/binding views; search/references/related/reports/CSV; attachments/rebind/download/preview/OCR/capture; import preview/confirm/replay; audit before pagination; notifications at delivery and read; current worker publication; responsible-editor/user changes; and pruning/purge. Root adds the prepare/complete calls to existing expense editor paths and the central Finance dispatcher. All new financial routes are excluded from browser persistent/offline queues.

No selected control is accepted as permanently disabled. Core29 keeps shared-only while installing primitives; Expenses5 supplies all21 tables; Core30 completes producers/history/consumers; Core31 deliberately removes the rollout barrier only with exact final readiness. Existing85SQL bytes remain immutable. Final compatibility includes Fuel8/Pantry14/Mealplan9,92pins and121audit policies. Fresh install, populated upgrade, real selected access, revoke/replay, owner/RI, limits, race, file recovery and real purge are executed only after the entire Finance source gate.

## In-flight goal reversal and private-factory boundary

A new goal-reversal subject exists briefly before its own origin head inside the insert trigger. Parent completeness permits only that exact pending target when its goal policy, reversal kind, current transaction ID/start, actor, membership and validated selected operation all match. Another missing head, a retired head, an absent business row or another actor/root remains denied. Ordinary head visibility and deferred origin completion remain required.

Direct owner-defined metadata factories require an actual current prepared operation; a standalone owner-definer frame or custom setting is insufficient. Nested installed business/artifact triggers retain their explicit producer checks and may emit classified evidence. Real Owner maintenance remains a separate session/authorized-role boundary. Application-role schema/function privileges are not expanded.
