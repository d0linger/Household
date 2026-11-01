# Finance ledger command and view contract

Implementation handshake for the ledger lane. The [module runtime](finance-module-contract.md) owns transaction boundaries, strict decoding, review tokens, idempotency and current authority. All methods below use its supplied transaction; no nested transaction or Pool read. This document is a source contract, not test evidence.

## Common command

The existing `FinanceCommand` envelope contains resource/action/id/generation/version/access_version/payload/review_token. Create IDs are client-generated canonical UUIDs and version 0. Existing records require their current positive version. Nested optional facts are explicit JSON null; monetary fields are canonical signed integer strings, not numbers. All required members below must be present; unknown/duplicate/case-ambiguous members fail strict decoding.

`FX` is null or `{numerator:string,denominator:string,effective_on:YYYY-MM-DD,source:string}`. Numerator/denominator are positive canonical integers of at most18digits. The rate is EUR major per one major unit of the relevant original/account currency. EUR uses null FX and identity conversion. New local currency metadata supplies the exact exponent; caller exponent must agree.

`Money` is `{currency:string,exponent:integer,minor:string,fx:FX}`. Expense original is positive, refund negative; zero is rejected except an explicit opening. A foreign draft with null FX has unknown EUR base. Posting requires reviewed FX; genuine base zero is retained.

`Payment` is null or `{account_id:UUID,account_version:integer,native_minor:string,fx:FX,difference_reviewed:boolean}`. Native amount is a positive absolute amount; the action supplies debit/credit direction. Account currency/exponent come from its current authorized row. The payment rate values that account denomination independently. An entry may have no assigned account. If linked, posting requires a known native amount and valuation. A nonzero receipt/payment valuation difference requires explicit acknowledgement; it never becomes a fabricated fee.

## accounts

- `save`: `{name:string,account_type:cash|bank|card|other,currency:string,exponent:integer,notes:string}`. Account denomination freezes after any native-context reference, including a draft, bank profile or template payment. Stored old/new account references acquire sorted row locks so reference creation cannot race a currency edit. No automatic opening.
- `archive`: `{}`. Already archived is a versioned conflict rather than an unreviewed repeat. Historical facts remain.

## cash

- `income`: `{occurred_on:date,description:string,payee?:string,money:Money,payment:Payment,state?:draft|posted}`. Positive money; payment may be null only in a draft. Optional payee preserves at most200 characters of the actual counterparty. No category consumption. Default state is posted for explicit direct entry; bank/recurrence passes draft. A foreign draft may lack a reviewed rate and has no movements. Posting requires a real account and known valuation; the same existing income ID/version creates its single actual credit, not a second event.
- `opening`: `{occurred_on:date,description:string,account_id:UUID,account_version:integer,native_minor:string,fx:FX}`. Signed native balance at the start of that civil day, including an explicitly reviewed zero. Exactly one opening per account; it must precede all posted account movements. Future posting before it is rejected rather than double-counted. No earlier full-balance assertion.
- `adjustment`: the same closed payload as opening, with a signed nonzero native amount and mandatory explanation. It records exactly one explicitly reviewed account effect without expense consumption, inferred fees or automatic reconciliation adjustment. An explicit foreign-currency zero opening uses known base zero with null FX; adjustments never receive that zero exception.
- `transfer`: `{occurred_on:date,description:string,from:Payment,to:Payment}`. Both nonnull, distinct accounts, positive native amounts. Same-currency values equal; separate FX snapshots for different currencies. Debit from/credit to in one event; no category consumption.
- `settle`: `{occurred_on:date,description:string,debtor:UUID,creditor:UUID,debt_currency:string,debt_exponent:integer,debt_minor:string,debt_fx:FX,from_payment:Payment,to_payment:Payment,reviewed_sources:[{entry_id:UUID,version:integer}]}`. Positive debt units, distinct current household members. Foreign debt has its own explicitly approved valuation; EUR uses null. Payments may be null for an explicitly recorded payment with unassigned account; declared payments must be real nonzero native movements. Sources are a bounded complete current debt basis, not client authority. No cross-currency netting or consumption mutation.

## entries

`save` and `post` share this exact payload:

```text
occurred_on: date
description: string
payee: string
kind: expense | refund
money: Money
payment: Payment
paid_by_membership_id: UUID | null
lines: [{id:UUID, version:integer, category_id:UUID, original_minor:string, note:string}]
baseline: [{id:UUID,version:positive integer}]
removed: [{id:UUID,version:positive integer}]
split: null | {
  method: equal | percent | weights | fixed,
  participants: [{membership_id:UUID,basis:string}]
}
```

Each new line already has its stable UUID and version0; an existing line retains its ID/current version. No separate new category-line identity exists. Baseline is the complete old child set; removed explicitly names each omitted old child. At most100category lines. Draft original allocation may be incomplete; posting requires nonempty exact original and base sums. Conversion allocates signed rational quotas using mathematical floor/largest remainder/UUID ties. Negative discount lines are valid. All header/context/line/split/cash changes commit atomically.

Participant maximum50, unique canonical memberships. `basis` is `"1"` for equal, integer parts-per-million for percent (total1000000, corresponding to four decimal places in percent), positive integer for weights, signed original minor amount for fixed. Fixed/equal/percent/weights all yield the signed original total. Person debt denomination is original currency, independently from EUR presentation. A null payer stays unknown and no complete payer balance is invented.

