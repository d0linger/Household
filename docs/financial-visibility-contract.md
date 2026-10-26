# Financial visibility — EX19 implementation contract

This is the next implementation contract, not an available capability. Today's financial records are explicitly household-shared. The EX02 native candidate is frozen separately while this work is prepared in ignored `.local/finance-access-work`. Existing balances, responsibility and access grants are never inferred from payer or creator.

## Responsibility and recovery decisions

1. Legacy entries migrate to explicit household access with unknown responsibility. Preserve every amount, date, content version, payer and posting state.
2. Selecting restricted access requires an explicit active responsible membership and at least its edit grant. A membership's global reader role still prevents mutation even if a stored grant says edit.
3. Content edit grants do not confer grant-management authority. The responsible active editor manages ordinary selected grants. On a shared legacy entry without responsibility, an authorized editor may explicitly take responsibility before restricting it; this is a separate audited decision.
4. A household administrator does not automatically read selected financial content. Administrative recovery is a separate, recent-authentication operation with a required reason and a review of the new responsible membership. It returns administrative metadata without financial values and leaves a security event. Never silently turn a selected record into shared access.
5. Disabling, removing or downgrading the last active responsible editor requires explicit reassignment first. Show a neutral recovery requirement to an administrator who cannot see the financial content; do not reveal amounts, payees, filenames, private titles or counts.
6. Keep membership identities and historical grants when a member becomes inactive. Inactive membership never authorizes current access. At most100 grants, including responsibility, may belong to a policy.
7. Missing, retired, ambiguous or generation-mismatched required policy means denied access. Do not fall back to shared access after deletion, purge, broken bindings or an unavailable source.
8. New accounts will add account visibility as an intersection with record visibility. The current application has no account model; an entry policy does not certify account permissions.

## Core data boundary

Core owns generation-bound policies, grants and typed subject bindings. Optional schemas never reference each other. A policy identifies household, Expenses generation, resource identity, explicit shared/selected mode, optional responsibility, revision and retirement. Grants reference same-household Core memberships. Subject bindings let category lines and sensitive Core derivatives inherit the entry policy; bindings survive source deletion as denied historical identities.

Sensitive Core history can require more than one source policy. An attachment, link, capture result or audit event must never become readable merely because one endpoint is visible. Bind the complete required policy set and require all relevant rights before displaying metadata. A missing required binding denies access. Separate administrative access-change events contain no financial content.

Policy revision is distinct from entry content version. Lock order is household/current session/module → policy → entry → bounded ordered children → referenced categories/memberships. Existing opposite-order child writes keep bounded NOWAIT failure. Reads use a current authorized snapshot; mutation and replay check current access before returning any resource identity or result.

## Concrete source integration inventory

