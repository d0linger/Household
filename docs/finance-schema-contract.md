# Finance schema contract

Planning contract for the complete Expenses increment, based on local checkpoint `200d3fc4dec9031075ce202349b9eca7fabd333a` (Core28/Expenses4). The 21 relation names below are frozen by the module plan; this document specifies their proposed columns and invariants before implementation. It is not evidence that a migration, feature or test has run. The accepted EX02 EUR editor and its absent-Finance canonical request bytes remain compatible.

## Ownership and migration boundary

- Expenses5 owns these 21 new relations and the narrowly scoped changes to existing `entries` and `entry_lines`. Those existing relations remain the sole authoritative booking and category-line identities.
- Core29 owns authority primitives; Core30 owns producers, immutable origins, indirect surfaces and historical classification; Core31 owns deliberate selected-access cutover. Core owns durable operation/ingest claims, origin requirements and private preference anchors. There is no Expenses `entry_origins` or bank-row table.
- Fuel8, Pantry14 and Mealplan9 provide the required forward compatibility. Seven new forward SQL files yield 92 migration pins; all 85 accepted predecessor SQL files retain their raw bytes.
- The 21 new classified domain tables take the current audit-policy census from 100 to the final 121. New Core security infrastructure has explicit narrowly enumerated treatment; no further unclassified business relation is permitted. Exact final Core exclusions/classifications must be enumerated, not guessed from a count.
- Optional module data is referenced through typed Core contracts. Within Expenses, use same-household composite foreign keys. No Expenses foreign key points into Fuel, Documents or another optional schema.

## Common column and boundary conventions

Every relation has a uniform `id uuid` primary key and `UNIQUE(household_id,id)`. Identity aliases are CHECK-constrained: `entry_finance.id=entry_id`, `entry_original_lines.id=line_id`, and `settlement_details.id=event_id`; these are not additional financial identities. Every relation has `household_id uuid` referencing Core households. Mutable records have `version bigint > 0`, `created_by uuid`, `changed_by uuid`, `created_at timestamptz`, and `updated_at timestamptz`; actors reference Core users and are server attributed. Immutable facts retain the same identity/actor/timestamp shape but cannot be rewritten by App after posting/confirmation. Membership references use `(household_id,membership_id)` to Core memberships. Genuine maintenance uses `core.finance_real_maintenance()`; foreground or genuine leased-worker business attribution uses `core.finance_business_actor(household_id)` without rewriting the audit channel or manufacturing a user session.

All money inputs and outputs are canonical integer strings, with explicit supported currency and exponent. Database minor amounts use `bigint`, bounded to absolute 1e12 per individual value; aggregates use `numeric` and serialize as text. Positive FX numerator/denominator inputs are integer decimal strings of at most 18 digits each, persisted as `numeric(36,0)`; conversion uses bounded exact integer/rational arithmetic. Currency metadata is a local versioned allowlist supporting exponents 0, 2 and 3. Household base remains EUR/exponent2.

No SQL comparison may treat NULL as an accepted financial equality. Amount-state guards use explicit knownness and `IS DISTINCT FROM`. Unknown is never normalized to zero. Text has both character and byte limits; JSON accepts only declared fields, bounds structure and collection lengths, enforces byte limits, and uses canonical server normalization. Primary financial policy, account policy and inherited original requirements remain authoritative Core identities, not writable policy-ID arrays in domain payloads.

## Existing booking changes

`entries` gains `money_mode text NOT NULL DEFAULT 'legacy_eur'`, restricted to `legacy_eur` and `foreign_original`. Its `amount_minor` remains captured EUR base but becomes nullable only for a typed foreign draft with valid original context and no approved rate. Legacy mode still requires a non-null nonzero amount and the existing expense/refund sign rules. A known approved foreign conversion may be zero; posted base can never be NULL. A nonzero original is always retained. Existing `kind`, draft/posted states and identities remain authoritative.

