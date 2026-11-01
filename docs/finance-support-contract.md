# Finance planning, import and receipt bindings

Published source contract; no executed checks are claimed. All payload members below are required unless marked optional. A required nullable member must be present as null when unknown. Each command also uses the envelope/version/idempotency/review rules in finance-module-contract.md.

## Exact action payloads

| Resource/action | Closed payload |
|---|---|
| filters/save | `{name,criteria:{from,to,account,person,category,payee,state}}`; criteria strings may be empty, references still reauthorized. |
| filters/delete | `{}`; personal owner only, Reader allowed. |
| budgets/save | `{name,category_id,starts_on,ends_on:null\|date,monthly_minor,carry_rule,initial_carry_minor,scope:{account,person}}`; carry_rule none/positive_only/all. Starts/ends are first-of-month dates. |
| budgets/archive | `{}`. |
| goals/save | `{name,currency,exponent,target_minor,target_on:null\|date,account_id:null\|UUID,planned_period_minor:null\|string,planned_period:null\|month\|week,state}`; state active/completed/archived, planned pair both present or absent. |
| goals/archive | `{}`. |
| contributions/save | `{goal_id,goal_version,occurred_on,amount_minor,contribution_kind,event_id:null\|UUID,movement_id:null\|UUID,note}`; kind planned/manual_recorded/ledger_recorded; positive goal-denomination units; ledger kind requires actual authorized source. |
| contributions/reverse | `{original_id,original_version,occurred_on,note}`; command ID is new reversal identity; original remains immutable. |
| recurrences/save | `{name,anchor_on,ends_on:null\|date,repeat_unit,repeat_interval,monthday_policy,template,account_id:null\|UUID,active,automatic}`; repeat_unit day/week/month/year, interval1..365, monthday_policy clamp/skip, template exactly FinanceEntryInput. Automatic explicitly opts into separate Core authority. |
| recurrences/pause | `{}`; removes automatic consent as well as active generation. |
| recurrences/generate | `{through:date}`; command identifies current template/version; generate earliest unclaimed dates up to through, at most12/template, report backlog rather than silently skipping. |
| rules/save | `{name,priority,active,match_kind,payee_pattern,currency:null\|code,minimum_minor:null\|string,maximum_minor:null\|string,category_id,account_id:null\|UUID}`; match_kind equals/prefix/contains, priority0..1000000, literal pattern1..100chars, exact amount bounds. |
| rules/archive | `{}`. |
| rules/explain | `{payee,currency,amount_minor,account_id:null\|UUID}`; bounded deterministic applicable rules and winner, no automatic category mutation. |
| bank_profiles/save | `{name,account_id,delimiter,encoding,date_layout,decimal_separator,group_separator,amount_mode,field_map}`. delimiter is the literal character `","`, `";"` or `"\t"` (tab), encoding utf-8, date_layout iso/dmy/slash_dmy/slash_mdy, decimal . or comma, group separator empty/dot/comma/space and distinct from decimal. amount_mode signed/debit_credit. field_map exact keys `{date,amount,debit,credit,currency,payee,description,reference,external_id}`; unused columns empty string, max100chars/column, date and active amount fields required. |
| bank_profiles/archive | `{}`; private owner plus account authorization required. |
| receipts/review | `{entry_id:null\|UUID,entry_version:null\|integer,capture_job_id:null\|UUID,attachment_id,source_version,source_hash,currency,exponent,receipt_total_minor:null\|string,state,difference_acknowledged,lines:[{id,position,label,quantity:null\|string,unit_price:null\|string,amount_minor:null\|string,source_text,source_kind,page_number,source_line,confidence:null\|decimal-string,entry_line_id:null\|UUID,reviewed}]}`; state draft/confirmed; source_kind ocr/manual; max100lines/20pages. Confidence is original0..100evidence, not invented for manual input. |

Date values use YYYY-MM-DD. All money/weights/percent/rates are exact strings, never JSON money numbers. Null remains unknown. A recorded goal contribution does not silently move cash; a recurrence creates only drafts. Archived resources remain readable through current authority.

Access payloads are separately frozen in finance-authority-contract.md; ledger payloads in finance-ledger-contract.md. Source contracts and actual structs must remain aligned before whole-source acceptance.

## View projections

Common runtime keys are retained. Each list is bounded50with next_cursor; full totals do not depend on page size. A detail record includes id/version/access_version and only current-authorized facts.