- `refund`: `{original_entry_id:UUID,original_version:integer,entry:<the complete save/post payload>}`. The outer command ID is the new refund. The nested entry kind is refund; it posts immediately after complete review. Original currency matches; cumulative effective original-unit refund capacity is checked under source lock. The original remains immutable.
- `reverse`: `{original_entry_id:UUID,original_version:integer,occurred_on:date,reason:string}`. Outer ID is the new reversal. Exact frozen original/base/category/person/account snapshots are negated, with no rerounding or new rate inference.
- `correct`: `{original_entry_id:UUID,original_version:integer,reversal_id:UUID,entry:<the complete save/post payload>}`. Outer ID is the reviewed replacement. Exact original reversal and fully posted replacement commit together; existing facts remain immutable. New replacement lines have client UUIDs/version0 and empty baseline/removals.

The advanced purpose is fixed `expense-finance-v1`; existing EX02 absent-Finance hash bytes/types stay unchanged. Parent versions advance for finance-context changes. A generic/legacy write cannot silently erase or contradict an advanced context.

## reconciliations

- `confirm`: `{account_id:UUID,account_version:integer,cutoff:date,external_minor:string,currency:string,exponent:integer,statement_fingerprint:string,starts_on?:date|null,opening_minor?:string|null}`. Both optional period fields must be present together or both absent/null. Without them, compare complete history through cutoff and require a known explicit opening. With them, use the manually reviewed external opening plus every posted native movement from `starts_on` through cutoff, excluding opening events because the external baseline already includes them. The complete period is authorized before counts or materialization; the unchanged Core1024 unique-anchor limit applies, requiring a shorter explicit period when necessary. Preview supplies the exact fingerprint including the external opening and period. Apply recomputes under account/source authority locks and requires equality. Native statement difference may be nonzero and remains explicit; confirmation never books an adjustment.
- `reopen`: `{reason:string}`. The command ID/version identifies the existing confirmed reconciliation. Append immutable reopening history; retain original confirmed amounts/items. No movement rewrite.

## Query sections

`financeLedgerRead` handles accounts/people and ledger sections of entries/reports. Query panes/sections are closed by Root:

- `entries`: list current authorized entries/income in the selected month or explicit date window; totals use that identical complete filter. ID detail is independent of the list's date window. Cash-income IDs require the typed `section=ledger`; an expense lookup never falls back to another resource kind with the same UUID.
- `accounts`: account rows; ID returns account detail, visible statement and explicit opening status.
- `people`: original-currency member balances and settlement history; no cross-currency total.
- `reports` with section `ledger`: exact authorized consumption/cash/person projections using common filters.
- Root overview/planning helpers may consume the same domain projections inside the supplied authorized snapshot.

## View projection

Return the common runtime fields; the lane adds `rows`, `totals`, `detail`, `warnings`. Arrays are empty arrays; unknown values are null. All `*_minor` fields are strings, including aggregates.

Account row: `id,version,access_version,name,account_type,currency,exponent,notes,archived,opening_known,visible_movement_minor,visible_balance_minor,available_actions`. `visible_balance_minor` is null without a known opening. The label is always “Saldo sichtbarer Buchungen”; no hidden counts or bank-balance claim.

Account detail: `resource:"accounts",record:<account row>,statement:[{id,event_id,event_kind,occurred_on,description,native_minor,base_minor,currency,exponent}],next_cursor,reconciliations`. True reconciliation is refused neutrally if complete scoped authority is missing.

Entry row: `id,version,access_version,occurred_on,description,payee,kind,state,money_mode,money:{currency,exponent,minor,fx,base_minor,base_status},paid_by_membership_id,status_badges,available_actions`. `base_status` is `known`, `rate_missing` or `below_base_precision`. Posted base cannot be null. Status badges are server-derived relation facts, not extra stored states.

Entry detail: `resource:"entries",record:<entry row>,payment,lines:[{id,version,category_id,category_label,original_minor,base_minor,note}],baseline,removed:[],split,remaining_original_minor,remaining_base_minor,relations,source_status`. It is the same authoritative entry/line set used by EX02. Original context of older EUR data is explicitly captured-EUR/legacy-unknown rather than invented foreign facts. `source_status` is a current-authorized Core projection: `{status:known|source_unavailable,origin,source_available,independently_shared,financial_requirements_retained}`. It never guesses manual provenance from a missing money sidecar and contains no inaccessible source IDs/labels.

Person balance: `membership_id,label,currency,exponent,balance_minor`; a positive value means the member is owed money. Payer credit minus their allocated consumption establishes obligations; confirmed debtor-to-creditor settlements subtract creditor claim and reduce debtor liability. Results explicitly cover currently visible entries and settlements. Unknown payer/allocation facts are not fabricated into a balance. People detail supplies real `pairs:[{debtor,creditor,debtor_label,creditor_label,currency,exponent,debt_minor,reviewed_sources,cash_sources:[{event_id,version}]}]` for explicit settlement selection; the UI does not invent a source read set. The client sends the existing entry review list. Prior settlement facts are independently derived and revalidated on the server, included in review impact and permanently retained alongside entry sources in the new settlement's complete origin set; the combined source bound remains1000 and Core's unique-anchor bound remains1024.

Command result: `resource,id,version,available_actions,impact,result_ids` plus the relevant record/detail. `impact` is bounded human-readable financial effect metadata; `result_ids` are deterministic stable IDs across preview/apply, never proof authority. Root adds review/operation metadata. Root current-authorized operation receipts remain minimal and do not replay a historical private form.

## No execution gate

Implementation may prepare unit/integration/browser sources now. No tests, builds, migrations, database/browser/container operations occur until Root closes the complete Finance source gate. Source inspection and gofmt are permitted.