`entry_lines.amount_minor` is the EUR category allocation. It may be NULL only in an unknown-rate foreign draft, and zero only with matching validated foreign original-line context. Existing EUR paths still reject both. Draft foreign lines use the same existing line IDs and complete parent/child version basis. Draft context/line-context changes advance the existing parent version. Posted header, category lines, original money, account association, rate and reviewed person shares are frozen; corrections append linked opposite/replacement facts.

Existing validation/immutability guards are extended narrowly. A deferred final guard verifies complete foreign context, exact original totals, approved conversion, deterministic base allocation, correct sign and all posted cash/person effects. Generic writes cannot create an unproved foreign exception. Genuine Owner restore is distinguished from effective-owner functions running for App.

## 1. `expenses.accounts`

| Columns | Definition |
|---|---|
| `name text` | Required 1–100 characters. |
| `account_type text` | Closed `cash`, `bank`, `card`, `other`; no implicit debt accounting behavior. |
| `currency_code text`, `currency_exponent smallint` | Supported fixed denomination; immutable after the first movement. |
| `archived_at timestamptz NULL` | Referenced accounts archive rather than disappear. |
| `notes text` | Optional, at most 1000 characters. |

Core creates the account's primary policy/subject. No owner, payer or member share is inferred from its name or creator. Opening balance is an explicit cash event, including an explicitly reviewed zero; an absent opening remains unknown. Index `(household_id,archived_at,id)`.

## 2. `expenses.entry_finance`

| Columns | Definition |
|---|---|
| `entry_id uuid` | Primary key and same-household FK to the existing entry; unique identity is the entry ID. |
| `original_state text` | `known` or `legacy_unknown`; foreign mode requires `known`. |
| `original_currency text NULL`, `original_exponent smallint NULL`, `original_minor bigint NULL` | All present together for known original; nonzero and direction-consistent. Unknown legacy context never invents a foreign amount. |
| `fx_numerator numeric(36,0) NULL`, `fx_denominator numeric(36,0) NULL` | Positive, both present or absent. EUR original uses exact identity conversion. |
| `fx_effective_on date NULL`, `fx_source text NULL` | Approved date and bounded source description, present with a foreign rate. |
| `fx_rounding text` | Fixed `half_away_from_zero_v1`. |
| `fx_reviewed_by uuid NULL`, `fx_reviewed_at timestamptz NULL` | Server actor and timestamp; present only after explicit rate approval. |
| `account_id uuid NULL` | Explicit payment account; no inference from payer. |
| `payment_currency text NULL`, `payment_exponent smallint NULL`, `payment_minor bigint NULL` | Explicit native payment amount, all absent with an unassigned account. Sign follows the cash direction. |
| `payment_fx_numerator numeric(36,0) NULL`, `payment_fx_denominator numeric(36,0) NULL`, `payment_fx_on date NULL`, `payment_fx_source text NULL` | Separate reviewed valuation when account currency differs from EUR. Never copied from receipt FX by assumption. |
| `payment_base_minor bigint NULL`, `payment_difference_reviewed boolean` | Known approved account valuation and explicit acknowledgement of a receipt/payment valuation difference. No automatic fee or balancing expense. |

Common mutable metadata applies while the parent is draft. A posted context is immutable and must agree exactly with the entry and committed account movement. The Core effective requirement is entry policy AND current account policy AND immutable original requirements. Rebinding retains the union of original account anchors. Index `(household_id,account_id,entry_id)`.

## 3. `expenses.entry_original_lines`

| Columns | Definition |
|---|---|
| `line_id uuid` | Primary key: the existing category-line ID, never a second allocation identity. |
| `entry_id uuid` | Same-household FK to entry and matching existing line parent. |
| `original_minor bigint` | Nonzero signed amount in the parent's original currency; discounts may have opposite sign. |
| `allocation_rule text` | Fixed `signed_largest_remainder_floor_v1`. |