- overview: totals `{posted_consumption_minor,draft_known_minor,draft_unknown_count}` plus category rows `{category_id,label,amount_minor}`, current month and uncertainty warnings. Counts concern authorized records only.
- planning/budgets: rows include persisted budget fields, `spent_minor`, `carry_minor` (null if history cannot be completely evaluated), `available_minor`, `scope_complete`, `scope_label`; label states visible authorized consumption. No hidden-row counts.
- planning/goals: rows include goal fields, `recorded_minor`, `planned_minor`, `remaining_minor`; detail includes current-authorized contributions. None is an account balance.
- planning/recurrences: rows include template metadata and payload, current automatic consent state, next_due/backlog state. Generated drafts are explicit result IDs only after authorization.
- planning/rules: rows contain exact rule fields; explain impact has `matches`, `winner`, `reason`, with deterministic priority/UUID ordering.
- planning/cashflow: `rows:[{date,account_id,currency,exponent,confirmed_minor,estimated_minor,projected_minor:null|string}]`, explicit opening status, assumptions and horizon (default90days, at most365). Already-generated occurrences deduplicate estimated future entries.
- import: private current bank_profiles and current creator-owned reviewable previews only. No other user's preview filenames/row counts.
- reports: `rows` grouped according to closed section/filter, exact money and denominations; `totals` distinguish consumption/cash/person currencies; chart uses the same rows as its table. Export is the identical filtered server selection, with bounded all-or-error materialization.
- filters: rows only for current owner, including `criteria` and `valid` flag; denied referenced IDs are not expanded or replaced by empty criteria.
- receipts: current-authorized attachment metadata and `pages:[{number,url,available,reason}]`, bounded OCR suggestions with real source/page/line/hash, plus stored reviews. Unknown page count/capability is explicit; no false20page claim from a first-page renderer.

## Bank review routes and rows

POST `/api/v1/expenses/finance/bank/previews?account=UUID&profile=UUID&generation=N` with `Content-Type:text/csv` and Idempotency-Key; raw file max1MiB/500data rows. Profile/account current versions and source bytes are bound by the stored private preview.

GET `/api/v1/expenses/finance/bank/previews/{id}` returns `{id,version,generation,access_version,expires_at,account_id,profile_id,source_hash,rows,warnings}`. Row keys `{row_number,fingerprint,occurred_on,payee,description,currency,exponent,amount_minor,external_id,duplicate,similar,error}`. Duplicate is a neutral boolean, never a hidden predecessor ID; similarity warns without suppressing a legitimate payment.

POST `/api/v1/expenses/finance/bank/previews/{id}/confirm` uses strict `{generation,version,access_version,decisions:[{row_number,fingerprint,decision,category_id:null|UUID}]}` plus Idempotency-Key/CSRF. Decision import/skip; import creates a reviewable draft for negative native statement amounts and an explicit income draft/fact according to the reviewed flow, never silently posted consumption. Invalid/duplicate rows cannot be imported. The complete selected source/result requirement union is checked atomically, with no truncation. Same source proofs remain permanent after7day rich-payload pruning or purge.

## Review token and operation response

Preview response exact keys `{command,impact,impact_hash,review_token,expires_at}`. Token signs command hash, key, five-minute expiry, current actor/session scope and deterministic impact hash; no full private form inside token. Apply submits the same command with review_token. Store recomputes the impact in the actual locked transaction and rejects a changed result before commit. Impact must exclude wallclock/random identity values and include relevant source/version fingerprints. A reconciliation fingerprint returned by preview must be copied into the command and previewed again if it was initially absent.

Apply/status minimal receipt `{id,request_key,generation,resource,action,resource_id,saved_version,accepted_at,replayed,current_exists,current_version,current_state}`; no obsolete private body. Status missing/denied/retired is neutral404. No abandon endpoint is required: after an uncertain result keep the same key and resolve current-authorized status or same-key replay before a new operation.

Common references contain `accounts`, `members`, `categories`, `currencies` and explicit `more` flags. Each reference row has id/label/active, applicable currency/exponent/version/access_version. They are bounded choices, not advertised as complete. GET `/api/v1/expenses/finance/references/{kind}?q=...&selected=UUID&cursor=...` provides actual current-authorized50-result search/selected label for accounts/members/categories. Tokens/cursors are bound to current actor and filter; revoked selected references return neutral unavailable state. Currency code/exponent choices come from the local server allowlist, never guessed from historical data.
