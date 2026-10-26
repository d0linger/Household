# Shared expense editor — EX02 verification

EX02 is locally verified after the final gates below actually executed. Selected financial visibility is a separate EX19 capability and is unavailable. The existing household-wide sharing is explicit in the editor.

## Saved backend checkpoints

- b196d22: Expenses3 integrity/readiness, exact shared normalization, complete child reconciliation and current-authorized bounded RR reader. Six existing root compatibility contracts pass in 91.379s; ten new root contracts pass in 61.927s. Pure package coverage is 95.5%; standard unit coverage is 17.2%. Tagged lint and explicit application-source Gosec pass.
- 4904a79: atomic shared save/post and permanent minimal Core24 operation proofs. Root passes 36 main contracts in 284.843s plus two existing populated census upgrades in 22.471s. The 350-file source manifest remains unchanged through those checks. Verify passes 17.0% (1960/11531), 78 Node cases and 64 immutable migration hashes; tagged lint has zero issues, Gosec 137 files/21077 lines/zero findings.

These focused checkpoints do not certify later UI changes or replace final whole-suite/race/image/recovery checks.

## Connected development surface

`GET /m/expenses/editor?entry=<optional UUID>` opens the shared editor. `GET /api/v1/expenses/editor?entry=<optional UUID>` returns a complete current-authorized snapshot; POST to the same API accepts an explicit EUR body, full baseline/removals, current generation/version and idempotency key. Transport is authenticated and non-cacheable. Field problems return 422 with named fields; current-scope, posting and conflict decisions stay on the server.

The browser uses exact BigInt arithmetic, explicit draft/post review, deliberate row removal/undo/reordering and current-account checks before sending. Pending unknown outcomes freeze the exact in-memory body/key. A confirmed operation fetches a fresh complete editable basis; an old acceptance receipt is never used as the current form. Reader/posted views use saved text facts. Financial requests are excluded from the generic device queue; old opted-in financial drafts remain available for current-account manual review and explicit discard, without automatic transmission or silent deletion.

## Executed UI/API preparation

- Two HTTP contracts pass initially in 13.060s and after the bounded copy/title correction in 14.287s. They cover CSRF, strict JSON/EUR types, named validation, minimal replay, complete basis, real payer template identity, escaped text, readers, anonymous access and disabled-module privacy.
- Six pure Node contracts pass: exact EUR without rounding, signed discount/refund totals, incomplete/empty/invalid distinctions, bounded 100-row aggregates, wide stored cent formatting and unsafe browser-version refusal.
- The initial browser selector was ambiguous because both header and empty-state actions exist. A later assertion used a fieldset disabled matcher that did not check its affected input controls; the actual DOM disabled property confirmed the correct transition. The explicit-discard assertion also needed to await the IndexedDB transaction. These failed attempts remain logged.
- The corrected functional-only campaign passes 27 checks with no page errors or external requests; it intentionally skips captures/axe and does not claim visual acceptance. It includes a committed response deliberately lost in transit, exact same-body/key replay with one entry, complete posting review, retained 422/conflict input, account switching before POST, real reader access and new/legacy financial queue boundaries.
- One batched inspection opened desktop/mobile, validation, uncertain-result and legacy-queue captures. The bounded correction fixes saved/read-only titles, German validation spacing and the raw English network message. Focused desktop captures now avoid full-page stitching of fixed navigation after scrolling. The fresh complete browser chain is the confirmation gate.

The first complete current24-component chain passes843checks; the editor contributes40checks/ten clean axe results/ten valid captures. Fresh independent finish review finds one material issue, EX02-F1: legacy updates opened the new-entry editor. Recovery now targets the actual existing entry, with category drafts returning to category maintenance. The corrected browser contract clicks the real separate-tab destination and checks retained manual review/discard. A complete chain repeat is running;843is preceding evidence, not acceptance of that correction.