Currency/exponent inherit from `entry_finance`; EUR base remains only on the existing line. Advanced EUR originals also retain these rows with exact original=base, so changing a draft's original currency does not retire/recreate the same sidecar identity. Plain legacy EUR has no invented original rows. At most 100 current category lines. Complete foreign parent/line coverage is required, including zero-base lines. Rate-unreviewed drafts have all base values NULL. With an approved rate, floor exact signed line quotas and distribute the difference to the rounded original-line sum by descending remainder, UUID-byte tie-break. Posting also requires original-line sum equal to original header. Reversal copies and negates frozen values rather than recomputing. Index `(household_id,entry_id,line_id)`.

## 4. `expenses.cash_events`

| Columns | Definition |
|---|---|
| `event_kind text` | `expense`, `refund`, `reversal`, `income`, `transfer`, `opening`, `adjustment`, `settlement`. |
| `state text` | `draft` or `posted`; posted facts are immutable. |
| `occurred_on date`, `description text` | Explicit civil date and 1–300 character explanation. |
| `payee text NOT NULL DEFAULT ''` | Retained actual counterparty, at most200 characters; imported income keeps it after temporary preview payload pruning. |
| `entry_id uuid NULL` | Existing expense/refund/reversal source where applicable; unique event per booking. |
| `original_currency text`, `original_exponent smallint`, `original_minor bigint` | Explicit economic denomination and signed amount; an explicit zero opening is the sole non-FX original-zero exception. |
| `base_minor bigint NULL` | Frozen EUR valuation; required on post, may genuinely round to zero. |
| `fx_numerator numeric(36,0) NULL`, `fx_denominator numeric(36,0) NULL`, `fx_effective_on date NULL`, `fx_source text NULL` | Exact approved valuation snapshot where needed. |
| `reverses_event_id uuid NULL` | Same-household unique full reversal relationship. |
| `posted_at timestamptz NULL` | Present iff posted. |
| `account_id uuid NULL`, `payment_context jsonb NULL` | Current declared account and bounded typed payment context for reviewable income drafts before movements exist; authority includes this account even with no posted leg. |

Income, transfer, opening, adjustment and settlement are excluded from category consumption. Event/booking/account effects and durable Core proof commit together. Index `(household_id,occurred_on DESC,id)`; unique non-null `entry_id` and `reverses_event_id` within household.

## 5. `expenses.account_movements`

| Columns | Definition |
|---|---|
| `event_id uuid`, `account_id uuid` | Same-household event/account FKs. |
| `leg smallint` | Stable event leg; unique `(household_id,event_id,leg)`. |
| `currency_code text`, `currency_exponent smallint`, `amount_minor bigint` | Signed actual native account delta; currency must equal the account denomination. |
| `base_minor bigint` | Exact reviewed signed EUR valuation; genuine zero permitted. |
| `fx_numerator numeric(36,0) NULL`, `fx_denominator numeric(36,0) NULL`, `fx_effective_on date NULL`, `fx_source text NULL` | Frozen account-leg valuation if required. |

Expense delta is the negative of its signed payment; refund reverses it. Same-currency transfer has exactly two distinct-account opposite native legs; foreign transfer has both explicitly reviewed native amounts/valuations. Fees are explicit linked expenses. All legs are atomic. Posted movements are immutable and reversed by new events. Index `(household_id,account_id,event_id,id)`; statement date comes from the immutable event.

## 6. `expenses.entry_relations`

| Columns | Definition |
|---|---|
| `entry_id uuid`, `original_entry_id uuid` | New fact and its same-household original. |
| `relation text` | `refund_of`, `correction_of`, `reversal_of`. |
| `reviewed_original_version bigint` | Explicit original version; source requirements are retained in Core. |
| `reversal_entry_id uuid NULL` | Correction's exact linked reversal, present only for a correction. |

Immutable, unique `(household_id,entry_id,relation)`; no self/cyclic correction chain. `reversal_of` agrees with existing `reverses_entry_id`. Correction creates reversal and reviewed replacement atomically. Linked refunds lock/recheck original-denomination effective refund capacity. Legacy unlinked/ambiguous pointers remain explicit rather than being silently promoted. Index `(household_id,original_entry_id,relation,id)`.

## 7. `expenses.entry_split_sets`

