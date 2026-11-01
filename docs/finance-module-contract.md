# Finance module runtime contract

Status: implementation contract published before Finance phase2. The user approved all180improvements and requested strict whole-module implementation followed by tests. This document supersedes obsolete version numbers in older preparation documents, not their safety requirements.

## Storage and release boundaries

Core29 authority primitives → Expenses5 all21business tables → Core30 complete producers/derivatives/history → Core31 selected-access cutover → Fuel8/Pantry14/Mealplan9 compatibility. Preserve the existing early release barriers and cap early Expenses at4. Final121business audit policies/92raw migration pins, with all85predecessors byte-identical. Core infrastructure metadata has narrowly enumerated audit treatment, not an automatic exemption for new domain tables. The detailed [schema contract](finance-schema-contract.md) is required before source implementation.

## HTTP and Go ABI

Keep GET `/m/expenses/editor` and GET/POST `/api/v1/expenses/editor` unchanged for ordinary EX02 EUR editing. Add seven workspace pages at GET `/m/expenses/workspace/{pane}` with closed panes `overview`, `entries`, `accounts`, `planning`, `people`, `import`, `reports`.

- GET `/api/v1/expenses/finance/view`: query `pane`, optional `section`, `id`, `goal`, `month`, `from`, `to`, `account`, `person`, `category`, `payee`, `state`, `cursor`; closed names/values and bounded exact dates/text/UUIDs. Server defaults household month, max50rows/page. All full totals use the entire authorized selection. Contribution lists require `pane=planning&section=contributions&goal=GOAL_UUID`; `id=CONTRIBUTION_UUID` addresses one contribution instead. Income details require `pane=entries&section=ledger&id=UUID`; expense details use the default entries section. A UUID never selects an untyped cross-table fallback. Signed cursors bind the whole current actor/session/role/auth version, query and effective month, including a first request that omitted month.
- POST `/api/v1/expenses/finance/preview`: strict `FinanceCommand`; fully validate and return reviewed impacts plus short-lived authenticated review token. It must not commit domain/audit/idempotency writes.
- POST `/api/v1/expenses/finance/apply`: same strict command plus `review_token`, CSRF and `Idempotency-Key` UUID. Revalidate current authority, CAS and invariants; atomically commit business facts, projections, lineage and durable minimal proof. Same-key uncertain retries never create a second operation.
- GET `/api/v1/expenses/finance/operations/{key}`: minimal current-authorized operation status; no historic form body. Missing/retired/unauthorized evidence is neutral.
- POST `/api/v1/expenses/finance/operations/{key}/seal`: strict `{generation}` binds the original generation and permanently closes that actor/key to late unaccepted writes. The response always says sealed/effects unknown; it reveals no acceptance or financial content. Existing committed proofs remain. After authority loss the UI retains only key/original generation, clears private forms and requires an explicit new operation after reviewing current records.
- POST `/api/v1/expenses/finance/bank/previews`: raw UTF8 CSV, max1MiB/500data rows, explicit account/profile/generation parameters and request key; returns private creator-bound preview. GET preview and POST confirm recheck creator AND all source/result rights. Raw CSV never goes to browser persistent storage.
- GET `/api/v1/expenses/finance/export`: same closed authorized query, all-or-error bounded CSV response, exact integer strings and explicit denominations.
- GET `/api/v1/expenses/finance/receipts/{attachment}/pages/{page}`: protected local raster bytes only after complete current source authority; no user-controlled process path/arguments.
- POST `/api/v1/expenses/finance/receipts/{attachment}/recognize`: bounded local OCR of explicitly selected pages, current edit authority before CPU and again before publication. Read-only viewing and review eligibility are separate from configured OCR availability. A failed or incomplete recognition never invents positions, quantities, rates or totals.

`FinanceQuery` has string fields `Pane`, `Section`, `ID`, `Goal`, `Month`, `From`, `To`, `Account`, `Person`, `Category`, `Payee`, `State`, `Cursor`. `FinanceCommand` JSON: `resource`, `action`, `id`, `generation`, `version`, `access_version`, `payload`, optional `review_token` and `initial_access`; payload is json.RawMessage, then strictly decoded into the closed action type (unknown/missing/duplicate/case-ambiguous members rejected). ID is a client-generated UUID for creates and the existing UUID otherwise. Access-version is an opaque string for the complete authority intersection, independent of content version. The key is carried only in Idempotency-Key. Preview/apply bind the same key, actor, purpose, normalized command and all relevant generation/version/authority facts. Five-minute review expiry; bank preview30minutes.