Final standard verification passes with84Node tests, module/workflow checks and64migration hashes. Measured whole-module unit coverage is16.96%(1963/11571). These unit figures are distinct from the pending combined internal integration coverage. Current source/dependency freeze contains356files. Runtime/helper builds and final security checks are running. Recovery now includes an owner-seeded exact posted financial fixture, a complete logical before/after snapshot and cross-action/generation key uniqueness; it is not an authenticated restored-service replay.

## Final acceptance checklist

- [x] Backend integrity/reader and atomic writer/proof contracts actually executed.
- [x] Current-authorized HTTP protocol and exact browser-value contracts executed.
- [x] Browser functional recovery, concurrency presentation, account and queue paths executed.
- [x] Complete current 24-component browser chain:846checks; editor43checks/ten clean axe scans/ten captures, keyboard and actual separate-tab recovery.
- [x] Fresh independent finish review:EX02-F1 resolved/ship at correction scope; separate [design comparison](expense-editor-design-verification.md) opens ten captures/validates24links.
- [x] Final verify/lint/security/dependency/workflow checks:84Node/64hashes, tagged lint0, Gosec138files/21142lines/0findings, GOVul0reachable/imported, dependency/licenses and Actionlint.
- [x] Complete native regression:335main tests, Web1437.558s,74.1% combined internal coverage(8446/11392),356-file source/dependency manifest unchanged.
- [x] Scoped Linux race:37Web contracts(Web645.231s),14expenseedit contracts and one Store contract(Store1.029s), no race report; exact selected names recorded.
- [x] Current10001:10001runtime/helper builds and encrypted synthetic restore:7.12s backup/27.96s restore-and-verify, exact signed financial facts/proof/uniqueness/logical snapshot and two file hashes.
- [x] Final staged/actual-secret checks and checklist reconciliation:16actual distinct secret values absent from37current textual artifacts and staged diff;29source/documentation files staged,64prior migration hashes and356frozen source/dependency files preserved.

No hosted Actions, production capacity/RTO/RPO, authenticated restored API, live8100 upgrade, push or publication is claimed.

### Current final execution results

Complete corrected browser chain:846checks/24components; editor43checks/ten clean axe scans/ten captures, no JS errors/external requests. Independent EX02-F1 resolved/ship at correction scope; separate comparison opens all ten and validates24links. Final standard unit/Node/migration/workflow/lint/security/license evidence and current non-root image encrypted restore pass as recorded above. The earlier running statements describe preceding checkpoints.

Complete native regression passes335main tests, Web1437.558s, with74.1% combined internal coverage(8446/11392;55% gate). The356-file source/dependency manifest matches after execution. Scoped Linux race is running serially and selects37Web contracts,14expenseedit pure contracts and one Store canonical-basis contract; exact names are saved in outputs/expansion-review/finance-editor-race-scope.json. This does not claim whole-tree race acceptance. Final feature closure remains pending that result and final staged/secret reconciliation.

### Local acceptance

The scoped Linux race passes(Web645.231s, Store1.029s) with the exact52selected contracts and no race report. Whole-tree race acceptance is not claimed. The356-file frozen source manifest remains unchanged after native and race. Final Gitleaks reports no staged leaks; the completed actual-secret scan finds none of16distinct local secret values in37current text artifacts or the staged diff. Earlier artifact-check attempts encountered a locked active log and empty-log/null handling; they were incomplete, never acceptance. No ignored artifact is staged, all64prior migration hashes remain unchanged, and the race environment file is removed.

Together with the335-main-test complete native campaign/74.1% internal coverage,846browser checks, independent EX02-F1 resolution and separate design comparison, standard84Node/64hash verification, current scanners/dependency/workflow checks and matching non-root encrypted financial recovery, EX02 meets local feature acceptance. Checklist59verified/1inprogress(EX19)/120open. The live8100 deployment remains unchanged. Current source is saved locally; no push, hosted Actions, authenticated restored API or production capacity/RTO/RPO is claimed.