| Columns | Definition |
|---|---|
| `entry_id uuid` | Unique same-household booking. |
| `method text` | `equal`, `percent`, `weights`, `fixed`. |
| `currency_code text`, `currency_exponent smallint`, `total_minor bigint` | Debt denomination is the booking's original currency; captured legacy EUR uses EUR. |
| `payer_membership_id uuid NULL` | Explicit reviewed payer/recipient; never inferred from account ownership. |
| `basis jsonb` | Closed canonical input, at most 50 distinct memberships/16KiB. Percent uses integer parts-per-million totaling 1,000,000; weights use positive bounded integers; fixed uses exact signed minor strings. |
| `algorithm text` | Fixed `largest_remainder_uuid_v1`. |
| `reviewed_at timestamptz`, `reviewed_by uuid` | Server-attributed explicit review. |
| `active boolean` | Explicit removal deactivates the retained set identity; future review updates it rather than laundering a retired source ID. |

Draft updates advance the parent revision; posted sets freeze. Unknown payer may retain reviewed beneficiary shares but produces no fabricated complete debt balance. No legacy automatic equal split. Core supplies complete original/account visibility requirements.

## 8. `expenses.entry_person_shares`

| Columns | Definition |
|---|---|
| `split_set_id uuid`, `entry_id uuid`, `membership_id uuid` | Matching same-household parent/set/member; unique participant per set. |
| `share_minor bigint` | Exact signed original-currency allocation; zero is valid for deterministic rounding of a small amount. |
| `base_minor bigint NULL` | Derived EUR presentation only; not the denomination of the debt. |
| `position smallint` | Stable display position. |

At most 50 shares, exact total equal to the split set, no sign inversion. Equal/percentage/weights allocate absolute units by largest remainder and UUID tie-break, restoring the sign. Raw basis remains explainable. Historical inactive memberships remain readable; new selections require current valid household identity. Index `(household_id,membership_id,entry_id)`.

## 9. `expenses.settlement_details`

| Columns | Definition |
|---|---|
| `event_id uuid` | Primary identity and FK to a settlement cash event. |
| `from_membership_id uuid`, `to_membership_id uuid` | Distinct same-household members. |
| `currency_code text`, `currency_exponent smallint`, `amount_minor bigint` | Positive amount of debt actually settled; no cross-currency netting. |
| `reviewed_sources jsonb` | Closed immutable debt-source read set, bounded by 1000 rows/128KiB and Core's canonical unique-anchor limit. |
| `reviewed_balance_hash bytea` | Exactly 32 bytes; current balances/scope are revalidated before acceptance. |

Partial payments are supported. Actual native account payment and any approved conversion are separate event legs. No zero cash payment silently clears nonzero foreign debt. Settlement never edits category consumption or original expense shares. Later refund/correction changes debt while preserving the payment history. Immutable on post.

## 10. `expenses.saved_filters`

| Columns | Definition |
|---|---|
| `owner_user_id uuid`, `name text` | Private preference owner and 1–100 character name; unique name per owner. |
| `criteria jsonb` | Closed, canonical, versioned filter: dates, states, currency, account/person/category UUIDs and bounded payee text; at most 16KiB. |
| `criteria_version smallint` | Initial format 1. |

Private ownership is a Core preference anchor, not an account policy invented from selected filter IDs. Every use reauthorizes referenced filters before sorting/counting. Denied criteria never silently disappear and broaden the query. Index `(household_id,owner_user_id,id)`.

## 11. `expenses.category_budgets`

| Columns | Definition |
|---|---|
| `name text`, `category_id uuid` | Required label and same-household category. |
| `starts_on date`, `ends_on date NULL` | Explicit month boundaries. |
| `monthly_minor bigint` | Nonnegative EUR amount. |
| `carry_rule text` | `none`, `positive_only`, `all`; explicitly selected. |
| `initial_carry_minor bigint` | Explicit reviewed starting carry, default 0; never inferred from unavailable history. |
| `scope jsonb` | Closed bounded optional account/person selectors and schema revision. |
| `archived_at timestamptz NULL` | Preserve historical budget definition. |