For a new primary resource, optional `initial_access` is `{access_mode,responsible_membership_id,grants:[{membership_id,permission}]}`. Household mode uses an explicit null responsible and empty grants; selected mode requires explicit active responsible and creator/responsible edit grants. This policy is prepared atomically before the first business write and immutable audit; no temporarily shared record is created. Existing records use the separate access/change workflow. This applies only to accounts, cash, entries, budgets, goals, recurrences, rules, reconciliations and receipts, never to private filter/profile or contribution child identities.

Store exported methods: `FinanceView(ctx,id,query) (Record,error)`, `FinancePreview(ctx,id,key,requestID,command) (Record,error)`, `FinanceApply(ctx,id,key,requestID,command) (Record,error)`, `FinanceOperation(ctx,id,key) (Record,error)`. Root owns these transaction boundaries, strict HTTP and common types. Internal shared helpers use existing `financialScope` (Legacy extends it with verified user/membership/role), `financeLedgerRead(ctx,tx,id,scope,query) (Record,error)` and `financeLedgerCommand(ctx,tx,id,scope,command) (Record,error)`. Root dispatches support-resource helpers in the same transaction; no nested Pool transaction. Transaction preview executes validation in an uncommitted transaction and always rolls back; no external effects. Create IDs and all review facts must remain stable across preview/apply.

Closed resources/actions:

| Resource | Actions | Required semantic payload |
|---|---|---|
| accounts | save, archive | name,type,currency,exponent; optional explicit opening handled as separate cash fact |
| cash | income, opening, adjustment, transfer, settle | date,description,payee for income, signed/native payment legs and reviewed independent FX; settlement also debtor,creditor,debt_currency,debt_minor. An income draft may have no assigned payment account; posting requires the actual payment. Manual signed adjustments are separate explicit cash facts, never automatic reconciliation fixes. |
| entries | save, post, refund, reverse, correct | shared entry/complete line baseline, original money, approved FX, account payment, participant allocation; refund/correction/reversal source IDs/versions required |
| filters | save, delete | personal name and closed exact filter definition |
| budgets | save, archive | category, month,target_minor, carry_rule none/positive/all |
| goals | save, archive | name,target_minor,currency,exponent,target_date,planned_minor |
| contributions | save, reverse | goal,date,actual_minor, optional authorized source cash event; never invent cash |
| recurrences | save, pause, generate | reviewed draft template, cadence, interval, anchor,next_due, optional explicit automatic consent |
| rules | save, archive, explain | priority, bounded payee exact/contains condition, authorized category; UUID ties deterministic |
| bank_profiles | save, archive | explicit delimiter/encoding/date/decimal/group/sign/field mapping/currency/transactionID conventions |
| reconciliations | confirm, reopen | account,cutoff,external_minor,currency, complete statement-set fingerprint and reviewed difference; optional paired starts_on/opening_minor supplies an explicitly reviewed external period opening. Period reconciliation retains every within-period source anchor under the same1024bound and excludes separate account-opening events from that period sum. |
| receipts | review | attachment,entry,source_hash, pages/lines/source locations, candidate total, manual overrides and explicit mismatch decision |
| access | change, assign, recover | typed target, current access version, explicit responsible membership, household/selected, bounded view/edit grants; recovery metadata does not grant content access |

Every action's final fields are closed typed structs in its owned backend source, and are documented for frontend binding before integration. There are no arbitrary SQL table names or field writes derived from resource input. Implement all listed actions or close a documented contract adjustment before the whole-source gate; no inert controls count as functionality.

## View DTO

Common fields: `pane`, `section`, `generation`, `access_version`, `read_at`, `today`, `timezone`, `base_currency`, `can_write`, `capabilities`, `rows`, `next_cursor`, `totals`, `references`, `detail`, `warnings`. Arrays are empty arrays, not null; unknown facts remain explicit null. References are individually authorized bounded account/member/category records. Detail uses the same canonical typed money/line/action models as commands. Each pane adds explicit resource arrays and summaries; the frontend binds real DTO fields after lane handshake, never fabricated sample data. All record identifiers/labels are returned only after authority checks. Monetary values are canonical integer strings; counts/versions are bounded integer metadata, not money.

## Money and facts