| Surface | Current functions/files | Required boundary |
|---|---|---|
| Generic records | `Store.List/Get/LockResource/Write/Delete`, `internal/store/store.go` | Filter in SQL before ordering, LIMIT, offset or counts. Select read versus edit intent explicitly. Authorize parent before new child writes. |
| Domain actions | `Action/fuelToExpense`, `actions.go`; `validateDomain`, `rules.go` | Current target access before prior response or resource-link identity. A hidden existing target must not cause a duplicate creation. |
| Shared editor | `ExpenseBundle`, `expense_bundle.go`; `SaveExpense/readExpenseEditProof/currentExpenseReceipt`, `expense_edit.go` | Check policy before amounts, labels, text-size or101-line probes. Return real access revision and permitted actions. Recheck current access before every proof response. |
| References/search | `referenceOptions`, `internal/web/references.go`; `references/referencesAPI/searchRows`, `handlers.go` | Cover direct options SQL, selected-ID hydration, signed cursor pages, ranking and51-row sentinels. |
| Related records | `related`, `internal/web/related.go` | Filter child and destination identities before LIMIT. A resource link transfers no rights. |
| Reports/export | `report`, `handlers.go`; `exportCSV`, `imports.go`; `internal/store/reports.go` | Filter before SUM/GROUP BY. Give paged CSV one bounded authorized read context; do not aggregate hidden rows then hide the label. |
| Files | `upload/attachmentList/download/capturePage`, `attachments.go` | Check filename, size, preview, OCR text, bytes and edit intent. Validate current source and target before rebinding; never silently widen the audience. |
| Capture | `ConfirmCapture`, `internal/store/capture.go`; worker capture/thumbnail commits | A confirmed result ID is protected too. Authorize the source/capture/result before replay, and recheck liveness/binding at worker commit. |
| Imports | `importCSV`, `internal/web/imports.go`; `ConfirmImport`, `internal/store/import.go` | Give previews an explicit creator and Core policy before entries exist. Bind every confirmed result atomically; require current permission on replay and display. |
| Audit | `readAudit`, `internal/web/audit.go`; `core.write_audit_row`; `audit.Event` | Filter historical Expenses and sensitive Core events before pagination. Preserve denied bindings after deletion/purge. Admin role alone cannot expose content. |
| Projections/lifecycle | `afterWrite/cleanupOrphans`, `store.go`; `cleanupExpenseEditLines`; `maintenance.Purge` | Keep search and derivative policy bindings current. Purge rich payloads and preserve minimal denied identity/key evidence. |
| Notifications/jobs | `worker.deliver/outbox/job`, capture/thumbnail workers; notification readers | Use typed resource/generation/policy references, check recipient at delivery and read, and hide previously delivered private metadata after revocation. Financial reminders are not currently implemented. |
| User management | `updateUser`, `internal/web/handlers.go` | Preserve last active responsible editor; integrate explicit recent-auth recovery without content disclosure. |
| Device drafts | old-queue rendering in `internal/web/static/app.js` | Renew resource permission before describing stored financial values. New financial queue writes are already excluded by EX02. Revocation cannot erase previously downloaded data reliably. |

The household-wide file quota may still count all stored bytes internally, but its message remains undetailed. Integrity sums for an already authorized entry include every child. Operator backup/restore intentionally contains all data and remains outside application grants.

## Ordered implementation and acceptance

1. Preserve the accepted EX02 source before materializing any access preparation.
2. Add new forward Core policy/grant/subject migrations with individual grants, bounded checks and exact readiness.
3. Add audit classification and conservative historical Core bindings; ambiguous historical private content is denied.
4. Add Expenses forward backfill and narrow integrity hooks, preserving legacy data and content versions.
5. Add the shared SQL visibility predicate with current membership, global role, generation, complete required policies and view/edit intent.
6. Add locked mutation checks and independent expected access revision.
7. Bind all creators: editor, generic records, category lines, refund/reversal, fuel, capture and import.
8. Protect all minimal/rich replay responses before exposing IDs, existence or counts.
9. Add explicit policy retirement and retained historical subject identity to deletion and real purge.
10. Cover generic reads, direct references/related SQL, search, reports and bounded CSV snapshots.
11. Cover attachments, OCR/capture source/result, preview and rebinding.
12. Cover import-preview ownership and confirmation/replay.
13. Cover sensitive Core/Expenses audit rows before LIMIT and separate recovery security events.
14. Cover delivery/read revocation, worker commit and safe background diagnostics.
15. Cover user deactivation/downgrade and explicit administrative recovery.
16. Renew authorization before any old local draft is displayed; retain explicit discard without automatic transmission.
17. Add the selected-access form only after every server path is protected; keep household sharing clear on legacy records.
18. Test two users and an excluded administrator across every direct and indirect endpoint.
19. Test revocation during mutation, OCR, paging, delivery and replay, plus missing/retired policies and old generations.
20. Test hidden-first pagination/COUNT/SUM, incomplete bindings and visible-source/hidden-target links without duplicate writes.
21. Test empty and populated upgrades, old-writer drain, deferred rollback and last-editor contention.
22. Test encrypted restore and actual purge with retained denied history and permanent keys.
23. Execute current native, meaningful scoped race, browser/keyboard/axe, security and image checks.
24. Update README/checklist with actual evidence; expose selected access only after all required gates pass.

No selected visibility, current policy schema, account rights or operational result is claimed by this document. The source-only inventory was independently checked without tests or database actions.