No rollover table: compute bounded complete authorized carried periods. Denied or out-of-bound history cannot masquerade as a complete carry value. Posted category consumption excludes transfer/income/settlement. Index `(household_id,category_id,starts_on,id)`.

## 12. `expenses.savings_goals`

| Columns | Definition |
|---|---|
| `name text`, `currency_code text`, `currency_exponent smallint`, `target_minor bigint` | Required label, fixed denomination, positive target. |
| `target_on date NULL`, `account_id uuid NULL` | Optional deadline/account; association never makes goal progress an account balance. |
| `planned_period_minor bigint NULL`, `planned_period text NULL` | Explicit nonnegative recurring contribution plan, with both fields present together. |
| `state text` | `active`, `completed`, `archived`. |

No invented actual funds from planned contributions. Linked-account visibility intersects with the goal policy; immutable historical contribution requirements remain retained. Index `(household_id,state,target_on,id)`.

## 13. `expenses.goal_contributions`

| Columns | Definition |
|---|---|
| `goal_id uuid`, `occurred_on date`, `amount_minor bigint` | Same-household goal, explicit date and positive goal-currency amount. |
| `contribution_kind text` | `planned`, `manual_recorded`, `ledger_recorded`, `reversal`. |
| `event_id uuid NULL`, `movement_id uuid NULL` | Explicit actual ledger source where applicable. |
| `reverses_contribution_id uuid NULL`, `note text` | Immutable correction link and bounded explanation. |

Planned values do not count as recorded progress. Linking an event does not create a second account movement or expense. Any foreign valuation is an explicitly reviewed immutable source snapshot. Recorded contributions reverse rather than overwrite. Index `(household_id,goal_id,occurred_on,id)`.

## 14. `expenses.recurring_templates`

| Columns | Definition |
|---|---|
| `name text`, `anchor_on date`, `ends_on date NULL` | Label and civil scheduling bounds. |
| `repeat_unit text`, `repeat_interval smallint` | `day`, `week`, `month`, `year`; positive bounded interval. |
| `monthday_policy text` | Explicit `clamp` or `skip`, including leap-day behavior. |
| `template jsonb` | Closed versioned advanced booking draft, at most 100 lines/64KiB. No session, capability or bearer material. |
| `template_kind text` | `expense` (default) or `income`; JSON is the corresponding existing typed entry/income input, never an arbitrary writer payload. |
| `account_id uuid NULL`, `active boolean` | Optional account dependency and explicit activation. |

Generation produces reviewable drafts only, never posts automatically. Forecast distinguishes proposed occurrences from booked cash. Template/current account/original rights are checked at generation and read. Once an occurrence exists, anchor/unit/interval/monthday policy are immutable; clone a new template for a changed calendar. Delegated consent and authority exist only in Core; no domain consent mirror or Owner business write is introduced. Index `(household_id,active,anchor_on,id)`.

## 15. `expenses.recurring_occurrences`

| Columns | Definition |
|---|---|
| `template_id uuid`, `scheduled_on date` | Unique canonical occurrence identity. |
| `template_version bigint`, `template_hash bytea` | Frozen reviewed source revision and 32-byte snapshot fingerprint. |
| `entry_id uuid NULL`, `event_id uuid NULL` | Created draft/result identity; never both unless the entry owns that event. |
| `state text` | `proposed`, `draft_created`, `skipped`, `cancelled`. |

Unique `(household_id,template_id,scheduled_on)` and durable Core operation/ingest claim prevent duplicate generation/replay. Retry after deletion/purge does not invent a replacement. Future generation/forecast is bounded to a declared horizon and count, with no silent truncation. Index `(household_id,scheduled_on,state,id)`.

## 16. `expenses.categorization_rules`

| Columns | Definition |
|---|---|
| `name text`, `priority integer`, `active boolean` | Explicit stable ordering; ID is final tie-break. |
| `match_kind text`, `payee_pattern text` | Closed `equals`, `prefix`, `contains`; bounded literal matching, no arbitrary regexp execution. |
| `currency_code text NULL`, `minimum_minor bigint NULL`, `maximum_minor bigint NULL` | Optional exact declared matching bounds. |
| `category_id uuid`, `account_id uuid NULL` | Suggested same-household target category and optional account scope. |