Legacy EUR payload normalization and serialized canonical EX02 hashes stay byte-identical when Finance is absent. New advanced purpose is `expense-finance-v1`; actor/key collisions apply across purposes. One authoritative entry/line identity set. `money_mode` is server-controlled legacy_eur/foreign_original. Legacy nonzero known base; foreign draft may have null base only for explicitly missing/unreviewed rate, all lines similarly null. Foreign posted needs frozen reviewed FX and known base, including genuine "0". No generic nullable EUR loophole. Replace deferred `<>` knownness comparisons with explicit state checks and IS DISTINCT FROM.

Rate = EUR major per original major: round(original_minor × numerator ×100 / denominator /10^exponent), half-away-from-zero-v1. Positive bounded rational numerator/denominator (max18digits each), supported explicit currency/exponent, per-entry/line original absolute limit1e12,100lines. Signed-largest-remainder uses mathematical floor and UUID-byte ties. Complete original allocation and exact converted allocation required for posting. Reversal negates immutable snapshots without rerounding. UI distinguishes rate missing from below-base-precision zero.

Account native movement and receipt valuation are distinct facts with independently approved FX. Show reviewed difference without auto fee/income/balancing entry. Missing account/opening/payer/beneficiaries/origin stays unknown. Person shares and debts use original currency; no cross-currency netting. Maximum50participants, percent four decimals, positive bounded weights, fixed shares exact; all sum to original. Settlement history survives later corrections and never alters consumption.

## Complete authority and lineage

Current authenticated session/user/membership/global role/module generation ∩ record policy ∩ every linked account ∩ every original protected subject. Unknown/incomplete/retired authority denies. No admin content bypass. Explicit responsibility, max100grants including an active writeable responsible editor. Reader remains globally read-only even with edit grant; personal filter preferences are separately creator-owned. Content editing does not permit grant management. Stable lock order and fresh RC recheck after locks;10sSQL/20soperation remain bounded.

Private source requirements are unioned permanently before first immutable audit. Rebinding/correcting/replaying cannot wash them. Independently shared Fuel/Shopping copies remain shared sources: typed proven provenance, not invented financial policies. Fuel→Expense initial result can remain independently readable after Fuel disable/purge if no actual original protected financial subject existed; current link/mutation/replay requires both present sources. Copy/correction of an existing Expense retains every original expense/account requirement. A missing derived target cannot produce a duplicate.

Apply before pagination/ranking/counting/aggregation and on every generic and indirect path: list/detail/write/delete/action, references/search/related/report/export, attachment/rebind/download/raster/OCR/capture, import preview/result/replay, audit, notification read/badge, operation status, pruning and purge. Visible account subtotal says **Saldo sichtbarer Buchungen**; true reconciliation requires complete current statement authority and otherwise neutral refusal. No hidden count/difference diagnostic.

Historical sources classified only from complete proofs; ambiguous remain unresolved and neutral, without preventing valid new selected records. No author/payer-derived ownership or fabricated manual origin. Owner-only maintenance diagnostics do not create application content bypass. Genuine Owner/authorized SET ROLE is distinct from effective Owner inside SECURITY DEFINER or RI cascades.

## Background, files, retention

Recurrences create reviewable drafts, never posted facts. Dates in household timezone with explicit monthly-end/leap semantics, max12occurrences/template/100run and visible catch-up backlog. Optional generation/consent-bound system authority requires active responsible editor/current grants/account/source/auth version and real lease; no fake/reused browser session. Long OCR holds no policy locks; recheck source hash, binding/scope head, generation and authority before publication. Notifications check authority on delivery and every read.

OCR/image capability remains optional and honest. Local Poppler/Tesseract only in configured OCR image; max20pages, bounded bytes/pixels/scale3000/time, explicit page processing, nullable confidence, source hash and line/page provenance. No object/frame CSP weakening, no remote fonts/scripts/PDF.js.

Bank previews:1MiB/500rawdata rows, UTF8/BOM, explicit profile; permanent strong account+bankID or exact file+row-occurrence claims. Heuristic similarity is only a warning. Minimal proof survives7day rich-payload pruning and purge; global requirement1024/import1000 are distinct complete-union bounds, never truncation. Restored oversized previews are revalidated. Creator AND all results rights before identities/counters/replay.

No financial offline queue, private browser persistent storage or API caching. Authorised conflict may preserve the in-memory draft; revoked authority clears private DOM/previews/ObjectURLs and blocks resend. An uncertain result reuses the same key. Local fixtures only in tests, no production demo data. Final acceptance must include genuine populated encrypted restore/files and actual Expenses purge, not inferred operational success.