Return all applicable explanations and deterministic winner; rule application requires explicit review and never overwrites a confirmed posted category. No hidden account/rule metadata in previews. Index `(household_id,active,priority,id)`.

## 17. `expenses.bank_profiles`

| Columns | Definition |
|---|---|
| `owner_user_id uuid`, `name text`, `account_id uuid` | Private profile owner, label and required exact target account. |
| `delimiter text`, `encoding text`, `date_layout text`, `decimal_separator text`, `group_separator text` | Closed documented CSV parsing choices; optional explicit grouping separator differs from decimal separator; no parser code from the client. |
| `amount_mode text` | `signed` or `debit_credit`. |
| `field_map jsonb` | Closed bounded column names for date/amount/currency/payee/reference/external ID; at most 16KiB. |
| `profile_format smallint` | Initial format 1. |

One account per profile keeps source-authority sets bounded. No banking credentials. Core import batches store bounded preview payload/rows with private creator plus account requirements; Core permanent minimal ingest claims protect exact confirmed external IDs/file-row identities. At most 500 rows per review; canonical unique requirements may impose a smaller atomic bound. Identical-looking amount/date/payee is a warning, not a deduplication key. Index `(household_id,owner_user_id,account_id,id)`.

## 18. `expenses.reconciliations`

| Columns | Definition |
|---|---|
| `account_id uuid`, `statement_on date`, `statement_minor bigint` | Exact external native-currency statement fact. |
| `starts_on date NULL`, `opening_minor bigint NULL` | Optional explicit external statement period and reviewed opening, both null or both present; start must not exceed statement date. Signed opening is bounded to the existing exact money limit. |
| `currency_code text`, `currency_exponent smallint` | Must match account denomination. |
| `computed_minor numeric(36,0)`, `difference_minor numeric(36,0)` | Exact complete reviewed posting-set sum and difference; output as strings. |
| `scope_hash bytea`, `account_version bigint`, `posting_count integer` | 32-byte complete reviewed scope fingerprint and bounded exact count. |
| `state text` | `review`, `confirmed`, `reopened`. |
| `confirmed_by uuid NULL`, `confirmed_at timestamptz NULL`, `reopens_id uuid NULL` | Immutable confirmation/reopen relationship. |

True statement comparison requires complete current access to the scoped account/posting/original set. Otherwise neutral denial: a visible subset is not a bank balance. The optional external opening is added to all posted movements within the inclusive period, excluding opening events; absent this pair, retain the full-history/known-opening route. Keep the Core1024 unique-anchor bound, with no truncated history or raised authority limit. No automatic balancing transaction. New/backdated movements invalidate a pending review; confirmed historical facts remain, with explicit current recheck/reopen. Index `(household_id,account_id,statement_on DESC,id)`.

## 19. `expenses.reconciliation_items`

| Columns | Definition |
|---|---|
| `reconciliation_id uuid`, `movement_id uuid` | Same-household review and immutable posting, unique per review. |
| `reviewed_event_version bigint`, `reviewed_movement_version bigint` | Exact source revision pins. |
| `amount_minor bigint` | Frozen native amount used for the statement comparison. |

The item set is complete for its declared statement range, bounded before materialization, and checked against the scope hash. Every item inherits complete source/account/original requirements. Confirmation is all-or-nothing. Index `(household_id,reconciliation_id,movement_id)`.

## 20. `expenses.receipt_reviews`

| Columns | Definition |
|---|---|
| `entry_id uuid NULL`, `capture_job_id uuid NULL`, `attachment_id uuid NULL` | Typed target/source identities, validated through Core; no optional-schema FK. |
| `source_version bigint NULL`, `source_hash bytea NULL` | Exact current OCR/attachment evidence fingerprint; 32 bytes when present. |
| `currency_code text`, `currency_exponent smallint`, `receipt_total_minor bigint NULL` | Reviewed original receipt denomination and total; missing OCR total stays NULL. |
| `line_total_minor numeric(36,0) NULL`, `difference_minor numeric(36,0) NULL` | Exact known comparison; never zero-coalesce missing lines/totals. |
| `reviewed_entry_version bigint NULL`, `reviewed_entry_original_minor bigint NULL`, `reviewed_entry_currency text NULL`, `reviewed_entry_exponent smallint NULL` | Exact source-booking snapshot from the reviewed current version, all present iff an entry is linked. It survives later edits of that draft without pretending to be its current amount. |
| `entry_difference_minor numeric NULL` | Receipt total minus the frozen reviewed original booking amount, only for the same known denomination. A currency mismatch remains incomparable, not a subtraction across currencies. |
| `state text` | `draft`, `confirmed`, `superseded`. |
| `difference_acknowledged boolean` | Explicit manual acceptance when a known total/line/booking discrepancy remains; never inferred from OCR. |
| `confirmed_by uuid NULL`, `confirmed_at timestamptz NULL` | Server-reviewed confirmation. |

Review is evidence, not an automatic booking. It does not fabricate categories or original FX. OCR strings remain source-attributed and bounded; current file/source/target requirements are checked at review, confirmation, replay and read. Index `(household_id,entry_id,id)` and `(household_id,attachment_id,id)`.

## 21. `expenses.receipt_review_lines`

| Columns | Definition |
|---|---|
| `review_id uuid`, `position smallint`, `label text` | Same-household receipt review, stable position and at most 300 characters. |
| `quantity text NULL`, `unit_price text NULL` | Bounded exact decimal source/review values, never floating-point facts. |
| `amount_minor bigint NULL` | Reviewed original-currency line value; explicit free-item zero is allowed; unknown remains NULL. |
| `source_text text`, `source_kind text` | Bounded OCR/manual evidence and `ocr`/`manual` provenance. |
| `page_number smallint NULL`, `source_line integer NULL`, `confidence numeric NULL` | Actual source page 1–20/positive line and optional measured confidence 0–100; unknown confidence remains NULL. |
| `entry_line_id uuid NULL`, `reviewed boolean` | Optional explicit existing category-line linkage and confirmation marker. |

At most 100 rows/64KiB per receipt review. Receipt item rows do not become category allocations automatically. Unknown items prevent a complete line-total comparison. Discounts remain signed. Index `(household_id,review_id,position,id)`.

## Cross-cutting transaction and projection contract

The central Finance dispatcher owns one transaction: current household/session/module authority, idempotency lock, complete policy/account/original union, canonical account/source locks, complete versions, domain writes, projections/audit, immutable Core proof, and commit. No independently committing writer loops. All authorization and source revisions are rechecked at apply; the client cannot supply authoritative policy arrays.

Balances and reports distinguish consumption, native cash and original-currency person debt. Account-visible does not mean all linked entries are visible: show only authorized intersections and label the result “Saldo sichtbarer Buchungen”. Never reveal hidden-row counts or call the visible subset a bank balance. A true reconciliation requires complete authorized scope. Transfers require both account policies even for receipt/detail/replay.

The legacy absent-Finance SaveExpense canonical structure remains byte-identical. Advanced writes use a separate fixed `expense-finance-v1` canonical purpose with full money/context/version inputs, while preserving actor/key collision protection across purposes. Receipts expose acceptance identity and separately labelled current existence/state only after current access, not an obsolete private form.

## Acceptance sources to prepare, not execute yet

Prepare coverage for genuine foreign base zero; unknown-rate draft NULL; mixed signed line remainders; original-currency person obligations; account/receipt currency mismatch; explicit opening zero versus absent opening; atomic transfer/correction/refund/settlement; stale reconciliation; full original/account requirement retention; old EUR receipt byte compatibility; aggregate values beyond JS safe integers; populated Expenses4 upgrade; and genuine encrypted restore/purge. Tests/builds/DB/browser/container operations start only after the complete Finance source gate.
